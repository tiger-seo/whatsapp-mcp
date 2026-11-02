# Verification: WhatsApp Connection, Pairing and Status

**Date**: 2026-11-02
**Feature**: `003-whatsapp-pairing-status`
**Implementation status**: Not implemented. Planning and baseline evidence only; no feature acceptance or owner-review PASS is claimed.

## Security Exceptions

### E003-RX-01 - Temporary Upstream Receive-Resource Exception

| Field | Approved policy |
| --- | --- |
| Finding | F09, limited to upstream receive expansion and retained receive-work resource bounds. Principles V/X use the exception mechanism; the finding is not closed. |
| Approved by / accountable owner | `tiger-seo`, explicitly confirmed by the user on 2026-11-02, including expiry and earlier-review triggers. |
| Approved direction | Retain the exact current dependency for feature 003; no private fork, protocol patch, automatic upgrade or binding new upstream resource thresholds are authorized. |
| Dependency | `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`, commit `1eec1136a50b2ef148b05f1819e77a9d989e0457`. A pin change requires reassessment, not automatic inheritance. |
| Scope | Missing hard bounds on expanded/decoded receive storage and queued/overflow/slow-handler receive work before adapter controls. Includes residual receive-work memory retention; not every related amplification path was reproduced. |
| Threat boundary | Authenticated WhatsApp service transport. Ordinary-contact exploitability is unproven. Legitimate bursts/slow handling may create backlog. Availability loss is possible; no confidentiality/integrity defect or Critical exploit was demonstrated. |
| Deployment scope | Feature 003's private, local, single-owner deployment and explicitly authorized verification; not public release or broader/shared deployment without reassessment. |
| Expiry | `2026-12-02`, at the start of that UTC date; no automatic renewal. A development/release approval deadline, not a hidden calendar-expiring binary feature. |
| Earlier reassessment | Before first public release or broader/shared deployment; promptly when a relevant upstream fix becomes available or a resource incident occurs. Record a new decision before the triggering deployment/continued operation proceeds. |
| Follow-up | `docs/backlog/003-whatsmeow-receive-resource-bounds.md`, owned by `tiger-seo`. Recheck before another feature materially expands the receive workload or claims this exception. |

**Not covered**: device-store/private-file safety, disk-low/full and bounded operation policy, stop/drain ordering, QR consent/expiry/secrecy, discovery/application limits, reconnect/405 policy, credential disclosure, authentication/cryptography, MCP/HTTP/webhook/media controls, new independent vulnerabilities or per-step real-account permission. This is not a general F09 waiver, parked finding or constitution amendment.

### Mandatory Compensating Controls

These are binding design/implementation obligations, not controls already proved by baseline tests:

| Control | Required behaviour | Evidence status |
| --- | --- | --- |
| Reduced surface | One owner/client; no ingest/tools/listener/media/sending in 003; disable automatic history blob transfer and unnecessary decrypted-event/retry-message buffering. | Planned; implementation unverified. |
| Approved application limits | Preserve all fixed discovery, QR, retry, refresh, attempt and shutdown constants; no speculative settings or limit changes. | Planned; baseline is not feature acceptance. |
| Adapter/storage controls | Guard every persistence interface, including write-capable reads and transactions; retain private storage, bounded operations, disk-low/full and quiescent-close guarantees. Bound application-owned HTTP/materialization/cache work. | Planned; negative/native-platform tests required. |
| Cancellation | Stop owner contexts, callback/device admissions and reconnect/QR/discovery before store close/lock release. Ten-second failure does not release live storage. | Upstream context cleanup observed on Windows; full owner unverified. |
| Monitoring | Secret-free lifecycle and coarse heap/goroutine diagnostics plus documented OS memory observation; inspect sustained unexpected growth. Monitoring is not a hard RSS/decoder cap. | Planned; production monitoring unverified. |
| Recovery | On sustained unexpected growth/exhaustion, request orderly stop, retain the lock until writes drain, pause reconnection and reassess before resuming. Forced termination is crash/OS recovery, never clean shutdown. | Runbook/test obligation; no incident induced. |
| Review discipline | Check approval at planning, implementation readiness, code review and release/deployment; block continuation on expiry/earlier trigger pending a new explicit decision. Never auto-renew. | Approval recorded; later workflow checks pending. |

