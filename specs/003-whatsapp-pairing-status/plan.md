# Implementation Plan: WhatsApp Connection, Pairing and Status

**Branch**: `003-whatsapp-pairing-status` | **Date**: 2026-11-02 | **Spec**: [spec.md](spec.md)

**Status**: Phase 0 research and Phase 1 design complete under approved exception E003-RX-01. Task generation is a separate subsequent stage; no feature implementation or owner-judge PASS is claimed here. The exception is owned by `tiger-seo`, expires at the start of `2026-12-02` UTC, and requires earlier reassessment on its recorded triggers.

## Summary

Add a testable connection owner for terminal QR pairing, saved-session reuse, recoverable pairing state, offline status, secure version discovery and bounded 405 recovery. Preserve one client generation at a time, one private shared SQLite database and exclusive directory ownership until all admitted database work is quiescent.

Retain the exact upstream client rather than maintaining a fork. The user explicitly accepted only its documented receive-resource gaps through the temporary exception in [verification.md](verification.md#security-exceptions). Device-store safeguards are not excepted. The user separately approved device-path reuse of the existing store profile and a durable repair marker; migration 2 tracks interrupted protocol sync without deleting chats or credentials or claiming sibling writes are one transaction.

## Technical Context

**Language/Version**: Go 1.27.0 module, Go 1.27.3 toolchain, always `CGO_ENABLED=0`.

**Primary Dependencies**: Keep whatsmeow `v0.0.0-20261027104441-1eec1136a50b`, util v0.11.0, modernc SQLite v1.61.0 and gofrs/flock v0.13.1. Justify one new root QR encoder, `rsc.io/qr v0.2.0` (tagged, pure Go, no module dependencies), with an in-repo deterministic terminal renderer. Never import its C-backed `libqrencode` subpackage. No module changes were made by planning.

**Storage**: Existing private `<data dir>/whatsapp.db`; pinned device schema 16 unchanged. Plan application schema 2: retain migration 1 and add `device_repair`. All SQL belongs in internal/store. Preserve WAL, foreign keys, identity checks and disk/full error classification.

**Testing**: Go testing, controlled HTTP/TLS responses, synthetic device/SQLite fixtures, in-process command tests and subprocess signal/lock tests. Use testing/synctest for in-memory six-hour/five-second schedules; keep SQLite and OS/network I/O outside fake-time bubbles. No real account in automated tests; no race-detector requirement because it needs CGO.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. Native control verification is required on Windows/Linux/macOS; cross-build alone does not establish ACL or signal behaviour.

**Project Type**: Single-module CLI/server. Existing stdio/serve shells own the connection but expose no tools/listener until later features.

**Performance Goals**: Approved application limits remain one owned client/attempt; 30-second initial/explicit connection watchdog; discovery 15 seconds and at most 1 MiB consumed; one QR up to 4 KiB and min(upstream expiry, 60 seconds); three five-second outdated retries; six-hour refresh; ten-second stop-failure notification without premature release. These are design acceptance targets, not feature measurements.

**Constraints**: No protocol patch/fork, runtime global-version mutation, implicit pairing, credential logging, old-deployment import, new account-management command or second database. Device work reuses the approved store gate/disk/input/output limits, not independent quotas. Monitoring and the Go soft memory target used in baseline probes are not hard upstream memory controls.

**Scale/Scope**: One account and one client generation per directory. Bounded app-owned maps/channels; zero contact/LID cache in our device implementations. Upstream residual receive retention remains explicitly excepted, not described as bounded. No finite total-history retention is introduced.

## Constitution Check

*Initial stop*: earlier Phase 0 failed V/X because no complete upstream control or approved exception existed. The owner subsequently approved E003-RX-01 and durable device recovery. *Post-design check*: the gates below permit task generation while the exception is valid. Implementation proof and owner reviews remain later gates.

| Principle | Post-design disposition | Gate |
| --- | --- | --- |
| I. Source of truth | Source section 8.11 and feature Clarifications record both owner decisions; D1-D18 unchanged. | PASS |
| II. Self-contained binary | Same root module/five CGO-free targets; one justified pure-Go encoder, no external runtime. | PASS by design |
| III. Exclusive ownership | Shared gate, per-generation retirement, committed-view status, migration 2 and truthful quiescence before pool close/lock release. | PASS by design; tests pending |
| IV. Clean stdout | Shared argument-dropping stderr bridge; only pair may render a terminal QR; owner events omit secrets. | PASS by design; tests pending |
| V. Secure defaults | Mandatory private-file, device, application, transport and error controls; only listed upstream receive bounds use E003-RX-01. | PASS with the scoped exception, not an upstream fix |
| VI. Testability | Narrow fake capabilities and synthetic negative/race/SQLite/command tests; baseline probes distinguished from control evidence. | PASS by design; implementation pending |
| VII. Predictable tools | No tools here; preserve four login-status fields and bounded future pairing boundary. | PASS for scope |
| VIII. Real systems | No account operations in planning; manual guide requires separate permission for each connect/pair action. | PASS |
| IX. Simplicity | No speculative flags/IPC, custom protocol stack, general repair command or total DB quota. Durable marker is necessary for the approved recovery policy. | PASS |
| X. Per-feature security | Named approval, controls, expiry, backlog and limitations in verification.md; no broader F09 waiver or fake owner-review verdict. | PASS while E003-RX-01 is valid and controls remain implementation requirements |

Check approval/expiry again at tasks, implementation, code review and release. Public release/broader deployment requires reassessment even before expiry; an upstream fix or resource incident also triggers it. This is a workflow gate, not an undocumented expiration timer in the binary. Future features cannot assume the exception automatically covers additional workloads. Do not claim controls passed on untested platforms.

## Architecture and Ownership

### Connection Boundary

`internal/wa` owns the protocol adapter, current device/client generation, copied status, current QR, version selector and recovery state. Callers use narrow lifecycle/pairing/event and future service capabilities, not the concrete client or raw SQL. The fake implements every declared capability with in-memory responses; real sending/upload/download tool paths remain disabled here. See [connection-owner.md](contracts/connection-owner.md).

Each generation has a unique epoch, cancellable lifetime, guarded callback admission and a Store device barrier. Callbacks do bounded state updates only; retirement/handler removal/disconnect run outside callback locks. Epoch checks prevent delayed QR/login/logout events overwriting current state. Do not overlap new clients with unquiescent retired generations.

Retire and replace the client after a terminal pairing window before starting another; reusing a client for unrelated windows would make its untagged late events ambiguous. After old access drains, reload saved identity: a late but durable pairing must be reused, not overwritten with fresh keys. A recoverable 405 may replace the transport/client within the same still-approved logical attempt and its fixed retry budget, but no controller may reopen a timed-out/cancelled window without a new request. Ordinary saved-session reconnect remains within the current client generation.

Initial saved identity means locally logged-in, not verified online. PairSuccess clears QR and records saved identity; successful `pair` still requires Connected and durable credentials. Ordinary disconnect clears connected only. Confirmed logout invalidates login/QR, retires old access and ensures revoked device removal before creating a fresh device on the same container. Failure to persist revocation is fail-stopped, not successful re-pair. Application chats/messages are never removed.

### Version and Reconnect

Leave library globals immutable. Before first Connect install a payload callback that clones a fresh default device payload, freezes one atomic candidate version and updates AppVersion plus registration BuildHash consistently. Never hold socket/owner locks or perform I/O inside the callback. Preserve every identity/key field.

Discovery is single-flight: fixed verified HTTPS origin, no redirects/proxy surprise/credential headers, 15-second deadline, actual 1-MiB consumption cap, complete-body validation and uint32 decimal revision. Zero is allowed as upstream syntax; no invented floor. A truncated prefix is failure. Refresh only changes the next-handshake candidate; status describes the selected version, not a version negotiated by the active socket.

Set EnableAutoReconnect=true, InitialAutoReconnect=false and DisableLoginAutoReconnect=true before use. Ordinary library backoff remains; a post-error AutoReconnectHook is supplementary, not a pre-connect authorization check. Cancel lifetime/set expected-disconnect through Disconnect on retirement/terminal states. Route 515 manual login reconnect through one owner attempt; 405 through one coalesced three-by-five-second budget. Confirmed Connected alone resets that budget.

ConnectContext receives a generation lifetime, not a 30-second deadline that kills healthy connections. A separate watchdog limits initial/explicit progress to first valid QR or Connected; timeout cancels before Disconnect so socketLock can unwind. The QR window and code lifetimes are separate from that initial watchdog. Dedicated one-consumer QR channels are drained promptly; replacement codes share approval until final timeout.

### Device Admission and Persistence

Construct and retain one dbutil.Database over the existing writer, configure it equivalently to the pinned NewWithDB, and pass it to NewWithWrappedDB. The same transaction context works in vendor delegates and cache-less ContactStore/LIDStore implementations. Replace exported raw Device() with LoadDevice(ctx), returning only guarded device/barrier; reject a second live session. Install stable guard fields before NewClient, never briefly publish raw stores. For a fresh device, mark interface wiring Initialized before client use (not durable-login state), bind private delegates with exported sqlstore.NewSQLStore when the first validated JID is known, and let raw PutDevice skip its initializer. Ordinary calls before save-readiness fail fast before admission/SQL; do not let them occupy all active slots waiting for the Save that needs one. Failed save never signals success. Loaded devices bind before exposure. Audit every field/method and adapt internal fixtures rather than retaining a raw compatibility accessor.

Client-originated DeleteDevice must retire admission and return a typed deferred result without invoking Device.Delete's successful reset path; that path otherwise rewrites live interface/identity fields. After drain, the owner performs admitted confirmed-revocation removal through the private container. Failure blocks new generations/account work and reports unresolved cleanup, never success. On an explicit stop, still drain all work then close/unlock and exit unsuccessfully; do not hang forever after quiescence solely because deletion could not persist. Warn that saved state remains unverified and require operator remediation/reassessment before restart. A failed fresh pairing alone never erases a saved identity. Do not mutate live interface fields to reattach guards/Noop stores; see R22.

All device calls share the existing store gate: 16 active/64 waiting, 5-second admission, 10-second operation context, 256-MiB disk reserve/512-MiB resume, 30-second disk-check cache. Reuse 16-MiB aggregate input, 32-MiB output, 256-byte JIDs and 1-KiB names. Account typed element overhead and guard integer overflow before copying. Do not blindly cap an entire contact snapshot to the message API's 1,000 messages; process bounded SQL chunks within the admitted aggregate budget. Refuse unused unbounded device exports (GetAllContacts/GetAllAppStateSyncKeys); future tools use existing bounded application queries. Implement cache-less session migration as well as contacts/LIDs: do not delegate MigratePNToLID to the raw SQLStore's growing migratedPNSessionsCache. Audit every store-resident cache; upstream processor/receive caches are distinct from this non-excepted device-store surface.

Nested DoDecryptionTxn calls reuse one live admission/deadline and the SAME dbutil transaction values; do not acquire another writer/gate lease. Rebind cancellation after WithoutCancel without losing transaction values. Validate before each call's writes. No assertion that policy errors cannot occur between sibling app-state commits is made.

### Durable Recovery

Application migration 2 adds at most one `device_repair` row per saved device/known collection; see [data-model.md](data-model.md). Commit the marker before app-state progress/MAC or bulk contact-snapshot changes. Any later error/crash retains uncertainty; never pretend earlier commits rolled back. Device credentials and message/chat records are outside the repair target.

After an authorized connection, one worker verifies startup-pending collections with the existing FetchAppState(fullSync=true, onlyIfNotSynced=false). At most one automatic attempt per collection per owner generation; five collections are the protocol-derived bound. Reuse the ten-second operation deadline per attempt. Use a dedicated app-state HTTP client with the reused ten-second/16-MiB body profile, TLS verification and redirect rejection. No gate lease is held across network waits. Oversized/slow real collections remain pending with an actionable failure, not silent truncation or increased limits.

The SDK takes a context-blind appStateSyncLock before consulting ctx. Establish startup recovery admission before NewClient/Connect: while required repair is pending, untagged app-state progress/MAC/bulk-snapshot operations for all collections fail fast before shared admission/SQL/HTTP, so automatic SDK sync cannot occupy that lock with a long fetch ahead of repair. Tagged owned repair and essential sync-key delivery remain allowed. Expected deferral is not a write fault; coalesce ordinary sync demand in the fixed five-name bitset and replay it incrementally after required repair, not as additional full-resync attempts.

Bound the caller with a result/deadline select and cancellation; a ctx passed directly to FetchAppState does not preempt Mutex.Lock. Hold one owner-wide actual SDK-call slot across timeout/retirement until the call really returns; do not spawn a replacement or let late results clear markers. Link BackgroundEventCtx to generation lifetime before use. On an active guarded-write fault pause/cancel the generation and use restart-based durable recovery, not a contended same-client recovery loop. Missing-key failures retain pending state while allowing key delivery. No new numeric limits or full-resync retries are introduced; details/tests are in research R23 and the owner contract.

Clear only after the owner-controlled call completes, every guarded mutation succeeded and a ticket/revision/target fence proves no later write superseded recovery. Bare Connected/AppStateSyncComplete is insufficient. Compare-and-delete is fenced against a newly admitted mutation; late ordinary work either prevents clear or records a new dirty row before it writes. Successful ordinary incremental updates may leave a conservative marker for next-start verification. That is bounded extra work, not a hidden stale-state guarantee.

Track startup recovery health separately from physical login status. A marker alone is not proof of a live fault, but startup-pending or failed guarded writes prevent claiming device-data readiness. Later contact-dependent tool wiring must consult this internal health state. Pair success proves linking, not completed contact sync; a pending-repair diagnostic is allowed and markers survive pair exit. After a persistent fault, stop account work and require explicit operator restart/reassessment, not an unlimited repair/reconnect loop.

### Status, Output and Stop

Status never uses writable Open. Add AcquireExisting to preserve missing-storage no-artifact semantics while detecting a held lock before its owner creates the database. Present storage is inspected under exclusive ownership through the committed WAL view; older readable schema may be inspected without upgrading. See [cli.md](contracts/cli.md).

Move the audited argument-dropping log bridge into a shared internal package. Keep its tested storage log shape, redact upstream arguments at every level and safely handle module/template text. Emit bounded owned outcome/reason codes rather than raw errors/QR/payloads. One coarse, secret-free runtime sample per minute at debug level plus lifecycle samples supports operator monitoring; this is a fixed observation cadence, not a new resource ceiling. No history of samples or account identifiers is retained. OS process memory observation and incident steps belong in OPERATIONS.md.

Owner retirement closes per-generation admissions, cancels lifetime and removes/disconnects outside callbacks, then waits for device/callback quiescence. Full shutdown also closes shared admission. Correct gate.close so it reports still-active work instead of returning success after both waits. Add a single quiescence completion signal; leave pools open and `closed=false` until drain is proved. A timeout logs failure after ten seconds but waits the current step and finishes remaining cleanup using live contexts. Only after shared work drains may pools close and the lock release. Pool-close errors after quiescence cause unsuccessful exit; still-active work must not advance to lock release. Forced second signal remains explicitly unclean.

## Project Structure

### Documentation

```text
specs/003-whatsapp-pairing-status/
  spec.md
  plan.md
  research.md
  data-model.md
  contracts/connection-owner.md
  contracts/cli.md
  quickstart.md
  verification.md
```

`tasks.md` is generated separately after planning from this completed design; the discarded earlier draft is not reused. Requirements checklist remains local/ignored. Local source/backlog documents carry the owner decisions; portable approval is in verification.md.

### Source Destinations for Implementation

```text
cmd/whatsapp-mcp/          # commands, injected factory, Enter retry, safe stop
internal/wa/
  client.go               # narrow contracts/types
  owner.go                # state, epochs, health, monitoring and stop
  session.go              # pinned adapter, payload callback, login/logout
  pairing.go              # one QR attempt and current-code lifecycle
  version.go              # discovery/immutable candidate
  recovery.go             # 405, 515 and bounded durable-sync recovery
  qr.go                   # deterministic root-encoder renderer
  fake.go                 # offline substitute capabilities
internal/store/
  device.go               # same-gate device lease/wrappers
  device_contacts.go      # cache-less transaction-aware contact view
  device_lids.go          # cache-less LID view
  repair.go               # durable marker/ticket/fence queries
  inspect.go              # read-only committed-view inspection
  migrations/0002_device_repair.sql
internal/walog/           # relocated shared bridge
internal/lock/            # no-create acquisition using existing safety checks
internal/wa/testdata/cmd/session-check/
.github/workflows/ci.yml  # offline privacy fixture, unchanged target/security policy
OPERATIONS.md            # pairing/status/recovery/exception/incident guide
```

Keep code within these existing responsibilities; no protocol fork, general framework or new service. Explicitly build/review testdata helpers because ./... excludes them.

## Phase Outputs and Verification Strategy

Phase 0: research R1-R23, baseline R17, explicit decisions R18/R21, stable publication R22 and contention-aware repair R23. Phase 1: data model, connection/CLI contracts and quickstart. The owner approval resolves the prior research stop; it does not resolve F09 globally or pass implementation tests.

Required later coverage: fake capabilities; QR renewal/EOF and stale epochs; stable first-Save guard publication/readiness, deferred deletion and nested transactions; direct writes held through shutdown; migration 1 -> 2 and fresh startup; marker-before-write/crash/late-clear races; errors between version/MAC/contact methods and successful/failed full recovery; exact discovery consumption and payload/hash snapshots; coalesced 405/515/ordinary recovery; empty/locked/unsafe/committed-WAL offline status; secret-free logs and no stdio/listener/tool side effects. Replace CI's stub-status privacy fixture with an offline seed helper.

Run formatting, vet, bounded-time Go tests, module integrity, CI-pinned vulnerability/libc checks, five CGO-free cross-builds and native Windows/Linux/macOS tests. No unperformed platform control is passed. Preserve heavy-store-test opt-in. Real-account acceptance is manual, separately permissioned and redacted; no command in planning executes it. Evidence goes in verification.md, not in assertions that baseline reproduction proves a fix.

Next: `/speckit.tasks`, then cross-artifact analysis and the committed owner-judge design review. Do not implement before a current passing review/owner-resolved CONCERNS decision. Stop/reevaluate if the exception expires or a trigger occurs.

## Complexity Tracking

| Deviation | Why accepted / simpler alternative rejected | Control and owner |
| --- | --- | --- |
| V/X: pinned receive expansion/retained work lacks complete hard bounds | User selected temporary acceptance after source research and 21 synthetic baseline measurements; an upgrade alone does not fix it and maintaining a broad fork is not automatically proportionate. | E003-RX-01, tiger-seo, expires 2026-12-02; mandatory controls, earlier review and backlog; not F09 closure. |
| Added durable repair metadata | User chose restart-safe recovery over accepting indefinitely stale partial app-state/contact sync. Existing sibling methods have no whole-sync transaction seam. | One bounded migration, mark-before-write, ticket-fenced clear, bounded full resync and failure evidence; no key/chat deletion. |

The inherited feature 002 sidecar-creation deviation remains in its plan. It does not enlarge this exception. No other constitution deviation or new account action is authorized.
