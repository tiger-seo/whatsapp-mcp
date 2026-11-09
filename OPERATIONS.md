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

## WhatsApp connection, pairing and status (feature 003)

This section describes what the binary does today and what has and has not been checked. "Implemented" means
the code exists and its automated tests pass (Windows locally, and Linux, Windows and macOS runners in CI on every
push). "Verified" means checked against a real WhatsApp account or real hardware. **Three permitted runs have used a
real account, all on one account and one phone on Windows**: two pairings on 2026-11-06 (the first with the
post-pair reconnect refused, the second with it enabled) and on 2026-11-07 one full journey: a fresh link, an unsafe
restart refused, offline recovery, a live logout and the denial of a same-process re-pair (see `verification.md`).
Every other check below is simulated or local.

### Commands

| Command | What it does |
| --- | --- |
| `status` | Reports the local snapshot offline. It never connects, never contacts WhatsApp and creates no files. It prints four fields (`connected`, `logged_in`, `pairing_required`, `wa_version`); `connected` is always `false` offline, and `logged_in` means a saved identity exists locally, not that WhatsApp still accepts it. It fails with the holder's PID while another command owns the data directory, and with exit 1 and a warning when the store is marked unsafe or comes from an older schema. |
| `pair` | Opens one pairing window, draws the current QR code in the terminal and waits. It exits 0 only when the identity was saved and the connection was confirmed. After a fresh link whose stop check passed it prints "linked. The saved session can be reused." and the marker is clear; if the stop check failed it prints "linked. Offline recovery is required before the next run" with the failed category. |
| `stdio`, `serve` | Open the store and the owner. With a clean store and a saved identity they connect once at startup; with an unsafe store they exit 1 and tell you recovery is needed. At an orderly stop they print "stopped. The saved session can be reused." after a passed quiet-stop check, or "offline recovery required (<category>)" and exit 1 after a failed one. |

### Pairing

- One approved window covers any number of QR codes. A code lives for the earlier of the server's expiry and 60
  seconds, and is replaced automatically inside the same window. A code is at most 4 KiB.
- The window ends on success, on the final timeout, on `Ctrl+C` or a stop signal, on a
  failure, or on an unsupported flow (a passkey or phone-code request). **Pressing Enter does nothing**, and closed input (EOF) does not end the window: it never
  retries and never starts recovery.
- After a successful scan the server asks for a restart (stream error 515, seen about 0.6 s after the pair success on a real account). In `pair` only, the owner first retires the pairing client and checks that it left nothing running, and only then makes exactly one follow-up connection on a fresh client from the saved identity; the long-running commands never do. If that check fails, or the connection fails, stalls past 30 seconds or is cancelled, `pair` prints "linking could not be completed safely" and exits 1. The phone may show a failure message and list no device (observed once on a real account, see `verification.md`), or it may list a linked device; open Linked devices on the phone and remove any entry by hand before pairing again.
- After a final timeout, a failure or a cancel, `pair` exits 1. A device may be linked on the phone (check Linked
  devices), so the store stays unsafe (below). Do not just run `pair` again.
- The QR code appears only on the terminal that ran `pair`. It is never logged, never stored and never in an
  error message.
- Pairing success is not a complete sync. If repair is still pending (below), `pair` warns that some history may
  sync later.

### The unsafe marker

Any attempt to connect to WhatsApp (a startup connect, a pairing window, a failed or cancelled attempt) marks the
store **unsafe** while it runs. At an orderly stop the binary retires each client that attempted a connection: it
blocks the client's network and storage, then watches the process for the client library's goroutines. If none is
left, twice, 100 ms apart, within the approved time (1 s for an ordinary stop, 2 s for the pairing client before
the follow-up connection) and with no refused call, dropped event or late call, the check **passes** and the marker
is cleared, so the next start reuses the saved session. Any doubt fails the check and the marker stays unsafe; the
category is printed (`work_running`, `network_refused`, `storage_refused`, `call_refused`, `event_dropped`,
`stop_bound_exceeded`, `observation_incomplete`, `observation_error`) and the counts are in the
`quiet_stop_failed` event. A passed check logs `quiet_stop_passed`. The marker also stays unsafe after an unresolved
pairing, a storage fault, a failed logout cleanup, a kill or a second signal. While the marker is unsafe the binary
starts no connection and no QR window. Automatic reconnects are all denied: there are no startup retries, no 405
retry budget; the one exception is the single post-pair connection inside `pair`. A lost connection stays lost,
and after a failed check you must run offline recovery (below) and pair again.

The check counts library goroutines across the whole process. It is a strong signal, not a proof: it cannot see
work the library started outside its own packages, and its allow-list is empty.

What the categories mean in practice:

- `work_running`: a client-library goroutine was still alive at the deadline. One known cause was a library defect: a
  frame arriving after the client stopped reading could block the socket's read loop forever. It showed up mostly
  when the server ended the stream at the moment of the stop. The pinned library version (upstream commit `f1b7985`)
  fixes it. Any other cause is unknown and should be reported.
