// Package walog bridges the pinned WhatsApp logger to the process logger without formatting upstream arguments.
package walog

import (
	"context"
	"log/slog"
	"strings"
	"unicode"

	waLog "go.mau.fi/whatsmeow/util/log"
)

type logger struct {
	log    *slog.Logger
	module string
}

// New returns a bridge using the supplied process logger; nil discards output.
func New(log *slog.Logger) waLog.Logger {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &logger{log: log}
}

func (l *logger) emit(level slog.Level, template string) {
	var safe strings.Builder
	for _, r := range template {
		if safe.Len() >= 512 {
			break
		}
		if !unicode.IsPrint(r) {
			safe.WriteByte(' ')
			continue
		}
		if safe.Len()+len(string(r)) > 512 {
			break
		}
		safe.WriteRune(r)
	}
	l.log.LogAttrs(context.Background(), level, safe.String(), slog.String("event", "whatsmeow.log"), slog.String("module", l.module))
}

func (l *logger) Debugf(template string, _ ...any) { l.emit(slog.LevelDebug, template) }
func (l *logger) Infof(template string, _ ...any)  { l.emit(slog.LevelInfo, template) }
func (l *logger) Warnf(template string, _ ...any)  { l.emit(slog.LevelWarn, template) }
func (l *logger) Errorf(template string, _ ...any) { l.emit(slog.LevelError, template) }

func (l *logger) Sub(module string) waLog.Logger {
	switch module {
	case "Database", "Upgrade", "Recv", "Send", "AppState", "Socket", "QRChannel":
	default:
		module = "other"
	}
	if l.module != "" {
		module = l.module + "/" + module
	}
	if len(module) > 128 {
		module = "other"
	}
	return &logger{log: l.log, module: module}
}
