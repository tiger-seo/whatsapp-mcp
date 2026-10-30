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
the macOS runs of the manual checks (T056), which are marked "not verified"; Linux was run on WSL2.

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
| 2026-10-30 | CI run 41083638694 (commit a286cb1): ubuntu-latest, windows-latest, macos-latest | `go test -timeout 30m ./...` (T068) | PASS on all three: ubuntu 2 min 23 s, macos 5 min 19 s, windows 16 min 33 s; `check` job PASS. Earlier runs on the way found and fixed: the macOS ACL check refused every directory on the current runner image because it took any reply longer than the length word for an ACL (`internal/private`, now parsed, with macOS-only tests that add a real ACL with `chmod +a`); `snapshotDir` read a FIFO and hung `TestFifoArtifactIsRefused` on Linux and macOS, where it had never run; the memory-test child hit the store's 10 s operation limit on macOS and the 2 min child limit on Windows; Windows needed more than the 15 min that CI allowed, so the CI timeout is now 30 min (no test is skipped or reduced) |
| 2026-10-30 | windows/amd64 (local, TEMP on D:), fixes after the first code review | `gofmt -l .` (0 files); `go vet ./...`; `go test -count=1 -timeout 30m -json ./...`, each as its own exit code | all exit 0. Counts: 447 tests passed, 0 failed, 20 skipped, subtests included (242 passed, 0 failed, 16 skipped at top level; the skips are Unix-only or privilege-gated tests and child-process helpers); `store` 223.0 s, `whatsapp-mcp` 4.3 s, `config` 2.8 s, `private` 1.9 s, `lock` 1.8 s, `buildinfo` 1.0 s. The `store` suite writes several GB of temporary databases: with `TEMP` on a nearly full drive it fails with "database or disk is full", so point `TEMP` at a drive with 5 GB free |
| 2026-10-30 | windows/amd64 (local, TEMP on D:), commit 4a63bca, after the second code review | `gofmt -l .` (0 files); `go vet ./...`; five `go build ./cmd/whatsapp-mcp` (windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64); `go test -count=1 -timeout 30m -json ./...`, each as its own exit code | all exit 0. Counts: 446 tests passed, 0 failed, 21 skipped, subtests included (241 passed, 0 failed, 17 skipped at top level); `store` 194.9 s, `whatsapp-mcp` 6.1 s, `config` 3.4 s, `private` 2.3 s, `lock` 2.1 s, `buildinfo` 1.2 s. `TestHeavyContactListing` is skipped without `WA_HEAVY_TESTS=1`, and `TestPerContactMemory` runs 50,000 and 200,000 messages |
| 2026-10-30 | CI run 41103318671 (commit e30f654, push): ubuntu-latest, windows-latest, macos-latest | `go test -timeout 30m ./...` | PASS on all three and `check`; the `heavy` job was skipped as designed. ubuntu 2 min 17 s, macos 2 min 13 s, windows 12 min 54 s. Without the heavy tests Windows is still 13 minutes, so they were about a quarter of its time; where the rest goes is not measured |
| 2026-10-30 | CI run 41103333859 (commit e30f654, manual `workflow_dispatch`) | `go test -timeout 30m -count=1 -run 'TestHeavyContactListing\|TestPerContactMemory' -v ./internal/store`, `WA_HEAVY_TESTS=1` | PASS in 74 s on ubuntu-latest: the 2,000 x 256 KiB listing and the memory test at 50,000, 200,000 and 400,000 messages for both splits; `check` and `test` were skipped, so a manual run starts only this job |
| 2026-10-30 | CI run 41106640993 (commit 4a63bca, push): ubuntu-latest, windows-latest, macos-latest | `go test -timeout 30m ./...` | PASS on all three and `check`; `heavy` skipped. ubuntu 1 min 47 s, macos 2 min 59 s, windows 13 min 1 s. On macOS `internal/private` ran (0.157 s, not cached; the matrix runs `go test` without `-v`, so the log has no test names, and that the three macOS-only tests ran follows from the package building only for darwin and having no skip): `TestACLDetectionOnARealDirectory`, `TestACLInReplyLayouts` and `TestACLCheckOnAnOpenFile`, which asks the open handle (`fgetattrlist`) and, after a clean file is renamed over the path, still refuses the handle that has the entry |
| 2026-10-30 | CI run 41110766411 and 41113449639 (manual timing jobs), then run 41114834058 (commit 738d0d5, push) | per-test times with `go test -json`; `TestQueryPlans` with the runner's default temporary directory and with one on D:; then `go test -timeout 30m ./...` | Why the Windows job took 13 minutes: `TestQueryPlans` took 610 s on windows-latest and 26.5 s on ubuntu-latest (the rest of the suite about 145 s). Run alone, it took 493 s with the runner's default temporary directory (the slow C: drive) and 39 s with the directory on D:, against 27 s on Linux, so the drive was the cause and not `StoreMessages` or the heavy tests. The Windows test job now creates `D:\wa-tmp` and sets `TEMP` and `TMP` to it. Run 41114834058: PASS on all three and `check`, `heavy` skipped; ubuntu 2 min 0 s, macos 2 min 38 s, windows 4 min 7 s |
| 2026-10-28 | linux/amd64, darwin/amd64 (local, vet only) | `GOOS=linux go vet ./...`, `GOOS=darwin go vet ./...` | exit 0: the Unix and macOS test files compile; they have not been run |
| 2026-10-30 | linux, macOS | CI run 41083638694 | verified by the row above: file-mode tests (`TestExistingDatabaseWithOpenModeIsRefused`, `TestFifoArtifactIsRefused`, `TestNewDatabaseIsMode0600`, `TestCheckFileModes`), the macOS ACL check in `private_darwin.go` as it was then (a path-based `getattrlist`, since replaced by `fgetattrlist` on the open handle, see the CI row for run 41106640993) and the symlink and hard-link subtests |
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

