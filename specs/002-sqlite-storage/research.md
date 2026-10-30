# Research: Storage: one SQLite file and migrations

Phase 0 of the [plan](plan.md). Each entry records a decision, why it was made, and what else was considered. Sources
were read on 2026-10-28 from the pinned modules: `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`,
`go.mau.fi/util v0.11.0` (its `dbutil`) and `modernc.org/sqlite v1.61.0`, plus `docs/spikes.md` S1 and the code on
`main` (`internal/private`, `internal/config`, `cmd/whatsapp-mcp`).

## R1. Driver, DSN and pragmas

- **Decision:** `modernc.org/sqlite v1.61.0`, driver name `sqlite`. The read-write DSN is
  `file:<path>?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=journal_size_limit(67108864)&_pragma=hard_heap_limit(268435456)&_txlock=immediate`.
  The read DSN is a separate string, not the write DSN with an extra pragma:
  `file:<path>?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)&_pragma=hard_heap_limit(268435456)&_pragma=query_only(1)`.
  It has no `_txlock`, so its transactions are the default `BEGIN DEFERRED`, and no `journal_mode`,
  `synchronous` or `journal_size_limit`, which only the writer needs.
- **Rationale:** the S1 spike proved this DSN with whatsmeow's `sqlstore` (dialect `"sqlite"`), including 16×100
  concurrent writes without errors. `_txlock=immediate` takes the write lock at `BEGIN`, so writers wait in the busy
  handler instead of failing on lock upgrade. `synchronous(NORMAL)` is the documented safe setting for WAL: a
  committed transaction survives a process crash (SC-007); only an OS crash or power loss can lose the last
  commits, never corrupt the file. `temp_store(MEMORY)` keeps SQLite's temporary files (sorts, statement
  journals) out of the system temp directory, which is not private (FR-040); their memory is bounded by the
  hard heap limit (R16). `journal_size_limit` implements the approved 64 MiB WAL truncation. The read DSN must not
  inherit `_txlock=immediate`: `BEGIN IMMEDIATE` asks for the write lock, which a `query_only` connection refuses,
  so every transaction on the reader (ours, and dbutil's through `ReadOnlyDB`) would fail.
- **Alternatives:** `mattn/go-sqlite3` (needs CGO, forbidden by Principle II); `synchronous(FULL)` (slower on every
  commit for a guarantee the spec does not require); a private `SQLITE_TMPDIR` inside the data directory (works,
  but adds another artifact to check, protect and clean up).

## R2. Connection pools

- **Decision:** two `*sql.DB` handles on the same file. The **writer** has `SetMaxOpenConns(1)` and serves all
  writes, both ours and whatsmeow's device store. The **reader** has `SetMaxOpenConns(4)`, the read DSN (R1), and
  serves our queries; it is also given to dbutil as `ReadOnlyDB`. Idle connections are kept open (no lifetime
  limit), so no connection is reopened while the store runs.
- **Order at open:** `mcp_fold` (R7) is registered before either pool exists, because modernc applies registered
  functions to connections opened after the registration only. A reader connection opened earlier would answer
  "no such function" for as long as it lives. The reader is opened and pinged before the device upgrade, because
  the container given to `Upgrade` already carries it as `ReadOnlyDB`.
- **Rationale:** matches the approved profile (1 write + 4 read connections). One writer connection makes write
  ordering explicit and means a write never waits on SQLite's busy handler for another of our own connections;
  WAL lets the four readers run alongside it. whatsmeow's container is built with `sqlstore.NewWithWrappedDB` on a
  `dbutil.Database` wrapping the writer (`dbutil.NewWithDB(writer, "sqlite")`), so it shares the file and the lock.
- **Alternatives:** one pool of five connections (whatsmeow and our writes would compete through `SQLITE_BUSY`
  retries, the cause of the 5.4 s vs 2.3 s difference in the spike); a separate file for the device store
  (forbidden by S1).

## R3. whatsmeow device-schema upgrades: what the pinned version guarantees

- **Decision:** call `Container.Upgrade(ctx)` after both preflight checks pass and before message migrations.
  Treat each device upgrade step as atomic; no extra wrapping is needed.
- **Evidence:** `upgrades.Table` is built with `dbutil.BuildUpgradeTable().WithFS(...)`. None of the 15 upgrade
  files (`00-latest-schema.sql` v0→v16, compatible with v8+, and `03`–`16`) has a `-- transaction:` header, so
  `parseFileHeader` gives each the default mode `on`. In `Database.Upgrade`, mode `on` runs the step and
  `setVersion` inside one `DoTxn`; on error the transaction is rolled back and `Upgrade` returns. So each step and
  its version record commit or roll back together, and earlier steps stay committed: exactly FR-041's boundary.
- **Caveats found, and how they are handled:**
  - `Upgrade` first calls `checkDatabaseOwner` and `getVersion`, which **writes**: it creates `whatsmeow_version`, or
    adds its `compat` column, outside any transaction. It only refuses a too-new schema *after* that. So the
    whatsmeow upgrader must never be the preflight. Our own read-only preflight (R4) reads
    `whatsmeow_version (version, compat)` itself, and refuses `compat > len(upgrades.Table)` (today 16) before
    `Upgrade` is called.
  - `Upgrade` refuses to run unless `PRAGMA foreign_keys` is on (the DSN sets it).
  - `dbutil.NewWithDB` sets `Log: NoopLogger`, so dbutil never logs SQL or parameters (FR-048).
  - whatsmeow's own `waLog.Logger` for the container is bridged into the process `slog` logger (constitution
    Principle IV) by a small adapter, `internal/store/walog.go`. It forwards the level, the module path from `Sub`
    and the **format string only**. The arguments are dropped, because they can carry JIDs, keys or upstream error
    text (FR-048).
  - Feature 003 may widen the bridge for the client's logs. The rule of forwarding only the template stays.
  - A future whatsmeow bump must re-run this check. The transaction mode is not exported, so
    `TestDeviceUpgradeStepsAreTransactional` finds the pinned module's directory with `go list -m -f {{.Dir}}`. It
    then asserts that no file in `store/sqlstore/upgrades` has a `-- transaction:` header other than `on`, and that
    the number of versions equals `len(upgrades.Table)`. If it fails, FR-041 must be revisited before the bump.
