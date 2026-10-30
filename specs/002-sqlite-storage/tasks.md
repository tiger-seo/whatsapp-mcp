---

description: "Task list for feature 002: storage in one SQLite file with migrations"
---

# Tasks: Storage: one SQLite file and migrations

**Input**: Design documents from `specs/002-sqlite-storage/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md),
[contracts/store-api.md](contracts/store-api.md), [contracts/startup-and-events.md](contracts/startup-and-events.md),
[quickstart.md](quickstart.md)

**Tests**: required. FR-039 and constitution Principles VI and X demand store tests against SQLite in a temporary
directory, plus abuse cases as negative tests. Within each story, test tasks come first and must fail before the
implementation tasks make them pass.

**Organization**: tasks are grouped by user story. P1 stories come first in spec order (US1–US4, then US7, which
hardens all of them); the P2 stories US5 and US6 follow.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: the user story from spec.md (US1–US7)

## Conventions for every task

- All code is in the module root, the package is `internal/store` unless stated otherwise, and every build and
  test runs with `CGO_ENABLED=0`.
- Tests use `t.TempDir()` and synthetic data only. Never pair, connect or use a real account.
- Code comments never cite FR-, SC-, US-, D- or F- identifiers (constitution).
- Files are LF. After a scripted batch, `git ls-files --eol` must show no `w/crlf`.
- Limits are the constants from T005; tests reference those constants, not literals.
- Record evidence (command, date, result, platform) in `specs/002-sqlite-storage/verification.md` whenever a task
  says "record".

---

## Parked findings

Parked by the owner on 2026-10-28 after seven `BLOCK` rounds of the design review (constitution v1.3.0,
Development Workflow). They come from the seventh design review of commit `8d38457`. None is a high-severity
data-loss, security or real-account finding. Each must be resolved, and a review of that part must clear it,
before the tasks it blocks start, and before the code review at the latest.

All six were resolved in the design on 2026-10-30, with the owner's decision on P1 (spec.md, Clarifications, session
2026-10-30). The tasks they blocked start once the design review of that resolution, the first row in the "Owner
reviews" table of `verification.md` after the parking, is `PASS` (or `CONCERNS` accepted by the owner).

| ID | Finding (seventh design review, `8d38457`) | Blocks | Resolution (2026-10-30) |
| --- | --- | --- | --- |
| P1 | [high] A listing filtered by chat and by a sender with both identities, written as `chat_jid = ? AND (sender_phone = ? OR sender_jid = ?)`, plans as `SEARCH messages_chat_time (chat_jid=?)` and reads the whole chat (0.4–0.9 s at 300,000 messages). The distributed form `(chat_jid = ? AND sender_phone = ?) OR (chat_jid = ? AND sender_jid = ?)` reaches the two chat-and-sender indexes, and a `UNION ALL` of the two single-index branches merges them with no sort. T044 must name the shape and data-model.md's expected-plan table must match it. Owner decision still open: whether every sender query (not only chat and sender) uses the `UNION ALL` form, which would remove the per-contact row-ID sets, the key sort and possibly the deep-listing slots and the 1-million-message ceiling of research R16. | Phase 6 (T039–T046) | Owner's decision: every sender query is a `UNION ALL` of a phone branch and a LID branch, each on its own index (research R15, data-model.md § Indexes). T044 names the shape for the sender listing and for chat and sender; T043 finds a contact's groups with two `EXISTS` probes per chat; the expected-plan table now lists no temporary b-tree for any message query, and `TestQueryPlans` (T046) rejects `MULTI-INDEX OR`. The row-ID sets, the key sorts, the deep-listing slots and the 1-million-message ceiling are gone (research R9, R16). |
| P2 | [high] `TestQueryScale`'s 3× rule fails by construction: `SearchContacts` by name, `ContactChats` and the sender listings read every row they match by design and grow about 10×; the offset-99,800 listing is empty at a tenth; and Windows timing gives medians of 0 s. | T046 | T046 compares pages, not times. Constant-cost cases have their own rule; `SearchContacts`, `ContactChats` and the deep sender listing are compared by pages per row they must walk; the deep listing runs at the same relative depth (offset 9,800 at a tenth, 99,800 at full); times are recorded only. |
| P3 | [medium] The scale test can count pages instead of time: modernc exports `sqlite.DBStatus`, reachable through `(*sql.Conn).Raw`, whose cache hit + miss delta counts the pages a query touched (1.0–1.3× tenth to full for constant-cost queries, about 10× for linear ones). The linear queries need their own rule, a bound per matched row. | T046 | Adopted in T046: the `DBStatusCacheHit` + `DBStatusCacheMiss` delta on one reader connection (checked in `modernc.org/sqlite` v1.61.0, `dbstatus.go`), with a per-row rule for the linear cases. |
| P4 | [medium] T047's slope depends on how the seeded messages split between phone and LID: an even split measures about 12 B a message, all under the phone (the state after a merge) about 24 B. Seed under the phone, or take the larger of both splits. | T047 | With the `UNION ALL` form no query holds a per-message term, so T047 asserts that a listing's memory does not grow with the contact's history, and runs that check with both splits (even, and all under the phone). |
| P5 | [low] Own-account detection compares with `whatsmeow_device.lid`, which can be NULL or empty; an empty own LID would equal the empty LID of an unmapped contact and treat that contact as the account. Compare only non-empty values, and add the case to T040. | T040, T044 | Own number and LID are compared only when non-empty (data-model.md § Derived values "Current device", research R16, T044); T040 tests a `NULL` and an empty own LID with an unmapped contact. |
| P6 | [low] The 64-waiter cap is shared with callers waiting for a deep slot; if the cap is checked before trying the admission semaphore, an ingest call gets `ErrBusy` with 64 deep listings waiting although admission slots are free. Specify try-acquire before the cap check, and test `StoreMessage` with 64 deep waiters. | T011 (the `enterDeep` part only), T048 | Obsolete: the deep-listing slots are removed (research R9), so no other kind of waiter shares the cap. T011 removes `enterDeep` and its constants and tests. |

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: dependencies, package skeleton, evidence file.

- [X] T001 Add the dependencies to `go.mod`/`go.sum`:
  - `go get modernc.org/sqlite@v1.61.0 go.mau.fi/whatsmeow@v0.0.0-20261027104441-1eec1136a50b golang.org/x/text@latest`
    (keep the resolved x/text version);
  - check that `go.mau.fi/util` resolves to v0.11.0;
  - `go mod verify`.

  Never import `go.mau.fi/util/dbutil/litestream`. The requirements stay in `go.mod` once T005–T009 import them;
  run `go mod tidy` after T009.
- [X] T002 [P] Create `internal/store/doc.go`. Its package comment states:
  - the store owns `<data dir>/whatsapp.db`, and no other package issues database queries;
  - `Device()` is the only shared handle;
  - the store is safe for concurrent use.
- [X] T003 [P] Extend `specs/002-sqlite-storage/verification.md`, which already holds the "Owner reviews" table.
  Keep that table, and add these sections above it:
  - "Environment";
  - "Gate results (committed view, device upgrade transactions)";
  - "Automated tests per OS";
  - "Benchmark (SC-004)";
  - "Cross-identity evidence (SC-008)";
  - "Backup/restore rehearsal (SC-010)";
  - "govulncheck and exceptions";
  - "Limitations / not verified".
- [X] T004 [P] Read `.github/workflows/ci.yml`. Confirm that `go test ./...` runs with `CGO_ENABLED=0` on Linux,
  Windows and macOS, that `govulncheck` runs, and that the five cross-builds are present. If `go test` has no
  `-timeout`, add `-timeout 15m`, because the store tests include concurrency and crash tests. Run the test job
  on all three OSes for every push, not only for pull requests and `main`, so that T014 and the other platform
  gates can pass on the feature branch before a pull request exists.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the shared building blocks every story uses, and the two gates the design depends on.

**⚠️ CRITICAL**: no user story work starts until this phase is complete. The T014 gate must pass on Windows,
Linux and macOS before Phase 3; if it fails on one, fix the committed view, because there is no fallback (research
R4, spike S2).

- [X] T005 [P] Create `internal/store/limits.go`: unexported constants for the approved profile, verbatim.
  - **Field sizes:** "JID ≤ 256 B; message ID ≤ 256 B; chat and contact names ≤ 1 KiB; text ≤ 256 KiB; caption
    ≤ 256 KiB; search text ≤ 1 KiB; URL ≤ 4 KiB; direct path ≤ 4 KiB; MIME type ≤ 255 B; original file name
    ≤ 1 KiB".
  - **Media and times:** media key and both hashes "exactly 32 B when present"; "file length 0 to 4 GiB"; times
    "from 2000-01-01T00:00:00Z (inclusive) to 2100-01-01T00:00:00Z (exclusive)".
  - **Batches and results:** "Batch ≤ 1,000 messages and ≤ 16 MiB aggregate field bytes; list size 1–200; offset
    0–100,000; context ≤ 20 rows per side and ≤ 2,000 context rows per call in total; ≤ 32 MiB of result bytes
    per call; an ambiguous-ID error lists ≤ 100 chats".
  - **Work and waits:** "1 write connection and 4 read connections; ≤ 16 active and ≤ 64 waiting operations;
    admission wait ≤ 5 s; busy timeout 5 s; each query or write ≤ 10 s; resources released within 1 s of
    cancellation; startup schema upgrades ≤ 10 min". `Close` gives running operations a 2 s grace period before
    it cancels them (research R9). SQLite's hard heap limit is 256 MiB (research R16). The bound
    of 2 deep sender-filtered listings was withdrawn on 2026-10-30; T011 removes its constants.
  - **Disk:** "Free-space reserve 256 MiB … measurement at most 30 s old; … writes resume when ≥ 512 MiB is free";
    WAL limit 64 MiB.
  - Every limit is defined in bytes, with KiB = 1024.
- [X] T006 [P] Create `internal/store/errors.go` with the error set of `contracts/store-api.md` § Errors:
  - sentinels `ErrInvalid`, `ErrNotFound`, `ErrNotMedia`, `ErrResourceLimit`, `ErrBusy`, `ErrClosed`, `ErrDiskLow`,
    `ErrIndeterminate`, `ErrSchemaTooNew`, `ErrSchemaInvalid`, `ErrUnsafeStorage`, `ErrNotDatabase`, `ErrIO`;
  - types `ValidationError{Field, Reason}`, `NotFoundError{ChatJID, ID}`, `AmbiguousIDError{ChatJIDs}`,
    `SchemaError{Schema, Observed, Supported}`, `MigrationError{Schema, Step, Completed}`,
    `StorageError{Artifact, Code, Fix}`, each with `Is`/`Unwrap` to its sentinel;
  - `mapDriverError(err) error`, which converts modernc/SQLite errors to `ErrIO` with a code (`disk_full`,
    `readonly`, `corrupt`, `busy`, `io`), and `SQLITE_NOMEM` to `ErrResourceLimit` with code `memory`. It keeps
    the SQLite result code and never the driver's message text.

  Test the `errors.Is`/`errors.As` mapping and that `Error()` never contains the inputs it was given beyond
  identifiers, in `internal/store/errors_test.go`.
- [X] T007 [P] Create `internal/store/events.go`:
  - a typed event logger over `*slog.Logger` with methods for the events in
    `contracts/startup-and-events.md` (`open`, `refused`, `preflight`, `upgrade`, `migrate`, `ready`, `close`,
    `ioError`, `limit`);
  - each method accepts only typed fields: op ID, outcome, code, schema, versions, artifact, limit and the
    `suppressed` count;
  - `newOpID()` returns 8 random bytes as hex (crypto/rand);
  - `safePath(s string) string` bounds a path to 512 bytes and escapes control characters and invalid UTF-8;
  - `store.limit` is rate-limited to one event per limit per 10 s, with a suppressed counter.

  Also create `internal/store/walog.go`: `newWALog(logger *slog.Logger) waLog.Logger`, which implements
  `Debugf`, `Infof`, `Warnf`, `Errorf` and `Sub`.
  - Each call logs at the matching `slog` level with `event=whatsmeow.log`, `module=<Sub path>` and
    `msg=<format string>`.
  - The format arguments are **never** formatted or logged, because they can carry JIDs, keys or upstream error
    text.

  Test in `internal/store/events_test.go` and `internal/store/walog_test.go`, with a capturing `slog.Handler`
  helper in `internal/store/helpers_test.go`. The `walog` test checks that an argument holding a secret marker
  never appears in the output, and that `Sub` nests module paths.
- [X] T008 [P] Create `internal/store/fold.go`:
  - `fold(s string) string` = `cases.Fold().String(norm.NFC.String(s))`;
  - `registerFold()` registers it once (`sync.Once`) as the deterministic SQL function `mcp_fold(text)` via
    `sqlite.RegisterDeterministicScalarFunction`.

  Also create `internal/store/sqlite.go` (research R16): a package `init` that calls
  `sqlite3_config(SQLITE_CONFIG_MEMSTATUS, 1)` through `modernc.org/sqlite/lib` with its own `libc.TLS`, before
  any connection exists, and keeps the result code for `Open` (T024), which refuses with `ErrIO` code
  `memory_limit_unavailable` when it is not `SQLITE_OK`. Test in `internal/store/sqlite_test.go`, in a child
  process re-executed as T036 does, so that the lowered limit cannot leak into other tests: with
  `sqlite3_hard_heap_limit64` set to 4 MiB, a query sorting 2,000 rows of 64 KiB fails with `SQLITE_NOMEM`, and
  `sqlite3_memory_highwater` reads more than 0.

  `internal/store/fold_test.go` adds a golden table: "ПРИВІТ"→"привіт", "ÉTÉ"→"été", composed é = decomposed é,
  "ß"→"ss", Greek final sigma, "ete"≠fold("été"), `%`/`_` unchanged. A change in x/text that alters any row fails
  the test.
- [X] T009 [P] Create `internal/store/validate.go`. It covers the rules from data-model.md § Validation and
  research R11, using `types.ParseJID` from `go.mau.fi/whatsmeow/types`:
  - `parseChatJID`: server `s.whatsapp.net`, `g.us` or `lid`; non-empty user; ≤ 256 B;
  - `parseSenderJID`: server `s.whatsapp.net` or `lid`; the device part is stripped, so
    `491511234567:12@s.whatsapp.net` becomes `491511234567@s.whatsapp.net`;
  - `validateID`: 1–256 B, valid UTF-8, no control characters;
  - `validateTime`: in [2000-01-01, 2100-01-01);
  - `validateMedia`: type in `''`, `image`, `video`, `audio`, `document`; key and hashes exactly 32 B when
    present; file length 0 to 4 GiB; URL and direct path ≤ 4 KiB; MIME type ≤ 255 B; file name ≤ 1 KiB;
  - text and caption ≤ 256 KiB, names ≤ 1 KiB, search text ≤ 1 KiB;
  - exported `NormalizePhone(s string) (string, error)`: accepts an optional `+`, spaces, dashes, dots and
    parentheses around 1–20 digits, or an `@s.whatsapp.net` JID, and returns the digits;
  - `parseContact(s) (phone, lid string, err error)`: a `NormalizePhone` form gives the phone digits, a `@lid` JID
    (device part stripped) gives the LID, and anything else is a `*ValidationError` on field `contact`. The device
    store lookup that completes the identity is T045's `resolveContact`.

  Every rejection is a `*ValidationError` naming the field. In `internal/store/validate_test.go`:
  - table tests at each limit and one byte over, for every rule above;
  - "+49 151 1234567", "49-151-1234567" and "491511234567@s.whatsapp.net" → "491511234567";
  - `FuzzNormalizePhone` and `FuzzValidateMessage` (no panic; accepted values round-trip; rejected values name a
    field).
- [X] T010 [P] Add `CheckFile(path string) (FileID, error)` to `internal/private/private.go`, with implementations
  in `private_unix.go`, `private_darwin.go` and `private_windows.go`:
  - it opens the existing path without following links (as `OpenFile` does, read-only);
  - it runs `checkFile` on the handle;
  - on Unix it also requires no group or other permission bits (`perm&0o077 == 0`), with the fix
    "run: chmod 600 <path>";
  - on macOS it also refuses any extended ACL entry, read with `unix.Getattrlist` (`ATTR_CMN_EXTENDED_SECURITY`),
    with the fix "run: chmod -N <path>". The data-directory check in `private` gets the same macOS rule;
  - on Windows the existing DACL rule applies;
  - `FileID` is the file's identity from the same handle: device and inode on Unix, volume serial number and file
    index (`GetFileInformationByHandle`) on Windows. It is comparable with `==`;
  - exported `FileIDOf(f *os.File) (FileID, error)` returns the identity of a file the caller already opened, for
    the preflight's own handles (T023).

  `OpenFile` behaviour is unchanged. Tests in `internal/private/private_unix_test.go`, `private_darwin_test.go`
  and `private_windows_test.go`:
  - 0600 passes; 0640 and 0644 fail with the fix text; symlink, hard link, FIFO or directory fail;
  - on macOS, a 0600 file and a 0700 directory with `chmod +a "everyone allow read"` fail with the fix text;
  - on Windows an extra ACE for Everyone fails, and a junction or reparse point fails;
  - two calls on the same file return equal `FileID`s, and a file renamed over it gives a different one;
  - `FileIDOf` on an open handle equals `CheckFile` on its path, on Unix and Windows.
- [X] T011 [P] Create `internal/store/admit.go`: `gate.enter(ctx) (release func(), opCtx context.Context, err error)`.
  - A semaphore of 16 active slots, with an atomic count of waiters.
  - If 64 are already waiting, return `ErrBusy` immediately; otherwise wait at most 5 s or until the caller's
    deadline, whichever is earlier.
  - `opCtx` is the caller's context with a 10 s timeout, also cancelled when the gate's store-wide context is.
  - No second semaphore. Remove the deep-listing slot of the earlier design: `gate.enterDeep`, the `deep`
    semaphore, `deepSlots` and `deepPageEnd` in `limits.go`, and their tests (`TestGateDeepSlots`,
    `TestGateDeepWaitersDoNotBlockAdmission` and the `enterDeep` case of the close test). Sender listings no longer
    sort, so they need no bound of their own (research R9, R15).
  - `gate.close(ctx) error` (research R9): mark the gate closed, so new and waiting callers get `ErrClosed`; wait
    up to 2 s for active operations; cancel the store-wide context; wait up to 1 s more. `ctx` can shorten both
    waits. It returns once every slot is free or the waits are over.
  - `commitOutcome(ctx, err) error` maps a commit error after cancellation, or `driver.ErrBadConn` during
    commit, to `ErrIndeterminate`.

  Tests in `internal/store/admit_test.go`:
  - 16 run and the 17th waits;
  - with 16 running and 64 waiting, the 81st gets `ErrBusy` at once;
  - the wait gives up at 5 s (inject a clock) or at the caller's deadline;
  - cancellation releases the slot within 1 s;
  - `close` with 16 operations that block until their `opCtx` is cancelled returns within 3 s; the waiting
    callers and every later `enter` get `ErrClosed`;
  - no goroutine leaks (count before and after).
- [X] T012 [P] Create `internal/store/disk.go`, `disk_unix.go` (`unix.Statfs`, `Bavail*uint64(Bsize)`) and
  `disk_windows.go` (`windows.GetDiskFreeSpaceEx`).
  - `diskGuard{free func(dir string) (uint64, error); now func() time.Time}`: cached for at most 30 s.
  - `check()` returns `ErrDiskLow` below 256 MiB. Once refused, writes stay refused until ≥ 512 MiB is free.

  Tests in `internal/store/disk_test.go` with an injected `free` and clock: 256 MiB exactly passes and 1 byte less
  fails; resumption needs 512 MiB; the cache is refreshed after 30 s. A smoke test calls the real `free` on
  `t.TempDir()`.
- [X] T013 [P] In `cmd/whatsapp-mcp/commands.go` `startup`, add a `slog.HandlerOptions.ReplaceAttr` that renders
  the `time` attribute in UTC, keeping the text handler and level. Test in `cmd/whatsapp-mcp/cli_test.go` that a
  log line's time ends in `Z`.
- [X] T014 **Gate**: the committed view (research R4), test first.
  1. Create `internal/store/gate_test.go` with `TestCommittedViewChangesNothing`, which must fail before step 2.
     Build each fixture from a source database created with modernc in WAL mode with
     `PRAGMA wal_autocheckpoint=0`: rows checkpointed into the main file, then rows committed one per transaction
     only in `-wal`. A crash state is a copy taken while the source connection is still open. The fixtures are:
     - clean: main only, after a clean close;
     - main + empty `-wal`;
     - main + empty `-wal` + empty `-shm`;
     - main + `-wal`;
     - main + `-wal` + `-shm`;
     - main + `-wal` + zeroed `-shm`;
     - main + `-wal` with the last frame cut in half (the last commit is lost);
     - main + `-wal` restarted after a `PRAGMA wal_checkpoint(PASSIVE)`, with stale frames behind the new ones.

     For each fixture:
     - Snapshot every file in its directory: name, size, modification time and SHA-256 (add the helper `snapshotDir` to
       `internal/store/helpers_test.go`).
     - Open through `openCommittedView` and read `count(*)` and `max(id)`. Close, then snapshot again.
     - Assert that both snapshots are identical and that no file was created or removed.
     - Assert that the result equals SQLite's own (the differential check): open a throwaway copy of the same
       files read-write with the plain driver, and read the same values.

     Two more fixtures need only the parser, with no SQLite open:
     - a synthetic big-endian WAL (magic `0x377f0683`, checksums computed in the test) must give the same page
       index as its little-endian twin;
     - a WAL with format version ≠ 3007000, and one whose page size differs from the main file's, are refused
       with `ErrNotDatabase`.
  2. Create `internal/store/walview.go`:
     - `openCommittedView(path) (dsn string, release func() error, err error)`. It opens the main file and `-wal`
       with `os.Open` (read-only) and scans the WAL by the rules in research R4.
     - The scan builds `map[uint32]int64` from page number to the frame's WAL offset. It streams the WAL rather
       than reading it whole, and computes every offset in `int64`.
     - It registers an `fs.FS` exposing only `db` with `vfs.New` and returns `file:db?mode=ro&vfs=<name>`.
     - `release` closes the files and calls `(*vfs.FS).Close`. Callers close every connection first.
     - The `fs.File` implements `Seek` (only `io.SeekStart`) and `Read`, as modernc's VFS uses them. Reads take
       pages from the index or the main file and present bytes 18–19 as `1`. `Stat().Size()` is the committed
       size.
  3. Add `FuzzWALScan` in `internal/store/walview_test.go`. The seeds are the gate fixtures' WAL files. On any
     input, the scan returns without panic. Every indexed offset plus the page size lies within the WAL file. The
     index holds no more entries than the WAL has whole frames.

  Run the gate on Windows, Linux and macOS (CI) and record the results under "Gate results". Do not start T023
  until it passes on all three.
- [X] T015 [P] **Gate**: create `internal/store/device_upgrade_test.go` with
  `TestDeviceUpgradeStepsAreTransactional`:
  - locate the pinned whatsmeow module directory with
    `exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "go.mau.fi/whatsmeow")`, and skip with a clear message if
    `go` is unavailable;
  - assert that no `store/sqlstore/upgrades/*.sql` file has a `-- transaction:` header with a value other than
    `on`;
  - assert that the highest target version equals `len(upgrades.Table)` (16).

  Record the result.
- [X] T016 Extend `internal/store/helpers_test.go` with:
  - `newDataDir(t)`, a private directory via `private.MkdirAll`;
  - `openTest(t, dir)`, which opens a store with a capturing logger;
  - `seedDevice(t, st)`, which saves a synthetic `store.Device` with random keys and a made-up JID through
    `st.Device().NewDevice()` + `Save`;
  - `seedContacts(t, st, ...)`, which uses the device's `Contacts`/`LIDs` stores
    (`PutContactName`, `PutPushName`, `PutBusinessName`, `LIDs.PutLIDMapping`). Note the upstream argument order
    quirk from `docs/spikes.md`: `PutContactName` takes `(firstName, fullName)`;
  - `msg(chat, id, sender, t, opts...)`, a builder for messages.

**Checkpoint**: the foundation is ready and both gates are recorded. User stories can start.

---

## Phase 3: User Story 1 - One database file, created on first start (Priority: P1) 🎯 MVP

**Goal**: `stdio`, `serve` and `pair` create or open exactly one private `<data dir>/whatsapp.db` holding both
schemas, after the lock, and close it on shutdown.

**Independent Test**: start a long-running command on an empty data directory and stop it. Exactly one database
file (plus its sidecars) exists and holds both schemas, and all artifacts are private. A restart recreates nothing.

### Tests for User Story 1 ⚠️

- [X] T017 [P] [US1] `internal/store/files_test.go`:
  - **Path forms:** on Windows, `\\server\share\x`, `\\?\C:\x`, `\\.\C:\x` and `C:\x:stream` are rejected with
    `ErrUnsafeStorage` before any file is touched.
  - **Resolved drives (Windows):** a `subst` drive pointing at a private local directory is accepted, and its
    resolved path is the one checked. A drive mapped with `net use` to `\\localhost\<share>` of a temp directory is
    rejected as a network path. If the CI runner cannot create the share or the mapping, the subtest skips with a
    clear message, and `verification.md` records it as not verified there; it is then run once by hand.
  - **Swap between check and open:** a test hook in `Open`, after `checkArtifacts` and before the writer opens,
    renames a different private database over `whatsapp.db`. `Open` fails with `ErrUnsafeStorage` (code
    `unsafe_artifact`); no device upgrade or migration ran, and the swapped-in file's schema versions are
    unchanged.
  - **Artifacts:** `whatsapp.db`, `-wal`, `-shm` or `-journal` as a symlink (and as a junction or reparse point
    on Windows), a hard link, a directory or a FIFO (Unix) is refused before mutation, and a sentinel target
    outside the directory is unchanged (SHA-256).
  - **Orphans:** a `-wal`/`-shm`/`-journal` without `whatsapp.db`, or next to a 0-byte `whatsapp.db`, is refused
    ("orphaned journal file"), and every file is byte-for-byte unchanged afterwards (`snapshotDir`): in
    particular the `-wal` is not deleted.
  - **Rollback journal:** a `-journal` (empty or not) next to a valid `whatsapp.db` is refused ("rollback
    journal left behind") with the directory unchanged.
  - **Refusal text:** each refusal above names every file involved (bounded and escaped) and the "Recovering from
    a refused start" section of `OPERATIONS.md`.
  - **Created file swapped:** a test hook after `createDB` and before the writer opens renames another file over
    the new `whatsapp.db`; `Open` fails with `unsafe_artifact`.
  - **Permissions:** an existing `whatsapp.db` with mode 0644 is refused with the chmod fix and left at 0644.
- [X] T018 [P] [US1] `internal/store/open_test.go`:
  - **Fresh directory:** opening creates `whatsapp.db`; `whatsmeow_version` = 16; `mcp_version` = latest; the
    file is 0600 on Unix and passes `private.CheckFile` on all OSes; the same holds for `-wal` and `-shm` while
    open.
  - **Reopen:** reopening an up-to-date database changes no version and keeps seeded chats and messages.
  - **Not a database:** a `whatsapp.db` of 4 KiB of random bytes, or a valid database truncated to 1,000 bytes,
    gives `ErrNotDatabase`, and the file's SHA-256 is unchanged.
  - **Old files:** `store/whatsapp.db` and `store/messages.db` files are ignored and unchanged.
  - **Zero-length file:** an empty `whatsapp.db` (0 bytes) with no sidecar is treated as fresh.
  - **Memory limit:** `PRAGMA hard_heap_limit` returns 268435456 on the writer and on every reader connection, and
    T008's child-process test shows the limit is enforced, not just set.
  - **Reader pool:** with all 4 reader connections forced open at once (4 concurrent queries held by a test
    hook), each can call `mcp_fold`, and each can `BeginTx` with `ReadOnly: true` and run a query. A write on any
    reader connection fails as read-only.
  - **No temporary files on disk:** `PRAGMA temp_store` returns 2 (MEMORY) on the writer and on every reader
    connection. A query that forces a large sort (`ORDER BY` on an unindexed expression over 50,000 seeded rows)
    and a `CREATE INDEX` on a scratch table create no new file in the data directory, in `os.TempDir()` or in
    `SQLITE_TMPDIR`; compare directory listings before and after.
- [X] T019 [P] [US1] `cmd/whatsapp-mcp/e2e_test.go`:
  - `stdio` with closed stdin on an empty `--data-dir` exits 0, creates `whatsapp.db`, and logs `store.ready`;
  - a second run logs no `store.migrate`;
  - `pair` opens and closes the store, then still reports "not implemented" with its existing exit code;
  - `status` and `version` never create `whatsapp.db`;
  - a `whatsapp.db` of random bytes makes `stdio` exit 1 with "is not a usable SQLite database";
  - **graceful shutdown under load:** start `stdio` in-process with a body that writes messages in a loop, then
    request a stop. The store close step finishes, the whole shutdown takes less than the 10 s limit, the exit
    code is 0, and on reopen every acknowledged write is present;
  - **shutdown with a long operation:** a test hook holds one store operation running (it blocks until its context
    is cancelled). After a stop request, the store close step takes at most 3 s, the held operation returns a
    cancellation error, and a store call made after the stop gets `ErrClosed`.

### Implementation for User Story 1

- [X] T020 [US1] Create `internal/store/files.go`, with `files_windows.go` and `files_other.go`:
  - `checkPathForm(dataDir)`:
    - Windows: reject UNC, `\\?\`, `\\.\` and any `:` after the drive designator. Then open the directory and
      check where it resolves (research R6): `GetFinalPathNameByHandle` must not give `\\?\UNC\`, `GetDriveType`
      on the resolved volume must be fixed or removable, and `GetVolumeInformationByHandle` must report `NTFS`;
    - Unix: no-op;
  - `artifactNames(db)` returns `db`, `db-wal`, `db-shm`, `db-journal`;
  - `checkArtifacts(dir) (found map[string]private.FileID, err error)`: `Lstat` each name, refuse links, reparse
    points, non-regular files, hard links, any `-journal`, and orphans (a sidecar next to a missing or 0-byte main
    file), and call `private.CheckFile` on each existing one, keeping its identity;
  - `sameArtifacts(before, after map[string]private.FileID) error`: every name in `before` must have the same
    identity in `after`; otherwise `unsafe_artifact`;
  - `createDB(path) (private.FileID, error)`: `private.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL)`, record
    the identity from that handle, then close. `Open` adds it to the identities `sameArtifacts` compares.

  Errors are `*StorageError` with code `unsafe_artifact`, `orphan_sidecar`, `rollback_journal`, `unsupported_path`
  or `not_private`,
  plus the `private` fix text.
- [X] T021 [P] [US1] Create `internal/store/migrations/0001_init.sql` exactly as data-model.md specifies:
  - `CREATE TABLE mcp_version (version INTEGER NOT NULL) STRICT`, and `INSERT INTO mcp_version VALUES (1)`.
  - `chats` (STRICT) with:
    - `jid TEXT PRIMARY KEY`;
    - `phone TEXT NOT NULL DEFAULT ''`;
    - `name TEXT NOT NULL DEFAULT ''` and `name_fold TEXT NOT NULL DEFAULT ''`;
    - `name_quality INTEGER NOT NULL DEFAULT 0 CHECK (name_quality IN (0,1,2,3))`;
    - `name_time INTEGER`;
    - `last_message_time INTEGER`.
  - `messages` (STRICT) with:
    - `chat_jid TEXT NOT NULL REFERENCES chats(jid)` and `id TEXT NOT NULL`;
    - `sender_jid TEXT NOT NULL` and `sender_phone TEXT NOT NULL DEFAULT ''`;
    - `time INTEGER NOT NULL`;
    - `from_me INTEGER NOT NULL CHECK (from_me IN (0,1))`;
    - `text`, `text_fold`, `caption` and `caption_fold`, each `TEXT NOT NULL DEFAULT ''`;
    - `media_type TEXT NOT NULL DEFAULT '' CHECK (media_type IN ('','image','video','audio','document'))`;
    - `media_url TEXT`, `media_direct_path TEXT`;
    - `media_key BLOB`, `file_sha256 BLOB`, `file_enc_sha256 BLOB`;
    - `file_length INTEGER`, `mime_type TEXT`, `file_name TEXT`;
    - `PRIMARY KEY (chat_jid, id)`.
  - Indexes, none of them partial (data-model.md explains why):
    - `chats_last_activity ((last_message_time IS NULL), last_message_time DESC, jid)`;
    - `chats_name ((name_quality = 0), name_fold, jid)`;
    - `chats_phone (phone)`;
    - `messages_chat_time (chat_jid, time DESC, id DESC)`;
    - `messages_sender_time (sender_phone, time DESC, chat_jid DESC, id DESC)`;
    - `messages_sender_jid (sender_jid, time DESC, chat_jid DESC, id DESC)`;
    - `messages_time (time DESC, chat_jid DESC, id DESC)`, the listing order exactly;
    - `messages_id (id)`, for `GetMessageByID`;
    - `messages_chat_sender_phone (chat_jid, sender_phone, time DESC, id DESC)` and
      `messages_chat_sender_jid (chat_jid, sender_jid, time DESC, id DESC)`, for a listing filtered by chat and
      sender.
- [X] T022 [US1] Create `internal/store/migrate.go`:
  - `type migration struct{ version int; name string; sql string }`;
  - `loadMigrations(fs.FS)` parses `NNNN_<name>.sql` from `//go:embed migrations/*.sql`, requiring a contiguous
    1..N with no gaps;
  - `migrate(ctx, writer *sql.DB, current int, list []migration, ev *events) (completed int, err error)`: each
    pending migration plus `UPDATE mcp_version SET version = ?` (migration 1 inserts it) runs in one transaction
    on the writer (`_txlock=immediate`);
  - logs `store.migrate` per step;
  - on failure returns `*MigrationError{Schema: "messages", Step: "NNNN (<name>)", Completed: ...}`.
- [X] T023 [US1] Create `internal/store/preflight.go`:
  - `checkHeader(path)` reads 100 bytes and requires `"SQLite format 3\x00"` and a power-of-two page size from 512
    to 65536 (stored value 1 = 65536); otherwise `ErrNotDatabase`;
  - `preflight(ctx, path) (pf preflightResult, err error)` opens one connection on the DSN from
    `openCommittedView` (T014), and closes the connection then calls `release` before returning on every path.
    Extend `openCommittedView` to return the `private.FileID` of the main file and `-wal` from its own handles,
    using `private.FileIDOf` (T010), and refuse with `unsafe_artifact` unless they equal the identities
    `checkArtifacts` recorded. A `-wal` that `checkArtifacts` did not see but the view finds is a mismatch too;
    It never opens `path` itself through SQLite;
  - it reads `sqlite_master` for the presence of `mcp_version`, `chats`, `messages`, `whatsmeow_version` and any
    other `whatsmeow_%` table;
  - it reads `SELECT count(*), max(version) FROM mcp_version` and `SELECT version, compat FROM whatsmeow_version`;
  - it returns `fresh` when none of these tables exists.

  Refusal rules (US2 tests them in depth):
  - messages version > latest bundled → `ErrSchemaTooNew`;
  - device `compat` (or `version` when `compat` is NULL or 0) > `len(upgrades.Table)` → `ErrSchemaTooNew`;
  - `mcp_version` row count ≠ 1 → `ErrSchemaInvalid`;
  - `whatsmeow_version` with more than 1 row, or with 0 rows while another `whatsmeow_*` table exists →
    `ErrSchemaInvalid` (dbutil would read a missing row as version 0 and rerun the full schema). 0 rows and no
    other `whatsmeow_*` table is device version 0, an interrupted first start, and is not refused;
  - `chats`/`messages` without `mcp_version`, or `whatsmeow_*` tables without `whatsmeow_version` →
    `ErrSchemaInvalid`.
- [X] T024 [US1] Create `internal/store/store.go` with `type Store`, `Open`, `Close` and `Device`, following
  contracts/store-api.md § Lifecycle in this order:
  1. `checkPathForm`;
  2. `checkArtifacts`, keeping the identities;
  3. `createDB` if missing, or `checkHeader` + `preflight` if non-empty;
  4. `registerFold()`, before any pool exists, so every connection either pool opens has `mcp_fold`;
  5. open the writer `*sql.DB` with the write DSN from research R1, verbatim:
     `file:<path>?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=journal_size_limit(67108864)&_pragma=hard_heap_limit(268435456)&_txlock=immediate`,
     with `SetMaxOpenConns(1)`;
  6. `PingContext`, then re-run `checkArtifacts` and `sameArtifacts` against step 2. On a new database `-wal` and
     `-shm` do not exist yet at this point; they appear at the reader's ping or the first write, so the same
     re-check runs again after step 7 and after step 8, and every sidecar is checked before the store is ready;
     the test hook for T017's swap sits between steps 3 and 5;
  7. open the reader with the read DSN from research R1, verbatim:
     `file:<path>?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)&_pragma=hard_heap_limit(268435456)&_pragma=query_only(1)`,
     with `SetMaxOpenConns(4)`, and `PingContext`. It has no `_txlock`;
  8. the device upgrade: `dbutil.NewWithDB(writer, "sqlite")`, set `VersionTable="whatsmeow_version"`,
     `UpgradeTable=upgrades.Table`, `ReadOnlyDB=reader`, then
     `sqlstore.NewWithWrappedDB(wrapped, newWALog(logger)).Upgrade(ctx)`, logging `store.upgrade`; a failure is a
     `*MigrationError{Schema: "device"}`;
  9. `migrate`; log `store.ready`.

  On any error, close everything opened and return the mapped error. `Close` calls `gate.close` (T011), then
  closes the reader and the writer, logs `store.close` with its duration, and is idempotent. Log
  `store.open`/`store.refused`/`store.preflight` at the matching points.
- [X] T025 [US1] Wire the store into `cmd/whatsapp-mcp/commands.go`:
  - **`runUntilStopped`:** after `acquireLock`, call `store.Open(ctx, cfg.DataDir, logger)` with a 10-minute
    timeout derived from `lc.ctx`. On error, print `whatsapp-mcp: ` + the error to stderr, release the lock and
    exit 1. On success, `lc.addStop("store", st.Close)`, which is registered after the lock so it runs before the
    lock is released. Pass `st` to `body` (change its signature to
    `func(context.Context, *lifecycle, *slog.Logger, *store.Store)` and update `runServe`/`runStdio`).
  - **`runPair`:** open the store after the lock, `defer st.Close(ctx)`, then keep the current not-implemented
    result.
  - **Unchanged:** `status` and `version`.

**Checkpoint**: US1 works on its own. Run T017–T019 on all three OSes in CI.

---

## Phase 4: User Story 2 - The schema evolves safely (Priority: P1)

**Goal**: missing migrations are applied in order, failures stop at a clean step boundary, newer databases are
refused without changing any file, and the device schema is upgraded first.

**Independent Test**: databases at each earlier version upgrade to the latest; injected failures in each
history roll back their step only; a too-new schema in either history is refused with a byte-identical
directory.

### Tests for User Story 2 ⚠️

- [X] T026 [P] [US2] `internal/store/migrate_test.go`. Use an injected list of three test migrations
  (`0001` = real init, `0002` adds a test table, `0003` adds a column):
  - fresh → version 3, in order;
  - a database at version 1 or 2 applies only the rest and keeps seeded rows;
  - a failing `0003` (invalid SQL after a valid statement) leaves version 2, no partial table or column, and
    seeded data intact; the error is a `*MigrationError` naming `0003` and "completed: messages 2";
  - a retry with a fixed `0003` resumes from 2;
  - `loadMigrations` rejects gaps and duplicates.
- [X] T027 [P] [US2] `internal/store/preflight_test.go` (SC-006). For each refusal, snapshot the directory with
  `snapshotDir` before and after `Open` and require identical snapshots with no file created or removed. Cover
  each case with and without an un-checkpointed WAL holding committed data:
  - `mcp_version` = latest+1 (`ErrSchemaTooNew`, error text names "message schema", 1+latest and latest);
  - `whatsmeow_version` compat = 17 (`ErrSchemaTooNew`, "device schema");
  - `mcp_version` with 0 rows and with 2 rows (`ErrSchemaInvalid`);
  - `whatsmeow_version` with 0 rows and with 2 rows, next to `whatsmeow_*` tables (`ErrSchemaInvalid`, "device
    schema");
  - not a refusal: `whatsmeow_version` with 0 rows and no other `whatsmeow_*` table (an interrupted first start)
    opens, and the device schema is upgraded to 16;
  - `chats` present without `mcp_version`;
  - `whatsmeow_device` present without `whatsmeow_version`.
- [X] T028 [P] [US2] `internal/store/device_test.go`:
  - **Upgrade:** create a store, close it, simulate device v15 by
    `ALTER TABLE whatsmeow_chat_settings DROP COLUMN wasa_root_secret_id` and
    `UPDATE whatsmeow_version SET version=15`, then reopen: version 16, the column is back, seeded device data is
    kept.
  - **Failing device step:** set version 15 *without* dropping the column, so the v16 `ALTER` fails, and reopen.
    `Open` fails with `*MigrationError{Schema: "device"}`; `whatsmeow_version` is still 15; no message migration
    ran (`mcp_version` unchanged); no store is returned.
  - **Device succeeds, message migration fails:** device v15 → 16 is committed; the injected failing message
    migration leaves messages at its previous version; the error lists "completed: device 16, messages N".
- [X] T029 [P] [US2] `cmd/whatsapp-mcp/e2e_test.go`:
  - a too-new message schema makes `stdio` exit 1 with the schema and both versions in stderr;
  - the lock is released afterwards (a second run gets the same error, not "lock held").

### Implementation for User Story 2

- [X] T030 [US2] Complete the refusal and error paths in `internal/store/preflight.go` and
  `internal/store/store.go` until T026–T029 pass:
  - `*SchemaError` carries `Schema` (`messages`/`device`), `Observed` and `Supported`;
  - `*MigrationError.Completed` reports both histories' versions after the failure;
  - `store.refused` and `store.upgrade`/`store.migrate` events carry `outcome` `refused` or `failed` and stable
    codes `schema_too_new`, `schema_invalid`, `migration_failed`, `device_upgrade_failed`.
- [X] T031 [US2] Make `Open` take a migration list through an unexported option, used only by tests, so T026/T028
  inject migrations without changing the embedded files. Production `Open` uses `loadMigrations(embedded)`.

**Checkpoint**: US1 and US2 pass on their own. Record the SC-006 results per OS.

---

## Phase 5: User Story 3 - Ingest stores chats and messages correctly in any order (Priority: P1)

**Goal**: `StoreMessages` and `UpdateChats` give the same chats and messages whatever the order of arrival, with
duplicates, under concurrency and crashes.

**Independent Test**: the same fixed set stored in 20 random orders, with duplicates, gives identical tables
every time.

### Tests for User Story 3 ⚠️

- [X] T032 [P] [US3] `internal/store/messages_test.go` (the scenarios of US3):
  1. re-storing `(C, X)` with new text replaces it, and there is still exactly one row; storing two different
     copies A then B keeps B, and B then A keeps A (the one order-dependent case SC-003 excludes);
  2. the same `X` in two chats gives two rows;
  3. a 09:00 message after a 12:00 one keeps last activity at 12:00;
  4. a 12:00 message after 09:00 moves it to 12:00;
  5. a message for an unknown chat creates the chat with that time, atomically (inject a failure after the chat
     insert; neither row remains);
  6. a stored URL and direct path survive a re-store whose copy lacks them, while the other fields come from the
     new copy. This holds for each of the eight media fields: "an empty media metadata field … MUST NOT overwrite
     a stored non-empty one";
  7. a caption is stored in its own field;
  8. sender `491511234567:12@s.whatsapp.net` is stored as `491511234567@s.whatsapp.net` with phone
     `491511234567`;
  9. an unresolved LID sender keeps its full LID JID and an empty phone.

  Also: a batch of 1,000 messages is accepted, while 1,001 messages or one byte over 16 MiB is rejected with
  nothing stored; one invalid message in a batch rejects the whole batch.
- [X] T033 [P] [US3] `internal/store/chats_test.go`:
  - **Fallback vs real:** real "Alice" vs an offered fallback "491511234567" → stays "Alice"; fallback →
    replaced by real.
  - **Saved vs real:** a saved "Mum" vs a later push name "Bank Support" (real) → stays "Mum"; a real name is
    replaced by a saved one seen earlier; in both orders.
  - **Time:** real at 10:00 then a different real at 11:00 → new name; then one at 09:00 → unchanged; equal
    quality and time → the byte-wise greater name, in both orders.
  - **Empty and bad offers:** an empty name never replaces; a name without a time, or with a time outside the
    range, is a `*ValidationError`.
  - **Activity:** a zero `LastActivity` never clears; activity only moves forward.
  - **Ordering edge cases:** chats with no messages list with no latest message; chats with no activity sort
    last.
- [X] T034 [P] [US3] `internal/store/order_test.go` (SC-003): a fixed fixture of 50 chats and 2,000 messages
  with duplicates of identical content, name offers of all three qualities and several times, and history chats
  without messages. Store it in 20 seeded random orders and permutations of batch splits, each into a fresh
  store. Dump `chats` and `messages` ordered by key; all 20 dumps are identical. Re-stores with different content
  are the exception SC-003 names; T032 item 1 covers them.
- [X] T035 [P] [US3] `internal/store/concurrency_test.go` (SC-005): 16 goroutines × 100 `StoreMessage` calls
  while 4 goroutines run `ListMessages` and `ListChats` in a loop. Expect 0 errors (in particular none matching
  "database is locked"), and all 1,600 messages present.
- [X] T036 [P] [US3] `internal/store/crash_test.go` (SC-007). The test re-executes its own binary with
  `GO_STORE_CRASH_CHILD=1`. The child opens the store and writes messages in a loop, printing each committed ID to
  stdout after the call returns. The parent kills it with `Process.Kill` after about 300 commits, reopens the
  store, and asserts that every printed ID is present and that `PRAGMA integrity_check` returns `ok`.

### Implementation for User Story 3

- [X] T037 [US3] Create `internal/store/chats.go`:
  - **Types:** `NameQuality` (`NameNone`, `NameFallback`, `NameReal`, `NameSaved`) and `ChatUpdate`.
  - **`UpdateChats`:** validates every update first (`*ValidationError`). Then, under admission and the disk
    guard, it runs one writer transaction that upserts each chat:
    - `phone` comes from the JID (digits for `s.whatsapp.net`, else `''`);
    - the name is set only when
      `(excluded.name_quality, excluded.name_time, excluded.name) > (name_quality, coalesce(name_time, -1), name)`,
      together with `name_fold = fold(name)`;
    - `last_message_time = max(coalesce(last_message_time, excluded), excluded)` only when offered.
  - **Times:** stored as Unix ms.
  - **Single form:** `UpdateChat` wraps the batch.
- [X] T038 [US3] Create `internal/store/messages.go`:
  - **Types:** `Message` and `MediaMeta` (contract).
  - **`StoreMessages`:** validates all messages and checks the batch limits (≤ 1,000 messages, ≤ 16 MiB of
    aggregate field bytes) before any SQL. Then, under admission and the disk guard, it runs one transaction:
    - insert the chat with `ON CONFLICT DO NOTHING` (with `phone`), then
      `UPDATE chats SET last_message_time = max(...)`;
    - upsert the message on `(chat_jid, id)`; every column takes `excluded`, except each media column, which
      takes `coalesce(nullif(excluded.col, ''), col)` (blobs: `CASE WHEN length(excluded.col) > 0 ...`);
    - compute `text_fold`/`caption_fold` with `fold` and `sender_phone` from the parsed sender in Go.
  - **Commit:** reported through `commitOutcome`.
  - **Single form:** `StoreMessage` wraps the batch.

**Checkpoint**: US3 passes on its own (T032–T036).

---

## Phase 6: User Story 4 - The tools find chats, contacts and messages (Priority: P1)

**Parked finding P1**: resolved in the design on 2026-10-30. No task in this phase starts until the design review of
that resolution clears it (see Parked findings).

**Goal**: every query that tools 005/006 need, correct and deterministic, fast on 100,000 messages.

**Independent Test**: seed known chats, contacts and messages, and compare every query's result against
expected rows.

### Tests for User Story 4 ⚠️

- [X] T039 [P] [US4] `internal/store/queries_chats_test.go`:
  - **`ListChats`:**
    - matches the text in the name or the JID, case- and NFC-insensitively ("ПРИВІТ" finds "привіт");
    - sorts by activity (newest first, no activity last, ties by JID) or by name (ascending, unnamed last);
    - pages with limit and offset; `withLatest` returns the real latest message by time, or nil.
  - **`GetChat`:** with its latest message, or `*NotFoundError`.
  - **`DirectChat`:** an exact phone match only (a partial number does not match), or not found. The same chat is
    found by phone number, phone JID and mapped LID. A LID with an unmerged `@lid` chat and no phone chat gives
    the `@lid` chat. A malformed contact is a `*ValidationError` on `contact`.
  - **`ContactChats`:** a contact with a direct chat who wrote in two groups, one of them under a mapped LID
    (seeded with `LIDs.PutLIDMapping`), gives three chats, each once, newest first, each with its latest
    message, whether asked by phone number or by LID. Without the mapping, asking by phone gives two chats and
    asking by LID gives one. Before a merge, with both a phone chat P and a `@lid` chat L for the contact, both
    are returned, and `DirectChat` returns P.
  - **Paging rules:** limit 0, limit 201, offset -1 and offset 100,001 are `*ValidationError`; a page beyond the
    end is empty.
- [X] T040 [P] [US4] `internal/store/queries_messages_test.go`:
  - **Filters:** `ListMessages` with every combination of strict after, strict before, chat, sender (given as
    "+49 151 1234567", as the phone JID and as the mapped LID, each matching messages stored under both
    identities) and query, newest first, ties by `(chat_jid, id)` desc. Paging never
    skips or repeats when times are equal (iterate all pages and compare with the full set).
  - **Search:** matches text or caption; `%`, `_`, quotes and backslash match literally; "ÉTÉ" finds "été";
    composed and decomposed é match each other; "ete" does not find "été".
  - **After/before:** `after > before` gives empty, not an error.
  - **Context:** N before and M after (0–20), both oldest first, from the same chat only; `MessageContext` by
    `(chat_jid, id)`.
  - **Last interaction:** `LastInteraction` returns the newest message of the direct chat (either side) or one
    sent by the contact in a group, or nil. When the newest is a group message sent under the mapped LID, it is
    returned whether the contact is given by phone number or by LID. With both P and L present, a newer message
    in L (from either side) is the one returned. For the account's own number, with a newer message sent to Alice
    and an older one in the "message yourself" chat, the older one is returned; with no own chat, nil. The same
    holds when the account is given by its own LID, and when `whatsmeow_device.jid` carries a device part
    (`490000000001:7@s.whatsapp.net`). With no device row (logged out), asking for the former own number still
    never returns a message the account sent to someone else. With a device row whose `lid` is `NULL`, and again
    with `lid = ''`, a contact with no LID mapping is not taken for the account: its last interaction is its own
    newest message, not the newest of the own direct chat. Two candidates at the same second are ordered by
    chat JID, then ID.
  - **Chat and sender together:** `ListMessages` with a chat and a sender (by phone, by LID, and by a contact with
    both) returns only that sender's messages in that chat, newest first.
  - **Both identities:** messages of one contact under the phone and under the LID at the same millisecond are
    ordered by `(chat_jid, id)` across both, and paging through them never skips or repeats one.
  - **Fields:** every `MessageView` field is filled (scenario 9). The sender name follows the order full → first →
    push → business (device store), looked up under the sender JID and then its mapped counterpart, else the saved
    or real name of the direct chat with the sender, else ''. A contact with both a first name and a push name
    shows the first name. It is '' for from-me messages.
- [X] T041 [P] [US4] `internal/store/contacts_test.go`, seeded through the device store (T016):
  - `SearchContacts` matches full, push, first and business name, and phone, case- and NFC-insensitively;
  - groups are excluded;
  - a person under both a LID and a phone is returned once, "matched if either entry matches", with the
    phone-number JID and number;
  - the name is taken from the phone-number entry in the order full, first, push, business, and if that entry has
    none, from the LID entry's name in the same order;
  - a LID without a mapping is returned with its LID JID and an empty phone;
  - no device (never paired) gives an empty result, not an error;
  - a query with no phone number in it ("Alice", "Alice2") matches by name only: a contact named "Bob" is not
    returned, whatever its phone number;
  - a person whose phone entry has full name "Bob" and whose mapped LID entry has push name "Alice": a search for
    "alice" returns them once, with the phone JID and the name "Bob"; a search for "bob" returns the same row.

### Implementation for User Story 4

- [X] T042 [US4] Create `internal/store/queries.go` with the shared parts:
  - `MessageView`, `ChatView`, `Page`, `ChatSort`, `MessageFilter`, `Context` and `Hit` (contract);
  - `validatePage` (limit 1–200, offset 0–100,000) and `validateContext` (0–20 per side);
  - the shared message SELECT, joining chat name and sender name. The sender name is a correlated subquery on
    `whatsmeow_contacts` for the current device (`our_jid` = the single `whatsmeow_device.jid`), using
    `coalesce(nullif(full_name,''), nullif(first_name,''), nullif(push_name,''), nullif(business_name,''))`, first
    for `their_jid = sender_jid` and then for its counterpart in `whatsmeow_lid_map` (looked up by
    `pn = <sender_phone>` for an `@s.whatsapp.net` sender and `lid = <sender's user part>` for an `@lid` sender,
    never both for one sender, so the map's key is searched, never computed), then the
    direct chat's
    saved or real name (`name_quality >= 2`), else '';
  - a `resultBudget` that adds each row's byte size and returns `ErrResourceLimit` above 32 MiB.

  All SQL values are bound parameters. The sort columns and directions come from a fixed `map[ChatSort]string`.
- [X] T043 [US4] Implement `ListChats`, `GetChat`, `DirectChat` and `ContactChats` in
  `internal/store/queries.go`, on the reader pool under admission:
  - search uses `instr(name_fold, ?) > 0 OR instr(jid, ?) > 0` with the folded query;
  - the latest message uses the `messages_chat_time` index (`ORDER BY time DESC, id DESC LIMIT 1`);
  - `DirectChat` and `ContactChats` start with `resolveContact` (T045);
  - `DirectChat` is the chat with `jid = phoneJID`, else `jid = lidJID`;
  - `ContactChats` is one query over `chats`: both direct chats that exist (`jid IN (phoneJID, lidJID)`) plus every
    `g.us` chat with `EXISTS (SELECT 1 FROM messages WHERE chat_jid = chats.jid AND sender_phone = ?)` or the same
    with `sender_jid = ?` (a probe whose value is empty is left out of the SQL, not bound as `''`), ordered by
    `last_message_time IS NULL, last_message_time DESC, jid` so that it walks `chats_last_activity`. Like the
    message listings, it selects the page's `jid`s first, so that walk is covering, and then reads those chats'
    rows. Each probe is
    one search on `messages_chat_sender_phone` or `messages_chat_sender_jid` (research R15). Never `DISTINCT
    chat_jid` over the contact's messages with an `OR`, which collects the row IDs of the contact's whole history;
  - the sort orders are exact:
    - `ByLastActivity` is `ORDER BY last_message_time IS NULL, last_message_time DESC, jid`;
    - `ByName` is `ORDER BY name_quality = 0, name_fold, jid`, so unnamed chats come last even though their
      `name_fold` is `''`.
- [X] T044 [US4] Implement `ListMessages`, `MessageContext` and `LastInteraction` in `internal/store/queries.go`:
  - filters are built from fixed SQL fragments with bound values;
  - `MessageFilter.Sender` and `LastInteraction` resolve the contact with `resolveContact` (T045). A sender
    listing has a phone branch (`sender_phone = ?`) and a LID branch (`sender_jid = ?`), and a branch whose value
    is empty is left out. With one branch it is one query; with both, the key query is
    `<phone branch> UNION ALL <LID branch> ORDER BY time DESC, chat_jid DESC, id DESC LIMIT ? OFFSET ?`, where each
    branch is `SELECT time, chat_jid, id FROM messages WHERE <its sender column> = ?` followed by the other filters,
    built from the same fixed fragments with their own bound values (research R15). It never runs one query with
    `sender_phone = ? OR sender_jid = ?`;
  - `LastInteraction` is the newest of up to four `LIMIT 1` queries, each on a single index (research R16): the
    latest message of the phone chat and of the `@lid` chat (`messages_chat_time`), and the newest message with
    `sender_phone = phone` (`messages_sender_time`) and with `sender_jid = lidJID` (`messages_sender_jid`), each
    `ORDER BY time DESC, chat_jid DESC, id DESC LIMIT 1` with no sort step. The newest of the candidates is chosen in
    Go by `(time, chat_jid, id)`. It never runs one query over the `OR`, which would read the contact's whole
    history. Both sender queries also require `from_me = 0`, which costs nothing for another contact (every
    message the account sent carries the account's own JID) and keeps the result right when the own number is
    unknown. The own number and LID are the user parts of `whatsmeow_device.jid` and `.lid`, with any device part
    (`:7`) removed. Each is compared only when both it and the contact's value are non-empty, so a `NULL` or `''`
    own LID never matches an unmapped contact; when the contact's phone or LID equals one of them, the two sender queries are skipped, and the
    result is the latest message of the own direct chat. With no device row, the queries run with `from_me = 0`;
    for the former own number that walks only the account's own messages in the sender index, which is accepted
    for the logged-out state;
  - a chat filter together with a sender filter puts `chat_jid = ?` in each branch, so the phone branch searches
    `messages_chat_sender_phone` and the LID branch `messages_chat_sender_jid` (T021), with the same order and
    keys-first page as every sender listing;
  - search is `(instr(text_fold, ?) > 0 OR instr(caption_fold, ?) > 0)`;
  - order is `time DESC, chat_jid DESC, id DESC`. The page's keys are selected first
    (`SELECT time, chat_jid, id ... ORDER BY ... LIMIT ? OFFSET ?`), and the full rows are read for those keys
    only. No listing sorts: each walks its index in this order, and a sender listing merges its two branches.
    Without search text the rows skipped by the offset are read from covering indexes only, and the rows of up to
    512 KiB only for the page; with search text each skipped row's folded columns are read (research R15, R16);
  - context: per hit, two queries on `messages_chat_time` with the hit's position as a row value, so the index
    seeks straight to it: before is `chat_jid = ? AND (time, id) < (?, ?) ORDER BY time DESC, id DESC LIMIT ?`,
    then reversed; after is `chat_jid = ? AND (time, id) > (?, ?) ORDER BY time, id LIMIT ?`. Never the
    `time < ? OR (time = ? AND id < ?)` form, which binds only `chat_jid` and walks the chat from one end;
  - before any context query runs, check that `hits × (before+after) ≤ 2,000` with checked arithmetic, else
    `ErrResourceLimit`.
- [X] T045 [US4] Create `internal/store/contacts.go` with `SearchContacts`. The rules are in data-model.md
  § Derived values "Contact identity". One query over the current device's `whatsmeow_contacts`, left-joined
  with `whatsmeow_lid_map`:
  - person key = `coalesce(pn_jid, their_jid)`;
  - build one row per person first, then match: group the contact rows (each joined to the map) by person key
    and choose the name per the rule; the text then matches the person if `instr(mcp_fold(<name col>), ?) > 0`
    for any of the four names of either entry, as a `HAVING max(...)` over the group (or an equivalent join of
    the counterpart entry). Filtering rows before grouping is wrong: a person matched only through the LID entry
    would get that entry's name. Add `OR instr(phone, ?)` only when `NormalizePhone` accepts the whole query, so a
    name never matches through an empty digit string;
  - the LID map holds bare user parts. Join it with its key on its own side, so each contact is one index search:
    `m.lid = substr(c.their_jid, 1, length(c.their_jid) - 4) AND c.their_jid LIKE '%@lid'` for a LID contact, and
    `m.pn = substr(c.their_jid, 1, instr(c.their_jid, '@') - 1) AND c.their_jid LIKE '%@s.whatsapp.net'` for a
    phone contact. Never `m.lid || '@lid' = c.their_jid`, which scans the whole map for every contact;
  - exclude `@g.us`;
  - the name per the rule (full, first, push, business), from the phone entry, else from the LID entry;
  - order by name fold, then key; limit and offset; result budget.

  If the device has no row, return an empty result.

  Also add `resolveContact(ctx, conn, s) (contactIDs, error)` (research R15): `parseContact` (T009), then one
  lookup in the current device's `whatsmeow_lid_map` by `pn` or by `lid` to fill the other identity. Without a
  device or a mapping, the contact keeps the one identity it was given. Test it in `contacts_test.go`: phone, phone
  JID and LID inputs, with and without a mapping, and with no device.
- [X] T046 [P] [US4] Create `internal/store/bench_test.go`: `BenchmarkQueries` seeds 100,000 messages across
  1,000 chats once (through `StoreMessages` in batches of 1,000, cached per process in a temp dir). It has
  sub-benchmarks for each US4 query: `ListChats` (both sorts, with search),
  `GetChat`, `DirectChat`, `ContactChats` (by phone and by LID), `SearchContacts`, `ListMessages` (no filter,
  chat, sender, search, with context 5/5), `MessageContext` and `LastInteraction` (by phone and by LID).

  In the same file, `TestQueryPlans` runs `EXPLAIN QUERY PLAN` on the SQL of each of these queries (and of
  T058's lookups) with representative bound values, on the seeded database. The test holds the expected plan of
  each query, from data-model.md § Indexes: a `SEARCH` on a named index for every lookup by key, ID, chat, sender
  or contact; `SCAN messages USING INDEX messages_time` only for text search and an unfiltered listing;
  `SCAN chats USING INDEX chats_last_activity` or `chats_name` for the chat listings; and a temporary b-tree only
  where that table lists one, which no message query has. A sender query under both identities must plan as
  `MERGE (UNION ALL)` over its two indexes; a `MULTI-INDEX OR` anywhere fails. Any other plan fails the test. It runs in CI, unlike the
  benchmark, so a partial or missing index is caught there.

  Also add `TestQueryScale` in `internal/store/scale_test.go`, which runs in CI on all three OSes (not skipped by
  `-short`). It seeds, once, through raw batched inserts on a test-only hook so seeding stays under a minute: a
  group of 300,000 messages, 120,000 of them from one contact under both its phone number and a mapped LID;
  5,000 contacts in the device store with 10,000 LID mappings; and 1,000 small chats. Then it runs every query of
  US4 and US5 against the oldest, a middle and the newest message where that matters. Cases include: a search
  with 100 hits and 10/10 context in the big group; `MessageContext` on its oldest message; `SearchContacts` for a
  name and for a number; `ContactChats`, `LastInteraction` and a sender listing at offset 0 and at offset 99,800
  for the big contact; a listing with the big group and a sender who has 2 messages there; `GetMessageByID`.

  The check counts pages, not time, so it holds on any runner. The test seeds the same shapes twice, at one tenth
  (30,000 messages, 12,000 of them from the big contact; 500 contacts; 1,000 mappings; the 1,000 small chats) and
  at full size. Each case runs on one reader connection taken with `(*sql.DB).Conn`, through a test hook that runs
  the query's own code on that connection. Before and after, `(*sql.Conn).Raw` reads `sqlite.DBStatus`
  (`DBStatusCacheHit` + `DBStatusCacheMiss`, `modernc.org/sqlite` v1.61.0), and the delta is the pages the query
  touched. Each case runs once to warm the cache and is then counted, with the rows it must walk, which the case
  knows by construction. The rules:
  - **Constant-cost cases:**
    - every lookup, `DirectChat`, `LastInteraction`, `MessageContext` and a search hit's context;
    - every listing at offset 0, including the unfiltered listing and the big contact's sender listing;
    - the big group with a sender who has 2 messages there;
    - `GetMessageByID`.

    Pages at full size ≤ 2 × pages at a tenth + 16. A deeper B-tree adds a page or two per search, while an
    O(chat size) or O(contacts × mappings) plan grows about 10 times.
  - **Linear-by-design cases,** each normalised by the rows it must walk:
    - `SearchContacts` by name and by number (contacts plus mappings);
    - `ContactChats` (chats);
    - the big contact's deep sender listing, at the same relative depth: offset 9,800 at a tenth and 99,800 at
      full (offset + size rows).

    Pages per row at full size ≤ 1.5 × pages per row at a tenth + 0.01. A plan that walks more than it must (the
    whole group for a chat-and-sender listing, the contact's history for contact chats, contacts × mappings) grows
    about 10 times per row.
  - **Linear over messages by design,** with only a backstop of 5 s at full size: a text search, the unfiltered
    listing at a deep offset, and the big contact's deep sender listing with search text.

  The test records each case's pages, rows and median time of 5 runs in its output for `verification.md`. Times are
  not compared, because Windows timers give medians of 0 s for the fast cases.

**Checkpoint**: US4 passes on its own (T039–T041). Run the benchmark locally (it is not a CI gate).

---

## Phase 7: User Story 7 - Storage remains private, bounded and recoverable (Priority: P1)

**Goal**: the abuse cases, limits, diagnostics, recovery and operator guide that make the store safe across all
stories.

**Independent Test**: isolated temporary directories, injected failures and synthetic data show every abuse case
has no side effect. The recorded cross-identity and restore evidence passes.

### Tests for User Story 7 ⚠️

- [X] T047 [P] [US7] `internal/store/limits_test.go` (SC-009). For every constant in `limits.go`, a test at the
  boundary (accepted) and one beyond it (rejected, with no row written: compare table dumps). Log for each case:
  - elapsed time;
  - result bytes;
  - heap growth (`runtime.ReadMemStats` before and after);
  - SQLite's own memory high-water mark (`sqlite3_memory_highwater`, research R16), which Go's heap does not
    include;
  - the gate's active and waiting counts;
  - the size of the database and `-wal`.

  The test asserts that heap growth stays below 2× the approved result budget and SQLite's high-water mark below
  the 256 MiB limit. It also seeds one contact with 2,000 messages of 256 KiB and lists them by phone and LID at
  offset 1,800: the call succeeds within the limit, because the skipped rows come from covering indexes and only
  the page's rows are read. In a child process (as T008 and T036), a lowered limit set with
  `sqlite3_hard_heap_limit64` below what `SearchContacts` over 5,000 contacts needs for its grouping (measured first
  in the same child) makes that call fail with `ErrResourceLimit` code `memory`, not a crash; the limit never
  changes in the test binary itself.

  Per-contact memory, in a child process with no parallel tests (as T008), with identifiers of realistic length
  (chat JIDs of 120 B, message IDs of 150 B). Run it twice: once with the contact's messages split evenly between the
  phone and the mapped LID, and once with all of them under the phone (the state after a merge). Before each
  measurement, the same listings run once on every reader connection they will use, so each page cache is already
  at its 2,000 KiB ceiling and reuses its slots. A query's growth is then SQLite's high-water mark after it minus
  `sqlite3_memory_used` at the reset (`sqlite3_memory_highwater(1)`), which measures the query and not the cache.
  1. Seed the contact with 50,000 messages. Run a sender listing at offset 0 and at offset 49,800, and record
     each growth.
  2. Add 150,000 more (200,000) and then 200,000 more (400,000). Repeat both, and also run offset 99,800. Each
     growth is at most 256 KiB at every size and offset, and from 200,000 to 400,000 it rises by at most 16 KiB:
     nothing grows with the contact's history or with the offset. A fixed cost of about 98 KB per operation
     appears after the first tens of thousands of writes, in a `GetMessage` too; it follows the WAL
     state and not the contact's history (a checkpoint made no difference locally, where it had already appeared, and
     brought it in on one CI run), and it is the same at 200,000 and at 400,000, so the rule is absolute and does not
     subtract it.
  3. Run 2 deep (offset 99,800) and 2 shallow (offset 9,800) listings at once, with a test hook that holds all four
     statements open until each has read its first row, so the peaks overlap. The growth is at most 1 MiB.
  4. Record the measured growths in `verification.md`.
  5. Reset, then run `LastInteraction` for the same contact: below 1 MB of SQLite memory and 50 ms.

  Cover:
  - every field size;
  - batch count and bytes;
  - list size and offset;
  - context per side, and aggregate context rows: 200 hits × 10/10 = 4,000 is rejected, while 100 hits × 10/10 is
    accepted;
  - result bytes: seed rows near 256 KiB each so that one page exceeds 32 MiB → `ErrResourceLimit`, never a
    shorter list;
  - ambiguous-ID chats (101 chats → `ErrResourceLimit`);
  - overflowing offsets and context (`math.MaxInt`).
  _Partial: step 2 (the literal rule) and step 3 (listings held after their first row) are open, see T069 and T070._
- [X] T048 [P] [US7] `internal/store/admission_test.go`, with the real store:
  - 16 blocking operations (hold the writer with an open `BEGIN IMMEDIATE` on a separate raw connection) plus 64
    waiting, then one more gets `ErrBusy` at once;
  - waiting ends at 5 s;
  - a caller deadline of 200 ms ends a lock wait at about 200 ms, not at the 5 s busy timeout;
  - cancellation frees the slot within 1 s;
  - a commit cancelled at the commit point (test hook in `admit.go`) returns `ErrIndeterminate`, and a retry with
    the same `(chat_jid, id)` leaves one row;
  - after `Close`, every public method returns `ErrClosed`, and a second `Close` returns nil.
- [X] T049 [P] [US7] `internal/store/disk_store_test.go`:
  - **Low disk:** an injected free-space function below 256 MiB makes `StoreMessages` and `UpdateChats` fail
    with `ErrDiskLow`, with no row written and no file deleted. Recovery needs ≥ 512 MiB.
  - **Disk full:** simulated with `PRAGMA max_page_count` on the writer connection (test hook). A write fails
    with `ErrIO` code `disk_full`; earlier commits are intact; `-wal` and the main file still exist; the store
    keeps working after the limit is raised.
  - **Read-only database:** on Unix, make the file read-only by `chmod 0400` after close; on Windows, set the
    read-only attribute. Then `Open` fails with a safe `ErrIO`/`readonly` reason, and nothing is deleted.
  - **WAL bound:** write more than 64 MiB of messages in batches, then let an automatic checkpoint run, or force
    one with `PRAGMA wal_checkpoint(TRUNCATE)` through the test hook. The `-wal` file is then ≤ 64 MiB, the
    approved limit, and every row is readable.
  - **Long reader:** hold a read transaction open on the reader pool through a test hook while writes continue.
    The reader's operation ends with a deadline error at the 10 s operation limit (use a shortened test limit).
    After it ends, a checkpoint brings `-wal` back within 64 MiB.
- [X] T050 [P] [US7] `internal/store/abuse_test.go`: one subtest per abuse case 1–12 in spec.md § Security → Abuse
  cases that is automatable, each asserting "no side effect" (table dumps unchanged, sentinel file outside the
  data directory unchanged):
  - case 4: search text `x' OR '1'='1`, `%`, `_`, `\` matches literally;
  - case 5: unsupported sort value `ChatSort(99)`, malformed JIDs (`@g.us`, `x@evil.example`, `\x00@lid`), bad
    media key length, `-1` file length;
  - cases 6–10: use T047–T049 and T027 by reference, plus a small direct assertion each;
  - case 11: see T051;
  - case 12: documented in T054;
  - cases 1–3 are in T017 plus T056 (manual).
- [X] T051 [P] [US7] `internal/store/logsafety_test.go` (SC-011). At level Debug, run with a capturing handler:
  - every success path: open, upgrade, migrate, store, query, merge, close;
  - every failure path: each refusal, a migration failure, disk-low, disk-full, busy, validation, corruption
    (flip bytes in a page of a copy, then query).

  Seed secret markers: a 32-byte media key, a device key read from `whatsmeow_device`, the message text
  "SECRET-BODY-7f3a", a caption, a name containing `\x1b[31m` and `\x07`, and a search string. Assert:
  - none of the markers appears in any record or error string, whether raw, hex or base64;
  - every `store.*` event has `time` in UTC, `event`, `op_id`, `outcome` and `code`;
  - `whatsmeow.log` records carry only `time`, `level`, `module` and the format string;
  - paths are ≤ 512 bytes with control characters escaped.

### Implementation for User Story 7

- [X] T052 [US7] Apply the cross-cutting enforcement in `internal/store/queries.go`, `messages.go`, `chats.go`,
  `contacts.go` and `merge.go` until T047–T051 pass:
  - every public method goes through `gate.enter`, writes go through `diskGuard.check`, and errors go through
    `mapDriverError`;
  - `store.limit` and `store.io_error` events are emitted;
  - checked arithmetic is used for offsets, sizes and context;
  - a test-only hook (unexported `testHooks` struct) supports commit cancellation, `max_page_count` and the
    injected free-space function. Three more hooks serve the read-side tests: `queryHold` (a read holds its
    connection at its start; `TestPerContactMemoryChild` uses it to fill every reader), `wrapReader` (a read runs
    through a wrapper; the plan and scale tests count pages with it, and the memory test wraps it to hold listings)
    and `onRead` (a read reports its connection; the page meter uses it). The wrapper may implement
    `firstRowHolder`, which the sender listing calls after its first row (`TestPerContactMemoryChild`, four
    overlapping listings).
- [X] T053 [US7] Create `OPERATIONS.md` at the repository root with a "Storage" section and these subsections:
  - **Files and privacy:** what `whatsapp.db`, `-wal` and `-shm` hold; the data classes (device credentials,
    contacts and LID mappings, text and captions, media keys); owner-only access is not encryption at rest;
    supported local filesystems and rejected path forms; network filesystems are unsupported.
  - **Growth and retention:** messages are kept indefinitely; the database, WAL and backups grow; the 256 MiB
    reserve and what happens when it is reached.
  - **Backup:** the quiesced procedure from FR-047:
    1. stop the process and every connection;
    2. confirm a clean close (no `-wal`, or a zero-length `-wal`);
    3. otherwise copy the whole stopped file set together;
    4. copy into a private destination;
    5. protect the backup like live credentials.

    Copying only the main file during writes is unsupported. Directory locks do not coordinate copies.
  - **Restore:** into a new private directory, offline, checked with any SQLite shell (for example `sqlite3`):
    `PRAGMA integrity_check`,
    `PRAGMA foreign_key_check`, `SELECT * FROM mcp_version`, `SELECT version, compat FROM whatsmeow_version`.
    Replace live storage only after the checks pass, and keep the original until then. A restored device store
    must never connect beside its original; live account checks or re-pairing need separate permission.
  - **Recovery after a failed upgrade:** read the error's completed steps; restart with the same binary to retry;
    there is no downgrade; restore from backup if needed.
  - **Recovering from a refused start:** for each refusal code, what it means and what to do, never deleting
    anything first:
    - `rollback_journal`: stop; copy the whole file set to a private place; open the database once with an SQLite
      shell (`sqlite3 whatsapp.db "PRAGMA integrity_check"` on the live file in the data directory, not on the
      copy), which rolls back a journal that holds a transaction.
      SQLite leaves an empty or zeroed journal where it is, because it is not "hot", so then move any
      `whatsapp.db-journal` that is still there into the private copy; start again. T054 follows this step once
      with a 0-byte and once with a zeroed journal and checks that the next start succeeds;
    - `orphan_sidecar` (a sidecar next to a missing or empty `whatsapp.db`): move the sidecars to a private place
      and restore `whatsapp.db` from backup; with no backup, keep the moved files and start fresh, which pairs a
      new device;
    - `schema_too_new`, `schema_invalid`, `not_database`: keep the files, use the binary that wrote them or
      restore from backup;
    - `unsafe_artifact`, `not_private`, `unsupported_path`: fix ownership, access or the path as the error's fix
      text says;
    - `memory_limit_unavailable`: the binary could not switch on SQLite's memory limit. It is a build or
      packaging defect, not a data problem: keep the files and use an official release;
    - `corrupt` (an operation reached a damaged page): stop, keep the file set, and follow "Restore" with
      `PRAGMA integrity_check` on a copy before deciding what to restore.
  - **Decommissioning (FR-050):** stop; inventory only this installation's database, sidecars, logs and backups;
    unlinking does not guarantee erasure on SSDs, snapshots or backups; key destruction depends on the operator's
    storage.
  - **Operator responsibilities:** encryption, backup destinations and retention, recovery-point and
    recovery-time objectives, restore rehearsals.

  Add a one-line link to `OPERATIONS.md` in `README.md`.
- [X] T054 [US7] `internal/store/restore_test.go` (SC-010):
  1. Seed a device and 5,000 messages, close cleanly (quiesce).
  2. Copy the file set into a new directory made by `private.MkdirAll`.
  3. Open the copy offline with a raw read-only connection and run `integrity_check` = `ok`, `foreign_key_check` =
     no rows, both versions, and the counts.
  4. Open it with `store.Open` and compare the dumps with the original.
  5. Journal recovery, with three journals in turn: a 0-byte `-journal`; a zeroed 512-byte one; and a hot one,
     made by a child process that opens a copy of the database in `journal_mode=DELETE` with a small cache,
     updates enough rows inside one transaction to spill pages to the journal, and is killed before it commits.
     Each time `store.Open` refuses with `rollback_journal`. Then follow T053's `rollback_journal` step on the live
     `whatsapp.db`, not on the copy: copy the file set aside, open `whatsapp.db` once through the driver, then
     move any journal left into the private copy. The next `store.Open` succeeds, and the data equals the state
     before the killed transaction, which shows the hot journal was rolled back.

  Also follow the `OPERATIONS.md` procedure manually once on Windows and once on Linux, and record the result.
  _Partial: the manual Linux run is open, see T073._
- [X] T055 [P] [US7] Create `internal/store/testdata/cmd/bumpversion/main.go` (sets `mcp_version` or
  `whatsmeow_version.compat` in a given file, for quickstart step 4) and
  `internal/store/testdata/cmd/seed/main.go` (writes N synthetic messages through `store.Open`). Both refuse to
  run on a path outside a directory whose name starts with the system temp directory, unless `-force` is given.
- [X] T056 [US7] Manual cross-identity check (SC-008), following quickstart step 6 on Windows (`runas` with a
  second local account) and on Linux (a second user). Besides reading and writing the artifacts, the second
  identity tries to create a file in the data directory and to rename a file over `whatsapp.db`; both must be
  denied (abuse case 2). Record the commands, date, the denied output and the `icacls` and `ls -l` listings in
  `verification.md`. Mark macOS "not verified" unless it was run.
  _Partial: Linux is open, and so are the live-store steps on both systems, see T067._

**Checkpoint**: all P1 stories are complete and hardened.

---

## Phase 8: User Story 5 - A single message is found by chat and ID (Priority: P2)

**Goal**: exact lookups by `(chat_jid, id)`, lookups by ID alone with an ambiguity error, and media metadata.

**Independent Test**: the same ID stored in two chats and another ID in one chat, each looked up both ways.

### Tests for User Story 5 ⚠️

- [X] T057 [P] [US5] `internal/store/lookup_test.go`:
  - `(C, X)` is found; a missing pair gives `*NotFoundError` naming the chat and the ID;
  - an ID in exactly one chat is found by ID alone; none gives not found;
  - an ID in C1 and C2 gives `*AmbiguousIDError` listing C1 and C2 sorted;
  - 101 chats gives `ErrResourceLimit`;
  - `MediaInfo` on a media message returns all eight metadata fields; on a text message it returns
    `ErrNotMedia`.

### Implementation for User Story 5

- [X] T058 [US5] Implement `GetMessage`, `GetMessageByID` and `MediaInfo` in `internal/store/queries.go`:
  - `GetMessageByID` selects `chat_jid` for `id = ?` on `messages_id` with `LIMIT 101` to detect more than 100 matches without an
    unbounded read (an ID in 100 chats lists them, in 101 it is `ErrResourceLimit`);
  - `MediaInfo` returns `MediaMeta` with nil length when NULL, and `ErrNotMedia` when `media_type = ''`;
  - add sub-benchmarks for `GetMessage`, `GetMessageByID` and `MediaInfo` to `BenchmarkQueries` in
    `internal/store/bench_test.go` (from T046), so that SC-004 covers FR-021–FR-023.

**Checkpoint**: US5 passes on its own.

---

## Phase 9: User Story 6 - Merge a LID chat into the phone-number chat (Priority: P2)

**Goal**: `MergeLID` rewrites every message sent under a LID to its phone JID, in every chat, and moves the `@lid`
chat into the phone-number chat, atomically and idempotently.

**Independent Test**: seed a LID chat and a phone chat with overlapping and distinct messages, merge, check the
result, then merge again and check nothing changes.

### Tests for User Story 6 ⚠️

- [X] T059 [P] [US6] `internal/store/merge_test.go`:
  1. L and P: after the merge P holds all messages, L is gone, P's activity is the later one, and P's name is the
     better "by the name rule (higher quality, then newer name time, then the byte-wise greater name)";
  2. only L: P is created with L's time and messages, and with L's name when it is saved or real; when L has only a
     fallback, P is created without a name. With both chats, a newer fallback of L never replaces P's older
     fallback;
  3. both hold X: P keeps its own copy and L's is dropped;
  4. messages whose sender is the LID, in L and in two group chats, get sender = phone JID and the phone filled
     in, in every chat; messages from other senders in those groups are unchanged;
  5. no chat L, but group messages sent under the LID: their senders are rewritten, and no chat is created or
     removed;
  6. an injected failure part-way, after the sender rewrite and before the chat move (test hook), leaves every
     chat and message exactly as before (dump compare);
  7. a second merge is a no-op without error.

  Plus:
  - merging a chat into itself, a non-`@lid` source or a non-`@s.whatsapp.net` target gives `*ValidationError`;
  - a merge under disk-low gives `ErrDiskLow` with nothing changed.

### Implementation for User Story 6

- [X] T060 [US6] Create `internal/store/merge.go` with `MergeLID(ctx, lidJID, phoneJID)`: validate, then one
  writer transaction under admission and the disk guard:
  1. `UPDATE messages SET sender_jid = P, sender_phone = <digits> WHERE sender_jid = L`, across all chats (uses
     `messages_sender_jid`);
  2. read chat L; if it is absent, commit here;
  3. upsert P with `max` activity and `phone` from the JID; offer L's name by FR-018 only when its quality is saved
     or real;
  4. `INSERT INTO messages (...) SELECT ... FROM messages WHERE chat_jid = L ON CONFLICT (chat_jid, id) DO NOTHING`,
     with `chat_jid = P`;
  5. `DELETE FROM messages WHERE chat_jid = L`;
  6. `DELETE FROM chats WHERE jid = L`.

  Report through `commitOutcome`.

  Add a `MergeLID` sub-benchmark to `BenchmarkQueries` (T046): merge a seeded 20,000-message `@lid` chat into its
  phone chat on the SC-004 database. Record the time in `verification.md`; above 5 s, half the 10 s operation
  deadline, the merge needs to be split before 004 relies on it.

**Checkpoint**: all stories are complete.

---

## Phase 10: Polish & Cross-Cutting Concerns

- [X] T061 Run `go mod tidy` and `go mod verify`. Confirm no `mattn/go-sqlite3` or `litestream` in `go list -m all`.
  Run `CGO_ENABLED=0` builds for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64, and
  record the binary sizes. Check that `go list -m modernc.org/libc` equals the libc version that
  `modernc.org/sqlite`'s own `go.mod` requires (`go mod graph | grep '^modernc.org/sqlite@'`), and add the same
  check as a step of CI's `check` job, so a bump that moves libc alone fails there.
- [X] T062 Run `gofmt -l .` (empty), `go vet ./...`, `go test ./...` (read each exit code directly, not through a
  pipe) and `govulncheck ./...`. Record the results. Record any unreachable finding as an exception in
  `verification.md` with owner, compensating controls and expiry; a reachable one blocks the feature.
- [X] T063 Run every step of `specs/002-sqlite-storage/quickstart.md`, including the local benchmark (step 5,
  SC-004: record each `ns/op` and the machine) and the 60 s fuzz runs (step 2). Record the results in
  `verification.md`.
  _Partial: steps 6 and 7 ran on Windows only, see T067 and T073._
- [X] T064 [P] Update `AGENTS.md` "Current state": feature 002 adds `internal/store` (one file, two schema
  histories, approved limits) and `OPERATIONS.md`, and `stdio`/`serve`/`pair` open the store. Keep the existing
  style.
- [X] T065 [P] Review all new code for comments that cite requirement, decision or finding IDs (none allowed),
  and for any `slog` call or error that formats message text, SQL or raw driver errors. Fix what is found.
- [X] T066 Check `git ls-files --eol` for `w/crlf` (none allowed) and that `verification.md` marks each
  platform-specific control as passed or "not verified". Mark all tasks `[X]` in this file as they are completed.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: needs T001. T014 and T015 are gates and must pass on all three OSes before Phase 3.
- **US1 (Phase 3)**: needs Phase 2. It is the MVP.
- **US2 (Phase 4)**: needs US1 (`Open`, preflight and the runner exist).
- **US3 (Phase 5)**: needs US1 (`Open`); independent of US2.
- **US4 (Phase 6)**: needs US3 for seeding through `StoreMessages`. Contacts need T016.
- **US7 (Phase 7)**: needs US1–US4. Its enforcement task T052 touches their files.
- **US5 (Phase 8)**: needs US3 (writes) and the shared query code from T042.
- **US6 (Phase 9)**: needs US3. Its admission and disk checks come from Phase 2.
- **Polish (Phase 10)**: needs everything.

### Within Each User Story

Tests first (they must fail), then implementation, then the checkpoint run on CI's three OSes.

### Parallel Opportunities

- Phase 1: T002, T003 and T004 together.
- Phase 2: T005–T013 and T015 are all different files and run together; T014 needs T001 and T006 (`ErrNotDatabase`); T016 follows T005,
  T008 and T009.
- US1: T017, T018 and T019 together; T021 alongside T020.
- US2: T026–T029 together.
- US3: T032–T036 together.
- US4: T039–T041 and T046 together.
- US7: T047–T051 and T055 together.
- After US3, US5 (T057–T058) and US6 (T059–T060) can run in parallel with US4/US7. They touch different files,
  except T058, which adds to `queries.go`, so coordinate it with T043/T044.

---

## Parallel Example: User Story 3

```text
Task: "T032 messages_test.go: re-store, media keep, chat creation, sender normalisation, batch limits"
Task: "T033 chats_test.go: name rule with quality, time and byte-wise tie-break"
Task: "T034 order_test.go: 20 random orders give identical dumps"
Task: "T035 concurrency_test.go: 16×100 writers with readers, no 'database is locked'"
Task: "T036 crash_test.go: killed child, every committed write present"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 + Phase 2, with both gates recorded.
2. Phase 3 (US1): the binary creates and opens one private database with both schemas.
3. **Stop and validate**: T017–T019 green on Windows, Linux and macOS.

### Incremental Delivery

1. US2: safe evolution and refusals, so the database format can be shipped.
2. US3: correct ingest writes. 004 can start building against the store.
3. US4: queries. 005/006 can start.
4. US7: limits, diagnostics, recovery and the operator guide. Feature acceptance needs it.
5. US5 and US6: single-message lookups and the LID merge.
6. Polish: tidy, full checks, quickstart, evidence.

---

## Notes

- [P] = different files and no unfinished dependency. [USn] maps to spec.md user stories.
- Commit after each task or logical group, with Conventional Commits that explain why, and no attribution lines.
- Never act on a real WhatsApp account. Every device here is synthetic.

## Phase 11: Convergence

- [X] T067 Run quickstart step 6 on a Linux machine and, again, on Windows, this time with the store open (a `serve` or `stdio` process holding the data directory, so `-wal` and `-shm` exist). As a second user: `cat` and `ls` the data directory, read and write `whatsapp.db`, the lock file, `-wal` and `-shm`, create a file in the directory, and rename a file over `whatsapp.db`; all must be denied. On Windows a running store holds `whatsapp.db` open, so a rename or delete may fail with a sharing violation whatever the access rights: record the error text, count only "Access is denied" as a denial, and keep the rename and delete steps on the closed store if the open one cannot show it. Keep the process running with its standard input held open (for example `sleep 600 | whatsapp-mcp stdio --data-dir <new empty directory>`), on an unpaired data directory only, so it never pairs or connects. Record the commands, date, denied output and `ls -l` / `icacls` listings in `verification.md`, per SC-008 (partial)
- [X] T068 After T069 and T070, push the branch and confirm that CI's `go test` (run without `-short`) is green on ubuntu, windows and macOS with the new `limits_test.go` tests (`TestHeavyContactListing` seeds 500 MiB; `TestPerContactMemory` runs two children with up to 400,000 messages each, as T070 leaves it; the run recorded is the one made after the last test change). If a runner cannot run them, only `TestHeavyContactListing` and `TestPerContactMemory` may be changed, through an environment variable that CI sets for those two tests alone and never through a global `-short`, so the WAL-bound and long-reader tests of T049 keep running on all three systems. The seed of `TestHeavyContactListing` stays at 2,000 rows of 256 KiB at offset 1,800. Record every reduction in `verification.md` for the owner to accept, per SC-002 (partial)
- [X] T069 Add a test hook in the row loop of the sender listing in `queries.go` (`wrapReader` cannot stop after the first row, because `querier` returns a concrete `*sql.Rows`) that holds a listing after its first row has been read, use it in `TestPerContactMemoryChild` so that 2 deep and 2 shallow listings overlap at their peak, and record the measured growth (at most 1 MiB), per T047 step 3 (partial)
- [X] T070 Find the cause of the per-operation SQLite memory floor that rises from about 21 KB to about 98 KB after the first tens of thousands of writes (a `GetMessage` shows it too). First experiment: run `PRAGMA wal_checkpoint(TRUNCATE)` before the floor measurement and see whether it drops back. Then replace the floor-subtracting rule in `TestPerContactMemoryChild`, which cannot detect growth with history, with an absolute assertion that does not subtract a quantity that itself may grow: add a third size point (for example 400,000 messages) or show that the floor plateaus, and assert that the growth at every size and offset stays at most 256 KiB and rises by at most 16 KiB between the last two sizes (allocator rounding and slot reuse make a single reading differ by a few hundred bytes). Amend T047 with the measured rule and the cause, per T047 step 2 and SC-009 (partial)
- [X] T071 Correct the wording of T058 (`LIMIT 102`, "more than 101") to the contract: `GetMessageByID` lists at most 100 chats and 101 is `ErrResourceLimit`, as `queries.go` and `TestGetMessageByIDLimit` already do, per FR-022 (contradicts)
- [X] T072 Record `queryHold`, `wrapReader`, `onRead` and the hook that T069 adds in T052's list of test-only hooks, with the test that uses each; remove any that T069 makes redundant, per T052 (unrequested)
- [X] T073 Follow `OPERATIONS.md` by hand on a Linux machine (backup, restore into a new private directory, the offline checks, and the `rollback_journal` recovery step), and record the result in `verification.md`, per T054 and quickstart step 7 (partial)
