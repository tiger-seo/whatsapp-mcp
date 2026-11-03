# Quickstart Validation: WhatsApp Connection, Pairing and Status

**Stage**: Planned validation guide. Feature 003 is not implemented; the commands below describe checks to run after implementation. No real-account action is authorized by this file. Contracts: [owner](contracts/connection-owner.md), [CLI](contracts/cli.md), [data model](data-model.md).

## Prerequisites

- Go 1.27.3 and `CGO_ENABLED=0`; repository dependencies pinned/verified. Use the existing CI-pinned vulnerability checker.
- Current design review after task generation, and valid E003-RX-01 in verification.md. Check owner/expiry/earlier triggers before proceeding; approval is not a claim of tested compensating controls.
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
| Link/restart | One synthetic save, Connected confirmation, zero QR on next saved-session run; failed save cannot succeed. |
| QR/window | First/replacement expiry, automatic code rotation within one window, Enter after final timeout, EOF failure, concurrent sharing and stale-epoch rejection. |
| Offline status | Four fields/caveats, zero client/version/network calls, no missing artifacts created, held lock PID, committed WAL and unchanged DB/sidecars. |
| Version/retries | Real consumed-byte counting, redirects/TLS/oversize/stall rejection, no valid truncated prefix, immutable payload/hash snapshots, six-hour fake time and exactly three five-second 405 retries. |
| Device admission | Shared gate limits, field/aggregate overflow before copying, no raw caches, nested transaction rollback, stable first-Save publication/readiness and deferred client-delete cleanup after retirement. |
| Repair | Fresh and 1 -> 2 migration; mark-before-write and sibling failures; startup admission defers ordinary SDK sync but permits key delivery; real mutex-contention cancellation vs separate fake-time caller deadline; actual-call slot retained until return, no late clear; bounded full recovery and coalesced incremental replay without extra full-resync attempts. |
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

Update the existing CI cross-identity privacy fixture to create synthetic private storage with the offline store seed helper, not with status (status no longer creates a directory/fails as a stub). Keep the privacy denial check rather than dropping it.

## Permissioned Manual Journey

These are separate human steps, not a runnable batch. Before each connect/pair action obtain the user's explicit permission, name the exact acceptance directory/account/device and record the change/cleanup plan. Do not infer permission from this guide or exception approval.

1. With permission for a fresh link, run `whatsapp-mcp pair --data-dir <approved-private-dir>`, scan only the displayed current QR, confirm linking, observe successful safe exit. Do not capture the QR in evidence.
2. While no owner runs, execute offline status for that same directory. Expect connected=false, saved logged_in=true, pairing_required=false, bundled/unrefreshed version and the online-verification warning. This is not a live health check.
3. Obtain permission to reconnect the saved device; start the selected connection-owning shell or interactive helper. Confirm no QR. A second status/pair command must fail on the holder lock.
4. With the user's permission and action on the trusted phone, unlink that exact linked device while the helper remains running. Observe false login/connection and no unsolicited pairing. No automatic phone-side unlink is performed by software.
5. Obtain new explicit pairing permission in that SAME helper process. Request a fresh window, scan, confirm Connected and a new saved identity without losing chats/messages. After final timeout, require a new explicit request; do not silently restart a window.
6. Stop normally and inspect safe offline status. Record any pending recovery marker honestly; login success does not mean complete contact sync. Arrange cleanup only for the explicitly approved acceptance device/directory; never delete unrelated account data.

Real recovery/disk-full injection is not part of this journey. Exercise failure/cancellation using synthetic fixtures. No send/download/history-message acceptance is added to 003.

## Monitoring and Incident Handling

Enable the documented safe debug diagnostics only when needed; the bridge must still drop upstream arguments, including QR/keys/bodies. Observe runtime heap/goroutine samples and OS process memory against a representative baseline. They are not hard caps and cannot prove exploitability or prevent exhaustion.

On sustained unexpected growth: request normal stop, wait for device/store quiescence, retain ownership while active, and pause reconnection pending owner reassessment. If forced kill is unavoidable, record unclean termination and follow storage recovery guidance; do not claim clean shutdown or auto-renew the exception. Review before public release/broader deployment, expiry or the other triggers in verification.md.

## Existing Baseline Reproduction

The separately approved Windows probes already ran at `D:\wa-003-repro-20261102-202105`; `./run-repro.ps1` there runs only synthetic code and preserves JSON/source hashes. Seven cases x three repetitions passed. They demonstrate baseline expansion/overflow and cancellation cleanup, not a patched control, feature acceptance, other-platform result or ordinary-contact exploit. Refer to research R17; do not require that ephemeral path for CI.

## Recording Results

Append actual commands/dates/counts/platforms, bounded failure results, manual per-step approval and cleanup to verification.md. Keep unavailable platform/control/manual checks unverified. Check E003-RX-01 still applies, then obtain owner-judge code review on the committed implementation/evidence before any PR. This guide creates no review verdict and authorizes no implementation by itself.
