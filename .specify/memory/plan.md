# Main Implementation Plan
> **Revision**: 2026-11-22 — Archived specs/001-binary-skeleton to establish the baseline architecture and record its security follow-up.

## Summary

The `whatsapp-mcp` binary skeleton provides build identification, five CLI entry points, layered startup configuration, structured stderr logging, exclusive data-directory ownership and signal/EOF shutdown. Only `version` had its final behavior at this baseline; the other entry points established the startup, streams and shutdown wiring. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Create the `whatsapp-mcp` binary"]

## Technical Context

**Language/Version**: `go.mod` specifies Go 1.27.0; the security follow-up records the `go1.27.3` toolchain. Build with `CGO_ENABLED=0`. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Language/Version"] [Source: .specify/archive/001-binary-skeleton/verification.md -> "Toolchain"]

**Primary Dependencies**: Pure-Go `github.com/gofrs/flock` v0.13.1 for locking, with indirect `golang.org/x/sys`; `github.com/pelletier/go-toml/v2` v2.4.3 for TOML; standard library `flag`, `log/slog`, `os/signal` and `runtime/debug`. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Primary Dependencies"]

**Storage**: Optional read-only `config.toml` and `whatsapp-mcp.lock` in the data directory; no database in the skeleton. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Storage"]

**Testing**: Go standard `testing`; configuration and lock unit tests and process tests for the binary, including lock, signal and stdout behavior. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Testing"]

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. The native test matrix covers Linux, Windows and macOS. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Target Platform"]

**Project Type**: CLI, one binary and one Go module; MCP and HTTP are not implemented in the skeleton. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Project Type"]

**Performance Goals**: `version` and lock-contention refusal each under 1 s; graceful skeleton shutdown within 10 s. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Performance Goals"]

**Constraints**: No CGO or dependency on the working directory. Invalid configuration creates nothing and takes no lock. Skeleton `stdio` emits no stdout bytes because it generates no protocol traffic; stdout is reserved for that traffic during a stdio session. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Constraints"] [Source: .specify/archive/001-binary-skeleton/contracts/cli.md -> "stdout:"]

**Scale/Scope**: Two initial settings, five commands, about 1,000 Go lines including tests in the design estimate; extend the registry without changing its mechanism. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Scale/Scope"]

## Project Structure

### Source Code (repository root)

One module at the repository root. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Structure Decision"]

| Path | Responsibility |
|---|---|
| `cmd/whatsapp-mcp` | `main.go`, command table/help/exit codes in `cli.go`, handlers in `commands.go`, signal handling and named stop steps in `lifecycle.go`. Inject streams and environment into `run`; in-process, process and OS-specific signal tests. [Source: .specify/archive/001-binary-skeleton/plan.md -> "cmd/whatsapp-mcp holds the CLI wiring"] |
| `internal/config` | `setting.go` registry; `config.go` typed values, origins and loader; `env.go` snapshot and warnings; `file.go` TOML and line index; `datadir.go` directory resolution/preparation; `errors.go` validation and joined errors. [Source: .specify/archive/001-binary-skeleton/plan.md -> "internal/config is the A4 configuration package"] |
| `internal/lock` | `Acquire`, `Release`, `HeldError`, OS lock and PID diagnostics; isolated from CLI. [Source: .specify/archive/001-binary-skeleton/plan.md -> "The lock gets its own"] |
| `internal/buildinfo` | Build metadata and fallbacks; stable target for release `-ldflags -X` overrides. [Source: .specify/archive/001-binary-skeleton/plan.md -> "internal/buildinfo is the stable target"] |
| `internal/private` | Private data-directory creation used by the security follow-up. [Source: .specify/archive/001-binary-skeleton/verification.md -> "Security hardening follow-up"] |
| `.github/workflows/ci.yml` | Formatting, static checks, tests, security gates and five builds. [Source: .specify/archive/001-binary-skeleton/contracts/ci.md -> "check"] |

## Routing and Lifecycle

- One `flag.FlagSet` per command. Dispatch on the first argument; only top-level `-h`/`--help` is accepted before a command. Flags go after the command, accept either dash form and either space or `=`, and there are no positional arguments. Help lists the relevant environment variables and defaults. [Source: .specify/archive/001-binary-skeleton/research.md -> R1] [Source: .specify/archive/001-binary-skeleton/contracts/cli.md -> "Flags go after the command"]
- `version` bypasses configuration, environment, directory and lock and prints the version, commit, date, Go runtime and target in that order. `dev` is independent of the module version; commit/date use injected values, then VCS metadata, then `unknown`; append `-dirty` where applicable. [Source: .specify/archive/001-binary-skeleton/contracts/cli.md -> "On stdout, with one field per line"] [Source: .specify/archive/001-binary-skeleton/research.md -> R10]
- Other commands parse flags, load and validate config, initialize logging, emit warnings/debug config, prepare the directory, acquire the lock for `stdio`, `serve` and `pair`, and then run the command body. `status` takes no lock. Skeleton `pair` and `status` report not implemented; `stdio` and `serve` wait for shutdown. Exit 0 for success/help/clean stop, 1 for runtime refusal, forced stop or general failure, 2 for usage or invalid configuration. [Source: .specify/archive/001-binary-skeleton/plan.md -> "Startup sequence"] [Source: .specify/archive/001-binary-skeleton/contracts/cli.md -> "Behaviour in feature 001"] [Source: .specify/archive/001-binary-skeleton/research.md -> R11]
- Install signal handling before configuration for long-running commands. First signal cancels work; only `stdio` watches stdin, and EOF/read error also cancels the skeleton. Register each named cleanup as its resource is acquired, run steps in reverse, log errors and keep running cleanup. Second signal exits 1 immediately; at the skeleton's 10 s deadline log the pending step and exit 1. Unix uses SIGINT/SIGTERM; Windows console Ctrl+C/Ctrl+Break arrive as interrupt, and console close/logoff/shutdown as SIGTERM. [Source: .specify/archive/001-binary-skeleton/research.md -> R8] [Source: .specify/archive/001-binary-skeleton/research.md -> R9] [Source: .specify/archive/001-binary-skeleton/data-model.md -> "Run lifecycle (`cmd/whatsapp-mcp`)"]

