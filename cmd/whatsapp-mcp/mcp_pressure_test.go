package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

type pressureOutput struct {
	io.Writer
	entered chan struct{}
	release chan struct{}
}

func (w *pressureOutput) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("held-output-fixture")) {
		select {
		case <-w.entered:
		default:
			close(w.entered)
		}
		<-w.release
	}
	return w.Writer.Write(b)
}

type pressureAudit chan struct{}

func (w pressureAudit) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("tool=get_chat category=busy")) {
		select {
		case w <- struct{}{}:
		default:
		}
	}
	return len(b), nil
}

func TestMCPIngestPressure(t *testing.T) {
	dir := privateDir(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	owner := newScriptedOwner()
	opened := make(chan *store.Store, 1)
	original := newOwner
	defer func() { newOwner = original }()
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		opened <- deps.store
		return &ownerHandle{owner: owner}, nil
	}
	p := newStdioPeer(t)
	out := &pressureOutput{Writer: p.stdout, entered: make(chan struct{}), release: make(chan struct{})}
	audit := make(pressureAudit, 1)
	release := sync.OnceFunc(func() { close(out.release) })
	done := make(chan int, 1)
	go func() { done <- run([]string{"stdio", "--data-dir", dir}, p.stdin, out, audit, nil) }()
	defer func() {
		release()
		p.close()
		awaitStdio(t, done, "command cleanup")
	}()
	st := awaitStdio(t, opened, "command store")
	p.initialize(t)
	const jid = "15550000001@s.whatsapp.net"
	base := time.Unix(1700000000, 0)
	if err := st.StoreMessage(ctx, store.Message{ChatJID: jid, SenderJID: jid, ID: "seed", Time: base, Text: "held-output-fixture"}); err != nil {
		t.Fatal(err)
	}
	const call = `"method":"tools/call","params":{"name":"get_chat","arguments":{"chat_jid":"` + jid + `"}}`
	p.send(t, `{"jsonrpc":"2.0","id":2,`+call+`}`)
	awaitStdio(t, out.entered, "held tool reply")
	p.send(t, `{"jsonrpc":"2.0","id":3,`+call+`}`)
	awaitStdio(t, audit, "admission denial while stdout is held")

	// A full output pump must not retain the SQLite snapshot or stall ingest.
	const batches, messages = 3, 16
	writes := make(chan error, 1)
	text := strings.Repeat("a", 64<<10)
	go func() {
		for batch := range batches {
			rows := make([]store.Message, 0, messages)
			for i := range messages {
				n := batch*messages + i + 1
				rows = append(rows, store.Message{ChatJID: jid, SenderJID: jid, ID: fmt.Sprintf("ingest-%02d", n), Time: base.Add(time.Duration(n) * time.Second), Text: text})
			}
			if err := st.StoreMessages(ctx, rows); err != nil {
				writes <- err
				return
			}
		}
		writes <- nil
	}()
	if err := awaitStdio(t, writes, "ingest while stdout is held"); err != nil {
		t.Fatal(err)
	}
	release()
	if r := p.reply(t, 2); !bytes.Contains(r, []byte("held-output-fixture")) {
		t.Fatal("in-flight reply changed while held")
	}
	if r := p.reply(t, 3); !bytes.Contains(r, []byte(`"code":"busy"`)) {
		t.Fatalf("competing tool did not receive busy: %s", r)
	}
	p.send(t, `{"jsonrpc":"2.0","id":4,`+call+`}`)
	if r := p.reply(t, 4); !bytes.Contains(r, []byte(`"id":"ingest-48"`)) {
		t.Fatal("next tool did not see the final ingested message")
	}
	if owner.Snapshot().Connected || owner.requestN.Load() != 0 {
		t.Fatal("pressure fixture triggered account work")
	}
	_ = p.input.Close()
	code := awaitStdio(t, done, "normal EOF exit under pressure")
	done <- code
	if code != exitOK {
		t.Fatalf("command exit %d", code)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
}
