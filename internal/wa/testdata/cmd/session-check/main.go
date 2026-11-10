// Command session-check is a MANUAL developer helper for the feature 003 acceptance journey. It is not
// part of the shipped binary, opens no listener and is never run by go test or CI (testdata is excluded
// from ./...; build it explicitly).
//
// It uses the production owner, store and lock on one named acceptance directory, and performs exactly
// one account-affecting action per run (connect with a saved identity, or pair), each only after a typed
// approval that names the action and the directory. It observes phase changes (including a logout) and
// can then check that a second pairing request in the same process is refused. It never unlinks a
// device: unlinking is done by the owner on the phone.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/config"
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitRefused = 3

	maxLine = 256
	tooLong = "\x00too-long"
)

var errDenialBroken = errors.New("a second pairing request in the same process was not denied")

// ownerEnv is what the owner factory needs.
type ownerEnv struct {
	ctx   context.Context
	log   *slog.Logger
	store *store.Store
}

type deps struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	interactive func() bool
	defaultDir  func() (string, error)
	newOwner    func(ownerEnv) (wa.Owner, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	d := deps{
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		interactive: interactiveTerminal,
		defaultDir: func() (string, error) {
			base, err := os.UserConfigDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(base, "whatsapp-mcp"), nil
		},
		newOwner: productionOwner,
	}
	os.Exit(run(ctx, os.Args[1:], d))
}

func interactiveTerminal() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

// productionOwner is the shipped owner on the shipped store.
func productionOwner(env ownerEnv) (wa.Owner, error) {
	return wa.NewOwner(wa.OwnerConfig{
		Log:      env.log,
		Store:    env.store,
		Versions: wa.NewVersionProvider(env.ctx),
	}), nil
}

// lines reads stdin once, in bounded lines, so every prompt and the observation share one reader.
func lines(r io.Reader) <-chan string {
	out := make(chan string, 4)
	go func() {
		defer close(out)
		br := bufio.NewReaderSize(r, maxLine)
		for {
			b, err := br.ReadSlice('\n')
			switch {
			case err == nil:
				out <- strings.TrimRight(string(b), "\r\n")
			case errors.Is(err, bufio.ErrBufferFull):
				for errors.Is(err, bufio.ErrBufferFull) {
					_, err = br.ReadSlice('\n')
				}
				out <- tooLong
			default:
				return
			}
			if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
				return
			}
		}
	}()
	return out
}

func prompt(ctx context.Context, in <-chan string, w io.Writer, question, want string) bool {
	fmt.Fprintf(w, "%s\nType exactly: %s\n> ", question, want)
	select {
	case line, ok := <-in:
		return ok && line == want
	case <-ctx.Done():
		return false
	}
}

