# Research: Storage: one SQLite file and migrations

Phase 0 of the [plan](plan.md). Each entry records a decision, why it was made, and what else was considered. Sources
were read on 2026-10-28 from the pinned modules: `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`,
`go.mau.fi/util v0.11.0` (its `dbutil`) and `modernc.org/sqlite v1.61.0`, plus `docs/spikes.md` S1 and the code on
`main` (`internal/private`, `internal/config`, `cmd/whatsapp-mcp`).

## R1. Driver, DSN and pragmas

- **Decision:** `modernc.org/sqlite v1.61.0`, driver name `sqlite`. The read-write DSN is
  `file:<path>?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=journal_size_limit(67108864)&_txlock=immediate`.
  The read pool adds `_pragma=query_only(1)`.
- **Rationale:** the S1 spike proved this DSN with whatsmeow's `sqlstore` (dialect `"sqlite"`), including 16×100
  concurrent writes without errors. `_txlock=immediate` takes the write lock at `BEGIN`, so writers wait in the busy
  handler instead of failing on lock upgrade. `synchronous(NORMAL)` is the documented safe setting for WAL: a
  committed transaction survives a process crash (SC-007); only an OS crash or power loss can lose the last
  commits, never corrupt the file. `temp_store(MEMORY)` keeps SQLite's temporary files (sorts, statement
  journals) out of the system temp directory, which is not private (FR-040); their size is bounded by the
  result limits. `journal_size_limit` implements the approved 64 MiB WAL truncation.
- **Alternatives:** `mattn/go-sqlite3` (needs CGO, forbidden by Principle II); `synchronous(FULL)` (slower on every
  commit for a guarantee the spec does not require); a private `SQLITE_TMPDIR` inside the data directory (works,
  but adds another artifact to check, protect and clean up).

## R2. Connection pools

- **Decision:** two `*sql.DB` handles on the same file. The **writer** has `SetMaxOpenConns(1)` and serves all
  writes, both ours and whatsmeow's device store. The **reader** has `SetMaxOpenConns(4)`, `query_only(1)`, and
  serves our queries; it is also given to dbutil as `ReadOnlyDB`. Idle connections are kept open (no lifetime
  limit), so no connection is reopened while the store runs.
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
     without a main file is refused ("orphaned journal file").
  2. If the main file is missing or has length 0, there is nothing to preflight: it is a fresh database.
  3. Otherwise read its first 100 bytes in Go and require the SQLite magic `SQLite format 3\0` and a valid page
     size. Anything else is "not a usable database" (US1 scenario 3) without SQLite ever opening the file.
  4. Open one connection with `file:<path>?mode=ro&readonly_shm=1&_pragma=query_only(1)` (no other pragmas) and
     read: whether `mcp_version`, `whatsmeow_version` and any other `mcp_`/`whatsmeow_` tables exist; the message
     version; the device `version` and `compat`. Close it.
  5. Refuse a newer message version, a device `compat` above the bundled table, a version table with zero or
     more than one row, or schema tables present without their version table.
- **Rationale:** `mode=ro` never writes the main file. `readonly_shm=1` makes SQLite open an existing `-shm`
  read-only and, if there is none, read the WAL through private heap memory instead of creating `-shm`. Committed
  transactions still in the WAL are therefore visible to the preflight (FR-010) and no sidecar is created. The
  SC-006 test proves it per platform: it snapshots name, size, modification time and SHA-256 of every file in the
  directory before and after a refused open, for each refusal (newer message schema, newer device schema, bad
  version metadata, not a database), with and without a WAL holding committed data.
- **Risk and fallback:** if the SC-006 test shows that `readonly_shm` is not honoured on one platform, the
  preflight reads the main file and WAL into memory and opens them with `modernc.org/sqlite`'s in-memory
  deserialisation instead. This is slower, but bounded by the startup budget. Tasks schedule the SC-006 test
  first so that this is settled before the rest of the store is built on it.
- **Integrity:** the preflight does not run `PRAGMA integrity_check` on every start, which would cost seconds to
  minutes on a large history. Corruption in the header or schema pages is caught by steps 3–4. Corruption deeper
  in the file surfaces as `SQLITE_CORRUPT` on access and fails that operation safely. Full `integrity_check` and
  `foreign_key_check` are part of the restore procedure (FR-047).
- **Alternatives:** `immutable=1` (ignores the WAL, so it would miss committed state); copying the files to a
  temporary directory (creates new sensitive artifacts).

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
    where the existing rule only forbids group and other *write*. The Windows rule is unchanged.
- **Rationale:** the data directory is owned by this user and nobody else can write it (`private` checks the
  parent), so other users cannot create, rename or swap entries inside it. That is the defence against
  check/open races (FR-040); the handle checks catch anything already there. The approach reuses the
  constitution's mandated package rather than a second implementation.
- **Path forms (approved profile):** the data directory path is rejected before use if it is a UNC or network
  path (`\\server\...`), a device path (`\\?\`, `\\.\`) or contains an alternate data stream (`:` after the drive
  letter) on Windows. Unix network filesystems are not detected (no portable signal); they are documented as
  unsupported in `OPERATIONS.md`.
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

Transitive additions (zerolog, protobuf, `go.mau.fi/util`, `filippo.io/edwards25519` and others) are pure Go. CI's
module verification and `govulncheck` run on the new graph. A reachable vulnerability blocks the feature, and an
unreachable one is recorded as an exception in `verification.md` (Principle V, X).