- `network_refused`: the retired client tried to open a new connection after its network was blocked. A write on a
  connection that the block already closed (for example the TLS close alert during teardown) is refused and sends
  nothing, but does not fail the check.

A store from before this feature (schema 1 or 2) is treated as unsafe, not as clean, because its history is
unknown. A store whose marker is missing or malformed is refused.

### Offline recovery

Recovery is a separate, deliberate step that works on the local store only; this program never unlinks a device
on the phone and never connects.

1. Run `pair` from a real interactive terminal (on Windows a native console, not Git Bash or mintty; pipes and
   redirected input are refused).
2. Type `recover`. Read the text, and identify the exact account and linked device with your own evidence. A
   shared "Linux/Chrome" label, the time a device appeared or the lack of a local identity does not identify it.
3. Type `absent` if the matching entries are gone from the phone. If you cannot tell your entry apart, you may
   type `unlink`, then `authorize-unlink`, and remove every possibly matching entry yourself (this can disconnect
   other devices). Then type `verified`.
4. If a local identity exists, type `remove` to authorize its local removal.

Anything else, including Enter, EOF, an over-long line or `Ctrl+C`, leaves the marker unsafe. Recovery keeps
chats and messages and keeps pending repair needs. It never starts a connection; a fresh pair needs a second,
separate invocation. If the disk has less than 512 MiB free after being full, recovery refuses to clear the
marker; free space first.

A phone-side label for the linked device is not guaranteed by this program: it asks for Linux and Chrome, but the
text the phone shows has not been observed.

### Pending repair

If a sync was interrupted (a crash, a stop, a disk error), the store keeps a repair marker per collection. After
the next confirmed connection the owner runs one bounded repair pass: one full resync per collection, waiting at
most 10 seconds for each call, one call at a time, with no automatic retry. The marker is cleared only by a
fully successful repair. A failed or timed-out repair keeps it, and the next start tries again. Until then the
health state is "recovery pending" and `pair` prints a warning. Ordinary app-state sync is deferred while a
marker is pending and replayed once afterwards.

### Version and limits

| Item | Limit |
| --- | --- |
| Version discovery | One request to `web.whatsapp.com`, 15 s, 1 MiB, no redirects. A failure keeps the bundled version, which is the fallback shown by `status`. There is no scheduled refresh yet. |
| Connect watchdog | 30 s per attempt. |
| Pairing code | 4 KiB, 60 s. |
| Repair call | 10 s caller wait. |
| Disk | New messages refused below 256 MiB free, resumed at 512 MiB. |

### Stopping

The first stop signal starts an orderly drain: windows close, the client disconnects, work finishes, then the
store closes and the lock is released. A second signal, or a kill, ends the process uncleanly: the store stays
unsafe and the next start requires recovery. The drain has a timeout (10 s, including the quiet-stop check); hitting it is a failure, not permission to
release the store.

### What has been checked

- Automated tests pass on Linux, Windows and macOS runners, including process-kill tests of the markers.
- Feature 004 (quiet stop and the fresh-client post-pair link) is tested on the simulator and the store, and on
  2026-11-08 it ran on the same real account and phone on Windows: two `pair` links on a fresh client and restarts
  that reused the saved session. Before the fence rule above was changed, one stop of four failed with
  `network_refused` (a TLS close alert); after it, seven of seven checks passed. Seven passes are consistent with the
  fix, not a proof. These runs used the previous library version; the current pin (`f1b7985`) has passed the automated
  tests on native Windows and under WSL2 only (not yet on the CI runners) and has not run on a real account. The 1 s and 2 s deadlines are about 100 times the slowest measured
  simulator check; real checks took 270 to 440 ms. A server disconnect during a stop was not tried on a real account.
  The check counts goroutines process-wide, so a goroutine leaked by anything else in the process fails it.
  Evidence: `specs/004-shutdown-drain/verification.md`.
- **Not checked:** power loss and OS crash; macOS `F_FULLFSYNC` hardware flushes; interactive recovery on a real
  console, Linux terminal or macOS terminal; a kill between pairing and the first save; a second real account or
  phone model (every real run used one account and one phone).
- A known upstream gap, E003-RX-01, is open: the WhatsApp library does not bound some receive-side resources.
  It is a temporary exception for private, local use, owned by `tiger-seo`, expiring at the start of 2026-12-02
  UTC, with earlier review before any public release, a relevant upstream fix or a resource incident. The owner
  extended it to feature 004 and to the current library pin (2026-11-09). See
  `docs/backlog/003-whatsmeow-receive-resource-bounds.md`. It does not make the device safe to expose and is
  not a claim that retention is bounded.

### Monitoring and incident response

At debug log level the owner writes one sample at start, one a minute and one at stop (`event=runtime_sample`,
with the process heap in bytes, the goroutine count and a fixed reason). Nothing is kept between samples and they
carry no account, QR, key or message data. Also watch the process's memory and the data directory size from the
operating system against a baseline you have seen on your own machine. Both are observations, not limits, and
they cannot prevent or prove exhaustion. If memory grows without explanation, stop the
process with a normal stop signal and wait for it to exit. If you have to kill it, the next start will require
recovery as above; do not delete the database to get around that.
