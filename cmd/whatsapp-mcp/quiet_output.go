package main

import (
	"errors"
	"fmt"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// reportShellStop prints the one human line that explains a shell's stop: the category of a failed quiet-stop
// check, or that the saved session can be reused after a passed one. It carries the category only; the counts are
// in the quiet_stop_failed event.
func reportShellStop(kind string, owner wa.Owner, stopErr error, s *streams) {
	h := owner.Health()
	switch {
	case stopErr != nil && errors.Is(stopErr, store.ErrUnsafeRun) && h.QuietReason != "":
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s: offline recovery required (%s)\n", kind, h.QuietReason)
	case stopErr == nil && h.QuietPassed:
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s: stopped. The saved session can be reused.\n", kind)
	}
}

// quietSuffix names the failed check's category for a pair line, or is empty when no check failed.
func quietSuffix(owner wa.Owner) string {
	if reason := owner.Health().QuietReason; reason != "" {
		return " (" + reason + ")"
	}
	return ""
}