The 400,000-message point and `TestHeavyContactListing` (2,000 messages of 256 KiB) run only with `WA_HEAVY_TESTS=1`:
locally by hand, and in CI in the `heavy` job on Linux, nightly and only if main changed (owner decision 2026-10-30,
because they cost minutes on the Windows runner). Pull requests and pushes run the 50,000 and 200,000 points (about
57 s locally, against 97 s with 400,000). The 400,000-message rows below were measured locally on 2026-10-30, before the
variable existed, when the point ran every time; the CI `heavy` job (manual run 41103333859, 74 s) repeated both splits
at 400,000 messages with the variable set and passed.

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
thousands of writes and is the same in a `GetMessage`; `PRAGMA wal_checkpoint(TRUNCATE)` before the measurement made no
difference locally, where the cost was already at its plateau, but in one CI run (windows-latest, 50,000 messages) a
`GetMessage` went from 21,488 B to 93,584 B right after the checkpoint, so the cost follows the WAL state and not the
contact's history. Its exact cause is not found, but it plateaus (identical at 200,000 and 400,000), so the test
asserts the absolute bound and no longer subtracts it. The hook that holds the listings is the
`firstRowHolder` check in `messageKeys`.

## Cross-identity evidence (SC-008)

**Windows: passed, 2026-10-30. Linux: passed, 2026-10-30 (WSL2, see "Linux, store open"). macOS: not verified** (SC-008 asks for Unix and Windows).

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

### Windows, store open (T067), 2026-10-30

A `whatsapp-mcp stdio` process (standard input held open by a `sleep 900` pipe; a new, unpaired data directory
`D:\wa-002-t067\data` that the binary created itself; it never paired or connected) held `whatsapp.db`, `-wal`
(0 B), `-shm` (32 KiB) and the lock file open. `icacls` on the directory and on `-wal` listed only SYSTEM,
Administrators and `NOTEBOOKPRO\Tiger`. As `wa002test` (`whoami` = `notebookpro\wa002test`), through
`Start-Process -Credential`, with the Russian messages translated:

| # | Command | Result |
| --- | --- | --- |
| 1, 3 | read, append to `whatsapp.db` | Access is denied |
| 2 | `dir` of the data directory | nothing listed |
| 4, 5 | read, append to `whatsapp-mcp.lock` | Access is denied |
| 6, 7 | read, append to `whatsapp.db-wal` | Access is denied |
| 8, 9 | read, append to `whatsapp.db-shm` | Access is denied |
| 10 | create `newfile.txt` in the data directory | Access is denied |
| 11 | `move /Y` a file owned by `wa002test` over `whatsapp.db` | Access is denied (not a sharing violation, so the permissions refused it) |
| 12, 13 | `del` the database, the `-wal` | "Cannot find the file" (the directory cannot be listed) |

Afterwards the process was still running, its log held no warning or error, the four files had the sizes they had
before and no file had been added. The process was then stopped with `taskkill`, so `-wal` and `-shm` stayed in place
until the directory was removed.

### Linux, store open (T067), 2026-10-30

WSL2 on the same Windows machine: Ubuntu 22.04.3, kernel 5.15.90.1-microsoft-nbd-WSL2, in the distro's own ext4
filesystem under `/tmp` (`/mnt/d` does not keep Unix permissions). The binary was the linux/amd64 cross-build of
commit `5ec8d64`. The owner was `tiger`; the unrelated identity was the system account `nobody` (uid 65534), started
with `runuser` from a root shell (`wsl -u root`), so no account was created. The store (`wa stdio`, standard input
held open by `sleep 60 |`, a new unpaired data directory the binary created itself, 100 seeded messages) was
confirmed alive by `pgrep` before and after the checks. Listing: `/tmp/wa-t067` `drwxr-xr-x tiger`, `data`
`drwx------ tiger`, and `whatsapp.db`, `whatsapp.db-wal`, `whatsapp.db-shm`, `whatsapp-mcp.lock` all `-rw------- tiger`.

As `nobody`, every step failed with "Permission denied": `ls` of the directory, `cat` and append to `whatsapp.db`, the
lock file, `-wal` and `-shm`, creating `newfile.txt`, `mv -f` of a file owned by `nobody` over `whatsapp.db`, `rm` of
the database and the `-wal`, and `stat` of the database. Afterwards the four files had the same sizes and the same
modes, `sha256sum whatsapp.db` was unchanged, no file had been added, the process was still running, and its log had
no warning or error. A first attempt in which the store had already exited gave the same denials; it is not counted.

