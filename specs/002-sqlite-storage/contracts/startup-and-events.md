# Contract: startup behaviour and storage events

What the operator sees. This covers the commands that open the store, their exit codes, and the structured log
events (FR-005, FR-048).

## Commands

| Command | Opens the store | When |
| --- | --- | --- |
| `stdio`, `serve` | yes | after the data directory is prepared and the lock is taken; closed as a shutdown step, before the lock is released |
| `pair` | yes | after the lock; closed before the command returns |
| `status`, `version` | no | — |

Exit codes are unchanged from feature 001: 0 = clean stop, 1 = environment/runtime error (every store refusal and
open failure), 2 = usage error.

## Startup error messages (stderr)

Each starts `whatsapp-mcp: ` and names the file relative to the data directory or as an escaped, bounded
absolute path. It gives a safe reason and, where there is one, the fix. Examples (exact wording is fixed by the
tests):

```text
whatsapp-mcp: <data dir>/whatsapp.db is not a usable SQLite database; it was left unchanged
whatsapp-mcp: <data dir>/whatsapp.db: message schema version 7 is newer than this binary supports (5); use a newer whatsapp-mcp
whatsapp-mcp: <data dir>/whatsapp.db: device schema compatible from version 20, this binary supports up to 16; use a newer whatsapp-mcp
whatsapp-mcp: <data dir>/whatsapp.db-wal: other users can read it (mode 0644); run: chmod 600 '<data dir>/whatsapp.db-wal'
whatsapp-mcp: <data dir>/whatsapp.db-shm exists without whatsapp.db; move it away after checking it is not needed
whatsapp-mcp: <data dir>/whatsapp.db: message migration 0003 (add-index) failed; completed: device 16, messages 2; see OPERATIONS.md "Storage: recovery"
whatsapp-mcp: data directory \\server\share\wa is a network path; use a local disk
```

## Events

All events are emitted at `info`, or at `warn`/`error` for failures, through the process `slog` logger. In
`stdio` that logger writes to stderr. `time` is rendered in UTC.

| `event` | When | Fields besides `op_id`, `outcome`, `code` |
| --- | --- | --- |
| `store.open` | `Open` starts | `path` |
| `store.refused` | an artifact or path check or a preflight refuses | `artifact`, `schema`, `observed`, `supported` |
| `store.preflight` | preflight finished | `messages_version`, `device_version`, `device_compat`, `fresh` |
| `store.upgrade` | each device upgrade run (one event for the run) | `schema=device`, `from`, `to` |
| `store.migrate` | each message migration | `schema=messages`, `from`, `to`, `name` |
| `store.ready` | the store is usable | `messages_version`, `device_version` |
| `store.close` | `Close` finished | `duration_ms` |
| `store.io_error` | a runtime I/O, disk-full, disk-low or corruption failure | `op`, `sqlite_code` |
| `store.limit` | an operation was refused for a limit or for busy (at most one per limit per 10 s, then a count) | `limit`, `suppressed` |

whatsmeow's device-store logs arrive as `event=whatsmeow.log`, with `module` (the `Sub` path) and the message
set to the upstream format string. Their arguments are never logged (research R3), and they carry no `op_id`,
`outcome` or `code`.

`outcome` is one of `ok`, `refused`, `failed` or `indeterminate`. `code` is a stable snake_case reason. The full list
is fixed by the implementation and asserted by tests.

No event, error or level carries any of these:

- message or caption text;
- names taken from messages;
- media keys or hashes;
- device keys or other key material;
- QR data;
- SQL text or its parameters;
- raw driver error strings.

`artifact` and `path` values are at most 512 bytes, with control characters escaped.
