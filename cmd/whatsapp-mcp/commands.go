package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/tiger-seo/whatsapp-mcp/internal/buildinfo"
	"github.com/tiger-seo/whatsapp-mcp/internal/config"
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// runVersion prints the build information. It reads no configuration and touches no files, so it works
// whatever state the environment is in.
func runVersion(cmd *command, args []string, s *streams) int {
	if _, code, ok := parseFlags(cmd, args, s); !ok {
		return code
	}
	info := buildinfo.Get()
	fmt.Fprintf(s.stdout, "whatsapp-mcp %s\ncommit:  %s\nbuilt:   %s\ngo:      %s\ntarget:  %s\n",
		info.Version, info.Commit, info.Date, info.GoVersion, info.Target)
	return exitOK
}

// runStatus prints the offline local snapshot. It never connects, discovers a version, pairs,
// migrates or repairs, and it creates nothing for storage that does not exist.
func runStatus(cmd *command, args []string, s *streams) int {
	cfg, _, code, ok := loadStartup(cmd, args, s)
	if !ok {
		return code
	}
	snap, code, ok := statusSnapshot(cfg, s)
	if !ok {
		return code
	}
	fmt.Fprintf(s.stdout, "connected: false\nlogged_in: %t\npairing_required: false\nwa_version: %s (bundled; not refreshed)\n",
		snap.IdentityPresent, wa.BundledVersion())
	if snap.IdentityPresent {
		fmt.Fprintln(s.stderr, "Offline snapshot; saved credentials have not been verified with WhatsApp.")
	}
	if snap.Unsafe {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: status: the storage is in an unsafe or unknown run state; offline recovery is required before any connection (start it with pair)")
		return exitError
	}
	return exitOK
}

// statusSnapshot observes the local storage. Absent storage is the empty snapshot and creates
// nothing. A lock held by another process is reported with its PID and the database is not read.
// Present storage is inspected under the normal owner lock, taken without creating a lock file
// unless a database already exists.
func statusSnapshot(cfg *config.Config, s *streams) (store.Inspection, int, bool) {
	dir := cfg.DataDir
	fail := func(err error) (store.Inspection, int, bool) {
		var he *lock.HeldError
		if !errors.As(err, &he) {
			var de *config.DataDirError
			if !errors.As(err, &de) && !errors.Is(err, store.ErrUnsafeStorage) && !isStoreError(err) {
				err = &config.DataDirError{Dir: dir, Err: err}
			}
		}
		fmt.Fprintln(s.stderr, config.FormatError(err))
		return store.Inspection{}, exitError, false
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return store.Inspection{}, exitOK, true
		}
		return fail(&config.DataDirError{Dir: dir, Err: err})
	}
	if err := private.CheckDir(dir); err != nil {
		return fail(&config.DataDirError{Dir: dir, Err: err})
	}
	l, err := lock.AcquireExisting(dir)
	if errors.Is(err, fs.ErrNotExist) {
		present, perr := store.ArtifactsPresent(dir)
		if perr != nil {
			return fail(perr)
		}
		if !present {
			return store.Inspection{}, exitOK, true
		}
		// A database without a lock file: take the normal lock, which only adds lock metadata.
		l, err = lock.Acquire(dir)
	}
	if err != nil {
		return fail(err)
	}
	defer l.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	snap, err := store.Inspect(ctx, dir)
	if err != nil {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: "+err.Error())
		return store.Inspection{}, exitError, false
	}
	return snap, exitOK, true
}

// isStoreError reports whether err is one of the store's own classified errors, which carry their
// own text.
func isStoreError(err error) bool {
	var se *store.StorageError
	var sc *store.SchemaError
	return errors.As(err, &se) || errors.As(err, &sc)
}

// ownerDeps carries what the owner construction needs from the running command.
type ownerDeps struct {
	ctx   context.Context
	log   *slog.Logger
	store *store.Store
	// shell enables the bounded saved-session startup budget (stdio/serve); pair keeps its single
	// attempt.
	shell bool

	// discoveryTransport and sessionFactory are test seams; nil uses the approved defaults.
	discoveryTransport http.RoundTripper
	sessionFactory     wa.SessionFactory
}

