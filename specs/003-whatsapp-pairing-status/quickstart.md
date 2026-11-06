# Quickstart Validation: WhatsApp Connection, Pairing and Status

**Stage**: Validation guide, updated 2026-11-06. Feature 003 is partly implemented on its branch (see `tasks.md` "Current State" for what is open); the design review passed at 19b0406 and there is no full code review yet. No real-account action is authorized by this file. Contracts: [owner](contracts/connection-owner.md), [CLI](contracts/cli.md), [data model](data-model.md).

## Prerequisites

- Go 1.27.3 and `CGO_ENABLED=0`; repository dependencies pinned/verified. Use the existing CI-pinned vulnerability checker.
- Current committed design review of the aligned artifacts against constitution 2.0.0, and valid E003-RX-01 in verification.md. The historical d044f73 PASS predates this alignment. Check owner/expiry/earlier triggers before proceeding; the approved temporary deviation is not a satisfied receive bound or a claim of tested compensating controls.
- Several GB free for ordinary store tests; on Windows prefer an owner-controlled temporary directory on D. Do not point tests at a real data directory or account.
- Native Linux/Windows/macOS runners for claimed platform controls; a cross-build is not a native verification result.

## Automated Offline Checks

Run from the repository root in PowerShell:

```powershell
$env:CGO_ENABLED = '0'
go mod verify
gofmt -l .
go vet ./...
go test -timeout 30m ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Expected: no formatting output, exit 0, no reachable vulnerability and no real account/network test. Preserve the existing modernc driver/libc consistency check and five-target builds from CI. Heavy tests remain opt-in through WA_HEAVY_TESTS; do not set it for ordinary runs without adequate temp space.

The new tests must use injected fake clients/providers, synthetic keys and controlled local HTTP/TLS handlers. Evidence must include:

| Scenario | Required observation |
| --- | --- |
| Link/restart | Synthetic save and Connected; after any production adapter Connect attempt, including failure/cancellation, orderly drain retains unsafe and restart starts neither Connect nor QR. Clean-state saved identity is tested separately; failed save cannot succeed. |
| Startup/reconnect bounds | Both shells: initial failure/timeout. The startup retries, 405, 515 and ordinary reconnect are denied by default today, so assert no retry and an unavailable diagnostic; never Connect after retiring an unproven generation. They return only behind the proof gate (T077/T050/T053/T054). Verify watchdog and no overlap before drain. |
| QR/window | First/replacement expiry, automatic code rotation within one window, final timeout requiring offline recovery rather than Enter-to-retry, EOF failure, concurrent sharing and stale-epoch rejection. |
| Offline status | Four fields/caveats, zero client/version/network calls, no missing artifacts created, held lock PID, committed WAL and unchanged DB/sidecars. |
| Version/retries | Real consumed-byte counting, redirects/TLS/oversize/stall rejection, no valid truncated prefix, immutable payload/hash snapshots, six-hour fake time and conditional 405 budget or safe unavailability. |
| Device admission | Shared gate limits, field/aggregate overflow before copying, no raw caches, nested transaction rollback, stable first-Save publication/readiness and deferred client-delete cleanup after retirement. |
| Repair | Fresh and 1 -> 2 migration; mark-before-write and sibling failures; startup admission defers ordinary SDK sync but permits key delivery; real mutex-contention cancellation vs separate fake-time caller deadline; actual-call slot retained until return, no late clear; bounded full recovery and coalesced incremental replay without extra full-resync attempts. |
| Unsafe run | Fresh and legacy -> 3 marker migration, full writer synchronization, failed/ambiguous arm, process-crash boundaries before/after first Save and logout cleanup, explicit offline recovery and read-only schema-3/legacy status. A possible orphaned phone-side link is considered even without a saved row. Process-kill tests do not prove OS/power-loss durability. |
| Recovery safety | Actual callback/logout/delete vs retirement and clear barriers; disk latch between reserve and resume refuses clear; unverified device absence, redirected/mixed handles and unsupported terminal refuse recovery. For ambiguous entries, test the separately authorized manual removal of all potentially matching entries and the warning about unrelated devices, without real account actions. No assumed phone label. |
| Darwin sync | Native verification of actual SQLite VFS and writer connections, fullfsync availability/failure and conditional fallback; record process-crash separately from power-loss evidence and leave unavailable checks unverified. |
| Stop | Hold a direct device call; ten-second warning cannot close pools/release lock; true quiescence eventually permits close; cancellation/retry/QR work cannot restart after stop. |
| Secrecy/surface | All-level captures contain no QR/key/body/ticket/account identifier, no input terminal controls, no stdio human stdout, no tools/listener/implicit pairing. |

Use testing/synctest only with in-memory channels/providers for virtual time; use ordinary bounded tests for SQLite and HTTP fixtures. Do not use `go test -race` under this repository's CGO-free policy. Assert rejected own-input actions create no database/network side effects; distinguish previously committed sibling sync work from rollback.

## Cross-Platform and Helpers

Run the CI builds for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 with CGO disabled, writing outputs only to a verified scratch directory and restoring GOOS/GOARCH afterwards. Run native tests on Windows/Linux/macOS for permissions, identity swaps, lock contention and signals. Record unperformed native checks as not verified.

Explicitly build the manual helper (./... excludes testdata):

```powershell
go build -o "$env:TEMP\wa-session-check.exe" ./internal/wa/testdata/cmd/session-check
```

The helper is planned, not currently present. It must use the production owner/store/lock, an explicitly selected private acceptance directory and interactive per-action approval. It exposes no listener or shipped account-management command. Review it separately; never invoke it from automated tests.

The CI cross-identity privacy fixture creates synthetic private storage with the offline store seed helper, not with status (status is now a real offline command that creates nothing). The privacy denial check is kept.

## Permissioned Manual Journey

These are separate human steps, not a runnable batch. Before each connect/pair action obtain the user's explicit permission, name the exact acceptance directory/account/device and record the change/cleanup plan. Do not infer permission from this guide or exception approval.

1. Only after all readiness and safety gates and separate permission for a fresh link, use the named isolated directory and scan its current QR; record no QR material. Observe resource drain and unsafe marker retention, not automatic reuse.
2. Inspect offline status: saved identity is locally reported as unverified, with unsafe-recovery warning and unsuccessful status. Restart must not Connect or show a QR.
3. Separately authorize and perform offline account/device verification, any needed phone unlink, and local identity removal if present. Preserve chats and repair needs. Recovery itself stays offline; ambiguous or insufficient verification blocks it.
4. A later fresh pair requires separate explicit permission. In a separately approved live logout scenario, verify that no same-process re-pair is offered after production Connect, even after durable cleanup. Stop and drain before recovery. No automatic unlink is performed.
5. Record redacted outcomes and explicitly approved cleanup; login success does not mean completed contact sync. No-scan restart and same-process re-pair remain follow-up goals, not current acceptance tests.

Real recovery/disk-full injection is not part of this journey. Exercise failure/cancellation using synthetic fixtures. No send/download/history-message acceptance is added to 003.

## Monitoring and Incident Handling

Enable the documented safe debug diagnostics only when needed; the bridge must still drop upstream arguments, including QR/keys/bodies. Observe runtime heap/goroutine samples and OS process memory against a representative baseline. They are not hard caps and cannot prove exploitability or prevent exhaustion.

On sustained unexpected growth: request normal stop, wait for device/store quiescence, retain ownership while active, and pause reconnection pending owner reassessment. If forced kill is unavoidable, record unclean termination and follow storage recovery guidance; do not claim clean shutdown or auto-renew the exception. Review before public release/broader deployment, expiry or the other triggers in verification.md.

## Existing Baseline Reproduction

The separately approved Windows probes already ran at `D:\wa-003-repro-20261102-202105`; `./run-repro.ps1` there runs only synthetic code and preserves JSON/source hashes. Seven cases x three repetitions passed. They demonstrate baseline expansion/overflow and cancellation cleanup, not a patched control, feature acceptance, other-platform result or ordinary-contact exploit. Refer to research R17; do not require that ephemeral path for CI.

## Recording Results

Append actual commands/dates/counts/platforms, bounded failure results, manual per-step approval and cleanup to verification.md. Keep unavailable platform/control/manual checks unverified. Check E003-RX-01 still applies, then obtain owner-judge code review on the committed implementation/evidence before any PR. This guide creates no review verdict and authorizes no implementation by itself.
