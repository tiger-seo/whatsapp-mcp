---

description: "Task list for the binary skeleton (CLI, configuration, logging, lock, CI)"
---

# Tasks: Binary skeleton (CLI, configuration, logging, lock, CI)

**Input**: Design documents from `specs/001-binary-skeleton/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md),
[contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. Constitution Principle VI and Q3 require config precedence and validation tests in the same
change, and research R12 defines unit and process tests. Within each story the tests are written first. Where a
story's code already exists from an earlier story, its tests may pass at once; that is noted on the task.

**Organization**: Tasks are grouped by user story. The stories in this feature build on each other (the startup
sequence in `cmd/whatsapp-mcp/commands.go` grows one step per story), so they are delivered in order rather than in
parallel. See "Dependencies".

**Already done, no task:** `docs/specification.md` §8.7 M10 already names `WHATSAPP_MCP_LOG_LEVEL` (the spec's
"Settled in this spec" item).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: The user story the task belongs to (US1–US6)
- Paths are relative to the repository root (`D:\projects-mou\whatsapp-mcp-next`)

## Conventions for every task

- Module path: `github.com/tiger-seo/whatsapp-mcp-next`. Go 1.27.0 from `go.mod`. Always `CGO_ENABLED=0`.
- On this machine, before any `go` command: `export GOMODCACHE=D:/tmp/gocache/mod GOCACHE=D:/tmp/gocache/build
  GOTMPDIR=D:/tmp CGO_ENABLED=0` (C: is nearly full).
- Standard library `testing` only, no assertion library. Use `t.TempDir()` for files.
- No requirement IDs (FR-, SC-, US, R…, D…) in code comments.
- Message texts, exit codes and log attributes come verbatim from [contracts/cli.md](contracts/cli.md) and
  [contracts/configuration.md](contracts/configuration.md); tests match on them.
- Exit codes: `0` success / `--help` / graceful shutdown; `1` general error; `2` usage error and invalid
  configuration.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Package layout and the entry point.

- [ ] T001 Create `cmd/whatsapp-mcp/main.go` with `package main` and `func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ())) }`, plus a stub `run(args []string, stdin io.Reader, stdout, stderr io.Writer, environ []string) int` in `cmd/whatsapp-mcp/cli.go` that returns 2, so `go build ./...` succeeds
- [ ] T002 [P] Create the empty packages `internal/config/doc.go`, `internal/lock/doc.go` and `internal/buildinfo/doc.go`, each with a one-line package comment (`// Package config resolves and validates the settings of whatsapp-mcp.` and the equivalent for lock and buildinfo)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The command dispatcher and the settings registry. Every story depends on both: the dispatcher routes
the five commands, and the registry generates every command's flags and help.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [ ] T003 [P] Write registry tests in `internal/config/setting_test.go`: (a) setting names are unique; (b) every `Env` equals `"WHATSAPP_MCP_" + strings.ToUpper(Name)`; (c) every `FileKey` is empty or equal to `Name`; (d) log level text parsing accepts `debug`, `info`, `warn`, `warning` (= `slog.LevelWarn`), `error` in any letter case (`DEBUG`, `Info`) and rejects `verbose` and `""` with a reason that lists `debug, info, warn or error`; (e) log level TOML parsing accepts a TOML string and rejects an `int64` and a `bool` with a type error naming `string`; (f) data dir text parsing accepts an absolute path and returns it `filepath.Clean`ed, and rejects `data` and `./data` with the reason `is not an absolute path`; on Windows (`runtime.GOOS == "windows"`) it also rejects `\foo` and `C:foo`
- [ ] T004 Implement the registry in `internal/config/setting.go` per [data-model.md](data-model.md) "Setting": a `Setting` struct with `Name`, `Flag`, `Env`, `FileKey`, `Default func(env) (string, error)`, `Usage`, `Secret`, `ParseText func(string) (any, error)`, `ParseTOML func(any) (any, error)`, `Apply func(*Config, any)`; a `Settings()` function returning the registry in this order: `data_dir` (flag `data-dir`, env `WHATSAPP_MCP_DATA_DIR`, `FileKey` empty = "not allowed in the file", usage `Data directory`, validation: `filepath.IsAbs` then `filepath.Clean`) and `log_level` (flag `log-level`, env `WHATSAPP_MCP_LOG_LEVEL`, file key `log_level`, TOML string, default `info`, usage `Log level: debug, info, warn, error`, values case-insensitive `debug`, `info`, `warn`, `warning` (= `warn`), `error` → `slog.Level`). Derive `Env` in code from `Name`, never write it by hand. Declare a minimal `Config` struct with `DataDir string` and `LogLevel slog.Level` in `internal/config/config.go` so `Apply` compiles. Make T003 pass
- [ ] T005 Implement the dispatcher in `cmd/whatsapp-mcp/cli.go` per [contracts/cli.md](contracts/cli.md) "Synopsis", "Commands" and research R1/R11: exit code constants `exitOK = 0`, `exitError = 1`, `exitUsage = 2`; a command table in this order with these help lines: `stdio` "MCP over stdio, for Claude Desktop and Cursor", `serve` "Daemon: MCP over HTTP at /mcp", `pair` "Pair this device with a terminal QR code", `status` "Print connection, login and version state", `version` "Print build information"; `run` dispatches on `args[0]`: no arguments → `whatsapp-mcp: missing command` + top-level usage on stderr, exit 2; `-h`/`--help`/`-help` → top-level usage on stdout, exit 0; any other argument starting with `-` → `whatsapp-mcp: flags go after the command, e.g. "whatsapp-mcp stdio --log-level debug"`, exit 2; unknown name → `whatsapp-mcp: unknown command "foo"` + usage on stderr, exit 2. Top-level usage text exactly as in the contract's "Help output" block. Each command handler is a stub for now that prints `whatsapp-mcp: <command> is not implemented yet` to stderr and returns 1
- [ ] T006 Add per-command flag parsing in `cmd/whatsapp-mcp/cli.go`: for every command except `version`, build a `flag.FlagSet` named after the command with `flag.ContinueOnError` and `SetOutput(io.Discard)`, registering one string flag per registry setting (`fs.String(s.Flag, "", s.Usage)`; the default is not put in the flag, because resolution needs to know whether the flag was set). `version` gets a FlagSet with no flags. After `Parse`: `flag.ErrHelp` → command help on stdout, exit 0; any other parse error → `whatsapp-mcp: <command>: <flag package error>` (e.g. `whatsapp-mcp: stdio: flag provided but not defined: -foo`) + command help on stderr, exit 2; any remaining positional argument → `whatsapp-mcp: <command>: unexpected argument "x"`, exit 2. Collect the set flags with `fs.Visit` into a `map[string]string` keyed by flag name for `config.Inputs.Flags`. Command help format: `Usage:\n  whatsapp-mcp <command> [flags]\n\n<help line>\n\nFlags:\n` then one line per setting `  --<flag> string   <Usage> (env <Env>, default <default>)`; compute the data dir default with `os.UserConfigDir()` + `whatsapp-mcp`, and print `<user config dir>/whatsapp-mcp` when that call fails

