# Verification: WhatsApp Connection, Pairing and Status

**Date**: 2026-11-03
**Feature**: `003-whatsapp-pairing-status`
**Implementation status**: Not implemented. Owner-judge design review of the revised artifacts at 71f9004 returned PASS. Recheck E003-RX-01 and its triggers at implementation start. No feature/control acceptance or code-review PASS is claimed. Baseline probes are separate evidence.

## Security Exceptions

### E003-RX-01 - Temporary Upstream Receive-Resource Exception

| Field | Approved policy |
| --- | --- |
| Finding | F09, limited to upstream receive expansion and retained receive-work resource bounds. Approved temporary deviation, not a satisfied bound or closed finding. |
| Constitutional authority | Constitution 2.0.0, Principles V/X, explicitly authorizes only identified upstream receive-resource deferrals. The existing 2026-11-02 approval below is unchanged; the amendment does not approve, renew or expand this exception or replace design review. |
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

### Implementation-Start Recheck - 2026-11-03

At 2026-11-03T15:20:42Z, the recorded owner was `tiger-seo`, the expiry at the start of 2026-12-02 UTC had not been reached, and `go.mod` retained whatsmeow `v0.0.0-20261027104441-1eec1136a50b`. `git ls-remote https://github.com/tulir/whatsmeow.git HEAD` returned `54038813c47f3f351eb6fcaafab0e1b99d676850`, the same upstream revision inspected in research R13; this does not rule out an unmerged or unpublished fix. This work is limited to private local implementation, not release or broader deployment. No resource incident was reported in the available project record; this is not an independent incident investigation. Reassess immediately if a trigger becomes known.

Implementation environment: Windows amd64, Go 1.27.3; use `CGO_ENABLED=0`, synthetic credentials and isolated temporary paths. Existing approved shared limits include 16 active/64 waiting operations, five-second admission, ten-second operation and 16-MiB input/32-MiB output budgets; the recorded disk and lifecycle limits remain binding. No feature-control test, real-account action or exception renewal is claimed by this recheck.

**Setup T002, 2026-11-03**: With `CGO_ENABLED=0`, `go get rsc.io/qr@v0.2.0` added the approved root module; existing WhatsApp and SQLite pins are unchanged. `go mod verify` passed. `go list -deps rsc.io/qr` listed the Go root, `gf256` and `coding` packages, not `libqrencode`. `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 rsc.io/qr` found no vulnerabilities; the same command on `./...` reported zero reachable vulnerabilities and one unreachable module vulnerability. No five-target or native-platform result is implied.

**Setup T003, 2026-11-03**: `CGO_ENABLED=0 go test ./internal/wa` passed (one fixture test). The fixture supplies synthetic deterministic bytes, an HTTP transport that rejects requests, an injected synthetic HTTP provider, a channel barrier and `t.TempDir`. The rejecting transport guards clients into which it is injected; it is not a process-wide network sandbox. Future tests must inject the appropriate fake or forbidden transport and must not use real account state.

**Foundation T004-T005, 2026-11-03**: `CGO_ENABLED=0 go test -mod=mod ./internal/wa` first failed with undefined owner/capability types; after adding them, `CGO_ENABLED=0 go test ./internal/wa` passed (two tests), and `go vet ./internal/wa` passed. Compile-time assertions check the pinned client's send/upload/contact/group signatures, and the download contract uses stored metadata with nullable length. These are type/value checks, not proof of future owner concurrency or isolation. Importing the existing pinned client root required additional pinned transitive modules; `go mod verify` passed. A subsequent `govulncheck -show verbose ./...` identified two unreachable `golang.org/x/net` advisories, GO-2026-6866 and GO-2026-6860, so x/net was updated from v0.61.0 to the compatible fixed v0.62.0. The previously recorded x/crypto openpgp advisory remains separate; WhatsApp and SQLite pins are unchanged.

**Foundation T006-T008, 2026-11-03**: On Windows amd64, with `CGO_ENABLED=0` and `TEMP`/`TMP` set to the workspace-local D: temporary directory, `go test ./internal/store -timeout 5m -count=1` passed in 210.104 seconds; `go test ./internal/store -list '^Test'` listed 180 top-level test functions, including tests that may be skipped by their own conditions. The targeted device-admission suite passed twice, and the retirement and disk-latch regressions each passed ten repetitions. `go vet ./internal/store ./internal/wa` and `git diff --check` passed. Added tests cover every pinned device interface method, pre-ready refusal, durable-save publication, disk/slot limits, frozen identity, deferred removal, nested validation/cancellation, binary key IDs and held nested work. Earlier complete-suite attempts failed first on insufficient C: temporary space and then on an unsynchronized retirement test and a disk-fixture cache assumption; these are not reported as passes. No race-detector run, non-Windows run, heavy test, account operation or full-feature acceptance is claimed. Cache-less device stores and true shared-store quiescence remain T009-T012 and are not established by these checks.

