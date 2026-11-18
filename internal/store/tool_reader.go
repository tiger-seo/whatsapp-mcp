package store

import (
	"context"
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
	op := newOpID()
	size, statErr := s.walSize()
	if statErr == nil && size > walLimitBytes {
		s.startToolCheckpoint()
	}
	err, measured := s.disk.checkMeasured()
	s.disk.mu.Lock()
	free, refusing := s.disk.last, s.disk.refusing
	s.disk.mu.Unlock()
	if err == nil && refusing {
		err = &DiskLowError{Free: free, Reserve: diskReserve}
	}
	if err != nil {
		s.ev.limit(op, "low disk space", err)
		return nil, nil, err
	} else if measured {
		if free < diskResume+2*walLimitBytes {
			err := &DiskLowError{Free: free, Reserve: diskResume + 2*walLimitBytes}
			s.ev.limit(op, "low disk space", err)
			return nil, nil, err
		}
	}
	if statErr != nil {
		s.ev.limit(op, "wal size", ErrBusy)
		return nil, nil, ErrBusy
	}
	if size > walLimitBytes {
		s.ev.limit(op, "wal size", ErrBusy)
		return nil, nil, ErrBusy
	}
	release, opCtx, err := s.gate.enterTool(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	// A writer can finish between the first stat and gate admission. Recheck
	// after reserving the place so either this check or the writer sees it.
	size, statErr = s.walSize()
	if statErr != nil || size > walLimitBytes {
		release()
		if statErr == nil {
			s.startToolCheckpoint()
		}
		s.ev.limit(op, "wal size", ErrBusy)
		return nil, nil, ErrBusy
	}
	return release, opCtx, nil
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

func (s *Store) startToolCheckpoint() {
	release, _, err := s.gate.enterTool(context.Background(), false)
	if err != nil {
		return
	}
	s.gate.mu.Lock()
	if time.Since(s.gate.lastCheckpoint) < 30*time.Second {
		s.gate.mu.Unlock()
		release()
		return
	}
	s.gate.lastCheckpoint = time.Now()
	s.gate.mu.Unlock()
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

func (s *Store) checkToolWAL() {
	size, err := s.walSize()
	if err == nil && size <= 2*walLimitBytes {
		return
	}
	s.gate.mu.Lock()
	if s.gate.toolCancel != nil {
		s.gate.toolCancel()
	}
	s.gate.mu.Unlock()
	if err != nil {
		s.ev.limit(newOpID(), "wal size", ErrBusy)
	}
}
