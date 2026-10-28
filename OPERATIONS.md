# Operating whatsapp-mcp

This guide is for the person who runs `whatsapp-mcp`. It starts with storage, the part that holds your WhatsApp
session and your messages.

## Storage

Everything the binary keeps lives in one SQLite file, `whatsapp.db`, in the data directory. While the process
runs, SQLite also keeps `whatsapp.db-wal` and `whatsapp.db-shm` next to it. Nothing else is stored: no other
database, no temporary files.

### Files and privacy

| File | What it holds |
| --- | --- |
| `whatsapp.db` | The device credentials (noise, identity and signing keys, the advertised secret), contacts and LID mappings, chats, message text and captions, and the media keys needed to download media. |
| `whatsapp.db-wal` | Recent changes that are not yet merged into the main file. It holds the same kinds of data. |
| `whatsapp.db-shm` | An index over the log. It holds no message data. |

These files are private to the user running the binary: owner-only on Unix (mode 0600 inside a 0700
directory), and on Windows an access list limited to that user, SYSTEM and Administrators. **This is not
encryption at rest.** Anyone who can read the disk, a backup or a snapshot can read your credentials and messages.
Encrypt the disk or the backup destination if that matters to you.

Supported: a local disk. On Windows that means a local NTFS volume; a network path (`\\server\share`), a device
path (`\\?\`, `\\.\`), an alternate data stream and a mapped network drive are refused at startup. Network file
systems on Unix are not detected and are not supported: SQLite's locking is unreliable on them.

### Growth and retention

Messages are kept indefinitely. The database, its log and every backup grow with the history. Plan the disk for
it. When less than 256 MiB is free, the binary refuses to store new messages and chat updates until 512 MiB is
free again, and it deletes nothing to make room. The device store's own writes are not refused; they can fail
on a full disk.

### Backup

Back up only a quiesced database:

1. Stop the process and every other program that has the database open.
2. Confirm a clean close: there is no `whatsapp.db-wal`, or it has length 0.
3. If a log is left, copy the whole stopped file set together (`whatsapp.db`, `-wal` and `-shm`).
4. Copy into a private destination, a directory only you can reach.
5. Protect the backup like the live credentials: it holds them.

Copying only the main file while the process runs is unsupported and can produce a backup that is missing recent
commits or is damaged. Directory locks do not coordinate copies.

### Restore

Restore into a new private directory, offline, and check the copy with any SQLite shell (for example `sqlite3`)
before it replaces anything:

```sh
sqlite3 whatsapp.db "PRAGMA integrity_check"        # expect: ok
sqlite3 whatsapp.db "PRAGMA foreign_key_check"      # expect: no rows
sqlite3 whatsapp.db "SELECT * FROM mcp_version"
sqlite3 whatsapp.db "SELECT version, compat FROM whatsmeow_version"
```

Replace the live storage only after the checks pass, and keep the original until then. **A restored device store
must never connect beside the original:** two copies of one WhatsApp session can get the account banned. Checking
a restored copy against a live account, or re-pairing, needs a separate decision by you.

### Recovery after a failed upgrade

Startup upgrades the device schema first and then the message schema, one step at a time, and each step is one
transaction. If a step fails, the error names it and lists what completed, for example
`completed: device 16, messages 2`. Nothing is half-applied. Fix the cause (usually a full disk or a damaged
file) and start again with the same binary: it resumes from the last completed step. There is no downgrade. If
the file is damaged, restore it from a backup.

### Recovering from a refused start

The binary refuses to start rather than touch a file it cannot trust. It never deletes anything first. Each
refusal has a code; find yours:

- **`rollback_journal`**: `whatsapp.db-journal` is left over from an interrupted write. Stop everything, copy the
  whole file set to a private place, then open the database once with SQLite on the live file in the data
  directory, not on the copy:
  `sqlite3 whatsapp.db "PRAGMA integrity_check"`. If the journal holds an unfinished transaction, SQLite rolls it
  back. SQLite leaves an empty or zeroed journal where it is, because it is not "hot", so move any
  `whatsapp.db-journal` that is still there into the private copy. Then start again.
- **`orphan_sidecar`**: a `-wal`, `-shm` or `-journal` file is next to a missing or empty `whatsapp.db`. Move
  the sidecars to a private place and restore `whatsapp.db` from a backup. With no backup, keep the moved files and
  start fresh, which pairs a new device.
- **`schema_too_new`, `schema_invalid`, `not_database`**: the file was written by a newer binary, has damaged
  version metadata, or is not a database. Keep the files. Use the binary that wrote them, or restore from a
  backup.
- **`unsafe_artifact`, `not_private`, `unsupported_path`**: a file is a link, a hard link or not a regular file,
  was replaced while starting, is readable by other users, or the path is not a local one. Fix the ownership,
  access or path as the error's fix text says (for example `chmod 600 <file>`); the binary never changes the
  permissions of a file that exists.
- **`memory_limit_unavailable`**: the binary could not switch on SQLite's memory limit. It is a build or
  packaging defect, not a data problem: keep the files and use an official release.
- **`corrupt`**: an operation reached a damaged page. Stop, keep the whole file set, and follow "Restore" with
  `PRAGMA integrity_check` on a copy before deciding what to restore.

### Decommissioning

To retire an installation: stop the process, then list only this installation's database and sidecars, its logs and
its backups. Unlinking a file does not guarantee that the data is gone from an SSD, a snapshot or a backup, and
destroying keys works only as far as your storage supports it.

### Operator responsibilities

The binary does not do these for you: encrypting the disk or the backups, choosing where backups go and how long
they are kept, setting recovery-point and recovery-time objectives, and rehearsing a restore.
