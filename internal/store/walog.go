package store

import (
	"context"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// walogger forwards whatsmeow's logs into the process logger. It keeps the level, the module path and the
// format string, and drops the arguments, which can carry JIDs, keys or upstream error text.
type walogger struct {
	log    *slog.Logger
	module string
}

func newWALog(logger *slog.Logger) waLog.Logger {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &walogger{log: logger}
}

func (w *walogger) emit(level slog.Level, format string) {
	w.log.LogAttrs(context.Background(), level, format,
		slog.String("event", "whatsmeow.log"), slog.String("module", w.module))
}

func (w *walogger) Debugf(format string, _ ...any) { w.emit(slog.LevelDebug, format) }
func (w *walogger) Infof(format string, _ ...any)  { w.emit(slog.LevelInfo, format) }
func (w *walogger) Warnf(format string, _ ...any)  { w.emit(slog.LevelWarn, format) }
func (w *walogger) Errorf(format string, _ ...any) { w.emit(slog.LevelError, format) }

func (w *walogger) Sub(module string) waLog.Logger {
	if w.module != "" {
		module = w.module + "/" + module
	}
	return &walogger{log: w.log, module: module}
}