## Configuration and Diagnostics

- One registry drives flags, help, environment lookup, validation and debug output. Resolve the absolute data directory first from flag, environment or `os.UserConfigDir()` plus `whatsapp-mcp`; then read optional TOML and resolve other settings by flag, environment, file, default. Clean absolute overrides; reject relative paths. Join validation errors before making changes. [Source: .specify/archive/001-binary-skeleton/research.md -> R3] [Source: .specify/archive/001-binary-skeleton/contracts/configuration.md -> "Settings (feature 001)"]
- Use a single environment snapshot; names are case-insensitive on Windows, exact elsewhere, with empty values treated as unset and one warning per unknown prefixed name. Old names, including `LOG_LEVEL`, are silently ignored. [Source: .specify/archive/001-binary-skeleton/research.md -> R4]
- Decode TOML to `map[string]any` and use `unstable.Parser` for top-level key line numbers; parser-supplied text unmarshaling is not the validation layer. Allow an empty file, reject unsupported keys/types and `data_dir`, and enforce the 1 MiB limit while reading (one extra byte detects oversize). [Source: .specify/archive/001-binary-skeleton/research.md -> R2] [Source: .specify/archive/001-binary-skeleton/contracts/configuration.md -> "It is at most 1 MiB"]
- Refuse existing non-private directories with a remedy, never silently repair. Use Unix mode 0700 for the new data directory or a protected Windows ACL for the current user, SYSTEM and Administrators. `config.toml` and the lock must be regular single-link files; open without following links and inspect the open handle. New lock: Unix mode 0600 or the private directory's ACL on Windows. The feature records limits concerning ancestor trust and effective-access testing. [Source: .specify/archive/001-binary-skeleton/contracts/configuration.md -> "An existing data directory"] [Source: .specify/archive/001-binary-skeleton/contracts/configuration.md -> "Files inside it"] [Source: .specify/archive/001-binary-skeleton/verification.md -> "Remaining risks"]
- Use one `slog` text logger on stderr, filtered by the configured level. Print pre-logger errors on stderr, escape terminal controls and show the origin, setting and remedy. Mask secrets in values and parser reasons, including TOML syntax diagnostics in or after a secret value. [Source: .specify/archive/001-binary-skeleton/research.md -> R7] [Source: .specify/archive/001-binary-skeleton/contracts/cli.md -> "Every message starts with"] [Source: .specify/archive/001-binary-skeleton/contracts/configuration.md -> "Each error names the setting or key"]
- Use nonblocking `TryLock`. Read at most 32 bytes of PID text from offset 1; byte 0 is reserved because Windows locks it mandatorily, unlike Unix advisory locking. Write via a second handle, truncate under lock on release, and do not delete the file, to avoid racing a new holder. Ignore stale contents; report unknown when the PID cannot be read or parsed. [Source: .specify/archive/001-binary-skeleton/research.md -> R6]

## Testing and CI

- Standard-library tests cover pairwise config precedence; invalid input, source attribution, defaults and env naming; lock re-acquisition, invalid and stale PID; plus process coverage for version/help, per-directory isolation, abrupt holder exit, status without lock, stdin EOF, stdout, and both signal paths. Unix tests signal the child directly; Windows tests send `CTRL_BREAK_EVENT` to a new process group, with Ctrl+C checked separately by hand. [Source: .specify/archive/001-binary-skeleton/research.md -> R12]
- Push and pull request trigger CI with `contents: read`, no secrets, concurrency per ref and cancellation of older runs. The final feature contract puts the five target vet/build steps in `check`, alongside gofmt, vet, module verification, the pinned `govulncheck` gate and the Unix separate-user data-directory check. Ubuntu tests run on every push; Windows and macOS tests are required for PRs and pushes to `main`; the test matrix reports each OS independently. Pin every action to a full commit SHA. Dependabot proposes weekly action/module updates; review new pins, and bump `GOVULNCHECK_VERSION` by hand. The workflow makes no release artifacts. [Source: .specify/archive/001-binary-skeleton/contracts/ci.md -> "Trigger"] [Source: .specify/archive/001-binary-skeleton/contracts/ci.md -> "check"] [Source: .specify/archive/001-binary-skeleton/contracts/ci.md -> "Every action is pinned"]
- Feature verification records native test and build results, formatting failure checks, private-path and fuzz tests, module and vulnerability checks, plus their limitations. Treat WSL2 results as such, and do not infer the Windows separate-identity result, foreign ownership test or reachable-vulnerability failure-path test from the checks performed. [Source: .specify/archive/001-binary-skeleton/verification.md -> "Security hardening follow-up"] [Source: .specify/archive/001-binary-skeleton/verification.md -> "Remaining risks"]

## Complexity Tracking
