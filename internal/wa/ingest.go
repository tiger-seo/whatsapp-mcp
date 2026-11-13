package wa

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// ingestWorkerWait is how long retirement waits for the ingest workers beside the repair pass. Whatever still
// runs then is joined after the storage barrier, before the library context is cancelled.
const ingestWorkerWait = 2 * time.Second

// ingestClient is the part of the client library that ingest uses. It has no send, pairing or connect method, so
// nothing ingest does can send a message or start a connection.
type ingestClient interface {
	// OwnJID is the account's phone JID without a device part; empty before pairing.
	OwnJID() types.JID
	ParseWebMessage(chat types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error)
	DownloadHistorySync(ctx context.Context, notif *waE2E.HistorySyncNotification, synchronousStorage bool) (*waHistorySync.HistorySync, error)
	GetGroupInfo(ctx context.Context, jid types.JID) (*types.GroupInfo, error)
}

// ingestStore is the part of the store that ingest uses. Tests put a wrapper around the real store to make a
// write fail or wait.
type ingestStore interface {
	StoreMessage(ctx context.Context, m store.Message) error
	StoreMessages(ctx context.Context, msgs []store.Message) error
	UpdateChats(ctx context.Context, updates []store.ChatUpdate) error
}

// IngestConfig is the construction input of one generation's ingest component.
type IngestConfig struct {
	Store  *store.Store
	Client ingestClient
	Log    *slog.Logger
	// Lib is derived from the generation's library context, never from its work context: every library call and
	// store write of the workers runs under it, and only the last retirement step cancels it.
	Lib context.Context
	// Live is called after a live message was committed; nil in this feature.
	Live func(store.Message)
	// Failed is called once, when ingest becomes terminal.
	Failed func()

	// store replaces Store when set; tests only.
	store ingestStore
}

// IngestCounters are one generation's counts. They only grow.
type IngestCounters struct {
	Stored, Ignored, Rejected, Unavailable, DroppedMessages, HistoryIncomplete uint64
	LookupsDropped, HintsDropped, Merged, MergeFailed                          uint64
}

type ingestState int32

const (
	ingestRunning ingestState = iota
	ingestFailed
	ingestRetiring
	ingestStopped
)

func (s ingestState) String() string {
	switch s {
	case ingestRunning:
		return "running"
	case ingestFailed:
		return "failed"
	case ingestRetiring:
		return "retiring"
	case ingestStopped:
		return "stopped"
	}
	return "unknown"
}

// ingest stores one generation's messages. Live messages are written on the library's event handler; history,
// group names and LID merges each have one worker. Every message write, history batch and merge holds the ingest
// lock, so they are serialised.
type ingest struct {
	cfg IngestConfig
	log *slog.Logger
	st  ingestStore

	// mu guards state, halted and worker admission.
	mu     sync.Mutex
	state  ingestState
	halted bool
	// stop closes when the workers must stop at their next checkpoint; workersDone when they all returned.
	stop        chan struct{}
	workersDone chan struct{}
	workers     sync.WaitGroup

	// lock is the ingest lock.
	lock sync.Mutex

	stored, ignored, rejected, unavailable, dropped, historyIncomplete atomic.Uint64
	lookupsDropped, hintsDropped, merged, mergeFailed                  atomic.Uint64
}

func newIngest(cfg IngestConfig) *ingest {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Lib == nil {
		cfg.Lib = context.Background()
	}
	in := &ingest{cfg: cfg, log: cfg.Log, stop: make(chan struct{}), workersDone: make(chan struct{})}
	in.st = cfg.store
	if in.st == nil {
		in.st = cfg.Store
	}
	return in
}

// State reports the current state.
func (in *ingest) State() ingestState {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.state
}

func (in *ingest) running() bool { return in.State() == ingestRunning }

// HandleMessage stores one live message, or queues a history notification. It returns false when a message that
// should have been stored was not, so the library sends no acknowledgement for it.
func (in *ingest) HandleMessage(evt *events.Message) bool {
	if !in.running() {
		in.dropped.Add(1)
		return false
	}
	m, ok := extractMessage(evt, in.cfg.Client.OwnJID())
	if !ok {
		in.ignored.Add(1)
		return true
	}
	if err := store.ValidateMessage(m); err != nil {
		// The error names the field and never repeats its value.
		in.rejected.Add(1)
		in.log.Debug("message rejected", "event", "message_rejected", "chat", escapeLog(m.ChatJID),
			"message_id", escapeLog(m.ID), "reason", err.Error())
		return true
	}
	in.lock.Lock()
	err := in.st.StoreMessage(in.cfg.Lib, m)
	in.lock.Unlock()
	if err != nil {
		in.fail("live", err, "chat", escapeLog(m.ChatJID), "message_id", escapeLog(m.ID))
		return false
	}
	in.stored.Add(1)
	in.logReceived(m)
	if in.cfg.Live != nil {
		in.cfg.Live(m)
	}
	return true
}

// logReceived writes a stored message at debug level, the only level at which text and captions appear. Media
// keys, hashes, URLs and paths never appear.
func (in *ingest) logReceived(m store.Message) {
	if !in.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	in.log.Debug("message received", "event", "message_received", "chat", escapeLog(m.ChatJID),
		"message_id", escapeLog(m.ID), "from_me", m.FromMe, "media_type", m.MediaType, "text", m.Text,
		"caption", m.Caption)
}

// escapeLog quotes a JID or message ID for a log line, so a control character or a forged separator in a value
// another account chose cannot change the line's structure.
func escapeLog(s string) string { return strconv.QuoteToASCII(s) }

