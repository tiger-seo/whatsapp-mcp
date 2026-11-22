# Main Project Specification
> **Revision**: 2026-11-22 — Archived specs/001-binary-skeleton to preserve the baseline CLI, configuration, lifecycle and CI specification.

## Configuration Decisions

- **Log level variable.** M10 names the variable `LOG_LEVEL`, but C1 and D10 require every environment variable
  to carry the `WHATSAPP_MCP_` prefix, with no aliases. D10 wins: the variable is **`WHATSAPP_MCP_LOG_LEVEL`**.
  The flag is `--log-level` and the config file key is `log_level`. Plain `LOG_LEVEL` is not read. M10's intent
  (one logger whose level the operator sets) is unchanged; only the name follows D10. `docs/specification.md`
  M10 is updated to name `WHATSAPP_MCP_LOG_LEVEL`. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Log level variable."]

- **Invalid log level.** The old bridge silently fell back to `info` for an unknown value (§3.1). C4 overrides
  that: an unknown value fails at startup. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Invalid log level."]

## User Scenarios & Testing

The users of the binary skeleton are the operator who installs and runs it and the developers who build on it. [Source: .specify/archive/001-binary-skeleton/spec.md -> "The "users" of this feature"]

**Scope:** Placeholder behavior, the absence of protocol traffic, and the associated 10-second shutdown cases below describe the binary skeleton before account, storage or transport work was added. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Out of scope"]

### User Story 1 - Identify the installed binary (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 1]

An operator downloads `whatsapp-mcp` and runs `whatsapp-mcp version` to confirm which build they have before
wiring it into Claude Desktop or a service manager.

**Why this priority**: `version` is the only command that does real work in this feature, and it proves the
binary builds and runs on every target platform.

**Independent Test**: Build the binary, run `whatsapp-mcp version`, and check the output and the exit code.

**Acceptance Scenarios**:

1. **Given** a release build, **When** the operator runs `whatsapp-mcp version`, **Then** stdout shows the
   version, the source commit, the build date, the toolchain version and the target OS/architecture, and the
   exit code is 0.
2. **Given** a local development build with no version information injected, **When** the operator runs
   `whatsapp-mcp version`, **Then** the version reads `dev` and the other fields show what is known or
   `unknown`.
3. **Given** a broken `config.toml` or an unusable data directory, **When** the operator runs
   `whatsapp-mcp version`, **Then** it still prints the build information and exits 0, because `version` reads no
   configuration.
4. **Given** any state, **When** the operator runs `whatsapp-mcp` with no command, with `--help`, or with an
   unknown command, **Then** the five commands are listed with their purpose; an unknown or missing command exits
   with the usage exit code.

---

### User Story 2 - Configure the binary in layers and get clear errors (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 2]

An operator sets options in whichever place suits the deployment: a `config.toml` in the data directory for a
long-running daemon, `WHATSAPP_MCP_*` environment variables in a Claude Desktop config, or a flag for a one-off
run. When a value is wrong, the binary refuses to start and says which setting, which source and why.

**Why this priority**: every later feature adds settings to this mechanism. C4 requires that bad configuration
never surfaces later as a runtime failure.

**Independent Test**: Set the same setting in different sources and check which one wins; set invalid values in
each source and check the startup error. Automated tests cover both (Q3 "config precedence and validation").

**Acceptance Scenarios**:

1. **Given** `log_level = "warn"` in `config.toml`, `WHATSAPP_MCP_LOG_LEVEL=info` in the environment and
   `--log-level debug` on the command line, **When** a command starts, **Then** the effective level is `debug`.
2. **Given** the same file and environment but no flag, **When** a command starts, **Then** the level is `info`.
3. **Given** only the file setting, **When** a command starts, **Then** the level is `warn`.
4. **Given** no setting anywhere, **When** a command starts, **Then** the level is the default, `info`.
5. **Given** `WHATSAPP_MCP_LOG_LEVEL=verbose`, **When** a command other than `version` starts, **Then** it exits
   with the configuration exit code before doing anything else, and the message names the setting, the source
   (`WHATSAPP_MCP_LOG_LEVEL`), the rejected value and the accepted values.
6. **Given** a `config.toml` that is not valid TOML, has a key the binary does not know, or has a value of the
   wrong type, **When** a command starts, **Then** it fails with a message that names the file and the line or
   key.
