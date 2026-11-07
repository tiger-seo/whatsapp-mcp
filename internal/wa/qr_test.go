package wa

import (
	"errors"
	"strings"
	"testing"
)

func TestQRRenderingIsDeterministicWithQuietZone(t *testing.T) {
	const code = "2@synthetic-code,ABCDEF1234567890==,first"
	first, err := RenderTerminal(code)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := RenderTerminal(code)
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if first != second {
		t.Error("the same code rendered differently")
	}
	// A fixed quiet zone surrounds the modules; the border rows and columns are blank.
	lines := strings.Split(strings.TrimSuffix(first, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("rendering has %d lines", len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "  ") || !strings.HasSuffix(line, "  ") {
			t.Errorf("line %q lacks the quiet-zone columns", line)
		}
	}
	for _, i := range []int{0, len(lines) - 1} {
		if strings.TrimSpace(lines[i]) != "" {
			t.Errorf("line %d is not a blank quiet-zone row", i)
		}
	}
	for _, line := range lines {
		for _, r := range line {
			if r != ' ' && r != '▀' && r != '▄' && r != '█' {
				t.Fatalf("rendering used non-module character %q", r)
			}
		}
	}
	if other, err := RenderTerminal("2@different-payload"); err != nil {
		t.Fatalf("render: %v", err)
	} else if other == first {
		t.Error("different codes rendered identically")
	}
}

func TestQRBoundedAndInvalidInputs(t *testing.T) {
	if _, err := RenderTerminal(""); !errors.Is(err, ErrQRCodeInvalid) {
		t.Errorf("empty code = %v, want ErrQRCodeInvalid", err)
	}
	if _, err := RenderTerminal(strings.Repeat("a", codeMaxBytes+1)); !errors.Is(err, ErrQRCodeTooLong) {
		t.Errorf("oversize code = %v, want ErrQRCodeTooLong", err)
	}
	// A payload within the byte bound but beyond the QR capacity must fail at the encoder, and the
	// error must stay category-only.
	oversize := strings.Repeat("a", 3500)
	_, err := RenderTerminal(oversize)
	if !errors.Is(err, ErrQRCodeEncode) {
		t.Errorf("encoder failure = %v, want ErrQRCodeEncode", err)
	}
	if strings.Contains(err.Error(), oversize) {
		t.Error("the encoder error leaked the payload")
	}
}

func TestQRRejectsUnsupportedReplacement(t *testing.T) {
	// A valid state cannot be replaced by invalid input: the renderer refuses before producing any
	// representation, and every error is distinguishable.
	for _, tc := range []struct {
		code string
		want error
	}{
		{"", ErrQRCodeInvalid},
		{strings.Repeat("a", codeMaxBytes+1), ErrQRCodeTooLong},
	} {
		_, err := RenderTerminal(tc.code)
		if !errors.Is(err, tc.want) {
			t.Errorf("render(%q) = %v, want %v", tc.code, err, tc.want)
		}
	}
}