- **Alternatives:** wrapping `Upgrade` in our own transaction (impossible: dbutil opens its own transaction per
  step, and SQLite has no nested `BEGIN`); hand-editing whatsmeow's schema (forbidden by FR-041).

## R4. Read-only preflight that changes no file

- **Decision:** before any read-write connection exists:
  1. `Lstat` the database and each sidecar name (`-wal`, `-shm`, `-journal`). Reject links, reparse points,
     non-regular files and hard links, and check ownership and access through `internal/private` (R6). A sidecar
     next to a missing or empty main file is refused ("orphaned journal file"): SQLite deletes the log of a
     database with no pages (`pagerOpenWalIfPresent`), so treating that state as fresh would delete it. Any
     `-journal` is refused too ("rollback journal left behind"): the view cannot apply it, and the writer's open
     would roll it back and delete it. Only a crash during a journal-mode change or another tool leaves one.
  2. If the main file is missing, or has length 0 with no sidecar, there is nothing to preflight: it is a fresh
     database.
  3. Otherwise read its first 100 bytes in Go and require the SQLite magic `SQLite format 3\0` and a valid page
     size. Anything else is "not a usable database" (US1 scenario 3) without SQLite ever opening the file.
  4. Build the **committed view** (below) and open one connection through it. Read whether `mcp_version`,
     `whatsmeow_version` and any other `mcp_`/`whatsmeow_` tables exist, the message version, and the device
     `version` and `compat`. Close the connection and unregister the view.
  5. Refuse a newer message version, a device `compat` above the bundled table, a version table (either
     `mcp_version` or `whatsmeow_version`) with zero or more than one row, or schema tables present without
     their version table. dbutil reads `whatsmeow_version` with `LIMIT 1` and takes a missing row as version 0,
     which would run the full schema over existing tables, so this check must be ours. One state is not
     refused: an empty `whatsmeow_version` with no other `whatsmeow_*` table. dbutil creates the version table
     in its own statement, outside the transaction of step v0→v16 (`dbutil/upgrades.go`:52-70, :204-241), so a
     first start killed between the two leaves exactly that. It is device version 0, and the next start runs
     the first step.
  6. The view records the identity of the main file and `-wal` from the handles it read, and they must equal the
     identities `checkArtifacts` recorded (R6).
