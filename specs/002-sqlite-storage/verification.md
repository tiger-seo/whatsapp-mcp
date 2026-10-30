# Verification: SQLite storage

## Environment

| Item | Value |
| --- | --- |
| Local machine | Windows 10 Pro 10.0.19045, amd64, Go 1.27.3, `CGO_ENABLED=0` |
| Driver | `modernc.org/sqlite` v1.61.0, `modernc.org/libc` v1.79.0 (the version the driver requires) |
| whatsmeow | `v0.0.0-20261027104441-1eec1136a50b`, device schema version 16 |
| Implementation run | 2026-10-28, branch `002-sqlite-storage` |

The implementation run of 2026-10-28 covered every task that the parked findings P1-P6 did not block. The runs of
2026-10-30 added the queries, the scale, memory and limit tests, the hardening tests and the final checks. Open: the
Linux and macOS runs of the manual checks (T054, T056, T063), which are marked "not verified".

## Gate results (committed view, device upgrade transactions)

T014, the committed view, implemented ahead of the design review's pass at the owner's request (2026-10-28) so that
CI can produce the macOS evidence the gate needs.

| Date | Platform | Command | Result |
| --- | --- | --- | --- |
| 2026-10-28 | windows/amd64, Go 1.27.3, modernc.org/sqlite v1.61.0 | `CGO_ENABLED=0 go test ./internal/store/ -count=1 -v` | PASS: `TestCommittedViewChangesNothing` 8/8 fixtures (unchanged files, same answer as SQLite), big-endian twin, 3 refusals, 4 ignored logs, 9 fuzz seeds |
| 2026-10-28 | windows/amd64 | `CGO_ENABLED=0 go test ./internal/store/ -run '^$' -fuzz FuzzWALScan -fuzztime 30s` | PASS: 2,244 executions, no failure |
| 2026-10-28 | CI run 40671433110 (commit 915c280): ubuntu-latest, windows-latest, macos-latest (arm64) | `go test -timeout 15m ./...` | PASS on all three: `internal/store` ok (0.268 s, 1.068 s, 0.160 s), every other package ok; `check` job (gofmt, vet, govulncheck, five cross-builds) PASS |
| 2026-10-28 | windows/amd64 (commit after a024055: a log header SQLite ignores is ignored) | `CGO_ENABLED=0 go test -count=1 ./...` | PASS: all packages; `TestCommittedViewChangesNothing` 9/9 fixtures, including a zeroed `-wal` next to a clean database; linux and macOS not yet re-run on this change |
| 2026-10-28 | windows/amd64 (commit after 9a82bfe: a log no longer than its header is ignored, as SQLite does) | `CGO_ENABLED=0 go test -count=1 ./internal/store/` | PASS |
| 2026-10-28 | CI run 40677470454 (commit f78d7f7, includes 79e031f and the 32-byte log change): ubuntu-latest, windows-latest, macos-latest (arm64) | `go test -timeout 15m ./...` | PASS on all three: `internal/store` ok (0.382 s, 1.912 s, 0.159 s); `check` job PASS |
| 2026-10-28 | windows/amd64 (local), T015 | `CGO_ENABLED=0 go test -count=1 -run TestDeviceUpgradeStepsAreTransactional ./internal/store/` | PASS: no upgrade file in the pinned whatsmeow has a `-- transaction:` header other than `on`; the highest target version (16) equals `len(upgrades.Table)` |

## Automated tests per OS