**Checkpoint**: `go build ./...` and `go test ./internal/config` pass; every command name is routed.

---

## Phase 3: User Story 1 - Identify the installed binary (Priority: P1) 🎯 MVP

**Goal**: `whatsapp-mcp version` prints build information, and help and usage errors work.

**Independent Test**: Build the binary, run `whatsapp-mcp version`, `--help`, no command and an unknown command,
and check the output and exit codes (quickstart §2).

### Tests for User Story 1 ⚠️

> Write these tests first and check that they fail before implementing.

- [ ] T007 [P] [US1] Write `internal/buildinfo/buildinfo_test.go`: with the package variables at their defaults, `Get()` returns `Version == "dev"`, `GoVersion == runtime.Version()`, `Target == runtime.GOOS + "/" + runtime.GOARCH`, and non-empty `Commit` and `Date` (either a VCS value or `unknown`); with `Commit` and `Date` set (save and restore them in the test), `Get()` returns them unchanged; a table test of the fallback helper: `vcs.revision=abc` + `vcs.modified=true` → `abc-dirty`; `vcs.modified=false` → `abc`; no settings → `unknown` for both commit and date
- [ ] T008 [P] [US1] Write in-process tests in `cmd/whatsapp-mcp/cli_test.go` that call `run` with `bytes.Buffer` streams and an explicit environ: `version` → exit 0, empty stderr, stdout has exactly 5 lines whose prefixes in order are `whatsapp-mcp `, `commit:`, `built:`, `go:`, `target:`; `version` with environ `WHATSAPP_MCP_LOG_LEVEL=verbose` and `WHATSAPP_MCP_DATA_DIR=relative` → same output, exit 0, and no file created under a `t.TempDir()` used as the would-be data dir; `--help` and `-h` → exit 0, stdout lists all five commands with their help lines; no arguments and `frobnicate` → exit 2, stderr starts with `whatsapp-mcp: missing command` / `whatsapp-mcp: unknown command "frobnicate"` and contains the command list; `--log-level debug stdio` → exit 2 with the "flags go after the command" message; `stdio --help` → exit 0, stdout contains `--data-dir`, `WHATSAPP_MCP_DATA_DIR`, `--log-level`, `WHATSAPP_MCP_LOG_LEVEL` and `default info`; `stdio --foo` → exit 2, stderr contains `whatsapp-mcp: stdio: flag provided but not defined: -foo`; `status x` → exit 2, stderr contains `whatsapp-mcp: status: unexpected argument "x"`; `version --log-level debug` → exit 2 (version has no flags)

### Implementation for User Story 1

- [ ] T009 [P] [US1] Implement `internal/buildinfo/buildinfo.go` per research R10 and [data-model.md](data-model.md) "Build information": package variables `Version = "dev"`, `Commit = ""`, `Date = ""` (settable with `-ldflags "-X github.com/tiger-seo/whatsapp-mcp-next/internal/buildinfo.Version=…"`); an `Info` struct with `Version`, `Commit`, `Date`, `GoVersion`, `Target`; `Get() Info` that fills an empty `Commit` from `debug.ReadBuildInfo()` setting `vcs.revision` (with `-dirty` appended when `vcs.modified=true`) and an empty `Date` from `vcs.time`, falling back to `unknown`; `GoVersion = runtime.Version()`, `Target = runtime.GOOS + "/" + runtime.GOARCH`. Deliberately ignore `debug.BuildInfo.Main.Version`. Keep the settings lookup in a helper that takes `[]debug.BuildSetting` so T007 can test it. Make T007 pass
- [ ] T010 [US1] Implement the `version` command in `cmd/whatsapp-mcp/commands.go`: print to stdout, one field per line, exactly `whatsapp-mcp <Version>`, `commit:  <Commit>`, `built:   <Date>`, `go:      <GoVersion>`, `target:  <Target>` (format in [contracts/cli.md](contracts/cli.md) "`version` output") and return 0. It must not read the environ, the config file or the data directory. Move the stub handlers of `stdio`, `serve`, `pair` and `status` from `cli.go` into `commands.go`. Make T008 pass