- **The committed view:** SQLite never opens the real files during the preflight. A read-only `io/fs.FS`
  (`internal/store/walview.go`) is registered with `modernc.org/sqlite/vfs.New`, and the preflight connects with
  `file:db?mode=ro&vfs=<name>`. Through that VFS SQLite can neither write nor create a file. The view exposes a
  single name, `db`, and no `-wal`, `-shm` or `-journal` names.
  - **WAL scan:** the view opens the main file and `-wal` read-only with `os.Open` and scans the WAL once, in a
    stream, as SQLite's recovery does:
    - A header that SQLite's `walIndexRecover` would ignore makes the view ignore the log too: a magic other
      than `0x377f0682` or `0x377f0683`, a page size that is not a power of two from 512 to 65536, or a header
      checksum that does not match. Such a log holds no commit; a header of zeros after a power loss is one.
    - The magic selects the checksum byte order. A format version other than 3007000 is refused as "not a
      usable database", as SQLite refuses it.
    - A valid header whose page size differs from the main file's is also refused as "not a usable database".
    - A frame counts only if its salts equal the header's, its page number is not 0, and the running checksum
      matches. The first frame that fails ends the scan.
    - Frames apply only up to the last commit frame. That frame's commit field sets the database size in pages.
  - **Page index:** the scan keeps an index from page number to the WAL offset of that page's latest committed
    frame, not the page data. Memory grows with the number of distinct pages in the WAL (bounded by the WAL size
    divided by the page size), never with the database.
  - **Reads:** a page found in the index is read from the WAL with `ReadAt`, and any other page from the main
    file with `ReadAt`. A page past the main file's end that the WAL does not supply reads as zeros. The file
    size reported to SQLite is the committed size.
  - **Format bytes:** bytes 18–19 (file format write and read version) are presented as `1` (rollback journal),
    so SQLite never asks for shared memory.
  - **`-shm`:** it is never read. Its only job is to coordinate live connections, and the preflight runs while
    holding the instance lock, so none exists.
- **Rationale:** spike S2 (`docs/spikes.md`, 2026-10-28) tried every SQLite read-only mode on Windows and Linux,
  using clean, empty-sidecar, crash, torn-frame and restarted-WAL states.
  - `mode=ro` creates or rewrites `-shm` and `-wal` in every state.
  - `readonly_shm=1` is unchanged only when both `-wal` and `-shm` already exist. After a clean close it creates
    empty sidecars on Windows. On Linux it fails to open when there is no `-shm`.
  - `immutable=1` misses every commit still in the WAL.
  - Deserialising the whole image is correct but needs about 3× the database size in memory (3.0 GB for 1 GB).
  - The committed view gave SQLite's own answer in every state on both platforms and changed no file. On the
    1 GB database it ran in 2.4 s with a 19 MB peak RSS, and that time was mostly a full-table scan the real
    preflight never does.

  The SC-006 test proves it per platform. It snapshots name, size, modification time and SHA-256 of every file in
  the directory before and after a refused open, for each refusal (newer message schema, newer device schema, bad
  version metadata, not a database), with and without a WAL holding committed data.
- **Correctness of the view:** the WAL rules above are the part we own, so they are tested against SQLite
  itself:
  - **Fixture states:** a differential test builds clean, empty-WAL, crash, zeroed-`-shm`, torn-last-frame and
    restarted-WAL states. For each, it compares what the view returns with what SQLite returns when it opens a
    throwaway copy of the same files read-write.
  - **Fuzzing:** a fuzz target feeds arbitrary WAL bytes. The view may refuse or ignore the WAL, but it must
    never panic, read past either file's end, or index more pages than the WAL can hold.
- **Verified since the spike:** the gate (T014) passed on Linux, Windows and macOS arm64 in CI run 40671433110 on
  2026-10-28, and a synthetic big-endian log gives the same index as its little-endian twin.
- **Still not verified:** databases over 4 GiB (page numbers are 32-bit, so offsets are computed in `int64`).
- **Integrity:** the preflight does not run `PRAGMA integrity_check` on every start, which would cost seconds to
  minutes on a large history. Corruption in the header or schema pages is caught by steps 3–4. Corruption deeper
  in the file surfaces as `SQLITE_CORRUPT` on access and fails that operation safely. Full `integrity_check` and
  `foreign_key_check` are part of the restore procedure (FR-047).
- **Alternatives:**
  - `mode=ro&readonly_shm=1`, the earlier decision, abandoned after S2: it creates sidecars on Windows and
    fails on Linux when there is no `-shm`.
  - `immutable=1`, which ignores the WAL and so misses committed state.
  - `immutable=1` when the WAL holds no commit and the view otherwise: two code paths for no gain, since the
    view handles both cases.
  - In-memory deserialisation, which holds the whole database in memory.
  - Copying the files to a temporary directory, which creates new sensitive artifacts.

## R5. Message-schema migrations

- **Decision:** our own small runner, not dbutil's. Migrations are embedded files
  `internal/store/migrations/NNNN_<name>.sql`; the version is `NNNN`. The version lives in a one-row table
  `mcp_version (version INTEGER NOT NULL)`, separate from `whatsmeow_version`. Each pending migration runs on the
  writer connection in one `BEGIN IMMEDIATE` transaction together with its `UPDATE mcp_version`. Migration 1
  creates the table and inserts version 1. The startup context carries the approved 10-minute budget.