| Date | Platform | Command | Result |
| --- | --- | --- | --- |
| 2026-10-28 | windows/amd64 (local) | `CGO_ENABLED=0 go test -count=1 -timeout 15m ./...` | PASS, exit 0: `cmd/whatsapp-mcp`, `internal/buildinfo`, `config`, `lock`, `private`, `store` (about 34 s for `store`); 341 top-level tests passed, 0 failed, 18 skipped (child-process helpers and symlink tests that Windows cannot create without a privilege) |
| 2026-10-28 | windows/amd64 (local) | `gofmt -l .` and `go vet ./...` | no output, exit 0 |
| 2026-10-30 | windows/amd64 (local), T011 after removing the deep slots | `CGO_ENABLED=0 go test -count=1 -timeout 15m ./...`; `gofmt -l .`; `go vet ./...` | PASS, exit 0, every package; `TestGate*` 7/7 (`SeventeenthWaits`, `WaiterCap`, `WaitEnds`, `CancellationFreesSlot`, `OperationLimit`, `Close`, `NoGoroutineLeak`); gofmt and vet clean; `grep -rn deep` over `internal/` and `cmd/` finds no store code |
| 2026-10-30 | windows/amd64 (local), T039-T041 before T042-T045 | `CGO_ENABLED=0 go test -count=1 -v -run '<the 21 US4 tests>' ./internal/store/`, then `go test -count=1 ./... -skip '<the same>'` | red as intended: 21 of 21 new tests fail, every failure is the skeleton's "not implemented" (53 lines), no fixture error; every other test passes, exit 0. `seedContacts` fixed to pass the first name before the full name, as whatsmeow's SQL store expects |
| 2026-10-30 | windows/amd64 (local), T042-T045 and the plan and scale parts of T046 | `CGO_ENABLED=0 go test -count=1 -timeout 15m ./...`; `gofmt -l .`; `go vet ./...` | PASS, exit 0, every package (`store` 113.8 s); the 21 US4 tests are green; `TestQueryPlans` 21/21 on 106,000 messages (every lookup a SEARCH on a named index, sender listings `MERGE (UNION ALL)` over the two sender indexes, context queries `(time,id)<(?,?)`, no temporary b-tree outside `SearchContacts`); `TestQueryScale` PASS: 14 constant-cost cases (largest growth 1,055 vs 1,004 pages), 4 linear cases (per-row cost at full ≤ at a tenth, `SearchContacts` 0.3463 vs 0.3493), 4 backstop cases (slowest median 0.47 s at 300,000 messages); gofmt and vet clean; no CRLF file. `BenchmarkQueries` is written and compiles; it has not been run |
| 2026-10-30 | windows/amd64 (local), T046 complete, T057-T058 | `CGO_ENABLED=0 go test -count=1 -timeout 15m ./...`; `gofmt -l .`; `go vet ./...` | PASS, exit 0, every package (`store` 118.8 s); `TestGetMessage`, `TestGetMessageByID`, `TestGetMessageByIDLimit` (100 chats listed, 101 is `ErrResourceLimit`), `TestMediaInfo` green; `TestQueryPlans` now 24/24 and `TestQueryScale` 6 more constant-cost lookups (`GetMessage` 23 vs 25 pages, `GetMessageByID` 28 vs 31) |
| 2026-10-30 | windows/amd64 (local), T047-T052, T060, T062 | `gofmt -l .` (0 files); `go build ./...`, `go vet ./...`, `go test -count=1 ./...` (179 s), each read as its own exit code | all exit 0. New: `TestLimitsAtTheBoundary` 26 constants, each accepted at the limit and rejected beyond it with the tables unchanged; `TestLimitsOfTheListings` (aggregate context 100 x 10/10 accepted and 200 x 10/10 rejected, 100 chats with one ID listed and 101 `ErrResourceLimit`, 100 rows of 256 KiB accepted and 130 `ErrResourceLimit`); `TestHeavyContactListing` (2,000 messages of 256 KiB, offset 1,800, by phone and by LID); `TestSearchContactsHeapLimit` (needs 639,424 B for 5,000 contacts; with the limit at 521,096 B the call fails with `ErrResourceLimit` "memory"); `TestPerContactMemory` both splits (below); `TestCancellationFreesTheSlot`; abuse cases 4, 5 (sort, overflow), 8; `TestNoSecretsInLogsOrErrors` now also covers every query, a search marker, a failed migration, event fields and control characters |
| 2026-10-30 | windows/amd64 (local), T063 steps 2 and 5 | the three `-fuzz` runs of step 2 (60 s, 30 s, 60 s) and the benchmark of step 5 | fuzz: all exit 0; benchmark: see "Benchmark (SC-004)" |
| 2026-10-30 | windows/amd64 (local, TEMP on D:), T069, T070, T072 | `gofmt -l .` (0 files); `go vet ./...`; `go test -count=1 ./...` (store 201.8 s), each as its own exit code | all exit 0. `TestPerContactMemory` now measures 50,000, 200,000 and 400,000 messages for both splits and holds four listings after their first row (see "Per-contact memory"). C: had 20 MB free during one run, so the test temp directory moved to D: |
| 2026-10-28 | linux/amd64, darwin/amd64 (local, vet only) | `GOOS=linux go vet ./...`, `GOOS=darwin go vet ./...` | exit 0: the Unix and macOS test files compile; they have not been run |
| not run | linux, macOS | CI on the next push | not verified: file-mode tests (`TestExistingDatabaseWithOpenModeIsRefused`, `TestFifoArtifactIsRefused`, `TestNewDatabaseIsMode0600`, `TestCheckFileModes`), the macOS ACL check in `private_darwin.go` (`syscall.Syscall6(SYS_GETATTRLIST)`, written without a Mac) and the symlink and hard-link subtests |
| 2026-10-28 | windows/amd64 (local) | quickstart step 2, `-fuzz` on `FuzzValidateMessage` 60 s, `FuzzNormalizePhone` 30 s, `FuzzWALScan` 60 s | PASS: 860,742 / 522,880 / 3,361 executions, no failure, no new crasher files |
| 2026-10-28 | windows/amd64 (local) | quickstart step 3: `stdio --data-dir` on a new directory, twice | exit 0 both times; first start logs `store.preflight fresh=true`, `store.upgrade schema=device from=0 to=16`, `store.migrate schema=messages from=0 to=1`, `store.ready`, with UTC times (`...Z`); second start `fresh=false` and no upgrade or migrate lines; `icacls` lists this user, SYSTEM and Administrators |
| 2026-10-28 | windows/amd64 (local) | quickstart step 4: `bumpversion` messages 99, device 99, then a file of random bytes | exit 1 each time with the schema and both versions, or "is not a usable SQLite database"; `sha256sum -c` OK and no `-wal`/`-shm` appeared |