7. **Given** the old variable `LOG_LEVEL=debug` and nothing else, **When** a command starts, **Then** the level
   stays `info`: old names are not read and produce no warning (D10).

---

### User Story 3 - Data lives in one predictable per-user directory (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 3]

An operator runs the binary from Claude Desktop, a terminal or a service manager, each with a different working
directory, and every run uses the same data directory. They can point it elsewhere with `--data-dir` or
`WHATSAPP_MCP_DATA_DIR`.

**Why this priority**: the lock, the configuration file and, later, the database all live in the data directory.
C2 forbids any dependency on the working directory.

**Independent Test**: Run a command from two different working directories without overrides and check that both
use the same directory; run with an override and check that it is used and created when missing.

**Acceptance Scenarios**:

1. **Given** no override, **When** a command that needs the data directory starts, **Then** it uses
   `whatsapp-mcp` inside the operating system's per-user configuration directory (for example
   `%AppData%\whatsapp-mcp` on Windows, `~/Library/Application Support/whatsapp-mcp` on macOS and
   `$XDG_CONFIG_HOME/whatsapp-mcp` or `~/.config/whatsapp-mcp` on Linux), whatever the working directory is.
2. **Given** `--data-dir` pointing to a directory that does not exist, **When** the command starts, **Then** the
   directory is created, readable and writable only by the current user where the OS supports that.
3. **Given** a relative `--data-dir` or `WHATSAPP_MCP_DATA_DIR`, **When** the command starts, **Then** it fails
   with a configuration error asking for an absolute path.
4. **Given** a data directory path that is an existing file, or that cannot be created, **When** the command
   starts, **Then** it fails with a message naming the path and the reason.
5. **Given** an environment where the OS cannot report a per-user configuration directory and no override is
   set, **When** the command starts, **Then** it fails and the message suggests `--data-dir` or
   `WHATSAPP_MCP_DATA_DIR`.

---

### User Story 4 - Only one instance owns the data directory (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 4]

An operator starts `whatsapp-mcp serve` as a daemon and later, by mistake, a second instance on the same data
directory (for example Claude Desktop launching `stdio`). The second instance refuses to start and tells the
operator which process holds the directory.

**Why this priority**: two processes sharing one WhatsApp session and one database corrupt both (A2, constitution
Principle III). Roadmap "done when": a second instance fails with a message that names the first instance's PID.

**Independent Test**: Start one instance that holds the lock, start a second on the same data directory, and check
the second one's error and exit code; kill the first one abruptly and check that a new instance then starts.

**Acceptance Scenarios**:

1. **Given** a running instance with PID 4242 holding the data directory, **When** a second `stdio`, `serve` or
   `pair` starts on the same data directory, **Then** it exits with the "already running" exit code and a message
   that says another `whatsapp-mcp` (PID 4242) is using the data directory, and names the directory.
2. **Given** the holder crashed or was killed without cleanup, **When** a new instance starts, **Then** it takes
   the lock without any manual step: there is no stale lock.
3. **Given** a running instance on data directory A, **When** another instance starts on data directory B,
   **Then** both run.
4. **Given** a running instance, **When** `whatsapp-mcp version` or `whatsapp-mcp status` runs, **Then** it is
   not blocked by the lock.

---

### User Story 5 - Clean shutdown and clean output channels (Priority: P2)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 5]

An operator stops the daemon with Ctrl+C in a terminal (including on Windows), or a service manager or Claude
Desktop stops it with SIGTERM. The process stops in an orderly way and releases the data directory. In `stdio`
mode nothing but protocol traffic ever reaches stdout.

**Why this priority**: later features hang the WhatsApp client, the database and the HTTP server on this shutdown
path. A stray byte on stdout breaks the MCP client (Principle IV).

**Independent Test**: Start `serve`, send each stop signal, check the exit code and that the lock is free
afterwards; run `stdio` with debug logging and check that stdout stays empty.

**Acceptance Scenarios**:

1. **Given** a running `serve` or `stdio`, **When** it receives SIGINT or SIGTERM (or Ctrl+C in a Windows
   console), **Then** it logs that it is shutting down, stops, releases the lock and exits 0.
2. **Given** a shutdown in progress, **When** a second stop signal arrives, **Then** the process exits at once
   with a non-zero code.
3. **Given** `stdio` started by an MCP client, **When** the client closes the process's stdin, **Then** the
   process shuts down the same way and exits 0.
