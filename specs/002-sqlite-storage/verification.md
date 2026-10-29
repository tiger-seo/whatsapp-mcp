# Verification: SQLite storage

## Environment

| Item | Value |
| --- | --- |
| Local machine | Windows 10 Pro 10.0.19045, amd64, Go 1.27.3, `CGO_ENABLED=0` |
| Driver | `modernc.org/sqlite` v1.61.0, `modernc.org/libc` v1.79.0 (the version the driver requires) |
| whatsmeow | `v0.0.0-20261027104441-1eec1136a50b`, device schema version 16 |
| Implementation run | 2026-10-28, branch `002-sqlite-storage` |

The implementation run of 2026-10-28 covered every task that the parked findings P1-P6 did not block (see
"Limitations / not verified"). Queries (T039-T046, T057-T058), the scale and memory tests (T046, T047) and the
cross-identity check (T056) are not done.

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
| 2026-10-28 | linux/amd64, darwin/amd64 (local, vet only) | `GOOS=linux go vet ./...`, `GOOS=darwin go vet ./...` | exit 0: the Unix and macOS test files compile; they have not been run |
| not run | linux, macOS | CI on the next push | not verified: file-mode tests (`TestExistingDatabaseWithOpenModeIsRefused`, `TestFifoArtifactIsRefused`, `TestNewDatabaseIsMode0600`, `TestCheckFileModes`), the macOS ACL check in `private_darwin.go` (`syscall.Syscall6(SYS_GETATTRLIST)`, written without a Mac) and the symlink and hard-link subtests |
| 2026-10-28 | windows/amd64 (local) | quickstart step 2, `-fuzz` on `FuzzValidateMessage` 60 s, `FuzzNormalizePhone` 30 s, `FuzzWALScan` 60 s | PASS: 860,742 / 522,880 / 3,361 executions, no failure, no new crasher files |
| 2026-10-28 | windows/amd64 (local) | quickstart step 3: `stdio --data-dir` on a new directory, twice | exit 0 both times; first start logs `store.preflight fresh=true`, `store.upgrade schema=device from=0 to=16`, `store.migrate schema=messages from=0 to=1`, `store.ready`, with UTC times (`...Z`); second start `fresh=false` and no upgrade or migrate lines; `icacls` lists this user, SYSTEM and Administrators |
| 2026-10-28 | windows/amd64 (local) | quickstart step 4: `bumpversion` messages 99, device 99, then a file of random bytes | exit 1 each time with the schema and both versions, or "is not a usable SQLite database"; `sha256sum -c` OK and no `-wal`/`-shm` appeared |

Windows-only evidence from the same run: the path forms `\\server\share\x`, `\\?\C:\x`, `\\.\C:\x` and `C:\x:stream` were refused; a `subst` drive resolved to its real target and was accepted; junctions at `whatsapp.db`, `-wal`, `-shm` and `-journal` were refused with nothing created in their targets. A `net use` drive mapped to `\\localhost\<share>` was not tried: not verified.

## Benchmark (SC-004)

Not run. `BenchmarkQueries`, `TestQueryPlans` and `TestQueryScale` (T046) wait for the owner decision on parked
finding P1.

## Cross-identity evidence (SC-008)

Not verified (T056). It needs a second OS identity on Windows and on Linux, and has not been run. macOS is also not verified.

## Backup/restore rehearsal (SC-010)

| Date | Platform | What | Result |
| --- | --- | --- | --- |
| 2026-10-28 | windows/amd64 | `TestRestoredBackupIsValidatedOffline`: 5,000 messages and a device, copied into a new private directory, `integrity_check` = ok, `foreign_key_check` empty, versions 1 / 16, store dump equals the original | PASS |
| 2026-10-28 | windows/amd64 | `TestRollbackJournalRecoveryProcedure`: empty, zeroed and hot `-journal` each refused with `rollback_journal`; the `OPERATIONS.md` steps on the live file, then a start that succeeds with the data of the state before the killed transaction | PASS |
| not run | windows, linux | following `OPERATIONS.md` by hand once on each | not verified |

## Dependencies and govulncheck

- `go mod verify`: all modules verified. `go list -m all` lists `github.com/mattn/go-sqlite3` (a test-only requirement of
  whatsmeow); `go list -deps ./cmd/whatsapp-mcp` contains neither it nor `litestream`, so the binary does not link it.
- Cross-builds, `CGO_ENABLED=0`, 2026-10-28: windows/amd64 18,790,400 B, linux/amd64 18,728,393 B, linux/arm64 18,097,970 B,
  darwin/amd64 18,688,128 B, darwin/arm64 18,208,994 B.
- libc: `go list -m modernc.org/libc` = v1.79.0, the version `go mod graph` shows for `modernc.org/sqlite.61.0`. CI gained a
  step that fails if the two differ.
- `govulncheck`: not installed on this machine, not run. CI runs it; the result is not recorded yet. No exceptions are recorded.

## Limitations / not verified

- Parked findings P1-P6 were resolved in the design on 2026-10-30 (`e2356fe`, see `tasks.md` § Parked findings). They
  blocked T039-T046 (queries, query plans, scale), T047 (limit boundaries and memory), the `enterDeep` part of T011 and
  T048, and, through T042, T057-T058 (single-message lookups); the design review of `49ff123` cleared the resolution
  (see "Owner reviews"), so those tasks can start.
- Work that reached P6 while it was parked: commit `9c626d1` implemented `gate.enterDeep`, its constants and tests
  (`TestGateDeepSlots`, `TestGateDeepWaitersDoNotBlockAdmission`) and wrote `admission_test.go` (T048). The resolution
  makes the deep slots obsolete; T011 removes that code, and the code review must confirm it is gone and that
  `admission_test.go` matches the revised T048.
- T050, T051 and T052 are done for the write side and the lifecycle only; the search, sort, offset, context and corruption-on-query
  cases wait for the queries.
- T060: `MergeLID` is done and tested; its `BenchmarkQueries` sub-benchmark waits for T046.
- T054: the automated rehearsals pass; the manual runs on Windows and Linux are open. T056, the cross-identity check, is open.
- T061-T063: the quickstart steps, the fuzz runs of 60 s each, `govulncheck` and the benchmark have not been run.
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