Windows-only evidence from the same run: the path forms `\\server\share\x`, `\\?\C:\x`, `\\.\C:\x` and `C:\x:stream` were refused; a `subst` drive resolved to its real target and was accepted; junctions at `whatsapp.db`, `-wal`, `-shm` and `-journal` were refused with nothing created in their targets. A `net use` drive mapped to `\\localhost\<share>` was not tried: not verified.

## Benchmark (SC-004)

2026-10-30, Intel Core i7-8550U 1.80 GHz (8 threads), Windows 10 Pro 10.0.19045, windows/amd64, Go 1.27.3:
`go test ./internal/store -run '^$' -bench BenchmarkQueries -benchtime 20x -count 3`, exit 0, on 100,000+ messages.
Every query is below 200 ms. Slowest and fastest of the three runs, in ns/op:

| Query | ns/op |
| --- | --- |
| ListChats activity / name / search | 8.5-8.9 ms / 8.5-8.9 ms / 8.8-10.3 ms |
| GetChat / DirectChat | 0.29-0.38 ms / 0.29-0.32 ms |
| ContactChats phone / LID | 0.94-0.97 ms / 0.90-0.93 ms |
| SearchContacts name / number | 5.8-6.3 ms / 5.9-6.0 ms |
| ListMessages all / chat / sender | 8.1-9.0 ms / 7.9-8.2 ms / 8.4-12.4 ms |
| ListMessages search / with 5+5 context | 65-68 ms / 103-106 ms |
| MessageContext | 1.0-1.6 ms |
| GetMessage / GetMessageByID / MediaInfo | 0.20-0.22 ms / 0.25-0.26 ms / 0.24-0.25 ms |
| LastInteraction phone / LID | 0.41-0.42 ms / 0.42-0.47 ms |
| MergeLID of a 20,000-message chat | 1.74 s, 1.76 s, 2.77 s (below the 5 s bound, so no split is needed) |

### Per-contact memory (T047), 2026-10-30

SQLite's high-water mark above the memory in use, with every reader's page cache filled first. A sender listing, 200
rows, identifiers of realistic length (chat JIDs 120 B, message IDs 150 B). Both splits gave the same numbers.

| Messages of the contact | Offset | Growth (B) | A `GetMessage` at the same moment (B) |
| --- | --- | --- | --- |
| 50,000 | 0 / 49,800 | 21,488 / 46,064 | 21,488 |
| 200,000 | 0 / 49,800 / 99,800 | 97,952 each | 97,952 |
| 400,000 | 0 / 49,800 / 99,800 | 97,952 each | 97,952 |