// ownerHandle bundles the owner with its lifecycle wiring.
type ownerHandle struct {
	owner wa.Owner
}

// ownerSeam carries synthetic dependencies for tests; it is nil outside tests.
type ownerSeamConfig struct {
	discoveryRoundTripper http.RoundTripper
	sessionFactory        wa.SessionFactory
}

var ownerSeam *ownerSeamConfig

// productionOwner builds the real connection owner: a bounded version provider and the pinned
// adapter, with any synthetic test transports.
func productionOwner(deps ownerDeps) (*ownerHandle, error) {
	provider := wa.NewVersionProvider(deps.ctx)
	if ownerSeam != nil && ownerSeam.discoveryRoundTripper != nil {
		provider = wa.NewVersionProviderWithTransport(deps.ctx, ownerSeam.discoveryRoundTripper)
	}
	var sessions wa.SessionFactory
	if ownerSeam != nil && ownerSeam.sessionFactory != nil {
		sessions = ownerSeam.sessionFactory
	}
	owner := wa.NewOwner(wa.OwnerConfig{
		Log:                deps.log,
		Store:              deps.store,
		Versions:           provider,
		Sessions:           sessions,
		ShellStartupBudget: deps.shell,
	})
	return &ownerHandle{owner: owner}, nil
}

// newOwner builds the owner; tests replace this factory.
var newOwner = productionOwner

// lifecycleFactory builds the lifecycle; tests replace it to drive signals.
var lifecycleFactory = newLifecycle

var recoveryInteractive = terminalStreams

// stopStep is the owner's ordered cleanup: stop, then wait for the drain, before the store closes.
func (h *ownerHandle) stopStep() func(context.Context) error {
	return func(context.Context) error {
		h.owner.BeginStop()
		<-h.owner.Done()
		return h.owner.StopError()
	}
}

// runPair implements the pair command with the connection owner.
func runPair(cmd *command, args []string, s *streams) int {
	lc := lifecycleFactory(slog.New(slog.NewTextHandler(s.stderr, nil)))
	cfg, logger, code, ok := startup(cmd, args, s)
	if !ok {
		lc.close()
		return code
	}
	lc.setLogger(logger)
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	l, code, ok := acquireLock(cfg, logger, s)
	if !ok {
		lc.close()
		return code
	}
	lc.addStop("lock", func(context.Context) error { return l.Release() })
	logger.Info("lock acquired", "path", l.Path, "pid", os.Getpid())
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	st, code, ok := openStore(lc.ctx, cfg, logger, s)
	if !ok {
		lc.shutdown()
		return code
	}
	lc.addStoreStop(st.Close, st.Quiesced())
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	state, _, err := st.RunGuard(lc.ctx)
	if err != nil {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: invalid or missing run marker; external remediation is required")
		lc.shutdown()
		return exitError
	}
	if state == "unsafe" {
		ok := recoverOffline(lc.ctx, st, s)
		lc.requestStop("offline recovery finished")
		if lc.shutdown() != exitOK || !ok {
			return exitError
		}
		return exitOK
	}
	oh, err := newOwner(ownerDeps{ctx: lc.ctx, log: logger, store: st})
	if err != nil {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s\n", safeError(err))
		if code := lc.shutdown(); code != exitOK {
			return code
		}
		return exitError
	}
	// A confirmed link ends with the run marker unsafe by design: any production Connect needs a
	// separately authorized offline recovery. That one outcome is reported as success with a
	// recovery notice; a storage fault or any other stop failure still fails the command.
	var linked, recoveryRequired atomic.Bool
	ownerStop := oh.stopStep()
	lc.addStop("owner", func(ctx context.Context) error {
		err := ownerStop(ctx)
		if err != nil && errors.Is(err, store.ErrUnsafeRun) && linked.Load() && !oh.owner.Health().StorageFault {
			recoveryRequired.Store(true)
			return nil
		}
		return err
	})
	success := pairingFlow(lc, oh, logger, s)
	linked.Store(success)
	lc.requestStop("pair finished")
	<-lc.ctx.Done()
	if code := lc.shutdown(); code != exitOK {
		return code
	}
	if !success {
		return exitError
	}
	if recoveryRequired.Load() {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: linked. Offline recovery is required before the next run: the connection used for pairing leaves the data directory marked unsafe")
	}
	return exitOK
}

