# Research: Binary skeleton

Phase 0 of `/speckit-plan` for `001-binary-skeleton`. Every open technical question is settled below. Two of
them were checked with a throwaway spike, run on 2026-10-27 on Windows 10 with Go 1.27.0, `gofrs/flock v0.13.1`
and `pelletier/go-toml/v2 v2.4.3`. The spike is not kept in the repository.

## R1. Command-line parsing

- **Decision:** the standard library `flag` package. There is one `flag.FlagSet` per command, and `main`
  dispatches on the first argument. Flags follow the command (`whatsapp-mcp stdio --log-level debug`). At the
  top level only `-h` / `--help` is accepted. Any other leading flag is a usage error, and its message says
  that flags go after the command.
- **Rationale:** there are five commands and two flags. `FlagSet.Visit` reports exactly which flags were set,
  which is all the precedence logic needs. The help text is generated from the settings registry (R3), so each
  flag is listed with its environment variable and default. Principle IX: prefer the standard library.
- **Alternatives considered:**
  - `spf13/cobra` + `pflag`, rejected: a large dependency for five commands, and its own config layer (viper)
    would compete with ours.
  - `urfave/cli`, `alecthomas/kong`, rejected for the same reason. Neither is needed to print the environment
    variable next to each flag.

## R2. TOML library and line numbers

- **Decision:** `github.com/pelletier/go-toml/v2` (v2.4.3, pure Go). `config.toml` is decoded into
  `map[string]any`. A second pass with its `unstable.Parser` builds an index from each top-level key to its
  line. Errors about a key in the file name the file and that line.
- **Spike results (go-toml v2.4.3):**
  - a syntax error is a `*toml.DecodeError` with a line and column (`basic strings cannot have new lines` at
    1:18)
  - with `DisallowUnknownFields`, an unknown key is a `*toml.StrictMissingError` with the line and key
  - a type mismatch is a `*toml.DecodeError` with the line, column and key
  - an empty file decodes without error
  - `unstable.Parser` with `Shape(Range(...))` returns the line of each key (`log_level` at line 3 after a
    comment and a blank line).
  - A field type that implements `encoding.TextUnmarshaler` is **not** a reliable place to validate: an
    invalid string passed without an error, and an integer reached `UnmarshalText` as the text `"3"` with no
    position. Validation therefore happens after decoding, in our own code.
- **Why decode into a map:** a struct needs a field for every setting, besides the registry. The map plus the
  key index keeps one definition per setting (R3), and the same code handles unknown keys, `data_dir`, wrong
  types and invalid values, each with its line.
- **Alternatives considered:**
  - `BurntSushi/toml` v1.6.0, rejected: `ParseError` has a position for syntax errors, but `MetaData` has no
    position for a key. `Undecoded()` lists unknown keys without lines, so value and type errors could not name
    the line that FR-012 requires.
  - The standard library has no TOML parser, and D8 rules out YAML.

## R3. Settings registry and resolution

- **Decision:** `internal/config` holds one registry, a list of `Setting` descriptors (see
  [data-model.md](data-model.md)). Each descriptor has a name, a flag, an environment variable, a file key (or
  "not allowed in the file"), a default, a secret mark, a parser for text (flag and environment) and a parser
  for TOML values. Flags, help text, environment lookup, file checks and the debug dump are all generated from
  the registry. A later feature adds a setting by adding one descriptor and one field of `Config`.
- **Resolution order inside `Load`:**
  1. Resolve the data directory from flag > environment > default. It cannot come from the file (FR-010), and
     the file's location depends on it.
  2. Check that it is absolute (FR-016).
  3. Read `<data dir>/config.toml` if it exists. A missing directory or file means "no file values". Nothing is
     created at this stage.
  4. Resolve every other setting: flag > environment > file > default.
  5. Validate all values. Collect every error and return them together (`errors.Join`).
  6. Only when everything is valid does the caller prepare the data directory (R5) and take the lock (R6).
     Invalid configuration therefore causes no side effects.
- **Rationale:** FR-012 asks for validation before any other work, and for all errors in one run when possible.
  Keeping the side effects until after validation also means that `--data-dir` pointing at a mistyped path does
  not leave an empty directory behind when another setting is wrong.
- **Alternatives considered:** `spf13/viper` and `knadh/koanf`, rejected. Their merging hides which source won,
  so the source could not be reported per value. viper also has case and key-path quirks. Both are heavy
  dependencies for two settings.