**Foundation T009-T010, 2026-11-03**: On Windows amd64 with `CGO_ENABLED=0`, `go test ./internal/store -run 'TestDevice(Contact|LID|Migration)' -count=1 -timeout 5m` passed in 0.634 seconds, `go vet ./internal/store` passed, and `go test ./internal/store -count=1 -timeout 10m` passed in 231.457 seconds. The cache-less guarded contact and LID views read committed changes without stale negative caches, preserve push/business old-value results and misses, reject unbounded exports, keep multi-row contact/LID writes atomic across SQL failure, and reuse the enclosing decryption transaction for PN-to-LID session migration. Bulk writes retain the approved aggregate input budget and execute bounded 300-entry sub-batches inside one transaction; no total address-book or lifetime database quota was added. This is Windows-only synthetic SQLite evidence; heavy tests, race detection, non-Windows controls and account operations remain unverified.

**Foundation T011-T012, 2026-11-03**: On Windows amd64 with `CGO_ENABLED=0`, `go test ./internal/store -count=1 -timeout 10m` passed in 214.503 seconds; `go vet ./internal/store` and `git diff --check` passed. The held-device retirement, close and quiescence tests passed ten repetitions after the final test adjustments (`go test ./internal/store -run 'Test(RetirementKeepsSharedStorageOpen|ClosingRejectsNewCallsWhileDraining|ExpiredCloseKeepsPoolsOpenWithoutSuccess|HeldDeviceWriteDrainsBeforePoolClose|HeldDeviceTransactionDrainsBeforePoolClose|Quiesce|GateQuiesceOutcome|RepeatedCloseAttemptsShareOneWaiter)' -count=10 -timeout 5m`). One earlier repeated run failed due to a test assuming asynchronous cancellation had already reached the held save; the assertion was corrected to test drain independently of cancellation delivery. Shared admission rejects new work on closing, an undrained close reports `ErrNotQuiescent` without closing pools, and a single completion signal permits a later close after drain. This does not establish command lifecycle ordering (T013), native Linux/macOS behavior, race-detector results or real-account behavior.

**Foundation T013, 2026-11-03**: On Windows amd64 with `CGO_ENABLED=0`, `go test ./cmd/whatsapp-mcp -count=1 -timeout 5m` passed in 7.511 seconds; `go vet ./cmd/whatsapp-mcp` passed. Six lifecycle safety tests passed ten repetitions (`go test ./cmd/whatsapp-mcp -run 'Test(ShutdownTimeout|StoreNotQuiescentWaitsBeforeLock|StoreCloseErrorAfterDrainReleasesLock|ShutdownWaitsForHeldDeviceTransaction|SecondSignalForcesExit|StopStepsInReverseOrder)' -count=10 -timeout 5m`). They cover a held synthetic SQLite device transaction with a real directory lock, deadline failure without premature unlock, live cleanup contexts, not-quiescent close and drain retry, a pool-close failure after drain, and a second signal after timeout. The initial tests-first invocation failed to compile because the store-stop registration did not yet exist; a pre-implementation runtime failure was not separately observed. The owner step is represented by the device barrier in the test; the future owner integration remains T025/T028. No native Linux/macOS, forced-process-exit subprocess or real-account result is claimed.

**Ubuntu WSL2 verification, 2026-11-03**: Linux amd64 on Ubuntu WSL2, kernel `5.15.90.1-microsoft-nbd-WSL2+`, Go 1.27.3 and `CGO_ENABLED=0`: `go test ./... -count=1 -timeout 10m` passed in all seven packages (store 165.131 seconds; command 6.242 seconds). Linux temporary directories held test data; source was on the Windows-mounted workspace. Earlier separate command-package and targeted quiescence runs passed. The first full run failed `TestQuiesceReportsStillActive` because it assumed asynchronous cancellation had reached the held transaction before release; the assertion was corrected to test actual drain regardless of cancellation timing, and the full suite was rerun successfully. The temporary Linux Go archive matched the SHA-256 published by go.dev. WSL2 evidence is not native Linux hardware/CI, macOS, heavy-test, race-detector or real-account evidence.