4. **Given** `stdio` at log level `debug`, **When** it starts, runs and shuts down, **Then** stdout receives no
   bytes, and every log line is on stderr.
5. **Given** any command at any level, **When** it logs, **Then** each line carries a timestamp, a level and a
   message with structured key-value attributes, and lines below the configured level are not written.

---

### User Story 6 - Every change is checked on every target (Priority: P2)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 6]

A developer pushes a branch or opens a pull request, and CI reports whether the code is formatted, passes static
checks and tests, and builds for all five targets without a C toolchain.

**Why this priority**: the constitution requires every change to build for all five targets with
`CGO_ENABLED=0`, and `gofmt`, `go vet` and `go test ./...` to pass before a feature is done (Q2).

**Independent Test**: Push a commit and read the CI result; push one with an unformatted file and check that CI
fails on the format step.

**Acceptance Scenarios**:

1. **Given** a push to any branch or a pull request, **When** CI runs, **Then** it checks formatting, runs the
   static checks and the full test suite, and builds windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and
   darwin/arm64 with CGO disabled.
2. **Given** a file that is not `gofmt`-formatted, **When** CI runs, **Then** it fails and names the file.
3. **Given** a change that breaks the build for one target only, **When** CI runs, **Then** it fails and names
   that target.

---

### Edge Cases

- **`version` with a broken environment:** it reads no configuration, touches no data directory and takes no
  lock, so it works even when everything else is misconfigured. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`version` with a broken environment"]

- **Unknown flag or a flag that does not belong to the command:** usage error with the usage exit code, naming the
  flag. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Unknown flag or a flag that does not belong to the command"]

- **Empty environment variable** (`WHATSAPP_MCP_LOG_LEVEL=`): treated as unset, so the next source applies.
  Claude Desktop and service files often carry empty entries. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Empty environment variable"]

- **Unknown `WHATSAPP_MCP_*` environment variable** (for example a typo `WHATSAPP_MCP_LOGLEVEL`): a warning naming
  it is logged, and startup continues. The environment is inherited and shared, so it is not treated as strictly
  as the config file, which the operator owns. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Unknown `WHATSAPP_MCP_*` environment variable"]

- **Log level case:** `DEBUG` and `debug` are the same. `warning` is accepted as `warn`, as in §3.1. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Log level case"]

- **`data_dir` in `config.toml`:** rejected with a message saying the data directory can only be set by flag or
  environment variable, because the file lives inside it. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`data_dir` in `config.toml`"]

- **`config.toml` missing:** not an error; the defaults apply. **Present but unreadable** (permissions): an error
  naming the file. **Empty:** valid, the defaults apply. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`config.toml` missing"]

- **Lock file left over from an earlier run:** its contents are ignored; only the OS lock counts. The new holder
  overwrites the PID. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Lock file left over from an earlier run"]

- **Lock held but the PID cannot be read** (for example the holder is just starting): the message says another
  instance holds the data directory and that its PID is unknown. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Lock held but the PID cannot be read"]

- **Signal before startup finishes:** the process still exits cleanly and releases anything it took. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Signal before startup finishes"]

- **Commands not yet implemented:** `pair` and `status` validate the configuration, then report that the command
  is not implemented yet and exit with the general error code. `pair` takes the lock first, so it already reports
  "already running" the way its final version will. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Commands not yet implemented"]

## Requirements

### Functional Requirements

**Command line (A3)**

- **FR-001**: The binary MUST be named `whatsapp-mcp` and offer exactly the commands `stdio`, `serve`, `pair`, [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-001]
  `status` and `version`, each with a one-line description in the help output.
- **FR-002**: `version` MUST print to stdout the version, source commit, build date, toolchain version and target [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-002]
  OS/architecture, and exit 0. Values not injected at build time MUST show as `dev` (version) or `unknown`.
  `version` MUST NOT read configuration, the data directory or the lock.
- **FR-003**: In this feature, `stdio` and `serve` MUST load and validate the configuration, prepare the data [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-003]
  directory, take the lock, log that the command's functionality is not implemented yet, and then run until a
  shutdown is requested. `stdio` MUST also treat the end of its stdin as a shutdown request.
- **FR-004**: In this feature, `pair` MUST validate the configuration, prepare the data directory and take the [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-004]
  lock; `status` MUST validate the configuration and prepare the data directory without taking the lock. Both MUST
  then report on stderr that the command is not implemented yet and exit with the general error code.
