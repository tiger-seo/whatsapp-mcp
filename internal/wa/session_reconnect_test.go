package wa

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Library reconnect is denied at the actual adapter boundary: the pinned client may not start an
// ordinary, initial or login-time Connect on its own.
func TestSessionDeniesEveryLibraryReconnectPath(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer s.Retire(context.Background())
	c := s.client
	if c.EnableAutoReconnect {
		t.Error("EnableAutoReconnect = true; ordinary pinned reconnect must be denied")
	}
	if c.InitialAutoReconnect {
		t.Error("InitialAutoReconnect = true; the initial attempt must not retry inside the library")
	}
	if !c.DisableLoginAutoReconnect {
		t.Error("DisableLoginAutoReconnect = false; the post-login reconnect must stay with the owner, which denies it")
	}
}

// A failed adapter Connect with a denying transport reaches the transport once and the library
// starts no further attempt on its own.
func TestSessionFailedConnectDoesNotRetryInsideLibrary(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	network := &countingTransport{}
	cfg := newSessionTestConfig(st, lifetime)
	cfg.PreLoginHTTP = countingClient(network)
	cfg.WebsocketHTTP = countingClient(network)
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer s.Retire(context.Background())
	if err := s.Connect(lifetime); err == nil {
		t.Fatal("connect through a refusing transport succeeded")
	}
	after := network.count()
	if after == 0 {
		t.Fatal("no request reached the transport")
	}
	time.Sleep(300 * time.Millisecond)
	if got := network.count(); got != after {
		t.Errorf("transport requests grew from %d to %d without an owner Connect", after, got)
	}
}

// The attempted-Connect latch is set immediately before the upstream call, so a blocked, failed or
// cancelled attempt all retain it, while a rejection before the boundary does not.
func TestSessionAttemptLatchCoversEveryOutcome(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		st := openWADir(t)
		lifetime, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg := newSessionTestConfig(st, lifetime)
		cfg.PreLoginHTTP = countingClient(&countingTransport{})
		cfg.WebsocketHTTP = countingClient(&countingTransport{})
		s, err := OpenSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Retire(context.Background())
		if s.UpstreamDrainUnproven() {
			t.Fatal("latch set before any Connect")
		}
		if err := s.Connect(lifetime); err == nil {
			t.Fatal("connect succeeded")
		}
		if !s.UpstreamDrainUnproven() {
			t.Error("a failed attempt did not retain the latch")
		}
	})
	t.Run("blocked then cancelled", func(t *testing.T) {
		st := openWADir(t)
		lifetime, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg := newSessionTestConfig(st, lifetime)
		cfg.PreLoginHTTP = countingClient(stallTransport{})
		cfg.WebsocketHTTP = countingClient(stallTransport{})
		s, err := OpenSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Retire(context.Background())
		attemptCtx, stop := context.WithCancel(lifetime)
		done := make(chan error, 1)
		go func() { done <- s.Connect(attemptCtx) }()
		deadline := time.Now().Add(5 * time.Second)
		for !s.UpstreamDrainUnproven() {
			if time.Now().After(deadline) {
				t.Fatal("latch not set while the call was in flight")
			}
			time.Sleep(time.Millisecond)
		}
		select {
		case <-done:
			t.Fatal("stalled connect returned before cancellation")
		default:
		}
		stop()
		if err := <-done; err == nil {
			t.Error("cancelled connect succeeded")
		}
		if !s.UpstreamDrainUnproven() {
			t.Error("a cancelled attempt cleared the latch")
		}
	})
	t.Run("rejected before the boundary", func(t *testing.T) {
		st := openWADir(t)
		lifetime, cancel := context.WithCancel(context.Background())
		defer cancel()
		network := &countingTransport{}
		cfg := newSessionTestConfig(st, lifetime)
		cfg.PreLoginHTTP = countingClient(network)
		cfg.WebsocketHTTP = countingClient(network)
		s, err := OpenSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Retire(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.Connect(lifetime); !errors.Is(err, ErrSessionRetired) {
			t.Fatalf("connect after retirement = %v, want ErrSessionRetired", err)
		}
		if s.UpstreamDrainUnproven() {
			t.Error("a rejection before the boundary set the latch")
		}
		if network.count() != 0 {
			t.Errorf("a retired generation reached the network %d times", network.count())
		}
	})
}
