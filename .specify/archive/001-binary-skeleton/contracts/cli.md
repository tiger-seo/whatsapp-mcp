# Contract: `whatsapp-mcp` command line

This is what an operator, a service manager or an MCP client can rely on after feature 001. Later features
change the behaviour of `stdio`, `serve`, `pair` and `status`, but not their names, the shared flags, the exit
codes or the stream rules.

## Synopsis

```text
whatsapp-mcp <command> [flags]
whatsapp-mcp -h | --help
whatsapp-mcp <command> -h | --help
```

Flags go **after** the command. Both the `-flag` and `--flag` spellings work, as do `--flag value` and
`--flag=value` (standard `flag` package). A command does not accept positional arguments.

## Commands

| Command | Help line | Behaviour in feature 001 |
| --- | --- | --- |
| `stdio` | MCP over stdio, for Claude Desktop and Cursor | Load and validate the configuration, prepare the data directory, take the lock and log `stdio: MCP server not implemented yet`. Then run until a stop signal arrives or stdin closes, and exit 0. |
| `serve` | Daemon: MCP over HTTP at /mcp | As `stdio`, logging `serve: HTTP server not implemented yet`. It does not watch stdin. Run until a stop signal arrives, then exit 0. |
| `pair` | Pair this device with a terminal QR code | Load and validate the configuration, prepare the data directory, take the lock, print `whatsapp-mcp: pair is not implemented yet` to stderr and exit 1. |
| `status` | Print connection, login and version state | Load and validate the configuration, prepare the data directory (no lock), print `whatsapp-mcp: status is not implemented yet` to stderr and exit 1. |
| `version` | Print build information | Print the build information to stdout and exit 0. Reads no configuration and no environment variables, and touches no files. |

## Flags (all commands except `version`)

| Flag | Environment variable | Default | Meaning |
| --- | --- | --- | --- |
| `--data-dir <path>` | `WHATSAPP_MCP_DATA_DIR` | `<user config dir>/whatsapp-mcp` | Data directory (absolute path). |
| `--log-level <level>` | `WHATSAPP_MCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `-h`, `--help` | | | Show the command's help and exit 0. |

The sources, validation and file format are in [configuration.md](configuration.md).

## Help output

`whatsapp-mcp --help` prints to stdout and exits 0. With no command, or with an unknown command, the same text
goes to stderr, preceded by the error, and the exit code is 2.

```text
whatsapp-mcp: WhatsApp MCP server

Usage:
  whatsapp-mcp <command> [flags]

Commands:
  stdio     MCP over stdio, for Claude Desktop and Cursor
  serve     Daemon: MCP over HTTP at /mcp
  pair      Pair this device with a terminal QR code
  status    Print connection, login and version state
  version   Print build information

Run "whatsapp-mcp <command> --help" for the command's flags.
```

`whatsapp-mcp <command> --help` lists each flag together with its environment variable and default:

```text
Usage:
  whatsapp-mcp stdio [flags]

MCP over stdio, for Claude Desktop and Cursor

Flags:
  --data-dir string    Data directory (env WHATSAPP_MCP_DATA_DIR, default C:\Users\me\AppData\Roaming\whatsapp-mcp)
  --log-level string   Log level: debug, info, warn, error (env WHATSAPP_MCP_LOG_LEVEL, default info)
