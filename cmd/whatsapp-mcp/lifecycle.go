package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// shutdownTimeout marks a slow shutdown as unsuccessful without abandoning cleanup.
const shutdownTimeout = 10 * time.Second

type stopStep struct {
	name string
	stop func(context.Context) error
}

// lifecycle runs a long-running command until the first stop signal or stop request, then runs the stop
// steps in reverse order of registration. A second signal exits at once.
type lifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc

	logger   atomic.Pointer[slog.Logger]
	signals  <-chan os.Signal
	requests chan string
	exit     func(int)
	timeout  time.Duration
	release  func()

	mu    sync.Mutex
	steps []stopStep

	done      chan struct{}
	closeOnce sync.Once
}

// newLifecycle starts listening for SIGINT and SIGTERM. On Windows, Ctrl+C and Ctrl+Break arrive as
// os.Interrupt, and console close, logoff and shutdown as SIGTERM.
func newLifecycle(logger *slog.Logger) *lifecycle {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	lc := newLifecycleWith(logger, ch, os.Exit, shutdownTimeout)
	lc.release = func() { signal.Stop(ch) }
	return lc
}

func newLifecycleWith(logger *slog.Logger, signals <-chan os.Signal, exit func(int), timeout time.Duration) *lifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	lc := &lifecycle{
		ctx:      ctx,
		cancel:   cancel,
		signals:  signals,
		requests: make(chan string, 1),
		exit:     exit,
		timeout:  timeout,
		release:  func() {},
		done:     make(chan struct{}),
	}
	lc.logger.Store(logger)
	go lc.watch()
	return lc
}

func (lc *lifecycle) log() *slog.Logger { return lc.logger.Load() }

// setLogger switches to the configured logger once it exists.
func (lc *lifecycle) setLogger(logger *slog.Logger) { lc.logger.Store(logger) }

// addStop registers a stop step. Register each resource as soon as it is taken, so that an early stop
// releases it too.
func (lc *lifecycle) addStop(name string, stop func(context.Context) error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.steps = append(lc.steps, stopStep{name, stop})
}

// addStoreStop retains the lock until the store proves its admitted work has drained.
func (lc *lifecycle) addStoreStop(closeStore func(context.Context) error, drained <-chan struct{}) {
	lc.addStop("store", func(ctx context.Context) error {
		err := closeStore(ctx)
		if !errors.Is(err, store.ErrNotQuiescent) {
			return err
		}
		<-drained
		return errors.Join(err, closeStore(ctx))
	})
}

// requestStop stops the command for a reason other than a signal, such as stdin closing.
func (lc *lifecycle) requestStop(reason string) {
	select {
	case lc.requests <- reason:
	default:
	}
}

func (lc *lifecycle) watch() {
	stopping := false
	signalSeen := false
	begin := func(reason string) {
		stopping = true
		lc.log().Info("shutting down", "reason", reason)
		lc.cancel()
	}
	for {
		select {
		case sig := <-lc.signals:
			if signalSeen {
				lc.log().Warn("forced exit")
				lc.exit(exitError)
				return
			}
			signalSeen = true
			if !stopping {
				begin(sig.String())
			} else {
				lc.log().Info("first stop signal received; continuing orderly shutdown")
			}
		case reason := <-lc.requests:
			if !stopping {
				begin(reason)
			}
		case <-lc.done:
			return
		}
	}
}

// shutdown runs the stop steps and returns the exit code: 0 after a clean stop, 1 if a step failed or
// the notification deadline passed. The deadline never abandons a cleanup step.
func (lc *lifecycle) shutdown() int {
	defer lc.close()
	lc.cancel()
	lc.mu.Lock()
	steps := append([]stopStep(nil), lc.steps...)
	lc.mu.Unlock()

	timer := time.NewTimer(lc.timeout)
	defer timer.Stop()
	timeout := timer.C
	code := exitOK
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		errc := make(chan error, 1)
		go func() { errc <- step.stop(context.Background()) }()
		select {
		case err := <-errc:
			select {
			case <-timeout:
				lc.log().Error("shutdown timed out", "waiting_for", step.name)
				code = exitError
				timeout = nil
			default:
			}
			if err != nil {
				lc.log().Error("stop failed", "step", step.name, "error", err)
				code = exitError
			}
		case <-timeout:
			lc.log().Error("shutdown timed out", "waiting_for", step.name)
			code = exitError
			timeout = nil
			if err := <-errc; err != nil {
				lc.log().Error("stop failed", "step", step.name, "error", err)
			}
		}
	}
	lc.log().Info("stopped")
	return code
}

// close stops listening for signals without running the stop steps, for a command that ends before it
// has taken anything.
func (lc *lifecycle) close() {
	lc.closeOnce.Do(func() {
		lc.release()
		close(lc.done)
		lc.cancel()
	})
}
