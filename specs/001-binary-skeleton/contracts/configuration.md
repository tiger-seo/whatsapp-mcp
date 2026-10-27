# Contract: configuration

This contract covers how every setting is resolved, validated and reported. Later features add rows to the
settings table; the rules here stay the same.

## Sources and precedence

For each setting, the first source that has a value wins:

1. **Flag** on the command line (`--log-level debug`), only when it is given explicitly.
2. **Environment variable** `WHATSAPP_MCP_<NAME>` (`WHATSAPP_MCP_LOG_LEVEL`). An empty value counts as unset.
3. **`config.toml`** in the data directory, key `<name>` (`log_level`).
4. **Default.**

Environment variable names are exact on Linux and macOS, and case-insensitive on Windows (as Windows itself
treats them). No other names are read: `LOG_LEVEL`, `WHATSAPP_BRIDGE_*` and the other old names in §3.1 and §4.1 of
the specification are ignored silently.

## Settings (feature 001)

| Name | Flag | Environment variable | `config.toml` | Default | Accepted values |
| --- | --- | --- | --- | --- | --- |
| `data_dir` | `--data-dir` | `WHATSAPP_MCP_DATA_DIR` | **not allowed** | `os.UserConfigDir()`/`whatsapp-mcp` | an absolute path |
| `log_level` | `--log-level` | `WHATSAPP_MCP_LOG_LEVEL` | `log_level` (string) | `info` | `debug`, `info`, `warn`, `warning`, `error`, in any letter case |

Default data directories: `%AppData%\whatsapp-mcp` (Windows), `~/Library/Application Support/whatsapp-mcp`
(macOS), `$XDG_CONFIG_HOME/whatsapp-mcp` or `~/.config/whatsapp-mcp` (Linux).

## `config.toml`

- Its location is `<data dir>/config.toml`. If the file is missing, defaults apply. An empty file is valid.
- It is TOML v1.0, with top-level keys only. A table (`[section]`) is an unknown key.
- Every key must be a known setting that is allowed in the file. Any other key is an error. This is stricter
  than the environment, because the operator owns this file.
- Each value must have the setting's TOML type (a string for `log_level`).

Example:

```toml
# C:\Users\me\AppData\Roaming\whatsapp-mcp\config.toml
log_level = "debug"
```

## Validation and errors

- Every setting is validated before the command does anything else. In particular, before the data directory
  is created or the lock is taken.
- All errors found in one run are reported together, after one header line. The exit code is 2.
- Each error names the setting or key, the source, the rejected value and what is accepted.

```text
whatsapp-mcp: invalid configuration:
  log_level: "verbose" from WHATSAPP_MCP_LOG_LEVEL is not valid; use debug, info, warn or error
  data_dir: "data" from --data-dir is not an absolute path
```

**Message forms by source:**

| Source | Form |
| --- | --- |
| Flag | `<name>: "<value>" from --<flag> <reason>` |
| Environment | `<name>: "<value>" from WHATSAPP_MCP_<NAME> <reason>` |
| File value | `<name>: "<value>" from <path> line <n> <reason>` |
| File: TOML syntax | `<path> line <n>, column <c>: <parser message>` |
| File: unknown key | `<path> line <n>: unknown key "<key>"` |
| File: wrong type | `<path> line <n>: <key> must be a <type>, not a <toml type>` |
| File: `data_dir` | `<path> line <n>: data_dir cannot be set in config.toml, because the file is inside the data directory; use --data-dir or WHATSAPP_MCP_DATA_DIR` |
| File: unreadable | `<path>: <os error>` |

A TOML syntax error stops the file from being read any further, so no value errors are reported for that file.
Errors from flags and environment variables are still reported in the same run.

## Warnings

An environment variable that starts with `WHATSAPP_MCP_` but names no setting is logged once the logger exists.
Startup continues.

```text
time=… level=WARN msg="unknown environment variable" name=WHATSAPP_MCP_LOGLEVEL
```

## Debug dump

At `debug` level every command except `version` logs one line per setting:

```text
time=… level=DEBUG msg=config setting=log_level value=debug source=flag origin=--log-level
time=… level=DEBUG msg=config setting=data_dir value=C:\Users\me\AppData\Roaming\whatsapp-mcp source=default origin=default
```

A setting marked secret shows `value=(secret)`.
