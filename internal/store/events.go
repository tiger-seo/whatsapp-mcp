package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// events logs the store's lifecycle through the process logger. Every method takes typed fields only, never a
// free-form value, so message text, names, keys, SQL and raw driver errors cannot reach a log call.
type events struct {
	log *slog.Logger
	now func() time.Time

	mu         sync.Mutex
	lastLimit  map[string]time.Time
	suppressed map[string]int
}

func newEvents(log *slog.Logger) *events {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &events{log: log, now: time.Now, lastLimit: map[string]time.Time{}, suppressed: map[string]int{}}
}

// newOpID returns a random correlation ID: 8 bytes as hex.
func newOpID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

const (
	outcomeOK            = "ok"
	outcomeRefused       = "refused"
	outcomeFailed        = "failed"
	outcomeIndeterminate = "indeterminate"
)

// safePath bounds s to 512 bytes and escapes control characters and invalid UTF-8, so that a path or an
// identifier can be logged or shown without breaking the line or the terminal.
func safePath(s string) string {
	const limit = 512
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		var piece string
		switch {
		case r == utf8.RuneError && n <= 1:
			piece = `\x` + hex.EncodeToString([]byte{s[i]})
			n = 1
		case unicode.IsControl(r) || r == 0x2028 || r == 0x2029:
			if r < 0x10000 {
				piece = `\u` + hex.EncodeToString([]byte{byte(r >> 8), byte(r)})
			} else {
				piece = `\U` + hex.EncodeToString([]byte{byte(r >> 24), byte(r >> 16), byte(r >> 8), byte(r)})
			}
		default:
			piece = s[i : i+n]
		}
		if b.Len()+len(piece) > limit {
			b.WriteString("...")
			break
		}
		b.WriteString(piece)
		i += n
	}
	return b.String()
}

func (e *events) emit(level slog.Level, name, op, outcome, code string, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, 4+len(attrs))
	all = append(all, slog.String("event", name), slog.String("op_id", op), slog.String("outcome", outcome), slog.String("code", code))
	all = append(all, attrs...)
	e.log.LogAttrs(context.Background(), level, name, all...)
}

func (e *events) open(op, path string) {
	e.emit(slog.LevelInfo, "store.open", op, outcomeOK, "ok", slog.String("path", safePath(path)))
}

func (e *events) refused(op string, err error, artifact string, schema string, observed, supported int) {
	attrs := []slog.Attr{slog.String("artifact", safePath(artifact))}
	if schema != "" {
		attrs = append(attrs, slog.String("schema", schema), slog.Int("observed", observed), slog.Int("supported", supported))
	}
	e.emit(slog.LevelError, "store.refused", op, outcomeRefused, codeOf(err), attrs...)
}

func (e *events) preflight(op string, fresh bool, messages, device, compat int) {
	e.emit(slog.LevelInfo, "store.preflight", op, outcomeOK, "ok", slog.Bool("fresh", fresh),
		slog.Int("messages_version", messages), slog.Int("device_version", device), slog.Int("device_compat", compat))
}

func (e *events) upgrade(op string, from, to int, err error) {
	level, outcome := slog.LevelInfo, outcomeOK
	if err != nil {
		level, outcome = slog.LevelError, outcomeFailed
	}
	e.emit(level, "store.upgrade", op, outcome, codeOf(err), slog.String("schema", "device"), slog.Int("from", from), slog.Int("to", to))
}

func (e *events) migrate(op string, from, to int, name string, err error) {
	level, outcome := slog.LevelInfo, outcomeOK
	if err != nil {
		level, outcome = slog.LevelError, outcomeFailed
	}
	e.emit(level, "store.migrate", op, outcome, codeOf(err), slog.String("schema", "messages"), slog.Int("from", from),
		slog.Int("to", to), slog.String("name", safePath(name)))
}

func (e *events) ready(op string, messages, device int) {
	e.emit(slog.LevelInfo, "store.ready", op, outcomeOK, "ok", slog.Int("messages_version", messages), slog.Int("device_version", device))
}

func (e *events) closed(op string, d time.Duration) {
	e.emit(slog.LevelInfo, "store.close", op, outcomeOK, "ok", slog.Int64("duration_ms", d.Milliseconds()))
}

func (e *events) ioError(op, operation string, err error) {
	e.emit(slog.LevelError, "store.io_error", op, outcomeFailed, codeOf(err), slog.String("op", operation), slog.Int("sqlite_code", sqliteCode(err)))
}

// limit logs a refusal for a limit or for busy, at most once per limit every 10 s. The next event reports how
// many were suppressed in between.
func (e *events) limit(op, name string, err error) {
	e.mu.Lock()
	now := e.now()
	if last, ok := e.lastLimit[name]; ok && now.Sub(last) < limitEventEvery {
		e.suppressed[name]++
		e.mu.Unlock()
		return
	}
	e.lastLimit[name] = now
	n := e.suppressed[name]
	e.suppressed[name] = 0
	e.mu.Unlock()
	e.emit(slog.LevelWarn, "store.limit", op, outcomeRefused, codeOf(err), slog.String("limit", name), slog.Int("suppressed", n))
}

func (e *events) emitIndeterminate(op string) {
	e.emit(slog.LevelError, "store.io_error", op, outcomeIndeterminate, "indeterminate", slog.String("op", "commit"), slog.Int("sqlite_code", 0))
}