**Foundation T014 tests first, 2026-11-03**: On Ubuntu WSL2 with `CGO_ENABLED=0`, `go test ./internal/store -run '^TestDeviceRepairMigration' -count=1 -timeout 5m` failed as expected: fresh storage remained at schema 1 without `device_repair`, and the other three cases required the missing second embedded migration. They cover upgrade preservation, marker constraints and cascade, and atomic migration failure/reopen once T015 supplies that migration. `go test ./internal/store -run 'Test(PreflightRefusalsChangeNothing|OrphanSidecarsAreRefusedAndKept|RollbackJournalIsRefusedAndKept)' -count=1 -timeout 5m` passed on WSL2. Subsequent T015 results are recorded below; this is not native Linux hardware or CI evidence.

**Foundation T014-T015, 2026-11-03**: On Ubuntu WSL2 with `CGO_ENABLED=0`, the four migration tests and refusal/open regressions passed, `go test ./cmd/whatsapp-mcp -count=1 -timeout 5m` passed, and `go test ./internal/store -count=1 -timeout 10m` passed in 164.670 seconds. An earlier full store run failed two old latest-schema expectations in device-upgrade and restore tests; both were updated to schema 2 and the full suite rerun. Migration 2 creates bounded repair metadata and preserves the pinned device schema and application data. Marker mutation/overflow and ticket behavior remain T016-T017. WSL2 is not native Linux hardware or CI evidence.

**Foundation T016 in progress, 2026-11-03**: `CGO_ENABLED=0 go test ./internal/store -run '^TestRepairMarker' -count=1 -timeout 5m` on Ubuntu WSL2 produced three expected failures: no marker on a version write, mutation despite a marker-insert trigger failure, and acceptance of an unknown collection. These initial tests do not yet cover the full T016 matrix; T016 and T017 remain incomplete, and the store test suite is intentionally failing until the implementation is completed.

**Foundation T016-T017 partial progress, 2026-11-03**: Version and MAC mutations now record a committed marker before delegating, reject unknown collections and refuse incompatible nested work. On Ubuntu WSL2 with `CGO_ENABLED=0`, the targeted `TestRepairMarker` cases passed, covering those paths, restart, overflow, marker insertion failure and a protected-write failure; `go test ./internal/store -count=1 -timeout 10m` passed in 154.349 seconds and `go vet ./internal/store` passed. This supersedes the preceding intentionally failing test status, not the incomplete task status. Repair tickets/fences, fault tracking, startup admission, deferred demand, bulk-contact marking and their tests are not implemented or verified. Do not use this partial foundation as a production adapter or claim T016/T017 complete.

**Foundation T016-T017 continuation, 2026-11-03**: Pinned `appstate.go` calls `PutAllContactNames` only for a `critical_unblock_low` full-sync snapshot. A marker-before-write test first failed on the unguarded snapshot, then passed after adding that path. On Ubuntu WSL2, `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 10m` passed in 295.366 seconds and `go vet ./internal/store` passed. This supersedes only the bulk-contact marking statement above; ticket/fence and startup admission obligations remain incomplete and neither task is complete.

**Foundation T016-T017 completed, 2026-11-04**: On Ubuntu WSL2 with `CGO_ENABLED=0`, the final `go test ./internal/store -count=1 -timeout 10m` passed in 209.043 seconds; targeted `go test ./internal/store -run '^(TestRepair|TestPendingRepairs)' -count=2 -timeout 5m` passed, with earlier three-repeat runs and `go vet ./internal/store` also passing. Windows amd64 targeted repair tests passed before the final fault-notification additions. Tests cover marker-before-write including contact snapshots, interrupted MAC/contact sibling writes followed by reopen, bounded pending names, ticket/target/revision CAS, discarded guarded-write errors, restart-required fault notification, cancellation and stale generations, early deferral without gate admission, allowed keys, deferred coalescing and replay, and fenced clear. A nested write previously reproduced a lock inversion and now rejects before taking the fence; nested compatible reads reuse the existing lease. Expected CAS conflicts no longer map to storage I/O faults. The SDK owner integration and actual resync remain later-phase work, and WSL2 does not establish native Linux hardware or CI results.