## Backup/restore rehearsal (SC-010)

| Date | Platform | What | Result |
| --- | --- | --- | --- |
| 2026-10-28 | windows/amd64 | `TestRestoredBackupIsValidatedOffline`: 5,000 messages and a device, copied into a new private directory, `integrity_check` = ok, `foreign_key_check` empty, versions 1 / 16, store dump equals the original | PASS |
| 2026-10-28 | windows/amd64 | `TestRollbackJournalRecoveryProcedure`: empty, zeroed and hot `-journal` each refused with `rollback_journal`; the `OPERATIONS.md` steps on the live file, then a start that succeeds with the data of the state before the killed transaction | PASS |
| 2026-10-30 | windows/amd64 | by hand, quickstart step 7: `stdio --data-dir` on a new directory (exit 0), `testdata/cmd/seed -n 5000`, stop, copy `whatsapp.db` (no `-wal`: clean close) to a backup directory, copy that into a new restore directory, then Python's `sqlite3` module (mode=ro) on the restored file: `integrity_check` = ok, `foreign_key_check` = no rows, `mcp_version` = 1, `whatsmeow_version` = (16, 8), 5,000 messages; `store.Open` on the restored copy succeeds | PASS |
| 2026-10-30 | linux/amd64 (WSL2 Ubuntu 22.04.3) | by hand, quickstart step 7: a store of 5,000 seeded messages closed cleanly (no `-wal`), copied with `cp -p` into a `drwx------` backup directory, then into a new private directory; offline checks with Python 3's `sqlite3` module (SQLite 3.37.2), since the `sqlite3` shell is not installed: `integrity_check` = ok, `foreign_key_check` = no rows, `mcp_version` = 1, `whatsmeow_version` = (16, 8), 5,000 messages; `store.Open` on the restored copy succeeds and the original is not touched | PASS |
| 2026-10-30 | linux/amd64 (WSL2) | by hand, the `rollback_journal` recovery of `OPERATIONS.md` with a 0-byte, a zeroed 512-byte and a hot journal (an 855,552-byte one, left by a Python process that updated every row in `journal_mode=DELETE` with a small cache and was killed with SIGKILL before it committed): each start exits 1 with `rollback_journal`; the file set is copied aside; `PRAGMA integrity_check` on the live file returns ok; the empty and zeroed journals are left where they are and are moved into the copy, and the hot journal is rolled back and removed by SQLite; the next start exits 0 and the data equals the state before (5,000 messages, 108,890 text bytes in all three cases) | PASS |

## Dependencies and govulncheck

- `go mod verify`: all modules verified. `go list -m all` lists `github.com/mattn/go-sqlite3` (a test-only requirement of
  whatsmeow); `go list -deps ./cmd/whatsapp-mcp` contains neither it nor `litestream`, so the binary does not link it.
- Cross-builds, `CGO_ENABLED=0`, 2026-10-28: windows/amd64 18,790,400 B, linux/amd64 18,728,393 B, linux/arm64 18,097,970 B,
  darwin/amd64 18,688,128 B, darwin/arm64 18,208,994 B.
- libc: `go list -m modernc.org/libc` = v1.79.0, the version `go mod graph` shows for `modernc.org/sqlite.61.0`. CI gained a
  step that fails if the two differ.
- `govulncheck` v1.9.0 (the version CI pins), 2026-10-30, `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`, exit 0: "Your
  code is affected by 0 vulnerabilities". One finding in a required module that the code does not call: GO-2026-5932,
  `golang.org/x/crypto/openpgp` (in `golang.org/x/crypto@v0.58.0`, no fix available). Exception: owner s.shupylo
  (decided 2026-10-30), compensating control: nothing imports `openpgp` and CI runs `govulncheck` on every push, so a
  call to it or a new reachable finding fails the build. Expiry: the next change to the `golang.org/x/crypto`
  version in `go.mod`, when the finding is checked again (a fixed or removed package ends it); there is no calendar
  date.

## Owner decisions

- 2026-10-30, sender name (the second code review's open question): the direct chat with the sender in the sender-name
  lookup is the chat whose JID equals the sender's JID, as the code and `data-model.md` read it. A LID sender whose
  name is only on the phone-number direct chat has an empty sender name until `MergeLID` runs. A lookup through the
  counterpart's chat may be added later if ingest leaves too many names empty.
- 2026-10-30, heavy tests: see "Per-contact memory".
- 2026-10-30, `govulncheck` exception: see "Dependencies and govulncheck".

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
- T054 and T073: the automated rehearsals and the manual runs on Windows and on Linux (WSL2) pass. T056 (cross-identity) passed on Windows and Linux (WSL2); macOS is not verified.
- T063: steps 1 to 7 are recorded; steps 6 and 7 ran on Windows and on Linux (WSL2), not on macOS.
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
| code | 2026-10-30 | 0477099 | BLOCK | |
| code | 2026-10-30 | e30f654 | BLOCK | |
| code | 2026-10-30 | fb7af5a | PASS | |
