package main

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// hasControl reports a terminal control character other than a plain newline.
func hasControl(s string) (rune, bool) {
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || r == 0x2028 || r == 0x2029) {
			return r, true
		}
	}
	return 0, false
}

// Text that comes from outside the program (an owner or upstream error) must not reach the
// terminal with control sequences: escape, carriage return, bell, backspace, C1 and line separators.
func TestPairDiagnosticsNeverCarryTerminalControls(t *testing.T) {
	hostile := []string{
		"\x1b[2J\x1b[Hevil",
		"fake\rwhatsapp-mcp: pair: success",
		"bell\x07 and backspace\x08\x08",
		"c1\u009b31mred",
		"sep\u2028arator",
		"tab\there",
	}
	for _, text := range hostile {
		t.Run(strings.NewReplacer("\x1b", "ESC", "\r", "CR", "\x07", "BEL", "\x08", "BS", "\u009b", "CSI", "\u2028", "LS", "\t", "TAB").Replace(text), func(t *testing.T) {
			so := newScriptedOwner()
			so.request = func(int64) (wa.PairingState, error) { return wa.PairingState{}, errors.New(text) }
			pr := startPairWithInput(t, so, nil, strings.NewReader(""), "--data-dir", privateDir(t))
			if code := pr.waitExit(t, 10*time.Second); code != exitError {
				t.Fatalf("exit %d", code)
			}
			if r, bad := hasControl(pr.output() + pr.diagnostics()); bad {
				t.Fatalf("output carries control %U:\n%q", r, pr.diagnostics())
			}
		})
	}
}

// The QR drawing is the one deliberate terminal output, and it is built only from fixed block and
// space characters plus newlines, whatever the code contains.
func TestPairQRDrawingUsesOnlyFixedCharacters(t *testing.T) {
	so := newScriptedOwner()
	so.request = func(int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Code: "2@SYNTHETIC,ABCD==,x", Result: wa.PairingActive}, true)
		return so.window, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
			so.mu.Lock()
			active := so.window.Result == wa.PairingActive
			so.mu.Unlock()
			if active {
				time.Sleep(100 * time.Millisecond)
				so.setWindow(wa.PairingState{Result: wa.PairingCancelled}, true)
				return
			}
		}
	}()
	pr := startPairWithInput(t, so, nil, strings.NewReader(""), "--data-dir", privateDir(t))
	pr.waitExit(t, 10*time.Second)
	<-done
	out := pr.output()
	if !strings.ContainsAny(out, "█▀▄") {
		t.Fatalf("no QR drawing:\n%q", out)
	}
	for _, r := range out {
		switch r {
		case '█', '▀', '▄', ' ', '\n':
		default:
			t.Fatalf("QR output contains %U", r)
		}
	}
}
