# Quickstart: Validating the Quiet-Stop Check

How to show that feature 004 works. Every command uses `CGO_ENABLED=0`, Git Bash on Windows, and `TEMP`/`TMP` on
`D:\tmp`. Behaviour is defined in [contracts/quiet-stop.md](contracts/quiet-stop.md) and
[contracts/cli.md](contracts/cli.md); the data is in [data-model.md](data-model.md). Record each result in
`verification.md` with the command, the date, the counts and the exit code read directly (no pipe).

## 1. Measurement spike (before any production clear)

Run the test-only measurement on the simulator (research R6). It prints, for each kind of stop (`pair` with the
post-pair reconnect, `stdio`, `serve`), the time until the client library's work disappears (p50, p95, max), the
failure categories, and the pass rate under candidate deadlines, over at least 50 stops each.

```bash
CGO_ENABLED=0 go test ./internal/wa -run 'TestQuietStopMeasurement' -count=1 -v -timeout 30m
```

Expected: a table per kind of stop. The owner picks `quietStopDeadline` and `postPairQuietDeadline` from it and
records the approval in `verification.md`. Below 90% for any kind, the owner decides before the code review (spec
SC-005).

## 2. Unit and simulator checks

```bash
CGO_ENABLED=0 go test ./internal/wa ./internal/store -run 'Quiet|Fence|Refused|PostPair|RunGuard|Logout' -count=3 -timeout 20m
```

Expected, among others:
- **A clean stop.** The marker clears, and a reopen connects with zero QR codes and zero recoveries.
- **Faults.** Each of these leaves the marker unsafe: leftover work at the deadline, a refused dial or write, a
  refused storage call, a dropped event, a cut-off dump, an unresolved pairing, a logout without durable cleanup, low
  disk. Every reason category appears at least once.
- **The 2026-11-04 late-logout regression and a held pinned handler.** The check fails, and no clean marker exists
  while either is pending.
- **Post-pair with injected late sends from the pairing generation.** Zero of them reach the fresh connection, and
  the fresh client uses the saved identity with the selected version (no registration).
- **Order.** Swapping the fence and the storage block makes the order test fail.
- **Secrecy.** Logs captured at every level contain no dump text, keys or identifiers.

## 3. Command checks

```bash
CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 20m
```

Expected:
- `pair`, `stdio` and `serve` exit codes and messages match `contracts/cli.md`.
- `upstream_drain_unproven` no longer appears.
- `run_guard_recovery_required` appears exactly when the marker stays unsafe.

## 4. Full gates

```bash
gofmt -l .
go vet ./...
go mod verify
CGO_ENABLED=0 go test ./... -count=1 -timeout 30m
for t in windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build -o "$TEMP/wa-$RANDOM" ./cmd/whatsapp-mcp || echo "FAIL $t"; done
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Also run the suite under WSL2 (recorded as WSL2, not native Linux), and push so CI covers Linux, Windows and macOS.

## 5. Permitted real-account journey (spec SC-007)

Each step needs the owner's explicit permission at the time. Use a new empty directory, for example `D:\tmp\wa-004a`.
1. `whatsapp-mcp pair --data-dir D:\tmp\wa-004a --log-level debug`. Scan the QR code. Expected: `linked. The saved
   session can be reused.`, exit 0, `quiet_stop_passed` twice in stderr (once for the pairing generation and once at
   stop), and `status` showing `logged_in: true` with no unsafe warning.
2. Restart with the saved session: `whatsapp-mcp stdio --data-dir D:\tmp\wa-004a` (stop with EOF), or the
   `session-check -dir D:\tmp\wa-004a -action connect` helper. Expected: connects with no QR and no recovery, and
   stops with `quiet_stop_passed`.
3. Cleanup, owned by the owner: unlink the device on the phone, then run offline recovery on the directory, or
   delete it.

Record each check's result, the timings between `PairSuccess`, the 515 and the fresh connection, the exit codes and
the phone's linked-device entry. A step that was not run is recorded as not verified.