func run(ctx context.Context, args []string, d deps) int {
	fs := flag.NewFlagSet("session-check", flag.ContinueOnError)
	fs.SetOutput(d.stderr)
	dir := fs.String("dir", "", "named acceptance directory (absolute, isolated; never the default data directory)")
	action := fs.String("action", "", "connect (saved identity) or pair")
	denial := fs.Bool("denial", false, "afterwards check that a second pairing request in this process is refused")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *action != "connect" && *action != "pair" {
		fmt.Fprintln(d.stderr, "session-check: -action must be connect or pair")
		return exitUsage
	}
	if *dir == "" || !filepath.IsAbs(*dir) {
		fmt.Fprintln(d.stderr, "session-check: -dir must be an absolute path to a named acceptance directory")
		return exitUsage
	}
	clean := filepath.Clean(*dir)
	if def, err := d.defaultDir(); err == nil {
		same := clean == filepath.Clean(def)
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			same = strings.EqualFold(clean, filepath.Clean(def))
		}
		if same {
			fmt.Fprintln(d.stderr, "session-check: refusing the default data directory; use a separate acceptance directory")
			return exitUsage
		}
	}
	if !d.interactive() {
		fmt.Fprintln(d.stderr, "session-check: needs an interactive terminal for input and output; nothing was changed")
		return exitRefused
	}

	in := lines(d.stdin)
	name := filepath.Base(clean)
	fmt.Fprintf(d.stdout, "Action: %s\nDirectory: %s\n", *action, clean)
	switch *action {
	case "pair":
		fmt.Fprintln(d.stdout, "Effect: opens one pairing window and draws a QR code here. Scanning it adds a linked device to the phone's account.")
		fmt.Fprintln(d.stdout, "Cleanup: this tool never unlinks anything; remove the device yourself on the phone.")
	default:
		fmt.Fprintln(d.stdout, "Effect: connects once with the saved identity in that directory.")
	}
	fmt.Fprintln(d.stdout, "After any connect the run marker stays unsafe: offline recovery is required before another action.")
	if !prompt(ctx, in, d.stdout, "Approve this single action?", fmt.Sprintf("I permit %s in %s", *action, name)) {
		fmt.Fprintln(d.stderr, "session-check: not approved; nothing was changed")
		return exitRefused
	}

	if err := config.PrepareDataDir(clean); err != nil {
		fmt.Fprintf(d.stderr, "session-check: %v\n", err)
		return exitFailed
	}
	l, err := lock.Acquire(clean)
	if err != nil {
		fmt.Fprintf(d.stderr, "session-check: %v\n", err)
		return exitFailed
	}
	defer func() { _ = l.Release() }()
	logger := slog.New(slog.NewTextHandler(d.stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := store.Open(ctx, clean, logger)
	if err != nil {
		fmt.Fprintf(d.stderr, "session-check: %v\n", err)
		return exitFailed
	}
	closed := false
	closeStore := func() {
		if closed {
			return
		}
		closed = true
		if err := st.Close(context.Background()); err != nil && !errors.Is(err, store.ErrNotQuiescent) {
			fmt.Fprintf(d.stderr, "session-check: closing the store: %v\n", err)
		} else if errors.Is(err, store.ErrNotQuiescent) {
			<-st.Quiesced()
			_ = st.Close(context.Background())
		}
	}
	defer closeStore()
	if state, _, err := st.RunGuard(ctx); err != nil || state != "clean" {
		fmt.Fprintln(d.stderr, "session-check: the run marker is not clean; offline recovery is required before any connection (use the shipped pair command's recovery). No owner was started")
		return exitRefused
	}

	owner, err := d.newOwner(ownerEnv{ctx: ctx, log: logger, store: st})
	if err != nil {
		fmt.Fprintf(d.stderr, "session-check: %v\n", err)
		return exitFailed
	}
	code := exercise(ctx, owner, *action, *denial, in, d)
	owner.BeginStop()
	<-owner.Done()
	if err := owner.StopError(); err != nil {
		if errors.Is(err, store.ErrUnsafeRun) {
			fmt.Fprintln(d.stdout, "The run marker is unsafe, as designed: offline recovery is required before another action.")
		} else {
			fmt.Fprintf(d.stderr, "session-check: stop: %v\n", err)
			if code == exitOK {
				code = exitFailed
			}
		}
	}
	fmt.Fprintln(d.stdout, "This tool never unlinks a device: remove any linked entry yourself on the phone.")
	return code
}

type watcher struct{ events chan wa.Event }

func exercise(ctx context.Context, owner wa.Owner, action string, denial bool, in <-chan string, d deps) int {
	w := watcher{events: make(chan wa.Event, 16)}
	unsub := owner.Subscribe(func(ev wa.Event) {
		select {
		case w.events <- ev:
		default:
		}
	})
	defer unsub()
	if err := owner.Start(ctx); err != nil {
		fmt.Fprintf(d.stderr, "session-check: start: %v\n", err)
		return exitFailed
	}
	if action == "pair" {
		if code := pairOnce(ctx, owner, d); code != exitOK {
			return code
		}
	}
	if code := observe(ctx, owner, w, in, d); code != exitOK {
		return code
	}
	if !denial {
		return exitOK
	}
	if !prompt(ctx, in, d.stdout, "Check that a second pairing request in this process is refused? If the denial were broken this would open a new window.", "I permit the denial check") {
		fmt.Fprintln(d.stderr, "session-check: denial check not approved")
		return exitRefused
	}
	if _, err := owner.RequestPairing(ctx); err != nil {
		fmt.Fprintf(d.stdout, "denial confirmed: the second pairing request was refused (%v)\n", describe(err))
		return exitOK
	}
	owner.CancelPairing()
	fmt.Fprintf(d.stderr, "session-check: FAIL: %v; the pairing window was NOT denied and has been cancelled\n", errDenialBroken)
	return exitFailed
}

func describe(err error) string {
	switch {
	case errors.Is(err, store.ErrUnsafeRun):
		return "unsafe run"
	case errors.Is(err, wa.ErrAlreadyPaired):
		return "already paired"
	case errors.Is(err, wa.ErrStopping):
		return "stopping"
	default:
		return "refused"
	}
}

// pairOnce opens one window and draws each new code until the window ends.
func pairOnce(ctx context.Context, owner wa.Owner, d deps) int {
	if _, err := owner.RequestPairing(ctx); err != nil {
		fmt.Fprintf(d.stderr, "session-check: pairing request: %s\n", describe(err))
		return exitFailed
	}
	rendered := ""
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		snap, ok := owner.PairingSnapshot()
		if !ok {
			fmt.Fprintln(d.stderr, "session-check: the pairing window closed without a result")
			return exitFailed
		}
		switch snap.Result {
		case wa.PairingActive:
			if snap.Code != "" && snap.Code != rendered {
				art, err := wa.RenderTerminal(snap.Code)
				if err != nil {
					fmt.Fprintln(d.stderr, "session-check: the code could not be drawn")
					return exitFailed
				}
				rendered = snap.Code
				fmt.Fprintln(d.stdout, art)
			}
		case wa.PairingSucceeded:
			fmt.Fprintln(d.stdout, "pairing window ended: succeeded")
			return exitOK
		default:
			fmt.Fprintf(d.stderr, "session-check: pairing window ended: %s\n", snap.Result)
			return exitFailed
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return exitFailed
		}
	}
}

// observe prints phase names until a logout, the owner leaving a usable state, "done" on input or an interrupt.
func observe(ctx context.Context, owner wa.Owner, w watcher, in <-chan string, d deps) int {
	fmt.Fprintln(d.stdout, "Observing phase changes. Type done and Enter to stop; a logout from the phone is reported here.")
	report := func() {
		s := owner.Snapshot()
		fmt.Fprintf(d.stdout, "status: connected=%t logged_in=%t\n", s.Connected, s.LoggedIn)
	}
	report()
	for {
		select {
		case ev := <-w.events:
			fmt.Fprintf(d.stdout, "phase: %s\n", ev.Phase)
			switch ev.Phase {
			case wa.PhaseLoggedOut:
				fmt.Fprintln(d.stdout, "logout observed; no pairing is offered in this process")
				return exitOK
			case wa.PhaseDisconnected, wa.PhaseStorageFailed, wa.PhaseStopping:
				report()
				return exitOK
			}
		case line, ok := <-in:
			if !ok || line == "done" {
				report()
				return exitOK
			}
		case <-ctx.Done():
			fmt.Fprintln(d.stdout, "interrupted")
			return exitOK
		}
	}
}
