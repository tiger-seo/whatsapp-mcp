# Implementation Plan: Binary skeleton (CLI, configuration, logging, lock, CI)

**Branch**: `001-binary-skeleton` | **Date**: 2026-10-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-binary-skeleton/spec.md`

## Summary

Create the `whatsapp-mcp` binary that every later feature builds on:

- the five A3 commands, where only `version` does real work
- one settings registry in `internal/config` that resolves each setting as flag > `WHATSAPP_MCP_*` environment
  variable > `config.toml` > default, and validates everything before any side effect
- the data directory from `os.UserConfigDir()`
- one `slog` text logger on stderr
- an OS file lock on `<data dir>/whatsapp-mcp.lock` that names the holder's PID
- graceful shutdown with a second-signal escape and a 10 s deadline
- a GitHub Actions workflow that checks formatting, vets, tests on three OSes and cross-builds the five targets
  with `CGO_ENABLED=0`.

The approach uses the standard library wherever it is enough: `flag`, `slog` and `os/signal`. It adds two pure-Go
dependencies: `gofrs/flock` (settled by D14) and `pelletier/go-toml/v2`, which reports lines for unknown keys and
type errors. A spike found that on Windows `gofrs/flock` takes a mandatory lock on byte 0. The PID is therefore
stored from byte offset 1, so that a second instance can still read it ([research.md](research.md) R6).

The settled log level variable is `WHATSAPP_MCP_LOG_LEVEL` (spec "Settled in this spec"; §8.7 M10 already
updated).

## Technical Context

**Language/Version**: Go 1.27.0 (`go.mod`), built with `CGO_ENABLED=0`

**Primary Dependencies**:

- `github.com/gofrs/flock` v0.13.1: the lock (D14). It pulls in `golang.org/x/sys`.
- `github.com/pelletier/go-toml/v2` v2.4.3: `config.toml` (D8).
- Standard library: `flag`, `log/slog`, `os/signal`, `runtime/debug`.

All of them are pure Go.

**Storage**: Files in the data directory only: `config.toml` (read) and `whatsapp-mcp.lock`. No database (feature
002).

**Testing**: `go test ./...` with the standard `testing` package. The layers are:

- unit tests for config and lock
- process tests that build the binary and drive it, covering the lock, the signals and stdout.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. Tests run on Linux,
Windows and macOS.

**Project Type**: CLI (one binary; later also a stdio MCP server and an HTTP daemon)

**Performance Goals**:

- `version` returns in under 1 s (SC-001).
- A second instance fails in under 1 s (SC-003).
- Graceful shutdown finishes in 10 s or less (SC-004).

**Constraints**:

- 0 bytes on stdout in `stdio` mode (SC-005).
- No dependency on the working directory (SC-006).
- No CGO.
- Invalid configuration has no side effects: nothing is created and no lock is taken.

**Scale/Scope**: 2 settings, 5 commands, about 1,000 lines of Go including tests. The registry must take later
features' settings without any change to its mechanism.

No NEEDS CLARIFICATION items remain. The open technical questions are settled in [research.md](research.md)
R1–R13.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Check | Result |
| --- | --- | --- |
| I. Spec is the source of truth | Covers A3, A4, C1, C2, C4, M10, D8, D10, D14 and Q2 as written. The one conflict (M10 vs D10) is settled in the spec, and §8.7 M10 already names `WHATSAPP_MCP_LOG_LEVEL`. No D1–D18 decision is reopened. | Pass |
| II. One self-contained binary | One module at the root, `cmd/whatsapp-mcp` plus `internal/...`. Both new dependencies are pure Go. CI cross-builds all five targets with `CGO_ENABLED=0`. | Pass |
| III. One process owns the data | OS lock on `<data dir>/whatsapp-mcp.lock` via `gofrs/flock`, with no stale lock after a crash. No path depends on the working directory, because relative overrides are rejected. There is no database yet, so no migrations. | Pass |
| IV. Protocol channel stays clean | One `slog` logger, always on stderr. `stdio` writes nothing to stdout, and a process test asserts 0 bytes. The secret flag in the registry keeps future secrets out of the debug dump. | Pass |
| V. Secure by default | Invalid configuration fails at startup with exit code 2 and a precise message. The data directory is created with mode 0700. HTTP, the allow-list, secrets and HMAC are not in scope here. | Pass |
| VI. Testable and tested | The config precedence and validation tests (Q3) are in this change, plus lock and process tests. CI runs `gofmt`, `go vet` and `go test ./...`. | Pass |
| VII. Correct MCP tools | No tools in this feature. | N/A |
| VIII. Real accounts handled with care | Nothing touches WhatsApp. The quickstart touches the real per-user directory only in one scenario, and only by creating it. | Pass |
| IX. Simplicity | The standard library for the CLI, logging and signals. Two dependencies, justified in R2 and R6. No log format option, no aliases for old names, no configurable shutdown deadline. | Pass |

**Post-design re-check (after Phase 1):** still passes. The design adds one package that A4 does not list,
`internal/lock`, and one more, `internal/buildinfo`. A4's table is a "suggested layout". Both packages sit under
`internal/`, as Principle II requires, so there is nothing to put in Complexity Tracking. The offset-1 layout of
the lock file is a consequence of D14 on Windows, not a departure from it.

## Project Structure

### Documentation (this feature)

```text
specs/001-binary-skeleton/
├── plan.md              # This file
├── research.md          # Phase 0: R1–R13
├── data-model.md        # Phase 1: Setting, Config, lock file, build info, lifecycle
├── quickstart.md        # Phase 1: end-to-end validation guide
├── contracts/
│   ├── cli.md           # commands, flags, help, version output, exit codes, streams, messages
│   ├── configuration.md # sources, precedence, config.toml, error formats
│   └── ci.md            # workflow jobs and what fails them
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source Code (repository root)