Updated 2026-10-30 (T069, T070). Two deep and two shallow listings at once, held after their first row so that the
peaks overlap: 391,808 B (even split) and 365,600 B (all under the phone), limit 1 MiB. `LastInteraction`: 93,592 B
in 1.9-6.0 ms (limits 1 MB and 50 ms). Every listing is below 256 KiB, and from 200,000 to 400,000 messages the growth
does not rise at all (limit +16 KiB). A fixed cost of about 98 KB per operation appears after the first tens of
thousands of writes and is the same in a `GetMessage`; `PRAGMA wal_checkpoint(TRUNCATE)` before the measurement does not
change it, so it does not follow the WAL. Its cause is not found, but it plateaus (identical at 200,000 and 400,000),
so the test asserts the absolute bound and no longer subtracts it. The hook that holds the listings is the
`firstRowHolder` check in `messageKeys`.

## Cross-identity evidence (SC-008)

**Windows: passed, 2026-10-30.** Linux and macOS: not verified (no such machine here).

Setup: a throwaway standard local account `wa002test` (created and deleted with `net user`, profile folder removed;
`net user` is the same as before). The data directory was `D:\wa-002-t056\data`, made by `stdio --data-dir` plus
`testdata/cmd/seed -n 100`; its parent `D:\wa-002-t056` is readable by the Users group, so a denial comes from the
data directory's own permissions. `icacls D:\wa-002-t056\data` and `...\whatsapp.db` list only SYSTEM, Administrators
and the owner (`NOTEBOOKPRO\Tiger`, full control), with no entry for Users or Everyone.

Commands, run as `wa002test` through `Start-Process -Credential` (`whoami` printed `notebookpro\wa002test`). The system
language is Russian, so the messages are translated here:

| # | Command | Result |
| --- | --- | --- |
| 1 | `type whatsapp.db` | Access is denied |
| 2 | `dir D:\wa-002-t056\data` | nothing listed ("File not found") |
| 3 | `echo x>> whatsapp.db` | Access is denied |
| 4 | `echo x> data\newfile.txt` (create a file in the data directory) | Access is denied |
| 5 | `type whatsapp-mcp.lock` | Access is denied |
| 6 | `move /Y <file owned by wa002test> data\whatsapp.db` (rename over the database) | Access is denied |
| 7 | `del whatsapp.db` | "Cannot find the file" (the directory cannot be listed, so the file is not visible to it) |
| 8 | `echo x> whatsapp.db-wal` (create a sidecar) | Access is denied |

Afterwards `sha256sum whatsapp.db` equals the value before (`30d19946...be77`), and the directory holds only
`whatsapp.db` and `whatsapp-mcp.lock`: nothing was created, replaced or deleted. Abuse case 2's claim that an unrelated
identity cannot create or rename entries in the data directory is shown on Windows.

## Backup/restore rehearsal (SC-010)

| Date | Platform | What | Result |
| --- | --- | --- | --- |
| 2026-10-28 | windows/amd64 | `TestRestoredBackupIsValidatedOffline`: 5,000 messages and a device, copied into a new private directory, `integrity_check` = ok, `foreign_key_check` empty, versions 1 / 16, store dump equals the original | PASS |
| 2026-10-28 | windows/amd64 | `TestRollbackJournalRecoveryProcedure`: empty, zeroed and hot `-journal` each refused with `rollback_journal`; the `OPERATIONS.md` steps on the live file, then a start that succeeds with the data of the state before the killed transaction | PASS |
| 2026-10-30 | windows/amd64 | by hand, quickstart step 7: `stdio --data-dir` on a new directory (exit 0), `testdata/cmd/seed -n 5000`, stop, copy `whatsapp.db` (no `-wal`: clean close) to a backup directory, copy that into a new restore directory, then Python's `sqlite3` module (mode=ro) on the restored file: `integrity_check` = ok, `foreign_key_check` = no rows, `mcp_version` = 1, `whatsmeow_version` = (16, 8), 5,000 messages; `store.Open` on the restored copy succeeds | PASS |
| not run | linux | following `OPERATIONS.md` by hand | not verified: no Linux machine here; CI runs the automated rehearsals |

## Dependencies and govulncheck

- `go mod verify`: all modules verified. `go list -m all` lists `github.com/mattn/go-sqlite3` (a test-only requirement of
  whatsmeow); `go list -deps ./cmd/whatsapp-mcp` contains neither it nor `litestream`, so the binary does not link it.