**Foundation T018, 2026-11-04**: The shared `internal/walog` bridge retains upstream templates and severity but never evaluates or formats arguments; it bounds and sanitizes templates and restricts module labels, while preserving the existing database logger contract. Pinned whatsmeow `Sub` call sites were inspected and use fixed labels; a source search for direct non-literal `Debugf`/`Infof`/`Warnf`/`Errorf` calls found none. This is an audit of the pinned source, not a guarantee that an arbitrary preformatted template cannot contain a secret; re-audit when the pin changes. On Ubuntu WSL2, `CGO_ENABLED=0 go test ./internal/walog ./internal/store -run 'Test(Bridge|NilLogger|WALog)' -count=3 -timeout 5m` and `go vet ./internal/walog ./internal/store` passed. The same targeted tests passed once on Windows. Tests include all levels, secret arguments, a panic-on-format argument, module injection, control characters and length bounds; neither run uses an account.

**Foundation T019-T020, 2026-11-04**: On Ubuntu WSL2 with `CGO_ENABLED=0`, `go test ./internal/wa -count=1 -timeout 5m`, three repetitions of `go test ./internal/wa -run '^TestFake' -count=3 -timeout 5m`, and `go vet ./internal/wa` passed. Targeted fake tests also passed on Windows amd64. The fake implements all declared capabilities without a client or network, records copied call inputs including protobuf and optional send metadata, clones media metadata and nested response fixtures, injects deterministic events/failures, and stops when its lifecycle context is cancelled. This verifies only synthetic fake behavior, not real send/download or account operations.

**Foundation T021, 2026-11-04**: On Ubuntu WSL2 with `CGO_ENABLED=0`, `go test ./internal/wa -count=3 -timeout 5m` and `go vet ./internal/wa` passed; `go test ./internal/wa -count=1 -timeout 5m` also passed on Windows amd64. Tests use local TLS servers and injected in-memory transports, never the real discovery service. They reject untrusted certificates, redirects, plaintext/credential headers, advertised oversize, truncated and unknown-length budget-exhausting responses; count actual reads at the 16-MiB app-state limit; exercise stalled-body deadlines and concurrent single-flight waiters; and verify zero/max uint32 revisions and bundled/latest fallback without response/error text leakage. An initial concurrency test exposed a test-scheduling race and was corrected to synchronize both waiters before release. App-state client installation and live handshake selection remain later-phase integration tasks; WSL2 does not establish native Linux hardware or CI behavior.

**Phase 2 checkpoint, 2026-11-04**: With `CGO_ENABLED=0`, `go test ./... -count=1 -timeout 10m` passed for all eight packages on Ubuntu WSL2 (store 195.026 seconds) and on Windows amd64 (store 221.600 seconds). The initial Windows run failed after the default `C:` temporary drive fell below the 256-MiB disk reserve and a scale test filled it; the failed run's identified temporary database was removed, and the passing rerun used `TEMP`, `TMP` and `GOCACHE` on the `D:` workspace drive. Its temporary cache was removed afterward. On WSL2, `go vet ./...` and `go mod verify` passed; `gofmt -l internal/wa internal/walog internal/store cmd/whatsapp-mcp` and `git diff --check` returned no output. `git ls-files --eol` showed no tracked working-tree CRLF. These are synthetic local tests, not native Linux hardware, macOS, CI, heavy-test, race-detector or real-account evidence. Future owner, network adapter, status and real-account integration remain unimplemented; the pinned upstream receive-resource exception remains limited as recorded above.

**Final repair-context tightening, 2026-11-04**: After those full-suite runs, the marker-clear transaction was rooted directly in the repair context so repair cancellation propagates directly to database/sql; the admitted operation context remains linked separately. The targeted `go test ./internal/store -run '^(TestRepair|TestPendingRepairs)'` passed three repetitions on Ubuntu WSL2 and once on Windows with `CGO_ENABLED=0`, and WSL2 `go vet ./...` passed. The full suites were not rerun after this final narrow change.

## Governance Alignment - 2026-11-03

Constitution 2.0.0 supplies explicit authority for the recorded resource-bound deviation; the earlier risk acceptance alone did not amend Principle V. The exception owner, exact dependency, receive paths/resources, residual exposure, local deployment scope, controls, expiry and backlog are unchanged. Dependency changes, broader deployment, relevant upstream fixes/incidents and expiry still require reassessment; no renewal or expanded workload is approved here.