**Checkpoint**: `go test ./cmd/... ./internal/buildinfo` passes; `whatsapp-mcp version` works with any environment.

---

## Phase 4: User Story 2 - Configure the binary in layers and get clear errors (Priority: P1)

**Goal**: every setting resolves as flag > `WHATSAPP_MCP_*` environment variable > `config.toml` > default, every
invalid value is rejected at startup with exit 2 and a precise message, and one `slog` logger is built from the
effective level.

**Independent Test**: quickstart §3, and `go test ./internal/config`.

### Tests for User Story 2 ⚠️

- [ ] T011 [P] [US2] Write precedence and environment tests in `internal/config/config_test.go`, calling `Load(Inputs{Flags, Environ, UserConfigDir})` with a fake `UserConfigDir` returning `t.TempDir()` and an explicit `--data-dir` pointing to a temp dir where `config.toml` is written when needed. Cover every pair of sources for `log_level` with distinct values (flag>env, flag>file, flag>default, env>file, env>default, file>default), checking both `LogLevel` and `Origin("log_level")` (`Source` and `Detail`: `--log-level`, `WHATSAPP_MCP_LOG_LEVEL`, `<path> line <n>`, `default`); the spec's acceptance set: file `warn` + env `info` + flag `debug` → debug; file + env → info; file only → warn; nothing → info; `WHATSAPP_MCP_LOG_LEVEL=` (empty) → treated as unset, file value wins; `LOG_LEVEL=debug` alone → `info` and no warning; `WHATSAPP_MCP_LOGLEVEL=debug` → one `Warning` naming `WHATSAPP_MCP_LOGLEVEL` and no error; on Windows only, `whatsapp_mcp_log_level=debug` → debug (case-insensitive names), on other OSes it is ignored with no warning (the prefix does not match exactly); `data_dir` precedence flag > env > default; an invalid value from each source it can come from (`--log-level verbose`, `WHATSAPP_MCP_LOG_LEVEL=verbose`, `log_level = "verbose"` in the file, `--data-dir data`, `WHATSAPP_MCP_DATA_DIR=data`) → error; two invalid settings at once → both reported in the one joined error
- [ ] T012 [P] [US2] Write `config.toml` tests in `internal/config/file_test.go`: missing file → defaults, no error; empty file → defaults; data dir does not exist → no error and nothing created; `log_level = "info` (unterminated) → `FileError` with line 1 and a column, and an env error in the same run is still reported; `# comment\n\nloglevel = "info"` → `<path> line 3: unknown key "loglevel"`; `[section]` → unknown key error; `log_level = 5` on line 1 → `<path> line 1: log_level must be a string, not an integer` (contract form `<key> must be a <type>, not a <toml type>`); `data_dir = "/x"` → `<path> line <n>: data_dir cannot be set in config.toml, because the file is inside the data directory; use --data-dir or WHATSAPP_MCP_DATA_DIR`; unreadable file (on non-Windows, `chmod 000`; skip when running as root or on Windows) → `<path>: <os error>`; `log_level = "verbose"` on line 2 → `log_level: "verbose" from <path> line 2 is not valid; use debug, info, warn or error`
- [ ] T013 [P] [US2] Write message-format tests in `internal/config/errors_test.go`: each form in [contracts/configuration.md](contracts/configuration.md) "Message forms by source" renders exactly (flag, environment, file value, TOML syntax, unknown key, wrong type, `data_dir` in file, unreadable); `FormatError(err)` of a joined error renders the header `whatsapp-mcp: invalid configuration:` followed by one line per error indented by two spaces, as in the contract example

### Implementation for User Story 2