## R4. Environment variables

- **Decision:**
  - Read from one `os.Environ()` snapshot, which is passed into `Load` so tests can supply their own.
  - An empty value counts as unset (FR-009).
  - Any `WHATSAPP_MCP_*` name not in the registry produces one `WARN` log line per name after the logger
    exists (FR-013).
  - On Windows, variable names compare case-insensitively, as `os.Getenv` does there. On other systems they are
    exact.
  - Only the names in the registry are read. There are no aliases and no lookup of the old names (D10), so
    `LOG_LEVEL` is ignored silently.
- **Rationale:** the snapshot makes `Load` a pure function of its inputs, which the precedence tests need.
  Matching the OS's own case rules avoids reading a variable in one place and calling it unknown in another.
- **Alternatives considered:** calling `os.LookupEnv` per setting, rejected. It cannot list unknown names, and
  tests would have to change the process environment.

## R5. Data directory

- **Decision:**
  - The default is `filepath.Join(os.UserConfigDir(), "whatsapp-mcp")`. If `os.UserConfigDir` fails (no
    `%AppData%`; no `$XDG_CONFIG_HOME` and no `$HOME`), the error suggests `--data-dir` or
    `WHATSAPP_MCP_DATA_DIR` (FR-018).
  - An override must satisfy `filepath.IsAbs` and is then `filepath.Clean`ed. On Windows `\foo` and `C:foo`
    are not absolute, and are rejected.
  - Preparing the directory is `os.MkdirAll(dir, 0o700)`, followed by a `Stat` that it is a directory. An
    existing directory's permissions are left as they are: the operator may have chosen them.
  - On Windows the mode bits are ignored. The default location under `%AppData%` already inherits a per-user
    ACL, which is the "where the OS supports that" case of FR-017.
- **Rationale:** C2 fixes the location. No code anywhere calls `os.Getwd`, and relative paths are rejected, so
  the working directory never matters (SC-006).
- **Alternatives considered:** `os.UserHomeDir()/.whatsapp-mcp`, rejected because C2 names `os.UserConfigDir()`.

## R6. Single-instance lock and the PID

- **Decision:** `internal/lock` wraps `github.com/gofrs/flock` v0.13.1 (D14). The lock file is
  `<data dir>/whatsapp-mcp.lock`, opened with `flock.New(path, flock.SetFlag(os.O_CREATE|os.O_RDWR))`.
  - `TryLock` is used, never a blocking `Lock` (FR-025).
  - **The PID is stored from byte offset 1, not 0.** Byte 0 is reserved and its value is meaningless.
  - The holder writes through a second handle: `Truncate(1)`, then `WriteAt("<pid>\n", 1)`.
  - A process that fails to get the lock reads up to 32 bytes from offset 1 and trims NUL and white space. If
    the result does not parse as a positive integer, the message says the PID is unknown.
  - On release, the holder truncates the file to 0 bytes while it still holds the lock, then unlocks and
    closes it. The file is never deleted.
- **Spike results (Windows):**
  - `flock` calls `LockFileEx` on 1 byte at offset 0. Windows byte-range locks are mandatory: while the lock is
    held, `os.ReadFile` from another process fails with "another process has locked a portion of the file".
    So does a write at offset 0 from a second handle inside the holder.
  - A read from offset 1 in another process, and a write at offset 1 through the holder's second handle,
    both work.
  - `Truncate` through the second handle also works.
  - `gofrs/flock` v0.13 has no accessor for its own file handle, which is why the holder writes through a
    second handle.
  - On Unix `flock(2)` is advisory, so the same layout works there unchanged. One layout is used on every OS.
- **Why not delete the file on release:** deleting a lock file races with a new holder that has opened it.
  Truncating while still locked cannot clobber a newer holder's PID.
- **Crash safety:** the OS drops `flock`/`LockFileEx` locks when the process dies, so a stale file only holds an
  old PID, which is ignored (FR-026). Locks are per open file description or handle, so a second `Acquire` in
  the same process also fails. Tests use that.
- **Alternatives considered:**
  - A separate `whatsapp-mcp.pid` file, rejected: D14 and the spec put the PID in the lock file, and a second
    file can disagree with the lock.
  - Calling `golang.org/x/sys` `LockFileEx` directly on a far offset, rejected: D14 names `gofrs/flock`, and the
    offset-1 layout makes that unnecessary.
  - A PID file with no OS lock, checked for liveness, rejected: PID reuse makes it unsafe, and it leaves stale
    locks.

