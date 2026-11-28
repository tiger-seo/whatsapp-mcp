package media

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
	"go.mau.fi/whatsmeow/types"
)

type offline struct{}

func (offline) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
}

// connectedOwner pairs the real owner with the simulator and returns it connected, with its store.
func connectedOwner(t *testing.T, ctx context.Context, data string) (wa.Owner, *store.Store) {
	t.Helper()
	st, err := store.Open(ctx, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	srv := wasim.Start(t)
	phone, err := wasim.NewPhone(types.NewJID(ownPhone, types.DefaultUserServer), types.NewJID(ownPhone, types.HiddenUserServer))
	if err != nil {
		t.Fatal(err)
	}
	owner := wa.NewOwner(wa.OwnerConfig{Store: st, Versions: wa.NewVersionProviderWithTransport(ctx, offline{}),
		Sessions: func(cfg wa.SessionConfig) (wa.SessionHandle, error) {
			cfg.PreLoginHTTP, cfg.WebsocketHTTP, cfg.MediaHTTP = srv.Client(), srv.Client(), srv.Client()
			return wa.OpenSession(cfg)
		}})
	if err := owner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.BeginStop(); <-owner.Done() })
	if _, err := owner.RequestPairing(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendPairDevice(ctx, "ref-1"); err != nil {
		t.Fatal(err)
	}
	var code string
	waitFor(t, "the QR code", func() bool {
		snap, ok := owner.PairingSnapshot()
		code = snap.Code
		return ok && code != ""
	})
	link, err := phone.Scan(code)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendPairSuccess(ctx, phone, link); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Expect(ctx, wasim.IsPairConfirmation); err != nil {
		t.Fatal(err)
	}
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	next, err := srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.SendSuccess(ctx, phone.LID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the connection", func() bool { return owner.Snapshot().Connected })
	return owner, st
}

// A stop while the file stage is stuck: the request's cancellation, which the MCP session's stop causes, ends the
// call at once, no send call is in flight, and the owner's stop passes its quiet check.
func TestSendStopWithAStuckStageIsQuiet(t *testing.T) {
	for _, step := range []string{"resolve", "open", "read"} {
		t.Run(step, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			rig := newSendRig(t)
			owner, st := connectedOwner(t, ctx, rig.data)
			port, ok := owner.(wa.SendPort)
			if !ok {
				t.Fatal("the owner is not a send port")
			}
			svc, err := NewService(Options{Port: port, Store: st, Limits: rig.limits, Clock: SystemClock{},
				Recipients: []string{"*"}, Dirs: rig.svc.dirs, DataDir: rig.data})
			if err != nil {
				t.Fatal(err)
			}
			rig.svc = svc
			held := holdStage(rig, step)
			defer close(held.release)
			req := rig.file(ToolFile, "a.jpg", []byte("image"))
			req.Recipient, req.Admitted = "100000000005@lid", time.Now()
			request, stopSession := context.WithCancel(ctx)
			done := make(chan *Error, 1)
			go func() {
				_, err := svc.Send(request, req)
				done <- err
			}()
			select {
			case <-held.reached:
			case err := <-done:
				t.Fatalf("the call ended before the stage: %v", err)
			}
			stopSession()
			select {
			case err := <-done:
				wantCode(t, err, CodeCancelled)
			case <-time.After(time.Second):
				t.Fatal("the call did not return within a second")
			}
			start := time.Now()
			owner.BeginStop()
			select {
			case <-owner.Done():
			case <-ctx.Done():
				t.Fatal("the owner did not stop")
			}
			if err := owner.StopError(); err != nil {
				t.Fatalf("the stop was not quiet: %v", err)
			}
			if health := owner.Health(); !health.QuietPassed || health.QuietReason != "" {
				t.Errorf("health = %+v", health)
			}
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("the stop took %v", took)
			}
		})
	}
}
