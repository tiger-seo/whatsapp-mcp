package walog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

type panicStringer struct{}

func (panicStringer) String() string { panic("log argument evaluated") }

func TestBridgeDropsArgumentsAtEveryLevel(t *testing.T) {
	var out bytes.Buffer
	logger := New(slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Debugf("debug %s", "SECRET-QR")
	logger.Infof("info %x", []byte("SECRET-KEY"))
	logger.Warnf("warning %v", "SECRET-JID")
	logger.Errorf("error %v", "SECRET-ERROR")
	logger.Infof("object %v", panicStringer{})
	text := out.String()
	for _, secret := range []string{"SECRET-QR", "SECRET-KEY", "SECRET-JID", "SECRET-ERROR"} {
		if strings.Contains(text, secret) {
			t.Errorf("log leaked %s", secret)
		}
	}
	for _, expected := range []string{"level=DEBUG", "level=INFO", "level=WARN", "level=ERROR", "event=whatsmeow.log", "debug %s"} {
		if !strings.Contains(text, expected) {
			t.Errorf("log missing %q", expected)
		}
	}
}

func TestBridgeBoundsAndSanitizesModulesAndTemplates(t *testing.T) {
	var out bytes.Buffer
	bridge := New(slog.New(slog.NewTextHandler(&out, nil)))
	bridge.Sub("Database").Sub("Upgrade").Infof("safe template %s", "SECRET")
	bridge.Sub("SECRET-DEVICE\nINJECT").Infof("line\n\x1b[31m\u202e%s", "SECRET")
	bridge.Sub(strings.Repeat("a", 5000)).(*logger).emit(slog.LevelInfo, strings.Repeat("x", 5000))
	text := out.String()
	if !strings.Contains(text, "Database/Upgrade") {
		t.Error("known module path not retained")
	}
	if strings.Contains(text, "SECRET") || strings.Contains(text, "\\nINJECT") || strings.Contains(text, "\\x1b") || strings.ContainsRune(text, '\u202e') {
		t.Errorf("unsafe module or control escaped into logs: %s", text)
	}
	if len(text) > 2048 {
		t.Errorf("unbounded log output: %d bytes", len(text))
	}
}

func TestNilLoggerIsSafe(t *testing.T) {
	New(nil).Sub("Recv").Errorf("ignored %s", "SECRET")
}