The plan now labels the unmet bounds as an approved temporary deviation and describes task generation as complete. Research, spec, task readiness and quickstart references use the current authority without changing functional requirements, the fixed acceptance profile or any task checkbox. The historical PASS at d044f73 does not cover these changed artifacts. Cross-artifact reanalysis and a new committed design review are required before implementation; neither is claimed by this alignment text.

### Alignment Validation

| Check, 2026-11-03 | Result | Limitation |
| --- | --- | --- |
| `bash .specify/scripts/bash/setup-plan.sh --json` | PASS; existing plan retained, feature 003 resolved. | No design regeneration or implementation. |
| `bash .specify/scripts/bash/check-prerequisites.sh --json --require-spec --require-tasks --include-tasks` | PASS; required feature artifacts present. | Presence is not behavioural verification. |
| Cross-artifact reanalysis against constitution 2.0.0 | No remaining findings: 0 critical issues, ambiguities, duplications or unmapped tasks. The prior authority conflict, stale status wording and missing HTTP source entry are addressed. | Read-only document analysis, not an owner-judge verdict or runtime control evidence. |
| Requirement/task inventory (`rg -c` on declarations and task lines) | 33 identifiers: 16 FR, 9 SC and 8 SEC; 64 unchecked tasks and 13 parallel markers. Planned core coverage 25/25 and security coverage 8/8. | Task bodies/coverage mappings remain unchanged; no feature test has run. |
| Exception boundary comparison | Original owner, pin, receive scope, deployment scope, controls, expiry and reassessment obligations retained. | No renewal, new workload approval or claim of a new upstream-fix/incident investigation; T001 must recheck at actual start. |
| `git diff --check -- .specify/memory/constitution.md specs/003-whatsapp-pairing-status` | PASS. | Formatting check only. |

Reanalysis is complete. The fresh owner-judge review of committed alignment 630bef6 is recorded below; the historical PASS remains stale for this changed design. No code, dependency, account operation, native test or feature-control acceptance was performed by this alignment workflow.

### Previous Design Review

On 2026-11-03, owner-judge reviewed 630bef6 in design mode and returned CONCERNS. Read-only inspection validated the constitution 2.0.0 authority, unchanged E003-RX-01 boundaries, 33 requirement mappings and 64-task ordering. Pinned-source checks included handshake version payloads, context-blind app-state locking/detached key requests, logout/deferred deletion, QR-only passkey prevention and guarded contact-export assumptions. These are design/source checks, not executed feature-control tests.

**Medium concern - initial saved-session connection timeout in long-running shells**: plan.md Version and Reconnect, contracts/connection-owner.md Protocol Adapter and State, and T023/T025-T026 specify a 30-second watchdog and InitialAutoReconnect=false but do not define a recovery policy after that initial attempt is cancelled. If stdio/serve starts with saved credentials and a handshake stalls, it can remain alive disconnected without an initial retry; ordinary reconnect backoff covers dropped established connections. The owner must choose or explicitly accept the post-timeout policy (for example, bounded retry or restart-only recovery) before implementation. Pair's specified unsuccessful exit on connection failure is unaffected.

On 2026-11-03 the user chose at most three retries, each after five cancellable seconds, after a saved-session initial failure or timeout in `stdio`/`serve`; exhaustion leaves the shell disconnected with an actionable diagnostic. The 30-second watchdog remains per attempt. The covered design changes also clarify 405/515 attempt watchdogs and require reanalysis and a fresh committed review; the historical CONCERNS row does not authorize implementation. E003-RX-01 remains unrenewed and F09 remains open; native-platform/manual evidence is still unverified. The earlier inventory and analysis counts below are historical and predate SC-010 and the revised tasks.

### Current Design Review

On 2026-11-03, owner-judge reviewed committed design 71f9004 and returned PASS. The review checked the bounded startup and 405/515 recovery design, requirement/task mappings, device-store and repair races, shutdown ordering, hostile discovery inputs and offline status. Pinned-source mechanisms were inspected; their runtime correctness remains unverified. No code, builds, tests or real-account operations were run; historical probes were not rerun, and no fresh upstream-fix or operational-incident investigation was performed. Supplementary subagent checks did not complete and were not relied upon. T001 must recheck exception expiry and earlier triggers before implementation.