- **FR-005**: The binary MUST use these exit codes: 0 for success and graceful shutdown; 1 for a general error, [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-005]
  including a held lock and a command not implemented yet; 2 for a usage error (unknown command or flag, missing
  command) and for invalid configuration.
- **FR-006**: Help MUST be available with `--help` / `-h` for the binary and for each command, listing that [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-006]
  command's flags with their environment variable and default.

**Configuration (C1, C4, D8, D10)**

- **FR-007**: Every setting MUST be resolved from these sources, highest first: command-line flag, environment [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-007]
  variable, `config.toml` in the data directory, built-in default.
- **FR-008**: Environment variable names MUST be `WHATSAPP_MCP_` followed by the setting name in upper case. No [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-008]
  other names, including the old names of §3.1 and §4.1, are read, and no alias or deprecation warning exists.
- **FR-009**: An environment variable set to the empty string MUST be treated as unset. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-009]
- **FR-010**: Settings defined by this feature: [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-010]

  | Setting | Flag | Environment variable | `config.toml` key | Default | Valid values |
  | --- | --- | --- | --- | --- | --- |
  | Data directory | `--data-dir` | `WHATSAPP_MCP_DATA_DIR` | not allowed | per-user config dir + `/whatsapp-mcp` | absolute path |
  | Log level | `--log-level` | `WHATSAPP_MCP_LOG_LEVEL` | `log_level` | `info` | `debug`, `info`, `warn` (or `warning`), `error`; case-insensitive |

  Later features add their settings to the same mechanism.
- **FR-011**: The configuration file MUST be `config.toml` (TOML only) in the data directory. A missing file is [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-011]
  not an error; an unreadable or malformed file, an unknown key, a value of the wrong type, and the `data_dir` key
  MUST be errors.
- **FR-012**: All settings MUST be validated before the command does any other work. Every error MUST name the [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-012]
  setting, the source it came from (flag name, variable name, or file and line), the rejected value, and what is
  accepted. When several settings are invalid, all of them SHOULD be reported in one run.
- **FR-013**: An unknown environment variable with the `WHATSAPP_MCP_` prefix MUST produce a warning that names it [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-013]
  and MUST NOT stop startup.
- **FR-014**: At startup, the effective configuration and the source of each value MUST be logged at `debug` [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-014]
  level. Settings marked secret by later features MUST never be logged.

**Data directory (C2)**

- **FR-015**: The data directory MUST default to `whatsapp-mcp` inside the operating system's per-user [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-015]
  configuration directory, and MUST NOT depend on the working directory.
- **FR-016**: An override MUST be an absolute path; a relative path is a configuration error. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-016]
- **FR-017**: When the data directory does not exist it MUST be created, including missing parents, accessible [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-017]
  only by the current user where the OS supports that. A path that exists but is not a directory, or cannot be
  created, MUST fail with a message naming it.
- **FR-018**: When no per-user configuration directory is available and no override is given, startup MUST fail [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-018]
  with a message suggesting `--data-dir` or `WHATSAPP_MCP_DATA_DIR`.

**Logging (M10)**

- **FR-019**: The process MUST have one structured logger, set up once from the effective log level and used by [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-019]
  all code. Later features bridge their libraries' logs into it.
- **FR-020**: All logs MUST go to stderr in every command. In `stdio` mode nothing other than MCP protocol traffic [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-020]
  may ever be written to stdout. Stdout is otherwise used only for a command's own output (`version` now,
  `status` later).
- **FR-021**: Each log line MUST carry a timestamp, a level, a message and key-value attributes, in a [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-021]
  human-readable text form.
- **FR-022**: Configuration and logging errors that occur before the logger exists MUST still be reported on [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-022]
  stderr.

**Single-instance lock (A2, D14)**

- **FR-023**: `stdio`, `serve` and `pair` MUST take an exclusive OS-level lock on `<data dir>/whatsapp-mcp.lock` [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-023]
  before doing any work that touches the data directory beyond reading `config.toml`, and hold it until they
  exit.
- **FR-024**: The holder MUST write its PID into the lock file once it holds the lock. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-024]
- **FR-025**: When the lock is held by another process, the command MUST NOT wait; it MUST fail at once with a [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-025]
  message that names the holder's PID (or says it is unknown) and the data directory.