F09 remains open during this exception. Keep the original threat-model review unchanged. The approval is not an owner-judge verdict.

## Existing Baseline Evidence

Environment: Windows amd64, Go 1.27.3, `CGO_ENABLED=0`. User-authorized D-drive offline probes: [research.md R17](research.md#r17---authorized-offline-baseline-reproductions).

| Check, 2026-11-02 | Result | Limitation |
| --- | --- | --- |
| Scratch `go mod verify` | PASS, all modules verified | Not feature/patch correctness. |
| Pinned source | PASS, 135 vendored non-test Go files byte-for-byte identical | Extra synthetic test file only in disposable copy. |
| Unpack 1/8/32 MiB, three repeats | PASS, 9 workers; 65,770 bytes returned 32 MiB; about 73.7 MiB cumulative allocation in largest case | Reproduces expansion, not exploit, peak RSS or safe maximum. |
| Queue 0/128/512/2048 overflow, three repeats | PASS, 12 workers; goroutines 4 -> 2052 for 2048 extra nodes | Blocked synthetic handler; no real messages/socket. |
| Context cleanup / IQ bypass | PASS in all queue workers; overflow senders exit on connection-context cancellation; baseline after separate handler cancellation | Not real Disconnect/reconnect/logout or 300-second handler-timeout test. |
| Scratch formatting/vet | PASS, `gofmt -l` and `go vet -mod=vendor -p=2 . ./vendor/go.mau.fi/whatsmeow ./cmd/summarize` | Windows only; no patched implementation. |

Raw harness, 21 JSON worker records and summary remain at `D:\wa-003-repro-20261102-202105`. This is temporary, not portable; retain the numerical summary here/in research. No credentials, account traffic, OOM, crash or data loss was exercised.

## Feature Verification Pending

The user additionally approved device-path reuse of the accepted store profile and a durable repair marker on 2026-11-02. Planned application migration 2 adds bounded `device_repair` metadata; the current implementation still has message schema 1/device schema 16. This is a recovery design choice, not a storage exception or a migration already executed.

All feature implementation/acceptance tests, device failure/recovery, compensating controls, native Linux/macOS evidence, five builds with new code, QR encoder checks and individually permissioned manual checks are **not verified**. Record actual commands/dates/counts/limitations here during implementation. Baseline reproduction PASS is not a passed feature-control test.

## Planning Validation

| Check, 2026-11-02 | Result |
| --- | --- |
| Phase 1 artifact/requirement scan | PASS: eight planning files present, 33 unique FR/SC/SEC identifiers, no unresolved template markers, no broken local Markdown links. |
| Requirements quality checklist | 16/16 still passing after approved exception/recovery amendments; quality, not implementation evidence. |
| Read-only design coherence pass | Found one logout-state wording contradiction and one historical client-reuse ambiguity; both corrected. Other checked contracts coherent for task generation, with implementation/native/manual proof still required. Not an owner-judge verdict. |
| Scoped whitespace check | PASS, git diff --check. |
| User task deletion | tasks.md remains absent on disk. Existing staged copy is not adopted as a completed task breakdown and must not enter the planning commit. |

The source specification and backlog exist locally under the repository's intentionally ignored docs directory; the owner decisions were recorded there and the complete portable exception is retained above. No production Go, project dependency, real-account action or feature implementation was changed by this planning pass.

## Owner Reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |

No owner-judge review of the completed design/task set yet. Generate tasks after planning, commit the feature directory and obtain design review before implementation; obtain code review before a PR. Exception approval is not a design/code PASS.