- [ ] T014 [US2] Add `github.com/pelletier/go-toml/v2@v2.4.3` with `go get` (updates `go.mod` and `go.sum`)
- [ ] T015 [P] [US2] Implement `internal/config/errors.go`: `SettingError{Setting, Origin, Value, Reason}` with `Error()` = `<name>: "<value>" from <origin detail> <reason>`; `FileError{Path, Line, Column, Key, Reason}` with the forms `<path> line <n>, column <c>: <msg>`, `<path> line <n>: <msg>`, `<path>: <msg>` depending on which positions are known; `FormatError(err error) string` that unwraps an `errors.Join` result and renders the header plus indented lines. Make T013 pass
- [ ] T016 [P] [US2] Implement `internal/config/env.go` per research R4: build a lookup from the `Inputs.Environ` snapshot (`KEY=VALUE` strings; split on the first `=`; skip Windows' `=C:=…` entries that start with `=`); an empty value counts as unset; on Windows compare names case-insensitively (`strings.EqualFold`), elsewhere exactly; return the value for a registry `Env` name, and list every `WHATSAPP_MCP_*` name (prefix compared with the same case rule) that is not in the registry as a `Warning{Name}`, once per name, sorted. Never read `LOG_LEVEL` or any other unprefixed name
- [ ] T017 [P] [US2] Implement `internal/config/file.go` per research R2: `readFile(dataDir) (values map[string]any, lines map[string]int, err error)`; a missing directory or file returns no values and no error; any other read error → `FileError{Path, Reason: <os error>}`; decode with `toml.Unmarshal` into `map[string]any`; a `*toml.DecodeError` → `FileError` with its `Position()` line and column and its message, and stop processing the file; build the key → line index with `unstable.Parser` (`Shape(Range(...))`) over the top-level key-value and table expressions; then, per top-level key: a table or a key not in the registry → `unknown key "<key>"`; `data_dir` → the "cannot be set in config.toml" message; a value whose Go type does not match → `<key> must be a string, not a <toml type>` (with the article: `int64` → `an integer`, `float64` → `a float`, `bool` → `a boolean`, `time.*`/`toml.Local*` → `a datetime`, `[]any` → `an array`, `map[string]any` → `a table`); otherwise pass the value to the setting's `ParseTOML` and turn a parse failure into a `SettingError` whose origin is `<path> line <n>`
- [ ] T018 [US2] Implement data dir resolution in `internal/config/datadir.go` (needed by `Load` to find `config.toml`): `resolveDataDir(in Inputs, env)` takes the `--data-dir` flag, then `WHATSAPP_MCP_DATA_DIR`, then the default `filepath.Join(UserConfigDir(), "whatsapp-mcp")`; a failing `UserConfigDir` returns an error whose message is `cannot find the per-user configuration directory (<os error>); set --data-dir or WHATSAPP_MCP_DATA_DIR`; an override that is not `filepath.IsAbs` is a `SettingError` with reason `is not an absolute path`; the result is `filepath.Clean`ed. Never call `os.Getwd`. Wire it into the registry's `data_dir` `Default`
- [ ] T019 [US2] Implement `Load` in `internal/config/config.go` per research R3 and [data-model.md](data-model.md) "Config" and "Origin": `Inputs{Flags map[string]string; Environ []string; UserConfigDir func() (string, error)}`; `Source` enum `Default`, `File`, `Env`, `Flag` with `String()` `default`, `file`, `env`, `flag`; `Origin{Source, Detail}`; `Load(in) (*Config, []Warning, error)` that (1) resolves the data dir (T018), (2) reads `<data dir>/config.toml` (T017) without creating anything, (3) resolves every other setting flag > env > file > default via `ParseText`/`ParseTOML` and `Apply`, recording each `Origin` in an unexported `origins` map, (4) collects every `SettingError` and `FileError` and returns them with `errors.Join`. Add `(*Config).Origin(name) Origin` and `(*Config).LogValues() [][]slog.Attr` (or equivalent) returning per setting `setting`, `value` (`(secret)` when `Secret`), `source`, `origin`. Make T011 and T012 pass
- [ ] T020 [US2] Wire configuration and the logger into `cmd/whatsapp-mcp/commands.go` for `stdio`, `serve`, `pair` and `status` (startup steps 2–6 of the plan): pass the visited flags, `environ` and `os.UserConfigDir` to `config.Load`; on error write `config.FormatError(err)` to stderr (for the missing user config dir error, the message is `whatsapp-mcp: cannot find the per-user configuration directory (…); set --data-dir or WHATSAPP_MCP_DATA_DIR`) and return 2; otherwise build the one logger `slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))`, call `slog.SetDefault`, log each `Warning` at WARN as `unknown environment variable` with `name`, then log each setting at DEBUG as `config` with `setting`, `value`, `source`, `origin`. The commands still end with the not-implemented stub
- [ ] T021 [US2] Extend `cmd/whatsapp-mcp/cli_test.go` with in-process configuration tests: `status --data-dir <tmp> --log-level debug` with `WHATSAPP_MCP_LOG_LEVEL=info` and `log_level = "warn"` in the file → stderr has `level=DEBUG msg=config setting=log_level value=debug source=flag origin=--log-level`; the same without the flag → no `level=DEBUG` line; `WHATSAPP_MCP_LOG_LEVEL=verbose` → exit 2, stderr starts with `whatsapp-mcp: invalid configuration:` and contains `log_level`, `"verbose"`, `WHATSAPP_MCP_LOG_LEVEL` and `debug, info, warn or error`; `WHATSAPP_MCP_LOGLEVEL=debug` → stderr has `level=WARN msg="unknown environment variable" name=WHATSAPP_MCP_LOGLEVEL`; every stderr log line matches `^time=\S+ level=(DEBUG|INFO|WARN|ERROR) msg=`; stdout stays empty in all of these

**Checkpoint**: `go test ./internal/config ./cmd/...` passes; invalid configuration exits 2 and creates nothing.

---

## Phase 5: User Story 3 - Data lives in one predictable per-user directory (Priority: P1)

**Goal**: the data directory defaults to `<os.UserConfigDir()>/whatsapp-mcp` whatever the working directory is,
and it is created (mode 0700) only after the configuration is valid.

**Independent Test**: quickstart §3 rows for `--data-dir data`, a file in place of the directory, a nested new
directory, and the working-directory check.

### Tests for User Story 3 ⚠️

- [ ] T022 [P] [US3] Write `internal/config/datadir_test.go`: the default is `filepath.Join(<fake UserConfigDir>, "whatsapp-mcp")`; the default does not change after `os.Chdir` to another temp dir (use `t.Chdir`); a failing fake `UserConfigDir` with no override → error mentioning `--data-dir` and `WHATSAPP_MCP_DATA_DIR`; a failing `UserConfigDir` with `--data-dir` set → no error; `--data-dir` and `WHATSAPP_MCP_DATA_DIR` relative → `is not an absolute path`; an absolute path with `..` and a trailing separator is cleaned; `PrepareDataDir` on `<tmp>/new/nested` creates it, and on non-Windows its mode is `0700`; `PrepareDataDir` on an existing directory with mode `0755` leaves the mode unchanged (non-Windows); `PrepareDataDir` on an existing regular file → error containing the path and `exists and is not a directory`. The resolution cases pass at once (T018); the `PrepareDataDir` cases fail until T023

### Implementation for User Story 3

- [ ] T023 [US3] Implement `PrepareDataDir(dir string) error` in `internal/config/datadir.go` per research R5: `os.MkdirAll(dir, 0o700)`, then `os.Stat` and check `IsDir`; never change the permissions of a directory that exists; errors are `data directory <path>: <reason>` with the reasons `exists and is not a directory` or the OS error text (e.g. `permission denied`). Make T022 pass
- [ ] T024 [US3] Call `config.PrepareDataDir(cfg.DataDir)` in `cmd/whatsapp-mcp/commands.go` after the debug dump, for `stdio`, `serve`, `pair` and `status` (step 7 of the plan); on failure print `whatsapp-mcp: data directory <path>: <reason>` to stderr and return 1. After it, `status` prints `whatsapp-mcp: status is not implemented yet` to stderr and returns 1 (its final behaviour in this feature)
- [ ] T025 [US3] Extend `cmd/whatsapp-mcp/cli_test.go`: `status --data-dir <tmp>/a/b` → exit 1 with `status is not implemented yet`, and `<tmp>/a/b` exists; `status --data-dir data` → exit 2 and nothing created in the working directory (run under `t.Chdir(t.TempDir())` and check it stays empty); `status --data-dir <path of a regular file>` → exit 1, stderr contains the path and `not a directory`; invalid `WHATSAPP_MCP_LOG_LEVEL` together with a non-existent `--data-dir` → exit 2 and the directory is not created

**Checkpoint**: `go test ./...` passes; `status` is complete for this feature.

---

## Phase 6: User Story 4 - Only one instance owns the data directory (Priority: P1)

**Goal**: `stdio`, `serve` and `pair` take an OS lock on `<data dir>/whatsapp-mcp.lock` and write their PID at
byte offset 1; a second instance fails at once and names the holder's PID.

**Independent Test**: `go test ./internal/lock` (same-process and cross-process contention, kill frees the lock).
End-to-end checks with the real binary are in Phase 7 (T033), because a holder that keeps running needs the
`serve` placeholder from US5.

### Tests for User Story 4 ⚠️

- [ ] T026 [P] [US4] Write `internal/lock/lock_test.go`: `Acquire(dir)` on a new temp dir succeeds, and the file `<dir>/whatsapp-mcp.lock` holds `<os.Getpid()>\n` from offset 1 (read it only after `Release` on Windows, or through `ReadAt` at offset 1); a second `Acquire` in the same process while the first is held → `*HeldError` with `PID == os.Getpid()`, `DataDir == dir`, returned without waiting (assert under 1 s); `Release` then `Acquire` succeeds; after `Release` the file is 0 bytes and still exists; `Release` twice is safe; a stale file containing garbage (`"\x00junk"`) is ignored by `Acquire`, and the content is replaced by the new PID; a lock held through a raw `flock.New(path).TryLock()` with nothing written → `HeldError.PID == 0`; `HeldError.Error()` renders `another whatsapp-mcp (PID 4242) is using the data directory <dir>` and, for PID 0, `another whatsapp-mcp (PID unknown) is using the data directory <dir>`. Cross-process: re-exec the test binary as a helper (`os.Args[0]`, `-test.run=TestHelperHolder`, env `LOCK_HELPER_DIR=<dir>`) that acquires the lock, prints `ready` on stdout and blocks reading stdin; the parent's `Acquire` → `HeldError.PID == child PID`; after `cmd.Process.Kill()` and `Wait()`, the parent's `Acquire` succeeds at the first attempt

### Implementation for User Story 4

- [ ] T027 [US4] Add `github.com/gofrs/flock@v0.13.1` with `go get` (updates `go.mod` and `go.sum`; it brings in `golang.org/x/sys`)
- [ ] T028 [US4] Implement `internal/lock/lock.go` per research R6 and [data-model.md](data-model.md) "Lock file": constant file name `whatsapp-mcp.lock`; `Acquire(dataDir string) (*Lock, error)` opens `flock.New(path, flock.SetFlag(os.O_CREATE|os.O_RDWR))` and calls `TryLock` (never the blocking `Lock`); when not acquired, read up to 32 bytes from offset 1 through a fresh `os.Open` + `ReadAt`, trim NUL and white space, parse a positive integer (else 0), and return `&HeldError{Path, DataDir, PID}`; when acquired, open a second handle with `os.OpenFile(path, os.O_RDWR, 0o600)`, `Truncate(1)`, then `WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 1)`. Byte 0 is never read or written (it is under a mandatory lock on Windows); say so in a short comment. `(*Lock).Release() error` truncates the file to 0 bytes through the second handle while still locked, then unlocks and closes both handles; it is idempotent; the file is never deleted. Make T026 pass
- [ ] T029 [US4] Take the lock in `cmd/whatsapp-mcp/commands.go` for `stdio`, `serve` and `pair` after `PrepareDataDir` (step 8 of the plan; `status` and `version` never take it): on `*lock.HeldError` print `whatsapp-mcp: ` + its message to stderr and return 1; on success log DEBUG `lock acquired` with `path` and `pid`, and release it before returning. After the lock, `pair` prints `whatsapp-mcp: pair is not implemented yet` and returns 1 (its final behaviour in this feature); `stdio` and `serve` keep the stub until US5
- [ ] T030 [US4] Extend `cmd/whatsapp-mcp/cli_test.go`: hold `lock.Acquire(dir)` in the test, then `run` `pair --data-dir dir`, `stdio --data-dir dir` and `serve --data-dir dir` → each exit 1 within 1 s with `another whatsapp-mcp (PID <os.Getpid()>) is using the data directory <dir>`; `status --data-dir dir` is not blocked (exit 1 with `status is not implemented yet`); after releasing, `pair --data-dir dir` → exit 1 with `pair is not implemented yet` and the lock file is 0 bytes afterwards

**Checkpoint**: `go test ./...` passes; a held lock is reported with the holder's PID.

---

## Phase 7: User Story 5 - Clean shutdown and clean output channels (Priority: P2)

**Goal**: `stdio` and `serve` run until stopped; the first SIGINT/SIGTERM (Ctrl+C / Ctrl+Break on Windows) or, for
`stdio`, stdin EOF stops them in order within 10 s and exits 0; a second signal exits 1 at once; stdout stays empty.

**Independent Test**: quickstart §4 and §5, and `go test ./cmd/whatsapp-mcp` (process tests).

### Tests for User Story 5 ⚠️

- [ ] T031 [P] [US5] Write lifecycle unit tests in `cmd/whatsapp-mcp/lifecycle_test.go` against the injectable lifecycle of T034 (signal channel, deadline and exit function as fields): stop steps run in reverse order of registration; a step returning an error logs `stop failed` with `step` and `error`, the remaining steps still run, and the result is exit 1; a step that blocks past a short test deadline (e.g. 50 ms) → `shutdown timed out` with `waiting_for=<step name>` and exit 1; a first signal value `os.Interrupt` cancels the run context and logs `shutting down` with `reason=interrupt`, `syscall.SIGTERM` gives `reason=terminated`; a second signal during a blocking stop step logs `forced exit` and calls the exit function with 1; a request with reason `stdin closed` behaves like a first signal; a clean stop logs `stopped` and returns 0; a stop request before startup finished still runs the steps registered so far and returns 0
- [ ] T032 [P] [US5] Write the signal helpers: `cmd/whatsapp-mcp/signal_unix_test.go` (`//go:build !windows`) with `startForSignal(cmd *exec.Cmd)` (no-op) and `sendStop(p *os.Process, sig string)` sending `syscall.SIGINT` or `syscall.SIGTERM`; `cmd/whatsapp-mcp/signal_windows_test.go` (`//go:build windows`) where `startForSignal` sets `cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}` and `sendStop` calls `windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.Pid))` from `golang.org/x/sys/windows` (a new process group ignores `CTRL_C_EVENT`; real Ctrl+C is checked by hand in quickstart §5)
- [ ] T033 [US5] Write process tests in `cmd/whatsapp-mcp/e2e_test.go` per research R12: `TestMain` builds the binary once with `go build` and `CGO_ENABLED=0` into `os.MkdirTemp` (name `whatsapp-mcp.exe` on Windows), runs the tests, removes it; a helper starts the binary with a per-test `--data-dir`, captures stdout and stderr separately and waits for a given stderr line with a timeout. Cases: `version` output and exit 0 with `WHATSAPP_MCP_LOG_LEVEL=verbose`; `serve` logs `serve: HTTP server not implemented yet` and keeps running, a second `stdio` on the same dir exits 1 within 1 s naming the first process's PID and the dir, `pair` gives the same message, `status` is not blocked; a `stdio` on another dir (`<dir>-b`) runs alongside; `Process.Kill()` of the holder, then a new `stdio` with empty stdin exits 0 at the first attempt; `stdio --log-level debug` with stdin closed at once exits 0 with `reason="stdin closed"` on stderr and **0 bytes** on stdout; `serve` + `sendStop` → `shutting down`, `stopped`, exit 0 within 10 s, and the lock can be taken again at once (`lock.Acquire` in the test succeeds); on Unix also SIGTERM → `reason=terminated`. The second-signal case is covered by T031, because the shutdown in this feature is instant. These tests fail until T034–T036

### Implementation for User Story 5

- [ ] T034 [US5] Implement `cmd/whatsapp-mcp/lifecycle.go` per research R8 and [data-model.md](data-model.md) "Run lifecycle": `const shutdownTimeout = 10 * time.Second`; a `lifecycle` type created with `newLifecycle(logger)` that installs `signal.Notify(ch, os.Interrupt, syscall.SIGTERM)` before anything else and exposes the run `context.Context`; `addStop(name string, stop func(context.Context) error)`; `requestStop(reason string)` for non-signal reasons (stdin EOF); a goroutine that on the first signal (or request) logs INFO `shutting down` with `reason` (`sig.String()`: `interrupt`, `terminated`) and cancels the context, and on any later signal logs WARN `forced exit` and calls the injected exit function (`os.Exit` in production) with 1; `shutdown() int` that runs the stop steps in reverse order under a `shutdownTimeout` deadline, logs ERROR `stop failed` (`step`, `error`) for a failing step and continues, logs ERROR `shutdown timed out` with `waiting_for=<name>` and returns 1 if the deadline passes, otherwise logs INFO `stopped` and returns 0 (1 if any step failed); `signal.Stop` at the end. Make T031 pass
- [ ] T035 [US5] Rework `cmd/whatsapp-mcp/commands.go` so `stdio` and `serve` follow the plan's startup sequence: create the lifecycle first (step 1, before flag parsing and `config.Load`), check `ctx.Err()` between the startup steps and go straight to `shutdown()` when a stop was requested, register the lock as stop step `lock` (its `Release`) as soon as it is taken, then run the body. Also give the lifecycle a logger before the real one exists (a discarding or stderr text logger at INFO), and switch it to the configured logger once built
- [ ] T036 [US5] Implement the placeholder bodies in `cmd/whatsapp-mcp/commands.go`: `serve` logs INFO `serve: HTTP server not implemented yet` and waits for the context; `stdio` logs INFO `stdio: MCP server not implemented yet`, starts a goroutine that copies stdin to `io.Discard` and on EOF or read error calls `requestStop("stdin closed")`, and waits for the context. Neither writes to stdout. Both return `shutdown()`'s exit code. Make T033 pass on this machine (Windows)

**Checkpoint**: `go test ./...` passes; Ctrl+C on `serve` exits 0 and frees the lock; `stdio` writes nothing to stdout.

---

## Phase 8: User Story 6 - Every change is checked on every target (Priority: P2)

**Goal**: GitHub Actions checks formatting, vets, tests on three OSes and cross-builds the five targets with
`CGO_ENABLED=0`, with job names as fixed in [contracts/ci.md](contracts/ci.md).

**Independent Test**: push a commit and read the run; push a deliberately unformatted file on a scratch branch and
check that `check` fails and names it (quickstart §6).

### Implementation for User Story 6

- [ ] T037 [US6] Create `.github/workflows/ci.yml` per research R13 and [contracts/ci.md](contracts/ci.md): `name: ci`; `on: push` (all branches) and `pull_request`; `permissions: contents: read`; `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`; workflow-level `env: CGO_ENABLED: "0"`; every job uses `actions/checkout@v7` and `actions/setup-go@v7` with `go-version-file: go.mod`. Job `check` (ubuntu-latest): a step that runs `files=$(gofmt -l .)`, and if non-empty prints them (`echo "$files"`, plus `::error file=<f>::not gofmt-formatted` per file) and exits 1; then `go vet ./...`. Job `test`: `name: test (${{ matrix.os }})`, `strategy: { fail-fast: false, matrix: { os: [ubuntu-latest, windows-latest, macos-latest] } }`, `runs-on: ${{ matrix.os }}`, `go test ./...`. Job `build` (ubuntu-latest): `name: build (${{ matrix.goos }}/${{ matrix.goarch }})`, `fail-fast: false`, `matrix.include` of windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, with `GOOS`/`GOARCH` set from the matrix, running `go vet ./...` and `go build -o /dev/null ./cmd/whatsapp-mcp`. No artefacts
- [ ] T038 [US6] Validate the workflow locally: run the same commands as CI on this machine (`gofmt -l .` prints nothing; `go vet ./...`; `go test ./...`; and the five-target loop from quickstart §1 with `GOOS`/`GOARCH` set, also `GOOS=<target> go vet ./...` per target), reading each command's own exit code rather than through a pipe. Then **ask the user** before pushing the branch, and after pushing check that `check`, the three `test (…)` jobs and the five `build (…)` jobs are green (`gh run list --branch 001-binary-skeleton`, `gh run view <id>`). With the user's go-ahead, push a scratch branch with a deliberately unformatted Go file, check that `check` fails and names the file, and delete the scratch branch (local and remote) afterwards

**Checkpoint**: CI is green on the feature branch, and fails on an unformatted file.

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Tidy the module, update the docs that mention the code's absence, and record verification.

- [ ] T039 Run `go mod tidy` and check that `go.mod` lists `github.com/gofrs/flock v0.13.1`, `github.com/pelletier/go-toml/v2 v2.4.3` and `golang.org/x/sys` (direct, because `signal_windows_test.go` imports it), and nothing that needs CGO; `CGO_ENABLED=0 go build ./...` still passes
- [ ] T040 [P] Update `README.md`: replace only the line `**Status:** in development; no code yet.` with a status line saying the binary skeleton exists and only `version` works (the README is rewritten in feature 011)
- [ ] T041 [P] Update the "Current state" section of `AGENTS.md`: the code now exists (`cmd/whatsapp-mcp`, `internal/config`, `internal/lock`, `internal/buildinfo`, `.github/workflows/ci.yml`); feature 001 is done once verified; settings come from the registry in `internal/config/setting.go` (a new setting is one descriptor plus one `Config` field)
- [ ] T042 Run every scenario of [quickstart.md](quickstart.md) §1–§5 with the real binary on this machine (Windows, Git Bash), including the manual Ctrl+C check on `serve` and the working-directory check, which only creates the real `%AppData%\whatsapp-mcp` directory. Remove that directory afterwards if it did not exist before (check first and note it)
- [ ] T043 Create `specs/001-binary-skeleton/verification.md` recording, with commands, date (2026-10-27 or the actual day) and counts: `gofmt`/`go vet` results, `go test ./...` package and test counts, the five cross-builds, each quickstart scenario's result, and the CI run URL with the job results from T038
- [ ] T044 Check line endings and leftovers: `git ls-files --eol` shows no `w/crlf` for any tracked file (rewrite with LF if so); `gofmt -l .` is empty; no requirement IDs (`FR-`, `SC-`, `US`, `R1`…, `D14`…) in any `.go` comment (`grep -rnE '(FR|SC)-[0-9]|\bUS[0-9]|\bD1[0-9]?\b' --include=*.go .`)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup. Blocks every story.
- **US1 (Phase 3)**: depends on Foundational.
- **US2 (Phase 4)**: depends on Foundational. Independent of US1 in code, but both edit `cmd/whatsapp-mcp/commands.go`
  and `cli_test.go`, so do US1 first (T010 creates `commands.go`).
- **US3 (Phase 5)**: depends on US2 (`Load`, the data dir resolution of T018 and the startup wiring of T020).
- **US4 (Phase 6)**: `internal/lock` (T026–T028) depends only on Setup and can be built in parallel with US2/US3;
  wiring the lock into the commands (T029–T030) depends on US3 (`PrepareDataDir`).
- **US5 (Phase 7)**: depends on US4 (the lock is its first stop step; the process tests check the lock end to end).
- **US6 (Phase 8)**: T037 depends only on Setup and can be written any time; T038 needs all stories done so CI runs
  the full suite.
- **Polish (Phase 9)**: depends on all stories.

### Story completion order

```text
Setup → Foundational → US1 → US2 → US3 → US4 (lock package can start after Setup) → US5 → US6 → Polish
```

### Within each story

- Tests first, and they fail before the implementation (except where noted on the task).
- `internal/...` packages before their wiring in `cmd/whatsapp-mcp`.
- `go test ./...` is green at every checkpoint.

### Parallel Opportunities

- T001 ∥ T002 (Setup).
- T003 ∥ T005 (registry tests and the dispatcher are different packages); T006 needs T004 and T005.
- US1: T007 ∥ T008 ∥ T009.
- US2: T011 ∥ T012 ∥ T013 (tests); then T015 ∥ T016 ∥ T017 (different files of `internal/config`).
- US4: T026 and T028 (`internal/lock`) can run while US2/US3 are in progress.
- US5: T031 ∥ T032.
- US6: T037 can be written in parallel with any story.
- Polish: T040 ∥ T041.

---

## Parallel Example: User Story 2

```bash
# Tests together:
Task: "Precedence and environment tests in internal/config/config_test.go"
Task: "config.toml tests in internal/config/file_test.go"
Task: "Message-format tests in internal/config/errors_test.go"

# Then the independent building blocks together:
Task: "SettingError, FileError, FormatError in internal/config/errors.go"
Task: "Environment snapshot and unknown-prefix warnings in internal/config/env.go"
Task: "config.toml reader with key→line index in internal/config/file.go"
```

## Parallel Example: User Story 4 alongside User Story 2

```bash
Task: "Lock tests incl. cross-process helper in internal/lock/lock_test.go"
Task: "Acquire/Release/HeldError with the PID at offset 1 in internal/lock/lock.go"
# while US2's internal/config tasks are in progress
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1: Setup.
2. Phase 2: Foundational.
3. Phase 3: US1.
4. **Stop and validate**: `whatsapp-mcp version`, `--help` and the usage errors (quickstart §2).

### Incremental Delivery

1. Setup + Foundational → every command is routed.
2. US1 → `version` and help (MVP).
3. US2 → layered configuration and clear errors.
4. US3 → the data directory is created; `status` is final for this feature.
5. US4 → single instance; `pair` is final for this feature.
6. US5 → `stdio` and `serve` run until stopped; clean shutdown and clean stdout.
7. US6 → CI on every push.
8. Polish → docs, quickstart run, `verification.md`.

Each step leaves `go test ./...` green and can be committed on its own.

---

## Notes

- [P] tasks touch different files and do not depend on incomplete tasks.
- Commit after each task or checkpoint, with Conventional Commits that explain why; only when the user asks.
- Pushing (T038) is outward-facing: ask the user first.
- Nothing in this feature touches WhatsApp.