## R7. Logging

- **Decision:**
  - The logger is `slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))`, built once in
    `cmd/whatsapp-mcp` after the configuration loads. It is installed with `slog.SetDefault` and also passed
    explicitly to components.
  - Output looks like `time=2026-10-27T10:00:00.000+02:00 level=INFO msg="shutting down" signal=interrupt`.
  - Errors before the logger exists, such as configuration, usage and data directory errors, are written as
    plain text to stderr: `whatsapp-mcp: <message>` (FR-022).
  - Log level text is accepted case-insensitively: `debug`, `info`, `warn`, `warning` and `error`.
  - The effective configuration is logged at `DEBUG` as one line per setting, with `value`, `source` and
    `origin`. A secret's value is replaced by `(secret)` (FR-014).
- **Rationale:** the text handler meets FR-021, and stderr in every command meets FR-020 with the simplest rule.
  The level is the only thing that varies, and no option for the format is added (Principle IX).
- **Alternatives considered:**
  - `slog.JSONHandler` or a `--log-format` option, rejected as speculative (spec Assumptions).
  - `zerolog`, rejected: whatsmeow's logger interface is bridged into `slog` in feature 003, as M10 requires.

## R8. Shutdown

- **Decision:**
  - Long-running commands (`stdio`, `serve`; `pair` later) install `signal.Notify(ch, os.Interrupt,
    syscall.SIGTERM)` as the very first step, before the configuration loads.
  - The first signal cancels the run context and logs `shutting down` with the signal.
  - A second signal logs `forced exit` and calls `os.Exit(1)` (FR-028).
  - After cancellation, a 10 s deadline starts. Cleanup runs as named stop steps in reverse order of
    registration ("lock" in this feature; "whatsapp client", "database" and "http server" later). If the
    deadline passes, the process logs `shutdown timed out` with the name of the step still running and exits 1
    (FR-029).
- **Windows:** Go's `os/signal` delivers Ctrl+C and Ctrl+Break in a console as `os.Interrupt`. Console close,
  logoff and system shutdown arrive as `syscall.SIGTERM`. The same `Notify` call covers FR-027 on all five
  targets.
- **Signal before startup finishes:** the context is checked between the startup steps (configuration, data
  directory, lock). Each resource is registered as a stop step as soon as it is taken, so an early signal still
  releases what was taken, and the exit is still 0.
- **Alternatives considered:** `signal.NotifyContext`, rejected: it cannot tell a second signal apart, which
  FR-028 needs. A runtime option for the deadline was also rejected; 10 s is a fixed constant.

## R9. `stdio` and the end of stdin

- **Decision:** in this feature the `stdio` placeholder reads and discards stdin in a goroutine. EOF or a read
  error cancels the run context exactly like a first signal, with the reason `stdin closed`. Feature 005 swaps
  the reader for the MCP stdio transport and keeps the same cancellation.
- **Rationale:** FR-003 and acceptance scenario US5-3. It also makes the stdout check easy:
  `whatsapp-mcp stdio </dev/null` starts, shuts down at once and must write 0 bytes to stdout.

## R10. Build information

- **Decision:** `internal/buildinfo` has the package variables `Version = "dev"`, `Commit = ""` and
  `Date = ""`, which are set with `-ldflags "-X github.com/tiger-seo/whatsapp-mcp/internal/buildinfo.Version=…"`
  (feature 011 wires this into GoReleaser).
  - When `Commit` or `Date` is empty, they fall back to `debug.ReadBuildInfo()` `vcs.revision` (with `-dirty`
    appended when `vcs.modified=true`) and `vcs.time`, then to `unknown`.
  - The toolchain is `runtime.Version()`, and the target is `runtime.GOOS/runtime.GOARCH`.
  - `debug.BuildInfo.Main.Version` is deliberately ignored. Since Go 1.24 it is stamped from VCS tags as a
    pseudo-version, but the spec wants a local build to read `dev`.
- **Rationale:** FR-002 and US1-2. `version` touches nothing outside the binary (US1-3).

## R11. Exit codes

- **Decision:** `0` success and graceful shutdown; `1` general error (lock held, data directory unusable, command
  not implemented yet, forced exit, shutdown timeout); `2` usage error and invalid configuration. `--help`
  exits 0, while no command or an unknown command exits 2 after printing usage to stderr.