// pairingFlow runs the pair journey and reports whether it succeeded. Pair exits successfully only
// with the durable identity plus a confirmed connection; every other outcome is a failure exit.
func pairingFlow(lc *lifecycle, oh *ownerHandle, logger *slog.Logger, s *streams) bool {
	owner := oh.owner
	events := make(chan wa.Event, 1)
	unsub := owner.Subscribe(func(ev wa.Event) {
		select {
		case events <- ev:
		default:
			// Keep the newest state notification rather than losing a terminal event behind
			// a full queue of stale transitions. Status itself is read from the owner.
			select {
			case <-events:
			default:
			}
			select {
			case events <- ev:
			default:
			}
		}
	})
	defer unsub()

	if err := owner.Start(lc.ctx); err != nil {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: pair: %s\n", safeError(err))
		return false
	}
	if owner.Snapshot().LoggedIn {
		return waitSavedConnection(owner, events, s, lc.ctx, false)
	}
	return pairingWindowFlow(owner, events, s, lc.ctx)
}

// waitSavedConnection follows the saved-session attempt until it confirms or ends.
func waitSavedConnection(owner wa.Owner, events <-chan wa.Event, s *streams, ctx context.Context, afterPairing bool) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		status := owner.Snapshot()
		if status.Connected && status.LoggedIn {
			warnPendingRepairs(owner, s)
			return true
		}
		select {
		case ev := <-events:
			// Notifications can queue behind newer state changes; only inspect the latest one.
			for len(events) > 0 {
				ev = <-events
			}
			status := owner.Snapshot()
			if status.Connected && status.LoggedIn {
				warnPendingRepairs(owner, s)
				return true
			}
			switch ev.Phase {
			case wa.PhaseDisconnected:
				if afterPairing {
					fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: linking could not be completed safely because the follow-up connection is not available; the phone may show a failure or, in some cases, a linked device; check Linked devices on the phone. Offline recovery is required before another attempt")
					return false
				}
				fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: the saved session did not connect; the identity was kept, no new pairing was started")
				return false
			case wa.PhaseLoggedOut, wa.PhaseStorageFailed:
				fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: the saved session ended; the identity was kept")
				return false
			case wa.PhaseStopping:
				fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: interrupted")
				return false
			}
		case <-ctx.Done():
			return false
		}
	}
}

// pairingWindowFlow runs one window to a terminal outcome, rendering the current code only.
func pairingWindowFlow(owner wa.Owner, events <-chan wa.Event, s *streams, ctx context.Context) bool {
	if _, err := owner.RequestPairing(context.Background()); err != nil {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: pair: %s\n", safeError(err))
		return false
	}
	rendered := ""
	for {
		if ctx.Err() != nil {
			return false
		}
		snap, ok := owner.PairingSnapshot()
		if !ok {
			return false
		}
		switch snap.Result {
		case wa.PairingActive:
			// The owner already clears expired codes from the snapshot, so any code here is current.
			if snap.Code != "" && snap.Code != rendered {
				out, err := wa.RenderTerminal(snap.Code)
				if err != nil {
					fmt.Fprintf(s.stderr, "whatsapp-mcp: pair: %s\n", safeError(err))
					return false
				}
				if _, err := io.WriteString(s.stdout, out); err != nil {
					return false
				}
				rendered = snap.Code
			}
		case wa.PairingSucceeded:
			return waitSavedConnection(owner, events, s, ctx, true)
		case wa.PairingTimedOut:
			fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: the pairing attempt timed out; an orphaned phone link may exist. Offline recovery is required before another attempt")
			return false
		case wa.PairingCancelled:
			fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: the pairing attempt was cancelled")
			return false
		case wa.PairingFailed:
			fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: the pairing attempt failed; the identity was kept, no keys were replaced")
			return false
		}
		select {
		case ev := <-events:
			for len(events) > 0 {
				ev = <-events
			}
			// Re-read the window before acting on notifications that may already be stale.
			if current, ok := owner.PairingSnapshot(); !ok || current.Result != wa.PairingActive {
				continue
			}
			switch ev.Phase {
			case wa.PhaseLoggedOut, wa.PhaseStorageFailed:
				fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: the pairing attempt ended; the identity was kept")
				return false
			case wa.PhaseStopping:
				fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: interrupted")
				return false
			}
		case <-ctx.Done():
			return false
		}
	}
}

