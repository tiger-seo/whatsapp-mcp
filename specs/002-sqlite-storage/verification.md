# Verification: SQLite storage

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

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-10-28 | ac42e1f | BLOCK | |
| design | 2026-10-28 | 0a86edf | BLOCK | |
| design | 2026-10-28 | 3ab978f | BLOCK | |
| design | 2026-10-28 | 0e5d6e0 | CONCERNS | Accepted 2026-10-28: both medium and all low findings fixed in the design before implementation; SQLite's memory budget accepted as holding up to about 1 million messages from one contact (research R16) |