- Cross-builds, `CGO_ENABLED=0`, 2026-10-28: windows/amd64 18,790,400 B, linux/amd64 18,728,393 B, linux/arm64 18,097,970 B,
  darwin/amd64 18,688,128 B, darwin/arm64 18,208,994 B.
- libc: `go list -m modernc.org/libc` = v1.79.0, the version `go mod graph` shows for `modernc.org/sqlite.61.0`. CI gained a
  step that fails if the two differ.
- `govulncheck` v1.9.0 (the version CI pins), 2026-10-30, `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`, exit 0: "Your
  code is affected by 0 vulnerabilities". One finding in a required module that the code does not call: GO-2026-5932,
  `golang.org/x/crypto/openpgp` (in `golang.org/x/crypto@v0.58.0`, no fix available). Exception: owner s.shupylo,
  compensating control: nothing imports `openpgp` (the scan finds no call), expiry 2026-12-31 or the next
  `golang.org/x/crypto` bump, whichever comes first.

## Limitations / not verified

- Parked findings P1-P6 were resolved in the design on 2026-10-30 (`e2356fe`, see `tasks.md` § Parked findings). They
  blocked T039-T046 (queries, query plans, scale), T047 (limit boundaries and memory), the `enterDeep` part of T011 and
  T048, and, through T042, T057-T058 (single-message lookups); the design review of `49ff123` cleared the resolution
  (see "Owner reviews"), so those tasks can start.
- Work that reached P6 while it was parked: commit `9c626d1` implemented `gate.enterDeep`, its constants and tests
  (`TestGateDeepSlots`, `TestGateDeepWaitersDoNotBlockAdmission`) and wrote `admission_test.go` (T048). The resolution
  makes the deep slots obsolete. T011 removed that code on 2026-10-30 (`enterDeep`, the `deep` semaphore,
  `enterWithin`, `deepSlots`, `deepPageEnd` and the two tests); the code review must confirm it is gone and that
  `admission_test.go` matches the revised T048, which is done.
- T052 found one inconsistency, fixed: `UpdateChats` rejected 1,001 updates as a validation error where `StoreMessages` gives
  `ErrResourceLimit` for 1,001 messages; both are limits now (`chats.go`, `chats_test.go`).
- T054: the automated rehearsals and the manual Windows run pass; the manual Linux run is not verified. T056 (cross-identity) passed on Windows;
  Linux and macOS are not verified.
- T063: steps 1, 2, 3, 4 and 5 are recorded; steps 6 and 7 are done on Windows only.
- Windows symlinks could not be created without a privilege, so those subtests skip locally; the junction tests cover
  the reparse-point rule on Windows, and CI covers symlinks on Linux and macOS.
- A read-only database is refused by a write probe at open (`readonly`), because SQLite opens a read-only file read-only
  without an error. SQLite may leave empty `-wal` and `-shm` files of its own in that case; nothing that existed is changed.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-10-28 | ac42e1f | BLOCK | |
| design | 2026-10-28 | 0a86edf | BLOCK | |
| design | 2026-10-28 | 3ab978f | BLOCK | |
| design | 2026-10-28 | 0e5d6e0 | CONCERNS | Accepted 2026-10-28: both medium and all low findings fixed in the design before implementation; SQLite's memory budget accepted as holding up to about 1 million messages from one contact (research R16) |
| design | 2026-10-28 | fdd03f0 | BLOCK | |
| design | 2026-10-28 | fe48fce | BLOCK | |
| design | 2026-10-28 | 8d38457 | BLOCK | Parked at 0a307f3: P1 chat-and-sender query shape, P2 scale-test 3x rule, P3 page counter, P4 memory slope split, P5 empty own LID, P6 waiter-cap order |
| design | 2026-10-30 | e2356fe | CONCERNS | |
| design | 2026-10-30 | 49ff123 | PASS | |
| design | 2026-10-30 | af5e2e3 | CONCERNS | |
| design | 2026-10-30 | 239a3d4 | CONCERNS | Accepted 2026-10-30: the four low findings (T068 after T069 and T070, a stated margin in T070, "Access is denied" only in T067 on Windows, the partial notes moved to the end of T047, T054, T056 and T063) are fixed in the first commit of the implement run |