// warnPendingRepairs emits the honest pending-repair warning after a successful pairing.
func warnPendingRepairs(owner wa.Owner, s *streams) {
	if owner.Health().RecoveryPending {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: device data repair is still pending; some history may sync later")
	}
}

func runServe(cmd *command, args []string, s *streams) int {
	return runShell("serve", cmd, args, s, func(ctx context.Context, lc *lifecycle, logger *slog.Logger, st *store.Store, s *streams) bool {
		failed := attachOwner("serve", lc, logger, st, s)
		<-lc.ctx.Done()
		return failed
	})
}

func runStdio(cmd *command, args []string, s *streams) int {
	return runShell("stdio", cmd, args, s, func(ctx context.Context, lc *lifecycle, logger *slog.Logger, st *store.Store, s *streams) bool {
		failed := attachOwner("stdio", lc, logger, st, s)
		if !failed {
			go func() {
				_, _ = io.Copy(io.Discard, s.stdin)
				lc.requestStop("stdin closed")
			}()
		}
		<-lc.ctx.Done()
		return failed
	})
}

// runShell takes the data directory, attaches the owner and runs until a stop signal or stop
// request, then shuts down. The signal handler is installed before anything else, so an early signal
// still releases what was taken. The body's result makes a start failure an unsuccessful exit.
func runShell(kind string, cmd *command, args []string, s *streams, body func(context.Context, *lifecycle, *slog.Logger, *store.Store, *streams) bool) int {
	lc := lifecycleFactory(slog.New(slog.NewTextHandler(s.stderr, nil)))
	cfg, logger, code, ok := startup(cmd, args, s)
	if !ok {
		lc.close()
		return code
	}
	lc.setLogger(logger)
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	l, code, ok := acquireLock(cfg, logger, s)
	if !ok {
		lc.close()
		return code
	}
	lc.addStop("lock", func(context.Context) error { return l.Release() })
	logger.Info("lock acquired", "path", l.Path, "pid", os.Getpid())
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	st, code, ok := openStore(lc.ctx, cfg, logger, s)
	if !ok {
		lc.shutdown()
		return code
	}
	lc.addStoreStop(st.Close, st.Quiesced())
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	state, _, err := st.RunGuard(lc.ctx)
	if err != nil || state != "clean" {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s: unsafe or invalid run marker; offline recovery is required using pair\n", kind)
		lc.shutdown()
		return exitError
	}
	failed := body(lc.ctx, lc, logger, st, s)
	lc.requestStop("shell finished")
	<-lc.ctx.Done()
	if code := lc.shutdown(); code != exitOK {
		return code
	}
	if failed {
		return exitError
	}
	return exitOK
}

// attachOwner creates the owner after the store cleanup registration, starts it and stays with the
// shell until stop. A start failure is reported and makes the shell exit unsuccessfully.
func attachOwner(kind string, lc *lifecycle, logger *slog.Logger, st *store.Store, s *streams) bool {
	oh, err := newOwner(ownerDeps{ctx: lc.ctx, log: logger, store: st, shell: true})
	if err != nil {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s\n", safeError(err))
		lc.requestStop("owner failed")
		return true
	}
	// Registered after the store stop, so shutdown runs owner first and the store closes after the
	// device writes drained.
	lc.addStop("owner", oh.stopStep())
	if err := oh.owner.Start(lc.ctx); err != nil {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s: %s\n", kind, safeError(err))
		lc.requestStop("owner start failed")
		return true
	}
	logger.Info(kind+": connection owner running; MCP arrives in a later feature", "event", "owner_running")
	return false
}

