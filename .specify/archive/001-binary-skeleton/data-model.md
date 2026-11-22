# Data model: Binary skeleton

This feature has no database. Its "data" consists of the configuration model, the files in the data directory,
the build information and the shutdown state. The names below are the Go types planned in `internal/config`,
`internal/lock`, `internal/buildinfo` and `cmd/whatsapp-mcp`. Formats that operators see are fixed in
[contracts/](contracts/).

## Setting (`internal/config`)

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

## Origin (`internal/config`)

Where a resolved value came from. It is used in error messages and the debug dump.

| Field | Type | Meaning |
| --- | --- | --- |
| `Source` | enum `Default`, `File`, `Env`, `Flag` | The layer that won. Precedence: `Flag` > `Env` > `File` > `Default`. |
| `Detail` | string | `--log-level`; `WHATSAPP_MCP_LOG_LEVEL`; `<path>/config.toml line 3`; or `default`. |

## Config (`internal/config`)

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

The message formats are in [contracts/configuration.md](contracts/configuration.md).

## Data directory

| Path | Created by | Notes |
| --- | --- | --- |
| `<data dir>/` | `config.PrepareDataDir` after successful validation; `MkdirAll` with mode 0700 | Existing directories are not re-permissioned. A path that is not a directory is an error. |
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

## Lock file (`internal/lock`)

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

## Build information (`internal/buildinfo`)

| Field | Source | Fallback |
| --- | --- | --- |
| `Version` | `-ldflags -X …/buildinfo.Version=` | `dev` |
| `Commit` | `-ldflags -X …/buildinfo.Commit=` | `vcs.revision` (+ `-dirty`), then `unknown` |
| `Date` | `-ldflags -X …/buildinfo.Date=` (RFC 3339) | `vcs.time`, then `unknown` |
| `GoVersion` | `runtime.Version()` | — |
| `Target` | `runtime.GOOS + "/" + runtime.GOARCH` | — |

## Run lifecycle (`cmd/whatsapp-mcp`)

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
