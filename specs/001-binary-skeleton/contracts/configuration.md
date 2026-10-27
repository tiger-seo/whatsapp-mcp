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
- It is read only when the data directory already passes the privacy check below, and only if it is itself a
  private regular file. A symbolic link, a directory, a FIFO, a device or a file with more than one hard link is
  refused before it is read. Nothing is created or changed while it is read.
- It is at most 1 MiB (1 048 576 bytes). At most one byte more is read; a larger file is refused with
  `<path>: is larger than 1 MiB`, and its content is not shown.
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
- Each error names the setting or key, the source, the rejected value and what is accepted. The value of a
  secret setting is shown as `(secret)` instead, in every message form; if a parser reason repeats the value,
  that is replaced too. A TOML syntax error in or after a secret key's line keeps its line and column, but its
  reason becomes `invalid TOML in or after the value of <key>, which is secret`, because the parser's message
  may quote the text near the error.
- If the configuration is valid but an existing data directory is not private, the error is
  `whatsapp-mcp: data directory <path>: <reason>; <fix>` and the exit code is 1. `config.toml` is then not
  read. When settings are invalid too, only the setting errors are reported (exit 2).

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
| File: not private | `<path>: <reason>; <fix>`, e.g. `other users can write it (mode 0666); run: chmod 600 <path>` |
| File: not a regular file | `<path>: is not a regular file`, or `is a symbolic link or another reparse point; replace it with a regular file`, or `has more than one hard link; replace it with a copy` |
| File: too large | `<path>: is larger than 1 MiB` |
| Secret setting, any source | `<name>: (secret) from <origin> <reason>` |

A TOML syntax error stops the file from being read any further, so no value errors are reported for that file.
Errors from flags and environment variables are still reported in the same run.

## Data directory and file privacy

The data directory holds the WhatsApp session and, from feature 002, the messages. Only the user running
whatsapp-mcp may reach it. Existing directories and files are checked and never changed: a failed check says
what to run to fix it.

**A missing data directory** is created by every command except `version`, after the configuration is valid.
Missing parents are created with mode 0700 (Unix) or inherit their parent's access (Windows). The data directory
itself is created private: mode 0700 on Unix; on Windows a protected access list, not inherited from the parent,
that allows full control to this user, SYSTEM and BUILTIN\Administrators and is inherited by everything created
inside it.

**An existing data directory** must:

- not be a symbolic link, junction or other reparse point (`is a symbolic link or another reparse point; use the
  directory it points to`). The directories above it are trusted and may be links;
- be owned by this user (`is owned by uid 1001, not by this user (uid 1000); use one this user owns`). On Windows
  the owner may also be SYSTEM or Administrators, which own what an elevated process creates;
- on Unix, have no group or other permission bits (`other users can access it (mode 0755); run: chmod 700
  <path>`), and have a parent that is owned by this user or root and not writable by group or others, unless
  the parent is sticky like `/tmp` (`its parent <parent> can be written by other users (mode 0777), who could
  replace it; �`);
- on Windows, have an access list whose allow entries name only this user, SYSTEM, Administrators or OWNER
  RIGHTS (and CREATOR OWNER for inheritance only). Deny entries are ignored. Otherwise: `other users can access
  it: NT AUTHORITY\Authenticated Users, BUILTIN\Users; run: icacls <path> /inheritance:r /grant:r *<user
  SID>:(OI)(CI)F /grant:r *S-1-5-18:(OI)(CI)F /grant:r *S-1-5-32-544:(OI)(CI)F /remove:g *S-1-5-11 �`. The command
  uses SIDs, so it works whatever the Windows language.

`%AppData%\whatsapp-mcp`, `~/Library/Application Support/whatsapp-mcp` and `~/.config/whatsapp-mcp` pass on a
standard installation. A directory such as `D:\data` usually inherits `Authenticated Users` and is refused until
it is fixed with the suggested command.

**Files inside it** that whatsapp-mcp reads or writes (`config.toml`, `whatsapp-mcp.lock`) must be regular files
with one link and the same owner. On Unix group and others must not be able to write them (reading is harmless
inside a private directory); on Windows their access list follows the directory rule. They are opened without
following a link and then checked on the open handle, so a file swapped in after a check is not used. A new
lock file is created with mode 0600 on Unix and inherits the directory's access list on Windows.

Out of scope here: the database and other files of later features check their own files; disk and backup
encryption are the operator's responsibility. The privacy check is no defence against root or an administrator.

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
