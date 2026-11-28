package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow/types"
)

// sendChildEvents names the directory of a send child process: the test process, started again, runs the command
// with a synthetic sending owner and reports what happened in files there.
const sendChildEvents = "WA_TEST_SEND_CHILD_EVENTS"

const sendChildPhone = "491511000001"

// sendingOwner is a synthetic owner with the fake send port.
type sendingOwner struct {
	*scriptedOwner
	*wa.FakeSendPort
	note func(string)
}

func (o *sendingOwner) BeginStop() {
	o.note("owner_stop")
	o.scriptedOwner.BeginStop()
}

// runSendChild runs the command in a process of its own, on the real standard streams. A send of the text
// "hold" waits for the file "release"; an upload of a file whose content starts with "hold" waits to be cancelled.
func runSendChild(events string, args []string) int {
	var steps []string
	note := func(step string) {
		steps = append(steps, step)
		_ = os.WriteFile(filepath.Join(events, "steps"), []byte(strings.Join(steps, "\n")), 0o600)
		_ = os.WriteFile(filepath.Join(events, step), nil, 0o600)
	}
	owner := &sendingOwner{scriptedOwner: newScriptedOwner(), note: note,
		FakeSendPort: wa.NewFakeSendPort(wa.SendIdentity{Phone: types.NewJID("490000000001", types.DefaultUserServer)})}
	owner.SetRegistration(sendChildPhone, wa.Registration{Registered: true})
	delivering := func(ctx context.Context) error {
		sent := owner.Calls().Deliveries
		if sent[len(sent)-1].Message.GetConversation() != "synthetic-secret-hold" {
			return nil
		}
		note("send_held")
		for {
			if _, err := os.Stat(filepath.Join(events, "release")); err == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	owner.SetHook(wa.FakeDeliver, delivering)
	owner.SetHook(wa.FakeUpload, func(ctx context.Context) error {
		uploads := owner.Calls().Uploads
		if !bytes.HasPrefix(uploads[len(uploads)-1].Data, []byte("hold")) {
			return nil
		}
		note("upload_held")
		<-ctx.Done()
		note("upload_cancelled")
		return ctx.Err()
	})
	newOwner = func(ownerDeps) (*ownerHandle, error) { return &ownerHandle{owner: owner}, nil }
	code := run(args, os.Stdin, os.Stdout, os.Stderr, nil)
	calls := owner.Calls()
	note("sends=" + strconv.Itoa(len(calls.Deliveries)) + " uploads=" + strconv.Itoa(len(calls.Uploads)))
	return code
}

func awaitFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s from the child", filepath.Base(path))
		}
	}
}