- **FR-026**: The lock MUST be released by the OS when the holder exits for any reason, including a crash or a [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-026]
  forced kill, so no stale lock can block a later start. The lock MUST work on all five targets without a C
  toolchain.

**Shutdown (M10)**

- **FR-027**: Long-running commands MUST shut down gracefully on SIGINT and SIGTERM on Unix, and on Ctrl+C (and [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-027]
  Ctrl+Break) in a Windows console. Shutdown MUST stop the command's work, release the lock and exit 0.
- **FR-028**: A second stop signal during shutdown MUST end the process at once with a non-zero exit code. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-028]
- **FR-029**: Graceful shutdown MUST finish within 10 seconds; if it does not, the process MUST log what it was [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-029]
  waiting for and exit with a non-zero code.

**Layout (A4)**

- **FR-030**: The code MUST follow the layout of A4 and the constitution: the entry point in `cmd/whatsapp-mcp` [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-030]
  and the configuration in `internal/config`, in the single module at the repository root.

**CI (Q2)**

- **FR-031**: A CI workflow MUST run on every push and pull request. It MUST fail when any Go file is not [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-031]
  `gofmt`-formatted (naming the files), when `go vet` reports a problem, or when `go test ./...` fails.
- **FR-032**: CI MUST build the binary for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-032]
  with `CGO_ENABLED=0`, reporting each target separately.
- **FR-033**: CI MUST run the tests on Linux, Windows and macOS for every pull request and every push to `main`, [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-033]
  because the lock and signal handling differ per operating system. Pushes to other branches MAY run the tests
  on Linux only, for quick feedback.

### Key Entities

#### Setting

one configuration value, with its name, flag, environment variable, file key, default, validation
rule, and whether it is secret. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Setting"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Setting (`internal/config`)"]

A descriptor in the settings registry. It is the single definition from which the flags, the help text, the
environment lookup, the file checks and the debug dump are generated (research R3).

| Field | Type | Meaning |
| --- | --- | --- |
| `Name` | string | Canonical snake_case name, for example `log_level`. Used in error messages and the debug dump. |
| `Flag` | string | Flag name without dashes, for example `log-level`. |
| `Env` | string | `WHATSAPP_MCP_` + upper-case `Name`. Derived, never written by hand. |
| `FileKey` | string | Key in `config.toml`. Empty means the setting is not allowed in the file. |
| `Default` | func(env) (string, error) | Default value as text. A function, so the data directory can call `os.UserConfigDir`. |
| `Usage` | string | One-line description for help. |
| `Secret` | bool | When true, the value is never logged or printed (none in this feature). |
| `ParseText` | func(string) (any, error) | Parses and validates a flag or environment value. |
| `ParseTOML` | func(any) (any, error) | Checks the TOML type, then validates as `ParseText` does. |
| `Apply` | func(*Config, any) | Stores the parsed value into the typed `Config`. |

**Settings in this feature:**

| Name | Flag | Env | File key | Default | Validation |
| --- | --- | --- | --- | --- | --- |
| `data_dir` | `data-dir` | `WHATSAPP_MCP_DATA_DIR` | (not allowed) | `os.UserConfigDir()` + `whatsapp-mcp` | absolute path (`filepath.IsAbs`), then `filepath.Clean` |
| `log_level` | `log-level` | `WHATSAPP_MCP_LOG_LEVEL` | `log_level` (TOML string) | `info` | case-insensitive `debug`, `info`, `warn`, `warning` (= `warn`), `error` → `slog.Level` |

**Registry rules:**

- Names are unique.
- `Env` equals `"WHATSAPP_MCP_" + strings.ToUpper(Name)` (FR-008).
- `FileKey` is either empty or equal to `Name`.

A unit test enforces all three.

#### Effective configuration

the resolved value of every setting, together with the source each value came from.
It is read-only after startup. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Effective configuration"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Config (`internal/config`)"]

The effective configuration. It is built once by `config.Load` and is read-only afterwards.

| Field | Type | Notes |
| --- | --- | --- |
| `DataDir` | string | Absolute and cleaned. The directory may not exist yet; see "Data directory". |
| `LogLevel` | `slog.Level` | |
| `origins` | map[name]Origin | Read with `Origin(name)`, and used by `LogValues` for the debug dump. |

`Load` takes an `Inputs` value and returns `(*Config, []Warning, error)`:

