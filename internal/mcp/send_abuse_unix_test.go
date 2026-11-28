//go:build unix

package mcp

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A pipe in the outbox is refused without waiting for a writer.
func TestSendAbuseSpecialFiles(t *testing.T) {
	r := newSendRig(t)
	stored := r.storedMessages()
	client := r.client()
	for _, name := range []string{"pipe.pdf", "pipe.mp3"} {
		path := filepath.Join(r.outbox, name)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Skipf("not verified: cannot create a FIFO here: %v", err)
		}
		for _, tool := range []string{"send_file", "send_audio_message"} {
			if tool == "send_audio_message" && filepath.Ext(name) != ".mp3" {
				continue
			}
			if reply := call(t, client, tool, sendFile(path)); reply.code() != "file_refused" {
				t.Errorf("%s %s = %+v", tool, name, reply.structured)
			}
		}
	}
	r.wantNoEffect(stored)
	if len(r.port.Calls().Uploads) != 0 {
		t.Error("a pipe was uploaded")
	}
}
