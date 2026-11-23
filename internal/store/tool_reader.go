package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

// ToolReader exposes only the store's read operations for tool use.
type ToolReader struct{ store *Store }

type toolReadKey struct{}

// ToolReader returns a read-only facade with non-waiting tool admission.
func (s *Store) ToolReader() *ToolReader { return &ToolReader{store: s} }

func toolRead[T any](ctx context.Context, s *Store, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	release, opCtx, err := s.enterTool(ctx)
	if err != nil {
		return zero, err
	}
	defer release()
	result, err := fn(context.WithValue(opCtx, toolReadKey{}, s))
	if err == nil {
		err = opCtx.Err()
	}
	if err != nil {
		return zero, err
	}
	return result, nil
}

// messageSnapshot keeps admission through transaction and connection cleanup.
func messageSnapshot[T any](ctx context.Context, r *ToolReader, fn func(context.Context, querier) (T, error)) (T, error) {
	return toolRead(ctx, r.store, func(ctx context.Context) (T, error) {
		var result T
		err := r.store.read(ctx, "message tool", func(ctx context.Context, conn *sql.Conn) error {
			tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return err
			}
			defer tx.Rollback()
			var q querier = tx
			if r.store.hooks.wrapReader != nil {
				q = r.store.hooks.wrapReader(tx)
			}
			result, err = fn(ctx, q)
			return err
		})
		return result, err
	})
}

// MessageContextResult contains an exact message and its chronological neighbours.
type MessageContextResult struct {
	Message                     MessageView
	Before, After               []MessageView
	BeforeReduced, AfterReduced bool
}

// GetMessageExact reads one exact chat/ID pair and optional same-chat context.
// accept is called synchronously, first for the anchor and then for neighbours.
// It must not serialize, perform I/O, spawn work, reenter the store, or retain
// rejected messages. A rejected anchor fails the entire read.
func (r *ToolReader) GetMessageExact(ctx context.Context, chatJID, id string, counts Context, accept func(MessageView) (bool, error)) (MessageContextResult, error) {
	chat, err := parseChatJID("chat_jid", chatJID)
	if err != nil {
		return MessageContextResult{}, err
	}
	if err := validateID("id", id); err != nil {
		return MessageContextResult{}, err
	}
	if err := validateContext(counts); err != nil {
		return MessageContextResult{}, err
	}
	if accept == nil {
		return MessageContextResult{}, invalid("policy", "required")
	}
	return messageSnapshot(ctx, r, func(ctx context.Context, q querier) (MessageContextResult, error) {
		budget := &resultBudget{}
		anchor, err := messageProjection(ctx, q, messageKey{chatJID: chat.String(), id: id}, budget)
		if err != nil {
			return MessageContextResult{}, err
		}
		ok, err := accept(anchor)
		if err != nil {
			return MessageContextResult{}, err
		}
		if !ok {
			return MessageContextResult{}, limitErr("reply bytes")
		}
		hits, _, err := messageContext(ctx, q, []messageKey{{chatJID: anchor.ChatJID, id: anchor.ID, time: anchor.Time.UnixMilli()}}, counts, budget, accept)
		if err != nil {
			return MessageContextResult{}, err
		}
		result := MessageContextResult{Message: anchor, Before: hits[0].before.rows, After: hits[0].after.rows,
			BeforeReduced: hits[0].before.reduced, AfterReduced: hits[0].after.reduced}
		if result.Before == nil {
			result.Before = []MessageView{}
		}
		if result.After == nil {
			result.After = []MessageView{}
		}
		return result, nil
	})
}

func (r *ToolReader) ListChats(ctx context.Context, query string, sort ChatSort, withLatest bool, p Page) ([]ChatView, error) {
	return toolRead(ctx, r.store, func(ctx context.Context) ([]ChatView, error) {
		return r.store.ListChats(ctx, query, sort, withLatest, p)
	})
}