| `Inputs` field | Type | Production value |
| --- | --- | --- |
| `Flags` | map[string]string | Only the flags that were set (`FlagSet.Visit`). |
| `Environ` | []string | `os.Environ()`. |
| `UserConfigDir` | func() (string, error) | `os.UserConfigDir`. |

- A `Warning` is an unknown `WHATSAPP_MCP_*` variable. The caller logs it once the logger exists.
- The returned error joins every `*SettingError` and `*FileError` found (FR-012).

**Validation errors:**

| Type | Fields | Raised for |
| --- | --- | --- |
| `SettingError` | `Setting`, `Origin`, `Value`, `Reason` (includes the accepted values) | An invalid value from any source. A relative data directory. |
| `FileError` | `Path`, `Line` (0 if unknown), `Column` (0 if unknown), `Key`, `Reason` | TOML syntax, an unreadable file, an unknown key, a wrong type, `data_dir` in the file. |

The message formats are in [configuration contract](../archive/001-binary-skeleton/contracts/configuration.md).

#### Data directory

the per-user directory that holds `config.toml`, the lock file and, in later features, the
database, `downloads/`, `outbox/` and the generated secrets. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Data directory"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Data directory"]

| Path | Created by | Notes |
| --- | --- | --- |
| `<data dir>/` | `config.PrepareDataDir` after successful validation | Existing directories are not re-permissioned. A path that is not a directory is an error. See the [configuration contract](../archive/001-binary-skeleton/contracts/configuration.md#data-directory-and-file-privacy). |
| `<data dir>/config.toml` | the operator | Optional; never written by the binary. |
| `<data dir>/whatsapp-mcp.lock` | `lock.Acquire` | See "Lock file". |
| `whatsapp.db`, `downloads/`, `outbox/`, secrets | features 002–010 | Not created here. |

**Who touches the data directory, per command:**

| Command | Reads `config.toml` | Creates the directory | Takes the lock |
| --- | --- | --- | --- |
| `version` | no | no | no |
| `status` | yes | yes | no |
| `pair` | yes | yes | yes |
| `stdio`, `serve` | yes | yes | yes |

#### Lock file

`<data dir>/whatsapp-mcp.lock`. The OS lock on it marks ownership; its content is the holder's
PID, for messages only. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Lock file"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Lock file (`internal/lock`)"]

`<data dir>/whatsapp-mcp.lock`. The OS lock on byte 0 means ownership. The content serves only the error
message.

| Byte range | Content |
| --- | --- |
| 0 | Reserved, with no meaning (NUL in a new file). It is never read: on Windows it is under a mandatory lock (research R6). |
| 1 … | The holder's PID in decimal ASCII, then `\n`. |

**Types:**

- `Lock`: `Path`, the `*flock.Flock`, and a second write handle. `Release()` truncates the file to 0 bytes,
  unlocks it and closes both handles. It is idempotent.
- `HeldError`: `Path`, `DataDir`, `PID int` (0 = unknown). Returned by `Acquire` when `TryLock` reports that
  another process holds the lock.

**States of the file:**

```text
absent ──Acquire──► locked (PID written) ──Release──► unlocked, 0 bytes
                        │
                        └──process dies──► unlocked by the OS, stale PID left (ignored)
unlocked (any content) ──Acquire──► locked (content replaced by the new PID)
```

#### Build information

version, commit, build date, toolchain version and target, fixed at build time. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Build information"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Build information (`internal/buildinfo`)"]

| Field | Source | Fallback |
| --- | --- | --- |
| `Version` | `-ldflags -X …/buildinfo.Version=` | `dev` |
| `Commit` | `-ldflags -X …/buildinfo.Commit=` | `vcs.revision` (+ `-dirty`), then `unknown` |
| `Date` | `-ldflags -X …/buildinfo.Date=` (RFC 3339) | `vcs.time`, then `unknown` |
| `GoVersion` | `runtime.Version()` | — |
| `Target` | `runtime.GOOS + "/" + runtime.GOARCH` | — |

#### Origin

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Origin (`internal/config`)"]

Where a resolved value came from. It is used in error messages and the debug dump.

| Field | Type | Meaning |
| --- | --- | --- |
| `Source` | enum `Default`, `File`, `Env`, `Flag` | The layer that won. Precedence: `Flag` > `Env` > `File` > `Default`. |
| `Detail` | string | `--log-level`; `WHATSAPP_MCP_LOG_LEVEL`; `<path>/config.toml line 3`; or `default`. |