// NoteMapping reports a LID whose mapping the device store just changed: a new row (oldPhoneJID "") or a
// different phone. It never blocks.
func (in *ingest) NoteMapping(lidJID, oldPhoneJID, newPhoneJID string) {}

// NoteUnavailable counts a placeholder the server sent instead of a message; its content is not fetched.
func (in *ingest) NoteUnavailable() { in.unavailable.Add(1) }

// NoteDropped is told of a message event that the session dropped, and counted, after its retire bit. A history
// notification among them is reported as incomplete history, since the library has already sent its receipt.
func (in *ingest) NoteDropped(evt *events.Message) {
	if n := historyNotification(evt); n != nil {
		in.reportHistoryIncomplete("stopped", n)
	}
}

// historyNotification returns the history-sync notification an event carries, or nil.
func historyNotification(evt *events.Message) *waE2E.HistorySyncNotification {
	if evt == nil || evt.Message == nil {
		return nil
	}
	return evt.Message.GetProtocolMessage().GetHistorySyncNotification()
}

func (in *ingest) reportHistoryIncomplete(reason string, n *waE2E.HistorySyncNotification) {
	in.historyIncomplete.Add(1)
	in.log.Warn("history incomplete; messages may be missing", "event", "history_incomplete", "reason", reason,
		"chunk_order", n.GetChunkOrder(), "sync_type", n.GetSyncType().String())
}

// Stop tells the workers to stop at their next checkpoint and refuses new work. It is idempotent and never
// blocks.
func (in *ingest) Stop() {
	in.mu.Lock()
	if in.state == ingestRunning || in.state == ingestFailed {
		in.state = ingestRetiring
	}
	in.mu.Unlock()
	in.halt()
}

// halt closes the stop signal once and starts the join of the workers. No worker starts afterwards.
func (in *ingest) halt() {
	in.mu.Lock()
	if in.halted {
		in.mu.Unlock()
		return
	}
	in.halted = true
	close(in.stop)
	in.mu.Unlock()
	go func() {
		in.workers.Wait()
		close(in.workersDone)
	}()
}

// Wait waits for the workers to return, up to ctx. After a Stop, a completed wait ends in the stopped state.
func (in *ingest) Wait(ctx context.Context) error {
	select {
	case <-in.workersDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	in.mu.Lock()
	if in.state == ingestRetiring {
		in.state = ingestStopped
	}
	in.mu.Unlock()
	return nil
}

// goWorker starts fn as a worker unless ingest has already halted.
func (in *ingest) goWorker(fn func()) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.halted {
		return false
	}
	in.workers.Add(1)
	go func() {
		defer in.workers.Done()
		fn()
	}()
	return true
}

// stopping reports whether the workers must stop.
func (in *ingest) stopping() bool {
	select {
	case <-in.stop:
		return true
	default:
		return false
	}
}

// fail is the terminal-failure routine. Only a running component fails: once, it becomes failed, stops its
// workers, logs ingest_failed and calls Failed. An error after Stop, such as the context error the generation's
// own retirement causes, is not a failure. It reports whether this call made ingest terminal.
func (in *ingest) fail(op string, err error, attrs ...any) bool {
	in.mu.Lock()
	if in.state != ingestRunning {
		in.mu.Unlock()
		return false
	}
	in.state = ingestFailed
	in.mu.Unlock()
	in.halt()
	args := append([]any{"event", "ingest_failed", "operation", op, "error_class", errorClass(err)}, attrs...)
	args = append(args, "dropped", in.dropped.Load())
	in.log.Warn("message storage failed; messages may be missing", args...)
	if in.cfg.Failed != nil {
		in.cfg.Failed()
	}
	return true
}

// errorClass names a store error without repeating any of its text.
func errorClass(err error) string {
	switch {
	case errors.Is(err, store.ErrIndeterminate):
		return "indeterminate: the write may or may not be stored"
	case errors.Is(err, store.ErrBusy):
		return "busy"
	case errors.Is(err, store.ErrResourceLimit):
		return "resource_limit"
	case errors.Is(err, store.ErrDiskLow):
		return "disk_low"
	case errors.Is(err, store.ErrIO):
		return "io"
	case errors.Is(err, store.ErrClosed):
		return "closed"
	case errors.Is(err, store.ErrInvalid):
		return "invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "context"
	}
	return "storage"
}

// Counters returns a copy of the counts.
func (in *ingest) Counters() IngestCounters {
	return IngestCounters{
		Stored: in.stored.Load(), Ignored: in.ignored.Load(), Rejected: in.rejected.Load(),
		Unavailable: in.unavailable.Load(), DroppedMessages: in.dropped.Load(),
		HistoryIncomplete: in.historyIncomplete.Load(), LookupsDropped: in.lookupsDropped.Load(),
		HintsDropped: in.hintsDropped.Load(), Merged: in.merged.Load(), MergeFailed: in.mergeFailed.Load(),
	}
}

// logStopped writes the generation's counts once it is stopped, unless they are all zero. sessionDropped is the
// number of message events the session dropped after its retire bit, which the session counts.
func (in *ingest) logStopped(sessionDropped uint64) {
	c := in.Counters()
	c.DroppedMessages += sessionDropped
	if c == (IngestCounters{}) {
		return
	}
	in.log.Info("ingest stopped", "event", "ingest_stopped", "stored", c.Stored, "ignored", c.Ignored,
		"rejected", c.Rejected, "unavailable", c.Unavailable, "dropped", c.DroppedMessages,
		"history_incomplete", c.HistoryIncomplete, "lookups_dropped", c.LookupsDropped,
		"hints_dropped", c.HintsDropped, "merged", c.Merged, "merge_failed", c.MergeFailed)
}
