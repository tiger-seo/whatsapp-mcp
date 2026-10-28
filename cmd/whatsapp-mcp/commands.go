package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/buildinfo"
	"github.com/tiger-seo/whatsapp-mcp/internal/config"
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
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

func runStatus(cmd *command, args []string, s *streams) int {
	_, _, code, ok := startup(cmd, args, s)
	if !ok {
		return code
	}
	return notImplemented(cmd, s)
}

func runPair(cmd *command, args []string, s *streams) int {
	cfg, logger, code, ok := startup(cmd, args, s)
	if !ok {
		return code
	}
	l, code, ok := acquireLock(cfg, logger, s)
	if !ok {
		return code
	}
	defer l.Release()
	st, code, ok := openStore(context.Background(), cfg, logger, s)
	if !ok {
		return code
	}
	defer st.Close(context.Background())
	return notImplemented(cmd, s)
}

func runServe(cmd *command, args []string, s *streams) int {
	return runUntilStopped(cmd, args, s, func(ctx context.Context, lc *lifecycle, logger *slog.Logger, _ *store.Store) {
		logger.Info("serve: HTTP server not implemented yet")
		<-ctx.Done()
	})
}

func runStdio(cmd *command, args []string, s *streams) int {
	return runUntilStopped(cmd, args, s, func(ctx context.Context, lc *lifecycle, logger *slog.Logger, _ *store.Store) {
		logger.Info("stdio: MCP server not implemented yet")
		go func() {
			io.Copy(io.Discard, s.stdin)
			lc.requestStop("stdin closed")
		}()
		<-ctx.Done()
	})
}

func notImplemented(cmd *command, s *streams) int {
	fmt.Fprintf(s.stderr, "whatsapp-mcp: %s is not implemented yet\n", cmd.name)
	return exitError
}

// runUntilStopped takes the data directory and runs body until a stop signal or stop request, then shuts
// down. The signal handler is installed before anything else, so an early signal still releases what
// was taken.
func runUntilStopped(cmd *command, args []string, s *streams, body func(context.Context, *lifecycle, *slog.Logger, *store.Store)) int {
	lc := newLifecycle(slog.New(slog.NewTextHandler(s.stderr, nil)))
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
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	st, code, ok := openStore(lc.ctx, cfg, logger, s)
	if !ok {
		lc.shutdown()
		return code
	}
	lc.addStop("store", st.Close)
	if lc.ctx.Err() != nil {
		return lc.shutdown()
	}
	body(lc.ctx, lc, logger, st)
	return lc.shutdown()
}

// startup parses the flags, loads and validates the configuration, builds the logger and prepares the
// data directory. When ok is false the command must return code at once.
func startup(cmd *command, args []string, s *streams) (cfg *config.Config, logger *slog.Logger, code int, ok bool) {
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
	if err := config.PrepareDataDir(cfg.DataDir); err != nil {
		fmt.Fprintln(s.stderr, config.FormatError(err))
		return nil, nil, exitError, false
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
	logger.Debug("lock acquired", "path", l.Path, "pid", os.Getpid())
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