#### Run lifecycle

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Run lifecycle (`cmd/whatsapp-mcp`)"]

Long-running commands (`stdio`, `serve`) go through these states. `pair` and `status` stop after "lock taken"
or after "directory ready", with "not implemented yet" and exit 1.

```text
starting ──config invalid──────────────► exit 2
   │  (signal handler installed first)
   ├──data dir unusable / lock held──────► exit 1
   ▼
running ──1st signal / stdin EOF (stdio)──► stopping
                                               │ stop steps run in reverse order, 10 s deadline
                                               ├──all steps done──────────► exit 0
                                               ├──deadline passed─────────► log the pending step, exit 1
                                               └──2nd signal──────────────► exit 1 at once
```

- **Stop step:** `{Name string; Stop func(context.Context) error}`.
- A step is registered as soon as its resource is taken. In this feature that is `lock`. A signal that arrives
  during `starting` therefore releases only what was taken, and the exit is still 0.
- A step that returns an error is logged at `ERROR`, the remaining steps still run, and the exit code is 1.

## Success Criteria

### Measurable Outcomes

- **SC-001**: `whatsapp-mcp version` works on all 5 targets and returns within 1 second, with or without a valid [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-001]
  configuration.
- **SC-002**: For every pair of configuration sources, automated tests show the higher source winning; for every [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-002]
  setting, at least one invalid value in each source it can come from is rejected at startup with a message that
  names the setting and the source.
- **SC-003**: A second instance on the same data directory fails within 1 second with a message that contains the [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-003]
  first instance's PID; after the first instance is force-killed, a new instance starts at the first attempt.
- **SC-004**: After a stop signal, a long-running command exits 0 within 10 seconds and the data directory can be [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-004]
  locked again at once.
- **SC-005**: A `stdio` run at debug level, from start to shutdown, writes 0 bytes to stdout. [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-005]
- **SC-006**: Runs from at least 2 different working directories without an override use the same data directory. [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-006]
- **SC-007**: CI is green on the feature's pull request for formatting, static checks, tests on 3 operating [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-007]
  systems and builds for all 5 targets; a deliberately unformatted file makes it fail.

## Assumptions

- **AS-001**: **Settled decisions used as given:** TOML only (D8); the `WHATSAPP_MCP_` prefix with no aliases (D10); an OS
  file lock on `<data dir>/whatsapp-mcp.lock` that names the holder's PID, implemented with a pure-Go library
  (`gofrs/flock`, D14); the standard library's structured logger (`slog`, M10). [Source: .specify/archive/001-binary-skeleton/spec.md -> "Settled decisions used as given"]

- **AS-002**: **No log format option:** logs are human-readable text on stderr. A JSON option would be speculative
  (Principle IX) and can be added when someone needs it. [Source: .specify/archive/001-binary-skeleton/spec.md -> "No log format option"]

- **AS-003**: **Logs go to stderr in every command**, not only `stdio`: this is the simplest rule and suits service managers,
  which capture stderr. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Logs go to stderr in every command"]

- **AS-004**: **Flags follow the command** (`whatsapp-mcp stdio --log-level debug`), which matches the Claude Desktop config
  `"args": ["stdio"]`. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Flags follow the command"]

- **AS-005**: **`stdio` and `serve` placeholders run until stopped** so that the lock and shutdown can be checked end to end
  with the real binary before the MCP and HTTP features exist. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`stdio` and `serve` placeholders run until stopped"]

- **AS-006**: **`status` will not take the lock**, so that it can report on a running daemon later. Feature 003 decides how it
  reads the state. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`status` will not take the lock"]

- **AS-007**: **Build information** is injected at build time; how is a plan matter. Feature 011 sets it for releases. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Build information"]

- **AS-008**: **CI runs on GitHub Actions**, where the repository is hosted. The Go version comes from `go.mod`. [Source: .specify/archive/001-binary-skeleton/spec.md -> "CI runs on GitHub Actions"]

- **AS-009**: **Out of scope:** generating secrets (C3; features 009 and 010), anything that talks to WhatsApp, the database,
  MCP, HTTP, and releases (011). [Source: .specify/archive/001-binary-skeleton/spec.md -> "Out of scope"]