- **Rationale:** FR-005. `2` for usage errors matches the `flag` package's own `ExitOnError` convention. The
  dispatcher uses `ContinueOnError` and maps the error itself, so tests can call it in process.

## R12. Testing approach

- **Decision:** only the standard library `testing` package, with no assertion library. Tests come at three
  levels:
  1. **Unit tests** in `internal/config`, using an injected environ, flag map and `UserConfigDir` function, and
     `t.TempDir()` for `config.toml`. They cover:
     - every pair of sources (flag>env, flag>file, flag>default, env>file, env>default, file>default)
     - an invalid value in each source
     - TOML syntax, an unknown key, a wrong type and `data_dir` in the file, each with its line
     - an empty environment variable
     - an unknown `WHATSAPP_MCP_*` variable
     - log level case and `warning`
     - `LOG_LEVEL` being ignored
     - relative paths, a file where the data directory should be, and a failing `UserConfigDir`.
  2. **Unit tests** in `internal/lock`:
     - acquire, then a second acquire in the same process fails with the holder's PID
     - release, then acquire again
     - a PID that cannot be read gives "unknown"
     - a stale file with garbage in it is ignored.
  3. **Process tests** in `cmd/whatsapp-mcp` (`e2e_test.go`). `TestMain` builds the binary once with
     `CGO_ENABLED=0` into a temporary directory. The tests then run it and check:
     - the `version` output and that it ignores broken configuration
     - help and the exit codes
     - a second instance, whose message names the first one's PID
     - that killing the holder (`Process.Kill`) frees the lock
     - that data directories A and B can run side by side
     - that `status` is not blocked by a running instance
     - that stdin EOF stops `stdio` with exit 0
     - that `stdio` at `debug` writes 0 bytes to stdout
     - that a stop signal exits 0, and that a second signal exits non-zero.
- **Signals in tests:**
  - On Unix: `Process.Signal(syscall.SIGINT/SIGTERM)`.
  - On Windows: start the child with `CREATE_NEW_PROCESS_GROUP` and send `CTRL_BREAK_EVENT` through
    `windows.GenerateConsoleCtrlEvent`. Go's own `os/signal` tests use the same technique. A new process group
    ignores `CTRL_C_EVENT`, so Ctrl+C itself is checked by hand in [quickstart.md](quickstart.md).
  - These helpers live in `signal_unix_test.go` and `signal_windows_test.go`.
- **Rationale:** Q3 requires the config precedence and validation tests. The process tests are the only way to
  prove the lock, signal and stdout requirements on each OS, which is why CI runs them on three OSes (FR-033).

## R13. CI workflow

- **Decision:** `.github/workflows/ci.yml` runs on `push` (all branches) and `pull_request`, with
  `permissions: contents: read` and `concurrency` per ref with `cancel-in-progress`. It uses `actions/checkout@v7`
  and `actions/setup-go@v7` (the latest releases on 2026-10-27), with `go-version-file: go.mod`. Every job sets
  `CGO_ENABLED: "0"`.
  - **`check`** (ubuntu-latest):
    - `gofmt -l .` must print nothing. Otherwise the step prints the files and fails (FR-031).
    - `go vet ./...` must pass.
  - **`test`**: matrix `os: [ubuntu-latest, windows-latest, macos-latest]`, `fail-fast: false`, running
    `go test ./...` (FR-033).
  - **`build`**: matrix over the five `goos/goarch` pairs on ubuntu-latest, `fail-fast: false`. The job is named
    `build (<goos>/<goarch>)`, so a failure names its target (FR-032). Each runs `go vet ./...` and
    `go build -o /dev/null ./cmd/whatsapp-mcp` with `GOOS` and `GOARCH` set. The per-target `vet` catches
    problems in OS-specific files that the Linux `check` job never compiles.
- **Rationale:** Q2 and SC-007. A cross-build from one Linux runner is enough because `CGO_ENABLED=0`, and
  building on a runner without a C toolchain is itself the proof.
- **Alternatives considered:**
  - GoReleaser snapshot builds in CI, rejected: that is feature 011's tool (D12).
  - `golangci-lint`, rejected: Q2 asks for `go vet` only, and it is another tool to pin.
  - The race detector (`-race`), rejected: it needs CGO on most targets, which the constitution forbids.
