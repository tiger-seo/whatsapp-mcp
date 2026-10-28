# Verification: SQLite storage

## Gate results (committed view, device upgrade transactions)

T014, the committed view, implemented ahead of the design review's pass at the owner's request (2026-10-28) so that
CI can produce the macOS evidence the gate needs.

| Date | Platform | Command | Result |
| --- | --- | --- | --- |
| 2026-10-28 | windows/amd64, Go 1.27.3, modernc.org/sqlite v1.61.0 | `CGO_ENABLED=0 go test ./internal/store/ -count=1 -v` | PASS: `TestCommittedViewChangesNothing` 8/8 fixtures (unchanged files, same answer as SQLite), big-endian twin, 3 refusals, 4 ignored logs, 9 fuzz seeds |
| 2026-10-28 | windows/amd64 | `CGO_ENABLED=0 go test ./internal/store/ -run '^$' -fuzz FuzzWALScan -fuzztime 30s` | PASS: 2,244 executions, no failure |
| — | linux, macOS (CI) | `go test -timeout 15m ./...` | pending: first push of the branch |

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-10-28 | ac42e1f | BLOCK | |
| design | 2026-10-28 | 0a86edf | BLOCK | |