- **Rationale:** dbutil's runner writes before it checks compatibility (R3), and the plan already needs its own
  read-only preflight. A runner that executes numbered files in transactions is about 60 lines. A failing
  migration is reported as `migration NNNN (<name>) failed`, together with the versions that completed (FR-041).
- **Test hook:** the runner takes its migration list as a parameter, so tests inject a failing migration N+1 and
  a "future" version without touching the embedded files.
- **Alternatives:** dbutil's upgrade table with `VersionTable = "mcp_version"` (rejected because it writes before
  checking); `golang-migrate` or `goose` (new dependencies for 60 lines; Principle IX).

## R6. Private files: database, WAL, SHM and temporary files

- **Decision:**
  - The data directory is already private: `config.PrepareDataDir` → `private.MkdirAll` (merged in #2). On
    Windows it carries a protected DACL with inheritable entries for this user, SYSTEM and Administrators.
  - A new database is created with `private.OpenFile(path, O_RDWR|O_CREATE|O_EXCL)` before SQLite opens it: mode
    0600 on Unix, the directory's inherited DACL on Windows. SQLite then writes the schema into the existing file.
  - SQLite creates `-wal` and `-shm` itself. On Unix its VFS gives them the main file's permissions and owner; on
    Windows they inherit the private directory's DACL. After the read-write open, every artifact is checked
    through an open handle. An unsafe one closes the store and fails startup.
  - SQLite's temporary files never touch disk (`temp_store(MEMORY)`, R1). Rollback journals are not used in WAL
    mode, but a `-journal` left from another tool is checked like any other sidecar.
  - `internal/private` gains `CheckFile(path)` to check an existing file without opening it for writing. It adds
    the stricter rule FR-004 needs for these files: on Unix no group or other permission bits at all (0600),
    where the existing rule only forbids group and other *write*. The Windows rule is unchanged. It returns the
    file's identity (device and inode on Unix, volume serial and file ID on Windows).
  - **Linux ACLs** need no separate check: with a POSIX ACL, the group bits of the mode are the ACL mask, so a
    0600 or 0700 mode leaves every named-user or named-group entry without effective permissions.
  - **macOS ACLs** are separate from the mode bits and can grant access that 0600 does not show. `CheckFile` and
    the data-directory check read the extended security attribute with `unix.Getattrlist`
    (`ATTR_CMN_EXTENDED_SECURITY`) and refuse any artifact with an ACL entry, with the fix
    "run: chmod -N <path>". This is pure Go (`golang.org/x/sys/unix`). CI's macOS job tests it with `chmod +a`.
  - **File identity after open:** `checkArtifacts` records each existing file's identity; for a new database,
    `createDB` records it from the handle that created the file. The preflight's view checks the handles it
    reads against these (R4 step 6). After the writer's first ping, the files at the same paths are compared
    again. A difference means a file was replaced between the check and
    SQLite's open, and startup fails with `unsafe_artifact` before the device upgrade or any migration. This is
    the check abuse case 2 tests, with a test hook that swaps the file at that point.
- **Rationale:** the data directory is owned by this user and nobody else can write it (`private` checks the
  parent), so other users cannot create, rename or swap entries inside it. That is the defence against
  check/open races (FR-040); the handle checks catch anything already there. The approach reuses the
  constitution's mandated package rather than a second implementation.
- **Path forms (approved profile):** the data directory path is rejected before use if it is a UNC or network
  path (`\\server\...`), a device path (`\\?\`, `\\.\`) or contains an alternate data stream (`:` after the drive
  letter) on Windows. The text alone misses drive letters: a mapped network drive (`net use Z: \\server\share`)
  and a `subst` drive both look local. So the check also opens the directory and asks Windows where it really is:
  - `GetFinalPathNameByHandle` gives the resolved path. A `\\?\UNC\` result is a network path, and a `subst`
    drive resolves to its real target, which is then checked the same way;
  - `GetDriveType` on the resolved volume must be `DRIVE_FIXED` or `DRIVE_REMOVABLE`, never `DRIVE_REMOTE`;
  - `GetVolumeInformationByHandle` must report the file system `NTFS`, the only one the profile names.

  All three are in `golang.org/x/sys/windows`. Unix network filesystems are not detected (no portable signal);
  they are documented as unsupported in `OPERATIONS.md`.
- **Alternatives:** a custom SQLite VFS that opens every file through `private` (heavy, and duplicates what the
  directory guarantee already gives); `chmod` after creation (a race window, and it changes files, which the
  constitution forbids for existing ones).

## R7. Case-insensitive, NFC-normalised, literal search

- **Decision:** define `fold(s) = cases.Fold().String(norm.NFC.String(s))`, using `golang.org/x/text`. Store a
  folded copy next to every searchable column (`chats.name_fold`, `messages.text_fold`, `messages.caption_fold`),
  computed in Go on write. A query folds the search text the same way and matches with
  `instr(<col>_fold, ?) > 0`. For the device store's contact names, which we cannot add columns to, the same
  `fold` is registered as a deterministic SQL function `mcp_fold` through
  `sqlite.RegisterDeterministicScalarFunction`, and used as `instr(mcp_fold(full_name), ?) > 0`.
- **Rationale:** `instr` is a plain substring test, so `%`, `_`, quotes and backslashes are literal without any
  escaping (FR-034, FR-046). Full Unicode case folding (not `strings.ToLower`) handles `ß`/`SS`, Greek final sigma
  and Cyrillic. NFC makes composed and decomposed `é` equal, and accents still count (clarification 2026-10-28).
  Pre-folded columns avoid calling Go once per row on 100,000 messages; that cost would endanger SC-004.
  Contacts are few, so the function is cheap there. `golang.org/x/text` is pure Go and maintained by the Go
  team; it is the new dependency justified in the plan.
- **Consequence:** changing `fold` later requires a migration that recomputes the `_fold` columns. A golden test
  pins its output for a set of strings, so that an `x/text` upgrade that changes folding is noticed.
- **Alternatives:** SQLite `LIKE`/`lower()` (ASCII only); FTS5 with the `unicode61` or trigram tokenizer (word or
  trigram semantics differ from "contains", and folding rules differ from Go's); folding in Go after loading
  rows (unbounded work).

## R8. Time representation

- **Decision:** all times are `INTEGER` Unix **milliseconds**, UTC. The API takes and returns `time.Time`. The
  approved range is [2000-01-01, 2100-01-01) for message times, name times and filters. A missing chat activity
  time is `NULL`.
- **Rationale:** integers order and index correctly; milliseconds hold WhatsApp's second-precision timestamps
  and any finer local time without loss, and fit easily in 64 bits. RFC 3339 rendering is the tools' job (005/006).
- **Alternatives:** `TEXT` timestamps (sorting depends on format; the old bridge's `TIMESTAMP` was text in
  SQLite); seconds (loses the sub-second order of messages created locally).

## R9. Admission, deadlines, cancellation and outcomes

- **Decision:** each public store operation passes through an admission gate. It is a counting semaphore of
  16 active operations; waiters are tracked with an atomic counter. A 17th caller waits for at most 5 s (or its
  own deadline, if earlier). A caller arriving when 64 are already waiting gets `ErrBusy` at once. Admitted
  work runs under `context.WithTimeout(ctx, 10*time.Second)`, combined with the caller's deadline. Cancellation
  reaches SQLite through `database/sql` context support, which interrupts the statement in modernc.
- **Commit outcome:** `tx.Commit()` returning `nil` is "committed". An error from `Commit` after the context was
  cancelled, or `driver.ErrBadConn` during commit, is reported as `ErrIndeterminate`. Callers retry with the same
  `(chat_jid, id)`, which is idempotent. Any other failure is reported as rolled back.
- **Result bytes:** query code accounts the bytes of each scanned row against the 32 MiB budget, and the context
  rows against the 2,000-row budget. Crossing either aborts with `ErrResourceLimit`, never a truncated result.
- **No second semaphore:** the earlier design gave deep sender listings 2 slots of their own, to bound the memory
  of their sorts. Sender listings no longer sort (R15), so every operation, deep or not, goes through the one gate
  (owner's decision, 2026-10-30).
- **Close:** every `opCtx` also derives from a store-wide context that `Close` cancels. `Close` first marks the
  gate closed, so new and waiting callers get `ErrClosed` at once. It then waits up to 2 s for running
  operations, cancels the store-wide context, waits up to 1 s for them to release (the approved cancellation
  allowance), and closes the reader and then the writer. A caller's `ctx` can shorten each wait. So the store
  takes about 3 s of the 10 s shutdown limit at most, however long an operation was going to run. An operation
  cancelled during commit reports `ErrIndeterminate` as usual.
- **Rationale:** a bounded queue, no retry loops, and deadlines that never exceed the caller's (FR-043, FR-044).
  `busy_timeout(5000)` stays, but the context deadline wins when it is earlier.
- **Alternatives:** `golang.org/x/sync/semaphore` (adds a module for 20 lines; it also lacks a waiting cap).

## R10. Disk space and WAL growth

- **Decision:** `freeBytes(dir)` is implemented with `unix.Statfs` (`Bavail*Bsize`) and
  `windows.GetDiskFreeSpaceEx`, both from `golang.org/x/sys`, already a dependency. The value is cached for at
  most 30 s. Before each write transaction, a value below 256 MiB refuses the write with `ErrDiskLow`. Once
  refused, writes resume only when ≥ 512 MiB is free (hysteresis). whatsmeow's own writes are not gated: we do
  not control them, and the spec allows them to fail on full disk. WAL checkpointing stays SQLite's automatic
  one (1,000 pages), and `journal_size_limit` truncates the WAL to 64 MiB after a checkpoint. A reader holding a
  snapshot is bounded by the 10 s operation deadline, so it cannot pin the WAL indefinitely.
- **Rationale:** cheap (one syscall per 30 s), and fails before work rather than in the middle of it.
- **Alternatives:** a reserve file deleted on low space (deleting files is forbidden by FR-045).

## R11. Validation and identifiers

- **Decision:** parse JIDs with whatsmeow's `types.ParseJID` (already in the dependency graph) and then apply our
  own rules: allowed servers (`s.whatsapp.net`, `g.us`, `lid` for chats; `s.whatsapp.net`, `lid` for senders);
  non-empty user; device part stripped for senders; the 256-byte limit; valid UTF-8 with no control characters.
  Message IDs: 1–256 bytes, valid UTF-8, no control characters, otherwise opaque. Phone normalisation
  (FR-035) accepts `+`, spaces, dashes, dots and parentheses around 1–20 digits, or an `@s.whatsapp.net` JID.
- **Rationale:** reuses the parser that ingest (004) will also use, so the two agree on what a JID is; our rules
  add the domain allow-list and limits the spec requires.
- **Alternatives:** our own JID parser (duplicates upstream behaviour that 004 depends on).

## R12. Structured storage events

- **Decision:** the store logs through the `*slog.Logger` passed to `Open`, with stable `event` names:
  `store.open`, `store.refused`, `store.preflight`, `store.upgrade`, `store.migrate`, `store.ready`, `store.close`
  and `store.io_error`. Each event carries `op_id` (a random 8-byte hex correlation ID per open/operation),
  `outcome` (`ok` / `refused` / `failed` / `indeterminate`), `code` (a stable reason code such as
  `schema_too_new`, `unsafe_artifact`, `not_database`, `disk_low`) and, where relevant, `schema`
  (`messages` / `device`), `from` and `to`. Paths are passed through a bounded, control-character-escaping
  formatter (≤ 512 bytes). Raw driver errors are never logged or returned verbatim: they are mapped to a code, and
  the SQLite result code is kept as `sqlite_code`.
- **UTC:** the process logger (in `cmd/whatsapp-mcp`) gains a `ReplaceAttr` that renders `time` in UTC. This is
  a one-line change that serves all features.
- **Rationale:** FR-048's fields without a new service. Message text, captions, keys and SQL parameters never
  reach a log call: the store's log helpers accept only the typed fields above, not free-form values.
- **Alternatives:** logging raw `err.Error()` from modernc (can echo SQL fragments and values).

## R13. CLI integration

- **Decision:** `stdio` and `serve` open the store in `runUntilStopped` after the lock is acquired, and register
  `store.Close` as a lifecycle stop step (it runs before the lock is released, within the 10 s shutdown limit).
  `pair` opens and closes the store around its current "not implemented" body, so a too-new or unsafe database is
  reported by `pair` as well. `status` and `version` do not open it. Open failures print the safe error to
  stderr and exit with the existing environment-error code.
- **Rationale:** FR-005 and the "Settled" decision on which commands open the store.

## R14. Dependencies added

| Module | Why | Pure Go | Notes |
| --- | --- | --- | --- |
| `modernc.org/sqlite v1.61.0` | the driver (S1, D2) | yes | proven by spike S1 |
| `go.mau.fi/whatsmeow` (pinned pseudo-version above) | device store `sqlstore`, `types.ParseJID` | yes | the client itself is used from 003; never import `dbutil/litestream` |
| `golang.org/x/text` | Unicode case folding and NFC (R7) | yes | Go team module |
| `modernc.org/libc` (direct import; already required by the driver) | the `libc.TLS` and varargs for `sqlite3_config` and `sqlite3_memory_highwater` (R16) | yes | must stay at the version `modernc.org/sqlite v1.61.0` requires (v1.79.0): the generated code is built against one libc, and a `go get -u` that moves it alone is a known break. Bump the two together |

Transitive additions (zerolog, protobuf, `go.mau.fi/util`, `filippo.io/edwards25519` and others) are pure Go. CI's
module verification and `govulncheck` run on the new graph. A reachable vulnerability blocks the feature, and an
unreachable one is recorded as an exception in `verification.md` (Principle V, X).

## R15. Contacts under either identity

- **Decision:** `resolveContact(ctx, s)` accepts any `NormalizePhone` form or a `@lid` JID and returns
  `{phone, phoneJID, lid}`.
  - **The map:** `whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL)` holds bare user parts and is shared by
    every device in the file (pinned `00-latest-schema.sql`, line 156). The store looks up user parts and adds
    `@lid` or `@s.whatsapp.net` itself.
  - **From a phone number:** look up `pn = <digits>`, to find the LID.
  - **From a LID:** look up `lid = <user part>`, to find the phone number. With no row, only the LID is known.
  - **Queries:** messages match a phone branch (`sender_phone = :phone`) and a LID branch (`sender_jid =
    :lidJID`). A branch whose value is empty is left out of the SQL, so a contact with one identity is one plain
    query. With both, the query is a `UNION ALL` of the two branches, each on its own index
    (`messages_sender_time`, `messages_sender_jid`, or with a chat filter `messages_chat_sender_phone`,
    `messages_chat_sender_jid`), with the other filters repeated in each branch and the `ORDER BY`, `LIMIT` and
    `OFFSET` on the compound. The branches never overlap: `sender_phone` is `''` for a `@lid` sender, and a
    `@lid` sender is never a phone sender. Each index delivers its branch in the listing order, so SQLite merges
    them (`MERGE (UNION ALL)`) and stops after `offset + size` rows, with no temporary b-tree and no set of row
    IDs. One query with `OR` is never used: SQLite plans it as a `MULTI-INDEX OR`, which collects the row IDs of
    every message of the contact and then sorts, or, with a chat filter, as a search on the chat alone that reads
    the whole chat (owner's decision, 2026-10-30).
  - **Contact chats:** the groups a contact wrote in are found per chat, `EXISTS` on
    `messages_chat_sender_phone` and `EXISTS` on `messages_chat_sender_jid`, while walking `chats_last_activity`:
    two index searches per chat, no `DISTINCT` and no sort over the contact's messages.
  - **Direct chats:** `jid IN (:phoneJID, :lidJID)`. Before 004 merges a LID, live one-to-one messages often land
    in the `@lid` chat while older history is in the phone chat, so `ContactChats` and `LastInteraction` use
    both. `DirectChat` returns one chat: the phone chat, else the `@lid` chat.
  - **Ordering:** the listing selects only the keys `(time, chat_jid, id)` under `ORDER BY ... LIMIT ...
    OFFSET ...`, then reads the full rows for the page. Without search text the key branches are answered from the
    covering indexes alone, so the rows skipped by the offset are never read from the table, and only the page's
    rows of up to 512 KiB are. Search text is matched against the folded columns of each row, so with it every
    skipped row of the contact is read: such a listing is linear by design, like any text search, and bounded by
    the 10 s deadline.

  `ContactChats`, `DirectChat`, `LastInteraction` and `MessageFilter.Sender` all go through it.
- **Merge:** `MergeLID` first runs `UPDATE messages SET sender_jid = :phoneJID, sender_phone = :phone WHERE
  sender_jid = :lidJID` across all chats, then merges the `@lid` chat if it exists. The `@lid` chat's name is
  offered to the phone chat only if it is saved or real: a fallback derived from the LID would name the person
  after an opaque number (owner's decision, second design review). Rewriting makes the stored data
  canonical. Resolving at query time covers the time before 004 calls the merge, and LIDs whose mapping arrives
  while the process runs. The update is one indexed statement bounded by the 10 s operation deadline.
- **Chat and sender together:** two more indexes, `(chat_jid, sender_phone, time DESC, id DESC)` and
  `(chat_jid, sender_jid, time DESC, id DESC)`, make that combination an index search, one per branch of the
  `UNION ALL` when the contact has both identities; without them it reads every row of the chat (0.37 s for 300,000 messages in the sixth review's measurement). They add two index writes per
  stored message, which the owner accepted.
- **Rationale:** M6 requires every contact tool to take a LID, and a person who writes in groups under a LID before
  their number is known must still be found by that number. Doing both keeps the queries right whether or not
  the merge has run yet.
- **Alternatives:**
  - Resolving at query time only, with no rewrite: stored senders would stay inconsistent forever, and every
    query would depend on the map.
  - Rewriting only: queries made between a mapping's arrival and 004's merge would miss messages.
  - Narrowing M6: rejected by the owner (spec clarifications, design review session).

## R16. SQLite's own memory

- **Decision:** both DSNs set `_pragma=hard_heap_limit(268435456)`, the approved 256 MiB. The limit is
  process-wide in SQLite and only ever lowered by the pragma, so setting it on every connection is harmless.
  `mapDriverError` maps `SQLITE_NOMEM` to `ErrResourceLimit` with code `memory`, logged as `store.limit`.
- **Memory statistics must be switched on.** modernc.org/sqlite v1.61.0 is built with
  `-DSQLITE_DEFAULT_MEMSTATUS=0`, and SQLite checks the hard limit only in `mallocWithAlarm`, which runs only with
  statistics on (`lib/sqlite_g_000000000001ffff.go`:9445). Without them the pragma is accepted and nothing is
  limited; the third design review showed a sort far over a 4 MiB limit succeeding with a high-water mark of 0.
  So a package `init` in `internal/store/sqlite.go` calls `sqlite3_config(SQLITE_CONFIG_MEMSTATUS, 1)` through
  `modernc.org/sqlite/lib`, which works only before SQLite initialises, that is before any connection exists. The
  result code is kept: if it is not `SQLITE_OK`, `Open` refuses with `ErrIO` code `memory_limit_unavailable`
  rather than run without the limit. Statistics add a process-wide mutex to every SQLite allocation; the owner
  accepted that, and the SC-004 benchmark runs with them on.
- **Why it is needed:** modernc's SQLite allocates through `modernc.org/memory`, outside the Go heap, so neither
  Go's runtime nor the 32 MiB result budget sees it. With `temp_store(MEMORY)` a sort for `ORDER BY` holds every
  row it sorts in that memory. A contact with many large messages, listed at a deep offset, could otherwise need
  hundreds of megabytes before the first row is counted (second design review).
- **Keeping normal queries far below it:** no message listing sorts. Every listing walks an index in its own
  order, sender listings merge two such walks (R15), the page's keys come from covering indexes, and only the
  page's full rows are read. The page cache stays at SQLite's default of 2,000 KiB per connection, about 10 MiB for
  the 5 connections.
- **Measurement:** tests read SQLite's high-water mark with `sqlite3_memory_highwater` (through
  `modernc.org/sqlite/lib` and a `libc.TLS`) before and after each boundary case, next to Go's heap growth. It
  reads 0 unless statistics are on, so a test first asserts that an allocation over a lowered limit fails.
- **One budget for the process:** the allocation that crosses the limit fails, whichever connection makes it, so
  an ingest write or a whatsmeow write could fail while a reader holds the memory. The store keeps its own queries
  far below the limit:
  - **At most 4 queries run SQL at once,** one per reader connection, however many operations are admitted.
  - **Message queries hold no per-row state.** A sender listing at offset 99,800 for a contact with 300,000
    messages under both identities grew SQLite's memory by about 7 KB, the same as at offset 0 (measured
    2026-10-30). Neither the offset nor the size of a contact's history adds a term; the earlier key sorts (up to
    77 MB for a deep listing) and row-ID sets (24 B per message of the contact) are gone with the `OR` form.
  - **The one remaining temporary b-tree** is `SearchContacts`' grouping and order, which holds contact rows and is
    bounded by the device store's contact count.
  - **The page cache** is about 10 MiB.

  So the store's reads need about 10 MiB of page cache plus a few hundred kilobytes, however large one contact's
  history is, and the earlier ceiling of about 1 million messages from one contact (fourth design review) no
  longer applies (owner's decision, 2026-10-30). T047 checks that a listing's memory does not grow between 50,000
  and 200,000 messages from one contact.
- **`LastInteraction` reads O(1):** it is the newest of up to four `LIMIT 1` queries, each on one index (the
  latest message of the phone chat and of the `@lid` chat on `messages_chat_time`, the newest message by
  `sender_phone` and by `sender_jid` on the two sender indexes). One query over the `OR` would collect the
  contact's whole history first (1.35 s and 26 MB for a million messages in the review's measurement). Every
  message the account sent carries the account's own JID as sender, so for any other contact the sender queries
  match only what that contact sent, in their direct chat or a group. For the account's own number or LID (from
  `whatsmeow_device`), the sender queries are skipped and only the own direct chat counts (FR-033). Only non-empty
  values are compared: `whatsmeow_device.lid` can be `NULL` or `''`, and an empty own LID must not equal the empty
  LID of an unmapped contact, which would treat that contact as the account. The sender
  indexes end in `chat_jid DESC, id DESC`, so each query's `ORDER BY time DESC, chat_jid DESC, id DESC LIMIT 1` reads
  one index entry with no sort, and the four candidates are compared in Go by the same three keys.
- **Alternatives:** `soft_heap_limit` alone (advisory; SQLite may exceed it); no limit (the review's finding);
  temporary files on disk (`temp_store(FILE)`), which puts sensitive rows in the system temp directory (R1).
