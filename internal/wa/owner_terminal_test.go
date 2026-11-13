package wa

import (
	"context"
	"testing"
	"time"
)

// terminalFakeSession is a fake generation that can report an ingest failure and a receive overload.
type terminalFakeSession struct {
	fakeSession
	ingestFailed chan struct{}
	overload     string
}

func (f *terminalFakeSession) IngestFailed() <-chan struct{} { return f.ingestFailed }
func (f *terminalFakeSession) ReceiveOverload() string       { return f.overload }

func startTerminalOwner(t *testing.T, f *terminalFakeSession) *connectionOwner {
	t.Helper()
	o := NewOwner(OwnerConfig{
		Versions: bundledProvider(context.Background()),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return f, nil },
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		o.BeginStop()
		<-o.Done()
	})
	waitFor(t, func() bool { return f.connectN.Load() == 1 })
	return o
}

func newTerminalFake() *terminalFakeSession {
	return &terminalFakeSession{
		fakeSession:  fakeSession{identity: true, faultCh: make(chan struct{}), script: func(int64, context.Context) error { return nil }},
		ingestFailed: make(chan struct{}),
	}
}

func TestOwnerTerminalClosesOnIngestFailure(t *testing.T) {
	f := newTerminalFake()
	o := startTerminalOwner(t, f)
	select {
	case <-o.Terminal():
		t.Fatal("Terminal closed before any failure")
	default:
	}
	if o.Health().IngestFailed {
		t.Fatal("IngestFailed set before any failure")
	}
	close(f.ingestFailed)
	select {
	case <-o.Terminal():
	case <-time.After(5 * time.Second):
		t.Fatal("Terminal did not close on an ingest failure")
	}
	if !o.Health().IngestFailed {
		t.Error("Health().IngestFailed = false after the failure")
	}
	// Closing once is enough; a second read does not panic or block.
	<-o.Terminal()
}

// A device storage fault keeps feature 003's behaviour: the generation retires and the phase says so, but the
// command is not told to stop.
func TestOwnerTerminalIgnoresADeviceStorageFault(t *testing.T) {
	f := newTerminalFake()
	o := startTerminalOwner(t, f)
	close(f.faultCh)
	waitFor(t, func() bool { return o.Health().StorageFault })
	select {
	case <-o.Terminal():
		t.Fatal("a device storage fault closed Terminal")
	case <-time.After(100 * time.Millisecond):
	}
	if o.Health().IngestFailed {
		t.Error("a device storage fault was reported as an ingest failure")
	}
}

func TestOwnerHealthReportsReceiveOverload(t *testing.T) {
	f := newTerminalFake()
	o := startTerminalOwner(t, f)
	if got := o.Health().ReceiveOverload; got != "" {
		t.Fatalf("ReceiveOverload = %q before any overload", got)
	}
	f.overload = "item_limit"
	f.cfg.Dispatch(f.cfg.Epoch, SessionReceiveOverload)
	f.cfg.Dispatch(f.cfg.Epoch, SessionDisconnected)
	if got := o.Health().ReceiveOverload; got != "item_limit" {
		t.Fatalf("ReceiveOverload = %q, want item_limit", got)
	}
}