func (r *ToolReader) GetChat(ctx context.Context, jid string, withLatest bool) (ChatView, error) {
	return toolRead(ctx, r.store, func(ctx context.Context) (ChatView, error) {
		return r.store.GetChat(ctx, jid, withLatest)
	})
}

func (r *ToolReader) SearchContacts(ctx context.Context, query string, p Page) ([]Contact, error) {
	return toolRead(ctx, r.store, func(ctx context.Context) ([]Contact, error) {
		return r.store.SearchContacts(ctx, query, p)
	})
}

func (r *ToolReader) DirectChat(ctx context.Context, contact string) (ChatView, error) {
	return toolRead(ctx, r.store, func(ctx context.Context) (ChatView, error) {
		return r.store.DirectChat(ctx, contact)
	})
}

func (r *ToolReader) ContactChats(ctx context.Context, contact string, p Page) ([]ChatView, error) {
	return toolRead(ctx, r.store, func(ctx context.Context) ([]ChatView, error) {
		return r.store.ContactChats(ctx, contact, p)
	})
}

// ToolStoreNotQuiescent closes if a cancelled tool fails to return its resources.
func (s *Store) ToolStoreNotQuiescent() <-chan struct{} { return s.gate.toolFailure }

// enterTool reserves the tool place after the disk and WAL checks.
func (s *Store) enterTool(ctx context.Context) (func(), context.Context, error) {
	g := s.gate
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if g.writerActive != 0 || g.writerPending != 0 || g.tool || g.toolFailed || g.waiting != 0 {
		g.mu.Unlock()
		return nil, nil, ErrBusy
	}
	epoch := g.writerEpoch
	g.mu.Unlock()
	op := newOpID()
	size, statErr := s.walSize()
	if statErr == nil && size > walLimitBytes {
		s.startToolCheckpoint(epoch)
	}
	free, sampleErr := s.disk.free(s.dir)
	s.disk.mu.Lock()
	refusing := s.disk.refusing
	s.disk.mu.Unlock()
	if sampleErr != nil {
		err := &DiskLowError{Reserve: diskResume + 2*walLimitBytes}
		s.ev.limit(op, "free space measurement failed", err)
		return nil, nil, err
	}
	if refusing || free < diskResume+2*walLimitBytes {
		err := &DiskLowError{Free: free, Reserve: diskResume + 2*walLimitBytes}
		s.ev.limit(op, "low disk space", err)
		return nil, nil, err
	}
	if statErr != nil {
		s.ev.limit(op, "wal size", ErrBusy)
		return nil, nil, ErrBusy
	}
	if size > walLimitBytes {
		s.ev.limit(op, "wal size", ErrBusy)
		return nil, nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.writerEpoch != epoch || g.writerActive != 0 || g.writerPending != 0 {
		return nil, nil, ErrBusy
	}
	return g.enterToolLocked(ctx, true)
}

func (s *Store) walSize() (int64, error) {
	info, err := os.Stat(s.path + "-wal")
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Store) startToolCheckpoint(epoch uint64) {
	g := s.gate
	g.mu.Lock()
	if g.writerEpoch != epoch || g.writerActive != 0 || g.writerPending != 0 || time.Since(g.lastCheckpoint) < 30*time.Second {
		g.mu.Unlock()
		return
	}
	release, _, err := g.enterToolLocked(context.Background(), false)
	if err == nil {
		g.lastCheckpoint = time.Now()
	}
	g.mu.Unlock()
	if err != nil {
		return
	}
	go func() {
		defer release()
		ctx, cancel := context.WithTimeout(context.Background(), operationLimit)
		defer cancel()
		conn, err := s.writer.Conn(ctx)
		if err != nil {
			s.report(newOpID(), err)
			return
		}
		defer conn.Close()
		var busy, log, checkpointed int
		if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil {
			s.report(newOpID(), err)
		}
	}()
}