```text
cmd/whatsapp-mcp/
├── main.go              # os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
├── cli.go               # command table, per-command FlagSets from the registry, help, usage errors, exit codes
├── commands.go          # version, stdio, serve, pair, status
├── lifecycle.go         # signal handling, stop steps, 10 s deadline, second-signal exit
├── cli_test.go          # in-process: help, usage errors, version, exit codes
├── e2e_test.go          # builds the binary: lock contention and PID, kill, dirs A/B, status unblocked,
│                        # stdin EOF, 0 bytes on stdout, signals, second signal
├── signal_unix_test.go      # //go:build !windows: send SIGINT/SIGTERM to the child
└── signal_windows_test.go   # CREATE_NEW_PROCESS_GROUP + CTRL_BREAK_EVENT

internal/config/
├── setting.go           # Setting descriptor and the registry (data_dir, log_level)
├── config.go            # Config, Origin, Inputs, Load, LogValues
├── env.go               # environ snapshot, empty = unset, Windows case rules, unknown-prefix warnings
├── file.go              # config.toml: go-toml/v2 to map, unstable.Parser key→line index
├── datadir.go           # default from os.UserConfigDir, absolute check, PrepareDataDir (MkdirAll 0700)
├── errors.go            # SettingError, FileError, joined report
└── *_test.go            # precedence pairs, invalid values per source, TOML errors with lines, registry rules

internal/lock/
├── lock.go              # Acquire / Release / HeldError on gofrs/flock, PID at offset 1
└── lock_test.go

internal/buildinfo/
├── buildinfo.go         # Version/Commit/Date (ldflags), vcs fallback, runtime values
└── buildinfo_test.go

.github/workflows/
└── ci.yml               # check (gofmt, vet, 5 target builds), test (Linux; 3 OS on PRs and main)
```

`go.mod` gains `github.com/gofrs/flock`, `github.com/pelletier/go-toml/v2` and the indirect
`golang.org/x/sys`.

**Structure Decision**: a single Go module at the repository root (Principle II, A4).

- `cmd/whatsapp-mcp` holds the CLI wiring, the logger setup and the lifecycle. Its `run` function takes its
  streams and environment as parameters, so tests can call it in process.
- `internal/config` is the A4 configuration package, and the data directory belongs to it (C2).
- The lock gets its own `internal/lock`. Feature 003's `status` and the commands that later embed the WhatsApp
  client use it without pulling in the CLI.
- `internal/buildinfo` is the stable target for the `-ldflags -X` paths that GoReleaser uses in feature 011.

## Implementation notes for `/speckit-tasks`

1. **Order:**
   1. `internal/buildinfo` and `version` (US1)
   2. `internal/config` with its tests (US2, US3)
   3. the logger
   4. `internal/lock` (US4)
   5. the lifecycle and the `stdio`/`serve` placeholders (US5)
   6. the process tests
   7. CI (US6).

   Each step leaves `go test ./...` green.
2. **Startup sequence** for every command except `version`:
   1. install the signal handler, for long-running commands
   2. parse the flags
   3. `config.Load`
   4. on error, print to stderr and exit 2
   5. build the logger and log any warnings
   6. dump the configuration at debug level
   7. `PrepareDataDir`
   8. `lock.Acquire` for `stdio`, `serve` and `pair`, registered as a stop step
   9. run the command body.
3. **Testing the Windows signals** needs `golang.org/x/sys/windows` (`GenerateConsoleCtrlEvent`) in a test file
   only. It is already in the module graph through `gofrs/flock`.
4. **The README** is rewritten in feature 011. This feature changes only its "**Status:** in development; no code
   yet." line, and the "Current state" section of `AGENTS.md`, once the code exists.
5. **Verification:** record the results of [quickstart.md](quickstart.md) and the CI run in
   `specs/001-binary-skeleton/verification.md`.

## Complexity Tracking

No constitution violations. Nothing to justify.
