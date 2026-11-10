package wa

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

func runGuardOf(t *testing.T, dir string) string {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	state, _, err := st.RunGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// A connected run stops, the marker is clear, and the next start reuses the saved session: no QR window, no
// registration, and it reaches the connected state again.
func TestSimulatorConnectedStopClearsTheMarkerAndTheNextStartReusesTheSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	o, _, _, st, dir, srv1 := startSavedRunWith(t, ctx, nil)
	o.BeginStop()
	<-o.Done()
	if err := o.StopError(); err != nil {
		t.Fatalf("StopError = %v", err)
	}
	st.Close(ctx)
	srv1.Close() // one simulator at a time
	if got := runGuardOf(t, dir); got != "clean" {
		t.Fatalf("marker = %q, want clean", got)
	}

	st2, err := store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close(context.Background())
	srv := wasim.Start(t)
	o2 := NewOwner(OwnerConfig{
		Store: st2, Versions: bundledProvider(ctx), ShellStartupBudget: true,
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			return OpenSession(cfg)
		},
	})
	if err := o2.Start(ctx); err != nil {
		t.Fatalf("the restart was refused: %v", err)
	}
	conn, err := srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if conn.ClientPayload.GetDevicePairingData() != nil {
		t.Error("the restart registered again instead of using the saved identity")
	}
	if err := conn.SendSuccess(ctx, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return o2.Snapshot().Connected })
	if _, ok := o2.PairingSnapshot(); ok {
		t.Error("a pairing window opened for a saved session")
	}
	o2.BeginStop()
	<-o2.Done()
}

// Late work during the open check fails it and keeps the marker unsafe, each for its own reason.
func TestSimulatorLateWorkDuringTheCheckKeepsTheMarkerUnsafe(t *testing.T) {
	cases := []struct {
		name   string
		inject func(t *testing.T, s *Session)
	}{
		{"store call after the barrier", func(t *testing.T, s *Session) {
			if err := s.dev.Identities.PutIdentity(context.Background(), "491511234567:0", [32]byte{1}); err == nil {
				t.Error("a store call after the barrier succeeded")
			}
		}},
		{"media request after the fence", func(t *testing.T, s *Session) {
			req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:1/", nil)
			if resp, err := s.mediaClient.Do(req); err == nil {
				resp.Body.Close()
				t.Error("a media request after the fence succeeded")
			}
		}},
		{"late connect", func(t *testing.T, s *Session) { _ = s.Connect(context.Background()) }},
		{"late repair call", func(t *testing.T, s *Session) { _ = s.RunRepairs(context.Background()) }},
		{"late qr call", func(t *testing.T, s *Session) { _, _ = s.GetQRChannel(context.Background()) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var sess *Session
			o, _, s, st, dir, srv := startSavedRunWith(t, ctx, func(step string) {
				if step == "lib_cancelled" && sess != nil {
					c.inject(t, sess)
				}
			})
			sess = s
			dials := srv.Dials()
			o.BeginStop()
			<-o.Done()
			if o.StopError() == nil {
				t.Error("late work during the check reported a successful stop")
			}
			if srv.Dials() != dials {
				t.Error("the simulator saw a new dial from the retired generation")
			}
			st.Close(ctx)
			if got := runGuardOf(t, dir); got != "unsafe" {
				t.Fatalf("marker = %q, want unsafe", got)
			}
		})
	}
}

// A remote disconnect after retirement and an ordinary receipt do not fail the check.
func TestSimulatorRemoteDisconnectAndReceiptDoNotFailTheCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var sess *Session
	o, _, s, st, dir, _ := startSavedRunWith(t, ctx, func(step string) {
		if step == "lib_cancelled" && sess != nil {
			sess.handleEvent(&eventsDisconnected)
			sess.handleEvent(&eventsReceipt)
		}
	})
	sess = s
	o.BeginStop()
	<-o.Done()
	if err := o.StopError(); err != nil {
		t.Fatalf("StopError = %v", err)
	}
	st.Close(ctx)
	if got := runGuardOf(t, dir); got != "clean" {
		t.Fatalf("marker = %q, want clean", got)
	}
}

var (
	eventsDisconnected = events.Disconnected{}
	eventsReceipt      = events.Receipt{}
)