**Revision checks, 2026-11-03**: `bash .specify/scripts/bash/check-prerequisites.sh --json --require-spec --require-tasks --include-tasks` resolved feature 003 and its required artifacts; `git diff --check` passed; the Grep tool found 34 requirement declarations, 34 coverage rows and 64 unchecked tasks. `git ls-files --eol -- specs/003-whatsapp-pairing-status` reported LF for all tracked feature files. PowerShell `rg` was unavailable, so no `rg` count is claimed for this revision. These checks do not establish runtime behavior, complete cross-artifact reanalysis or a new owner review.

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
| User task deletion at the planning checkpoint | tasks.md was absent and the discarded staged draft was excluded from the planning commit. The subsequent task-generation stage creates a fresh list from the completed design, not that old draft. |

The source specification and backlog exist locally under the repository's intentionally ignored docs directory; the owner decisions were recorded there and the complete portable exception is retained above. No production Go, project dependency, real-account action or feature implementation was changed by this planning pass.

## Task-generation Validation

**Generated**: 2026-11-02; review amendments/revalidation: 2026-11-03. No implementation task has run and all checkboxes remain unchecked.

| Check | Result |
| --- | --- |
| Checklist/ID/path/phase validation | PASS: 64 unique sequential tasks, correct story labels and explicit destination paths; setup 3, foundation 18, US1 8, US2 12, US3 7, US4 8, polish 8. |
| Parallel scheduling | 13 marked tasks in four explicit story waves plus stable-behaviour docs; shared owner/session/recovery/command edits are serialized. |
| Planned coverage | 33/33 FR/SC/SEC identifiers have build/integration and test/evidence mappings. Mapping is not a passed-control claim. |
| Model constraints | 32/32 table-field constraints appear verbatim in task descriptions, including the four repair-coordinator constraints added after review. Target-version encoding is explicit big-endian; migration filename uses the existing four-digit loader convention. |
| Source-backed design refinements | R22 replaces unsafe post-Save reattachment with stable pre-client guards/readiness and deferred deletion; T006/T008/T035 require runtime proof. The QR-only unarmed-passkey invariant has a real pinned-channel test in T022, not only fake-consumer coverage. These are planned controls, not executed tests. |
| Exception | E003-RX-01 remains the same named/scoped/expiring approval; T001 rechecks it at actual implementation start. No private fork, account action or threshold change is authorized. |
| Historical committed design review | PASS at d044f73 after correcting two prior CONCERNS findings. It predates constitution 2.0.0 and the current alignment; use a fresh review for implementation readiness. Implementation/native/manual tests remain planned. |

Validation utility: `D:\wa-003-repro-20261102-202105\validate-tasks.py`, run successfully against the generated file. The durable requirement/dependency details are in tasks.md; the temporary utility is not a CI dependency. Owner-judge design review is performed against the committed task/design set and recorded below, separately from these mechanical checks.

## Owner Reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-02 | c8455f4 | CONCERNS | |
| design | 2026-11-03 | 0e2473c | CONCERNS | |
| design | 2026-11-03 | d044f73 | PASS | |
| design | 2026-11-03 | 630bef6 | CONCERNS | User chose up to three five-second startup retries; revised design requires fresh review. |
| design | 2026-11-03 | 71f9004 | PASS | |

The latest non-stale design row governs implementation readiness; an absent/BLOCK/stale row stops implementation and CONCERNS requires the owner's recorded decision. In particular, d044f73 remains historical evidence, not approval of constitution 2.0.0 alignment. Exception approval is not a design/code PASS. Obtain code review on committed implementation/evidence before a PR; re-review covered changes as required.

The c8455f4 review identified context-blind appStateSyncLock contention defeating repair timing. R23 and re-review resolved it; the concern was not accepted or added to E003-RX-01. This historical CONCERNS row is superseded by the later PASS, not self-cleared.

R23 establishes early repair admission, permits key delivery, coalesces ordinary demand, bounds caller wait separately from SDK return and retains one FetchAppState invocation lease across timeout/generation changes. T036-T038 require runtime proof. Approved budgets are unchanged; counts remain 64 tasks/13 parallel/33 requirement mappings.

The 0e2473c re-review confirmed the mutex mechanism but corrected the SDK's context.WithoutCancel key-request lifetime. d044f73 removes the invented tail timer, scopes the lease to owner-invoked FetchAppState and documents actual response-wait/disconnect/device-guard behaviour. The final review verified both corrections against the pin and returned PASS with no outstanding owner decision. Neither CONCERNS was accepted and no exception/limit expanded. Recheck E003-RX-01 and follow task order before implementation; feature/code/native/manual evidence remains pending.