func notImplemented(cmd *command, s *streams) int {
	fmt.Fprintf(s.stderr, "whatsapp-mcp: %s is not implemented yet\n", cmd.name)
	return exitError
}

// startup parses the flags, loads and validates the configuration, builds the logger and prepares the
// data directory. When ok is false the command must return code at once.
func startup(cmd *command, args []string, s *streams) (cfg *config.Config, logger *slog.Logger, code int, ok bool) {
	cfg, logger, code, ok = loadStartup(cmd, args, s)
	if !ok {
		return nil, nil, code, false
	}
	if err := config.PrepareDataDir(cfg.DataDir); err != nil {
		fmt.Fprintln(s.stderr, config.FormatError(err))
		return nil, nil, exitError, false
	}
	return cfg, logger, exitOK, true
}

// loadStartup parses the flags, loads and validates the configuration and builds the logger. It
// does not touch the data directory.
func loadStartup(cmd *command, args []string, s *streams) (cfg *config.Config, logger *slog.Logger, code int, ok bool) {
	flags, code, ok := parseFlags(cmd, args, s)
	if !ok {
		return nil, nil, code, false
	}
	cfg, warnings, err := config.Load(config.Inputs{Flags: flags, Environ: s.environ, UserConfigDir: os.UserConfigDir})
	if err != nil {
		fmt.Fprintln(s.stderr, config.FormatError(err))
		// An unusable data directory is a problem with the environment, not with the arguments.
		var de *config.DataDirError
		if errors.As(err, &de) {
			return nil, nil, exitError, false
		}
		return nil, nil, exitUsage, false
	}
	logger = slog.New(slog.NewTextHandler(s.stderr, &slog.HandlerOptions{Level: cfg.LogLevel, ReplaceAttr: utcTime}))
	slog.SetDefault(logger)
	for _, w := range warnings {
		logger.Warn("unknown environment variable", "name", w.Name)
	}
	for _, attrs := range cfg.LogValues() {
		logger.LogAttrs(context.Background(), slog.LevelDebug, "config", attrs...)
	}
	return cfg, logger, exitOK, true
}

func acquireLock(cfg *config.Config, logger *slog.Logger, s *streams) (*lock.Lock, int, bool) {
	l, err := lock.Acquire(cfg.DataDir)
	if err != nil {
		var he *lock.HeldError
		if !errors.As(err, &he) {
			err = &config.DataDirError{Dir: cfg.DataDir, Err: err}
		}
		fmt.Fprintln(s.stderr, config.FormatError(err))
		return nil, exitError, false
	}
	return l, exitOK, true
}

// utcTime renders the log time in UTC.
func utcTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
		a.Value = slog.TimeValue(a.Value.Time().UTC())
	}
	return a
}

// openStore opens the database after the lock is held. It gives the whole startup, including schema upgrades,
// the approved 10 minutes. On failure it prints the safe error and returns the environment-error exit code.
func openStore(ctx context.Context, cfg *config.Config, logger *slog.Logger, s *streams) (*store.Store, int, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	st, err := store.Open(ctx, cfg.DataDir, logger)
	if err != nil {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: "+err.Error())
		return nil, exitError, false
	}
	return st, exitOK, true
}

// safeError renders an error for the terminal. Text can originate outside this program, so every
// control character, including escape, carriage return, bell, backspace, tab, the C1 range and the
// Unicode line separators, becomes a question mark, and the length is bounded.
func safeError(err error) string {
	if err == nil {
		return ""
	}
	const maxRunes = 256
	var b strings.Builder
	count := 0
	for _, r := range err.Error() {
		if count == maxRunes {
			b.WriteString("...")
			break
		}
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			r = '?'
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}