func TestSendStdioProtocolAuditAndShutdown(t *testing.T) {
	const (
		message = "synthetic-secret-message"
		caption = "synthetic-secret-caption"
		name    = "synthetic-secret-name.pdf"
		held    = "synthetic-secret-held.pdf"
		content = "synthetic-secret-file-bytes"
	)
	for _, level := range []string{"debug", "info"} {
		t.Run(level, func(t *testing.T) {
			dir, events := privateDir(t), t.TempDir()
			outbox := filepath.Join(dir, "outbox")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "stdio", "--data-dir", dir, "--log-level", level, "--send-allowed-recipients", sendChildPhone)
			child.Env = append(os.Environ(), sendChildEvents+"="+events)
			stdin, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			child.Stderr = &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Process.Kill() }()
			out := bufio.NewReader(stdout)
			write := func(frame string) {
				t.Helper()
				if _, err := io.WriteString(stdin, frame+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			// reply reads one line of stdout, which must be a protocol reply to id.
			reply := func(id int) string {
				t.Helper()
				line, err := out.ReadBytes('\n')
				var m struct {
					JSONRPC string
					ID      int
					Result  json.RawMessage
				}
				if err != nil || json.Unmarshal(line, &m) != nil || m.JSONRPC != "2.0" || m.ID != id {
					t.Fatalf("stdout line %q, %v; want the reply to %d", line, err, id)
				}
				return string(m.Result)
			}
			call := func(id int, tool string, arguments map[string]any) {
				t.Helper()
				frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
					"params": map[string]any{"name": tool, "arguments": arguments}})
				if err != nil {
					t.Fatal(err)
				}
				write(string(frame))
			}
			exchange := func(id int, tool string, arguments map[string]any) string {
				t.Helper()
				call(id, tool, arguments)
				return reply(id)
			}
			write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
			reply(1)
			write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			for file, body := range map[string]string{name: content, held: "hold " + content} {
				if err := os.WriteFile(filepath.Join(outbox, file), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			text := func(recipient, body string) map[string]any {
				return map[string]any{"recipient": recipient, "message": body}
			}
			file := func(file string) map[string]any {
				return map[string]any{"recipient": sendChildPhone, "media_path": filepath.Join(outbox, file), "caption": caption}
			}
			if got := exchange(2, "send_message", text(sendChildPhone, message)); !strings.Contains(got, `"sent":true`) {
				t.Fatalf("send_message = %s", got)
			}
			if got := exchange(3, "send_file", file(name)); !strings.Contains(got, `"media_type":"document"`) {
				t.Fatalf("send_file = %s", got)
			}
			if got := exchange(4, "send_message", text("491511000009", message)); !strings.Contains(got, `"not_permitted"`) {
				t.Fatalf("refused = %s", got)
			}

			// During a send a read is refused as busy; afterwards it works.
			call(5, "send_message", text(sendChildPhone, "synthetic-secret-hold"))
			awaitFile(t, filepath.Join(events, "send_held"))
			if got := exchange(6, "list_chats", map[string]any{}); !strings.Contains(got, `"busy"`) {
				t.Fatalf("a read during a send = %s", got)
			}
			if err := os.WriteFile(filepath.Join(events, "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := reply(5); !strings.Contains(got, `"sent":true`) {
				t.Fatalf("held send = %s", got)
			}
			if got := exchange(7, "list_chats", map[string]any{}); strings.Contains(got, `"isError":true`) {
				t.Fatalf("a read after the send = %s", got)
			}

			// A shutdown during an upload cancels it.
			call(8, "send_file", file(held))
			awaitFile(t, filepath.Join(events, "upload_held"))
			start := time.Now()
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			// Whatever else reaches stdout is protocol too.
			rest, err := io.ReadAll(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(rest), []byte("\n")) {
				if len(line) != 0 && (!json.Valid(line) || !bytes.Contains(line, []byte(`"jsonrpc":"2.0"`))) {
					t.Errorf("stdout holds %q", line)
				}
			}
			if err := child.Wait(); err != nil || time.Since(start) > 5*time.Second {
				t.Fatalf("exit %v after %v: %s", err, time.Since(start), stderr.String())
			}
			recorded, err := os.ReadFile(filepath.Join(events, "steps"))
			if err != nil {
				t.Fatal(err)
			}
			// The MCP session's stop ends the call before the owner is stopped, and nothing was sent after it.
			steps := strings.Split(string(recorded), "\n")
			cancelled, stopped := slices.Index(steps, "upload_cancelled"), slices.Index(steps, "owner_stop")
			if cancelled < 0 || stopped < cancelled || !slices.Contains(steps, "sends=3 uploads=2") {
				t.Errorf("steps = %q", steps)
			}
			if entries, err := os.ReadDir(filepath.Join(dir, "send-work")); err != nil || len(entries) != 0 {
				t.Errorf("working files left: %v, %v", entries, err)
			}
			requireReleasedLock(t, dir)

			got := stderr.String()
			// One event for each of the five attempts; the refused read is not a send.
			if attempts := strings.Count(got, "event=send_attempt"); attempts != 5 {
				t.Errorf("%d audit events, want 5:\n%s", attempts, got)
			}
			for _, want := range []string{
				"tool=send_message recipient_kind=phone outcome=sent reason=ok media_type=none",
				"tool=send_file recipient_kind=phone outcome=sent reason=ok media_type=document",
				"tool=send_message recipient_kind=phone outcome=not_sent reason=not_permitted media_type=none",
				"tool=send_file recipient_kind=phone outcome=not_sent reason=cancelled media_type=document",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("stderr lacks %q:\n%s", want, got)
				}
			}
			for _, value := range []string{sendChildPhone, "491511000009", message, caption, name, held, content, "synthetic-secret"} {
				if strings.Contains(got, value) {
					t.Errorf("stderr at %s holds %q:\n%s", level, value, got)
				}
			}
			// A configured path is dumped at debug level at most.
			if level == "info" && strings.Contains(got, "outbox") {
				t.Errorf("stderr at info names the outbox:\n%s", got)
			}
		})
	}
}
