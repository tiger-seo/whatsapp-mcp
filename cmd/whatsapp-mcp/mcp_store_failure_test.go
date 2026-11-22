package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

type storeFailureOwner struct {
	*heldOwner
	stopping chan struct{}
}

func (o *storeFailureOwner) BeginStop() {
	o.heldOwner.BeginStop()
	close(o.stopping)
}

func TestMCPStoreFailureShutdown(t *testing.T) {
	for _, name := range []string{"prelatched", "running", "eof_race", "owner_drain", "normal_eof", "startup_failure", "serve"} {
		t.Run(name, func(t *testing.T) {
			kind := "stdio"
			if name == "serve" {
				kind = "serve"
			}
			latch := make(chan struct{})
			var once sync.Once
			fail := func() { once.Do(func() { close(latch) }) }
			if name == "prelatched" || name == "serve" {
				fail()
			}
			opened := make(chan *store.Store, 1)
			originalSignal, originalOwner, originalLifecycle := toolStoreFailureSignal, newOwner, lifecycleFactory
			t.Cleanup(func() {
				toolStoreFailureSignal, newOwner, lifecycleFactory = originalSignal, originalOwner, originalLifecycle
			})
			toolStoreFailureSignal = func(st *store.Store) <-chan struct{} {
				opened <- st
				return latch
			}
			lifecycles := make(chan *lifecycle, 1)
			lifecycleFactory = func(logger *slog.Logger) *lifecycle {
				lc := newLifecycleWith(logger, nil, func(int) {}, shutdownTimeout)
				lifecycles <- lc
				return lc
			}
			owner := &storeFailureOwner{heldOwner: newHeldOwner(), stopping: make(chan struct{})}
			started := make(chan *store.Store, 1)
			newOwner = func(deps ownerDeps) (*ownerHandle, error) {
				if name == "startup_failure" {
					return nil, errors.New("synthetic owner construction failure")
				}
				owner.startHook = func() {
					if err := deps.store.ArmRunGuard(context.Background()); err != nil {
						t.Errorf("arm run marker: %v", err)
					}
					started <- deps.store
				}
				return &ownerHandle{owner: owner}, nil
			}
			dir := privateDir(t)
			streams := newCooperativeStreams(t)
			r := startIngestShell(t, kind, streams.stdin, dir)
			t.Cleanup(func() {
				owner.complete()
				streams.close()
				(<-lifecycles).requestStop("test cleanup")
				// The result is put back by the main test after a successful wait.
				r.wait(t)
			})
			lc := <-lifecycles
			lifecycles <- lc

			if name == "prelatched" {
				// EOF ensures the unimplemented command still exits, but must not mask the latch.
				owner.complete()
				_ = streams.input.Close()
			} else if name != "startup_failure" {
				var st *store.Store
				select {
				case st = <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("owner did not start")
				}
				switch name {
				case "running":
					fail() // Keep stdin open: only the latch can stop the command.
				case "eof_race":
					go fail()
					_ = streams.input.Close()
					<-latch
				case "serve":
					lc.requestStop("normal stop")
				default:
					_ = streams.input.Close()
				}
				select {
				case <-owner.stopping:
				case <-time.After(5 * time.Second):
					t.Fatal("owner did not begin normal stop")
				}
				if name == "owner_drain" {
					fail()
				}
				select {
				case <-st.Quiesced():
					t.Error("store quiesced before owner returned")
				default:
				}
				if l, err := lock.Acquire(dir); err == nil {
					_ = l.Release()
					t.Error("lock released before owner returned")
				}
				owner.complete()
			}
			code := r.wait(t)
			r.done <- code
			want := exitError
			if name == "normal_eof" || name == "serve" {
				want = exitOK
			}
			if code != want || r.stdout.String() != "" {
				t.Errorf("exit = %d, want %d; stdout = %q", code, want, r.stdout.String())
			}
			wantReports := 0
			switch name {
			case "prelatched", "running", "eof_race", "owner_drain":
				wantReports = 1
			}
			if log := r.stderr.String(); strings.Count(log, "event=terminal_fault outcome=failed component=store tool=not_applicable category=tool_store_not_quiescent") != wantReports {
				t.Errorf("wanted %d store-control fault diagnostics: %q", wantReports, log)
			}
			if name == "prelatched" && owner.started.Load() {
				t.Error("owner started after already-latched store failure")
			}
			select {
			case <-lc.done:
			default:
				t.Error("lifecycle not closed")
			}
			l, err := lock.Acquire(dir)
			if err != nil {
				t.Fatalf("lock not released after command exit: %v", err)
			}
			defer l.Release()
			select {
			case st := <-opened:
				select {
				case <-st.Quiesced():
				default:
					t.Error("store not quiesced after command exit")
				}
				if name == "serve" {
					t.Error("serve installed the stdio observer")
				}
			default:
				if name != "serve" {
					t.Error("stdio did not install the store observer")
				}
			}
			if owner.started.Load() {
				st, err := store.Open(context.Background(), dir, slog.New(slog.DiscardHandler))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close(context.Background())
				state, _, err := st.RunGuard(context.Background())
				if err != nil || state != "unsafe" {
					t.Errorf("run marker = %q, %v; want unsafe", state, err)
				}
			}
		})
	}
}