```

The exact column alignment is not part of the contract. What is part of it: the flag name, the environment
variable name and the default.

## `version` output

On stdout, with one field per line in this order. Field names and order are stable.

```text
whatsapp-mcp dev
commit:  4f2c1d0e9b7a-dirty
built:   2026-10-27T09:14:03Z
go:      go1.27.0
target:  windows/amd64
```

The first line is `whatsapp-mcp <version>`: `dev` unless the version was injected at build time (feature 011
injects it). A field whose value is not known shows `unknown`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success; `--help`; graceful shutdown after a stop signal or stdin EOF |
| 1 | General error: lock held by another instance, unusable data directory, command not implemented yet, second stop signal (forced exit), shutdown not finished within 10 s |
| 2 | Usage error (missing or unknown command, unknown flag, positional argument, flag before the command), invalid configuration |

## Streams

- **stdout:** only `version` output and `--help` output in this feature. `status` output will follow in feature 003. In
  `stdio`, stdout carries only MCP protocol traffic. In this feature that means nothing at all.
- **stderr:** all log lines (format below), and all errors that occur before the logger exists.
- **stdin:** read only by `stdio`. EOF is a shutdown request.

## Log lines (stderr)

Log lines use the `slog` text format: `time=<RFC 3339 with ms> level=<DEBUG|INFO|WARN|ERROR> msg=<text> key=value…`.

| When | Level | Message and attributes |
| --- | --- | --- |
| Start of `stdio`/`serve`/`pair`/`status` | DEBUG | `config` with `setting`, `value`, `source`, `origin` (one line per setting; a secret shows `(secret)`) |
| Unknown `WHATSAPP_MCP_*` variable | WARN | `unknown environment variable` with `name` |
| Lock taken | DEBUG | `lock acquired` with `path`, `pid` |
| Placeholder running | INFO | `<command>: … not implemented yet` |
| First signal or stdin EOF | INFO | `shutting down` with `reason` (`interrupt`, `terminated`, `stdin closed`) |
| Second signal | WARN | `forced exit` |
| Deadline passed | ERROR | `shutdown timed out` with `waiting_for` (stop step name) |
| Stop step failed | ERROR | `stop failed` with `step`, `error` |
| Clean stop | INFO | `stopped` |

The message texts are informative, and tests match on them. They may be reworded later only together with the tests.

## Error messages (stderr, before the logger exists)

Every message starts with `whatsapp-mcp: `. Control characters, line breaks and bidirectional formatting
characters that come from input (a path, a flag name, `config.toml`) are printed as Go escapes (`\x1b`,
`\n`, `\u202e`), so each error stays on its own line and cannot drive the terminal.

| Situation | Message | Exit |
| --- | --- | --- |
| No command | `whatsapp-mcp: missing command` + usage | 2 |
| Unknown command | `whatsapp-mcp: unknown command "foo"` + usage | 2 |
| Unknown flag | `whatsapp-mcp: stdio: flag provided but not defined: -foo` + command usage | 2 |
| Flag before the command | `whatsapp-mcp: flags go after the command, e.g. "whatsapp-mcp stdio --log-level debug"` | 2 |
| Positional argument | `whatsapp-mcp: stdio: unexpected argument "x"` | 2 |
| Invalid configuration | see [configuration.md](configuration.md) | 2 |
| No user config dir | `whatsapp-mcp: cannot find the per-user configuration directory (<os error>); set --data-dir or WHATSAPP_MCP_DATA_DIR` | 2 |
| Data directory unusable | `whatsapp-mcp: data directory <path>: <reason>` (e.g. `exists and is not a directory`, `permission denied`, `other users can access it (mode 0755); run: chmod 700 <path>`); see [configuration.md](configuration.md#data-directory-and-file-privacy) | 1 |
| Lock file unusable | `whatsapp-mcp: data directory <path>: lock <path>/whatsapp-mcp.lock: <reason>` (e.g. `is a symbolic link or another reparse point; replace it with a regular file`) | 1 |
| Lock held | `whatsapp-mcp: another whatsapp-mcp (PID 4242) is using the data directory <path>` | 1 |
| Lock held, PID unreadable | `whatsapp-mcp: another whatsapp-mcp (PID unknown) is using the data directory <path>` | 1 |
| Not implemented | `whatsapp-mcp: pair is not implemented yet` | 1 |

## Signals

| Platform | Graceful (first) | Forced (second during shutdown) |
| --- | --- | --- |
| Linux, macOS | SIGINT, SIGTERM | any second SIGINT/SIGTERM |
| Windows | Ctrl+C, Ctrl+Break, console close, logoff, shutdown | any second one |

Graceful shutdown finishes within 10 s, releases the lock and exits 0.
