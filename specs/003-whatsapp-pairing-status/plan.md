# Implementation Plan: WhatsApp Connection, Pairing and Status

**Branch**: `003-whatsapp-pairing-status` | **Date**: 2026-11-02 | **Spec**: [spec.md](spec.md)

**Status**: Implementation is in progress. The 2026-11-04 Linux/Chrome pairing compatibility change modifies reviewed design artifacts; prior design verdicts do not cover it. Re-run consistency analysis and obtain a fresh design review of the committed revised feature directory before further implementation. E003-RX-01's upstream receive bounds remain unmet; its owner is `tiger-seo`, expiry is the start of `2026-12-02` UTC, and its earlier reassessment triggers remain unchanged.

## Summary

Add a testable connection owner for terminal QR pairing, saved-session reuse, recoverable pairing state, offline status, secure version discovery and bounded 405 recovery. Preserve one client generation at a time, one private shared SQLite database and exclusive directory ownership until all admitted database work is quiescent.

Retain the exact upstream client rather than maintaining a fork. The user explicitly accepted only its documented receive-resource gaps through the temporary exception in [verification.md](verification.md#security-exceptions). Device-store safeguards are not excepted. The user separately approved device-path reuse of the existing store profile, migration 2 for interrupted protocol sync, and migration 3 for a conservative unsafe-run barrier. Neither marker authorizes deleting chats or credentials or implies sibling writes are one transaction.

## Technical Context

**Language/Version**: Go 1.27.0 module, Go 1.27.3 toolchain, always `CGO_ENABLED=0`.

**Primary Dependencies**: Keep whatsmeow `v0.0.0-20261027104441-1eec1136a50b`, util v0.11.0, modernc SQLite v1.61.0 and gofrs/flock v0.13.1. Justify one new root QR encoder, `rsc.io/qr v0.2.0` (tagged, pure Go, no module dependencies), with an in-repo deterministic terminal renderer. Never import its C-backed `libqrencode` subpackage. No module changes were made by planning.

**Storage**: Existing private `<data dir>/whatsapp.db`; pinned device schema 16 unchanged. Application schema 2 adds `device_repair`; schema 3 adds the bounded singleton unsafe-run marker. All SQL belongs in internal/store. Preserve WAL, foreign keys, identity checks and disk/full error classification. Marker and required revocation-cleanup commits use a verified stronger synchronization policy than the current WAL `synchronous(NORMAL)`; no account work begins if the required durability cannot be established.

**Testing**: Go testing, controlled HTTP/TLS responses, synthetic device/SQLite fixtures, in-process command tests and subprocess signal/lock tests. Use testing/synctest for in-memory six-hour/five-second schedules; keep SQLite and OS/network I/O outside fake-time bubbles. No real account in automated tests; no race-detector requirement because it needs CGO.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. Native control verification is required on Windows/Linux/macOS; cross-build alone does not establish ACL or signal behaviour.

**Project Type**: Single-module CLI/server. Existing stdio/serve shells own the connection but expose no tools/listener until later features.

**Performance Goals**: Approved application limits remain one owned client/attempt; 30-second watchdog for every owner-initiated connection, including startup, 405 and 515; at most three five-second saved-session shell startup retries; discovery 15 seconds and at most 1 MiB consumed; one QR up to 4 KiB and min(upstream expiry, 60 seconds); three five-second outdated retries; six-hour refresh; ten-second stop-failure notification without premature release. These are design acceptance targets, not feature measurements.

**Constraints**: No protocol patch/fork, runtime global-version mutation, implicit pairing, credential logging, old-deployment import, new account-management command or second database. Device work reuses the approved store gate/disk/input/output limits, not independent quotas. Monitoring and the Go soft memory target used in baseline probes are not hard upstream memory controls.

**Scale/Scope**: One account and one client generation per directory. Bounded app-owned maps/channels; zero contact/LID cache in our device implementations. Upstream residual receive retention remains explicitly excepted, not described as bounded. No finite total-history retention is introduced.

## Constitution Check

*Historical context*: Phase 0 initially stopped for missing upstream controls. The owner subsequently approved E003-RX-01 and durable device recovery, but that risk acceptance did not itself amend Principle V. *Current authority*: constitution 2.0.0 explicitly permits the identified upstream receive-resource deferral through Principles V/X. The existing approval supplies its scope, owner, compensating controls and expiry; the amendment neither renews nor expands it. The table distinguishes that approved temporary deviation from satisfied requirements. Fresh design review and implementation proof remain separate gates.

| Principle | Post-design disposition | Gate |
| --- | --- | --- |
| I. Source of truth | Source section 8.11 and feature Clarifications record both owner decisions; D1-D18 unchanged. | PASS |
| II. Self-contained binary | Same root module/five CGO-free targets; one justified pure-Go encoder, no external runtime. | PASS by design |
| III. Exclusive ownership | Shared gate, per-generation retirement, committed-view status, migrations 2/3 and truthful quiescence before pool close/lock release. | PASS by design; tests pending |
| IV. Clean stdout | Shared argument-dropping stderr bridge; only pair may render a terminal QR; owner events omit secrets. | PASS by design; tests pending |
| V. Secure defaults | Constitution 2.0.0 permits only the identified upstream receive-resource deferral in E003-RX-01. Private-file, device, application, transport and error controls remain mandatory. | Approved temporary deviation; upstream receive bounds remain unmet |
| VI. Testability | Narrow fake capabilities and synthetic negative/race/SQLite/command tests; baseline probes distinguished from control evidence. | PASS by design; implementation pending |
| VII. Predictable tools | No tools here; preserve four login-status fields and bounded future pairing boundary. | PASS for scope |
| VIII. Real systems | No account operations in planning; manual guide requires separate permission for each connect/pair action. | PASS |
| IX. Simplicity | No speculative flags/IPC, custom protocol stack, general repair command or total DB quota. Durable marker is necessary for the approved recovery policy. | PASS |
| X. Per-feature security | The existing named approval, exact pin, receive scope, controls, expiry, backlog and limitations are recorded in verification.md under constitution 2.0.0. F09 remains open; monitoring is not a limit. | Exception obligations recorded; fresh design review and control verification required |

Check approval/expiry at implementation readiness, code review and release/deployment. Reassessment is required before public release/broader deployment, on a dependency version change, or on a relevant upstream fix/resource incident. Expiry or a trigger blocks affected work/deployment/continued operation pending a new explicit owner decision. This is a workflow gate, not an undocumented expiration timer in the binary. Future features cannot inherit the exception for additional workloads. Do not claim controls passed on untested platforms.

## Architecture and Ownership

### Connection Boundary

`internal/wa` owns the protocol adapter, current device/client generation, copied status, current QR, version selector and recovery state. Callers use narrow lifecycle/pairing/event and future service capabilities, not the concrete client or raw SQL. The fake implements every declared capability with in-memory responses; real sending/upload/download tool paths remain disabled here. See [connection-owner.md](contracts/connection-owner.md).

Each generation has a unique epoch, cancellable lifetime, guarded callback admission and a Store device barrier. Callbacks do bounded state updates only; retirement/handler removal/disconnect run outside callback locks. Epoch checks prevent delayed QR/login/logout events overwriting current state. Do not overlap new clients with unquiescent retired generations.

Retire and replace the client after a terminal pairing window before starting another; reusing a client for unrelated windows would make its untagged late events ambiguous. After old access drains, reload saved identity to avoid overwriting it; a late identity from an unresolved attempt remains behind FR-018's offline recovery barrier, not automatically reused. A recoverable 405 may replace the transport/client within the same still-approved logical attempt and its fixed retry budget, but no controller may reopen a timed-out/cancelled window without recovery and a separate request. Ordinary saved-session reconnect remains within the current client generation.

Initial saved identity means locally logged-in, not verified online. PairSuccess clears QR and records saved identity; successful `pair` still requires Connected and durable credentials. Ordinary disconnect clears connected only. Confirmed logout invalidates login/QR, retires old access and ensures revoked device removal before creating a fresh device on the same container. Failure to persist revocation is fail-stopped, not successful re-pair. Application chats/messages are never removed.

### Version and Reconnect

Leave library globals immutable. Before first Connect install a payload callback that clones a fresh default device payload, freezes one atomic candidate version and updates AppVersion plus registration BuildHash consistently. Never hold socket/owner locks or perform I/O inside the callback. Preserve every identity/key field.

Restore the previous service's Linux/Chrome identification for fresh registration: decode the cloned registration's serialized companion properties, set OS `Linux`, platform `CHROME` and its version to the same selected handshake triple, and re-encode without changing other fields, including unknown protobuf fields on enclosing and nested messages. Mutate the three known fields of existing cloned AppVersion and companion Version messages instead of replacing them; allocate only if absent. Set the cloned user-agent OS version and build number from that triple as the previous `SetOSInfo` call did, and configure the client-specific Chrome QR client type before acquiring the QR channel. On decode or encode failure, return a nil payload to the pinned handshake's existing rejection path and never send an unmodified or partially modified registration; preserve credentials. The callback has no error return, so do not panic or assume it can propagate an ordinary error. Atomically coalesce fixed failure-category bits before a nonblocking wakeup; a generation-lifetime consumer reports fixed secret-free categories outside the callback and drains pending categories during orderly retirement. A full wakeup channel cannot erase pending bits; repeated identical failures may coalesce. Do not wait for the callback on logging or promise diagnostic delivery after a forced termination or indefinitely stalled logger. Do not mutate `DeviceProps`, `BaseClientPayload`, version globals or any shared protobuf; do not change saved identity, force a new pairing, add a setting, or claim an exact WhatsApp-controlled phone label. Keep the paired-session path and immutable version/hash snapshot coherent.

Offline runtime tests inject per-session codec failures into the real adapter callback and check its nil output, safety and diagnostics. An offline synthetic server cannot be assumed to pass the pinned Noise certificate verification: separately inspect and record the pin's nil-payload rejection before client-finish encoding/sending as source evidence, not an executed handshake test. No source-text assertion substitutes for the callback runtime tests, and no production certificate override or protocol fork is permitted. Exercise the real pinned QR construction through test-only `DangerousInternals().HandlePairDevice`, synthetic keys/reference and a denying transport, without printing QR data or contacting WhatsApp. The phone label remains a separately permissioned manual observation.

Discovery is single-flight: fixed verified HTTPS origin, no redirects/proxy surprise/credential headers, 15-second deadline, actual 1-MiB consumption cap, complete-body validation and uint32 decimal revision. Zero is allowed as upstream syntax; no invented floor. A truncated prefix is failure. Refresh only changes the next-handshake candidate; status describes the selected version, not a version negotiated by the active socket.

Set EnableAutoReconnect=true, InitialAutoReconnect=false and DisableLoginAutoReconnect=true before use. Ordinary library backoff remains; a post-error AutoReconnectHook is supplementary, not a pre-connect authorization check. Cancel lifetime/set expected-disconnect through Disconnect on retirement/terminal states. Route 515 manual login reconnect through one owner attempt; 405 through one coalesced three-by-five-second budget. Confirmed Connected alone resets that budget.

ConnectContext receives a generation lifetime, not a 30-second deadline that kills healthy connections. Each owner-initiated initial, explicit, startup-retry, 405 and 515 attempt gets a separate 30-second watchdog, disarmed by the first valid QR for an initial pairing connection or by Connected. Timeout cancels before Disconnect so socketLock can unwind; retain the sole attempt slot until the old call and generation access drain, never overlap a replacement. QR window and code lifetimes are separate from that watchdog. Dedicated one-consumer QR channels are drained promptly; replacement codes share approval until final timeout.

For `stdio`/`serve` with a saved identity, an initial failure or timeout starts at most three additional attempts, each after a cancellable five-second wait. The sequence ends on current Connected, shutdown, logout, storage fault or 405 handoff; 405 is handled exclusively by its existing budget, never supplemented by startup retries. On exhaustion retain the saved identity and disconnected snapshot, keep the shell running and emit an actionable restart/reassessment diagnostic. No automatic QR or fresh keys. `pair` retains its existing unsuccessful-exit behavior on saved-session connection failure; ordinary reconnect after a confirmed connection remains the library's separate policy.

### Device Admission and Persistence

Construct and retain one dbutil.Database over the existing writer, configure it equivalently to the pinned NewWithDB, and pass it to NewWithWrappedDB. The same transaction context works in vendor delegates and cache-less ContactStore/LIDStore implementations. Replace exported raw Device() with LoadDevice(ctx), returning only guarded device/barrier; reject a second live session. Install stable guard fields before NewClient, never briefly publish raw stores. For a fresh device, mark interface wiring Initialized before client use (not durable-login state), bind private delegates with exported sqlstore.NewSQLStore when the first validated JID is known, and let raw PutDevice skip its initializer. Ordinary calls before save-readiness fail fast before admission/SQL; do not let them occupy all active slots waiting for the Save that needs one. Failed save never signals success. Loaded devices bind before exposure. Audit every field/method and adapt internal fixtures rather than retaining a raw compatibility accessor.

Client-originated DeleteDevice must retire admission and return a typed deferred result without invoking Device.Delete's successful reset path; that path otherwise rewrites live interface/identity fields. After drain, the owner performs admitted confirmed-revocation removal through the private container. Failure blocks new generations/account work and reports unresolved cleanup, never success. On an explicit stop, still drain all work then close/unlock and exit unsuccessfully; do not hang forever after quiescence solely because deletion could not persist. The previously armed unsafe-run marker survives failed cleanup and blocks account work after restart; a warning alone is not the enforcement mechanism. A failed fresh pairing alone never erases a saved identity. Do not mutate live interface fields to reattach guards/Noop stores; see R22 as superseded below.

### Unsafe-Run Barrier And Offline Recovery

Application migration 3 adds a bounded singleton clean/unsafe marker and five-bit pending-repair transfer set in the existing database, not a sidecar. Under the exclusive data-directory lock, establish a marker state before account-capable work; durably arm unsafe before creating or starting an account-capable client, including first pairing, and keep it armed across reconnects and same-process re-pair. An arm error or ambiguous commit fails closed before account work and disables automatic clearing in that invocation; reopen and inspect committed state. Once armed, a later full/read-only disk cannot erase the recorded uncertainty. After orderly stop rejects new callbacks, drains all generations and durable writes, and commits any confirmed-logout cleanup, durably clear the marker before final store close. A definite failed cleanup/clear leaves the marker unsafe and exits unsuccessfully after actual drain. A clear with an ambiguous commit also exits unsuccessfully and performs no further account work; reopening must inspect the actual committed state, which may be clean only because all prerequisites were already fulfilled. No timeout alone permits clearing or lock release. An interrupted first pairing remains unsafe even with no local device row because a phone-side link may exist.

An opened fresh-pairing window that ends or is stopped before both durable identity and current-generation Connected confirmation is unresolved even after an otherwise orderly drain. Do not clear the marker for timeout, failure or cancellation merely because no local row exists; a phone-side link may have been created. PairSuccess without Connected does not resolve it. After the two confirmations, an ordinary later disconnect does not itself reclassify the saved session as unresolved; confirmed logout still requires its durable cleanup before clearing.

Use verified SQLite WAL `synchronous(FULL)` or a justified equivalent on every actual writer connection for marker arm/clear and preceding cleanup, under the existing store gate. On Darwin, FULL and `fullfsync` are distinct: verify whether the actual SQLite VFS/driver requests `F_FULLFSYNC` (including WAL synchronization) and whether it succeeds. If the stronger operation is unavailable or fails, fail closed if the required policy cannot be established; a documented ordinary-sync fallback may be described only with its weaker, conditional durability and never as a full hardware-flush guarantee. Do not claim unconditional power-loss survival: it depends on actual OS, filesystem and hardware honoring the sync used. Test writer replacement and errors on native platforms; process kills do not prove OS/power-loss behavior. Record unverified cases, filesystem flush violations, physical corruption and older-backup rollback as limitations.

Retirement must latch every logout or deferred delete recognized before its callback/device admissions drain, including one racing handler removal or an already-started stop. Synchronize the final drain, latch inspection, durable deletion and marker clear so clean cannot commit while recognized revocation is outstanding; failed or ambiguous deletion leaves the already-armed marker unsafe. Events not recognized before admission closes cannot be asserted to have been observed. Test the actual owner callback and guarded deletion with deterministic barriers, including shutdown/clear races and restart inspection.

The existing disk gate applies to all marker and cleanup writes, including clearing after an otherwise clean stop. In particular, once latched below 256 MiB it refuses until 512 MiB free, even between thresholds; do not bypass it for a marker. A definite refusal leaves unsafe state, exits unsuccessfully after drain, and tells the operator to free sufficient space and use the offline recovery procedure. Preserve ambiguous-commit reinspection semantics.

On reopening unsafe, missing/malformed marker in schema 3, or an existing legacy schema 1/2 database whose previous run state is unknown, refuse automatic Connect, pairing and QR. A newly created database may initialize clean only when the absent database was verified under the lock; if creation/migration is interrupted, the reopened database is existing/unknown, never reconstructed as fresh. Migrate an existing legacy database conservatively to unsafe even if it has no saved identity. The existing `pair` command offers an explicitly selected offline recovery branch for valid unsafe or legacy-unknown state, never the default, a QR retry Enter, redirected input or an ordinary shell startup: the operator identifies and verifies the named account/device in the phone, separately authorizes phone-side unlink if needed, and separately authorizes the application to remove only the local device identity if present. If a first-pairing crash left no local row, still require checking for an orphaned phone-side device. Missing/malformed schema-3 marker, corruption, unsupported schema or inability to validate storage instead fails closed and requires external remediation; automatic recreation could lose unknown repair bits. Commit local removal and clean marker atomically or with ordering that cannot expose clean state before removal, remain offline, and require a separate explicit action for fresh pairing. Refusal, EOF, inability to establish the account/device, definite failed persistence or lack of separate authorization leaves the marker armed and credentials/chats unchanged except for any already separately authorized, successfully committed local removal. An ambiguous recovery commit exits unsuccessfully and requires reopening to classify its committed state; clean can appear only after the separately authorized prerequisites were satisfied. No saved-credential reconnect is offered from uncertain state. Offline `status` does not migrate or start account work; it reports local identity presence when readable, warns on unsafe/legacy-unknown state and exits 1 without adding a login field.

Before confirmed-revocation deletion or separately authorized offline identity removal, atomically transfer that device's pending collection names into the store-level five-bit set in the same transaction as the deletion, so the `device_repair` foreign-key cascade cannot erase recovery need. Keep the bits across crash, clean marker state and re-pair. Materialize them as unknown-target repair needs for a new identity in the first-Save transaction or hold ordinary app-state admission closed until materialization commits; allow generation-valid sync-key delivery required by repair. Clear only after current fenced repair succeeds. Preserve chats/messages throughout; affected device keys may be removed only by genuine confirmed revocation or separately authorized offline identity removal, never by app-state repair itself.

All device calls share the existing store gate: 16 active/64 waiting, 5-second admission, 10-second operation context, 256-MiB disk reserve/512-MiB resume, 30-second disk-check cache. Reuse 16-MiB aggregate input, 32-MiB output, 256-byte JIDs and 1-KiB names. Account typed element overhead and guard integer overflow before copying. Do not blindly cap an entire contact snapshot to the message API's 1,000 messages; process bounded SQL chunks within the admitted aggregate budget. Refuse unused unbounded device exports (GetAllContacts/GetAllAppStateSyncKeys); future tools use existing bounded application queries. Implement cache-less session migration as well as contacts/LIDs: do not delegate MigratePNToLID to the raw SQLStore's growing migratedPNSessionsCache. Audit every store-resident cache; upstream processor/receive caches are distinct from this non-excepted device-store surface.

Nested DoDecryptionTxn calls reuse one live admission/deadline and the SAME dbutil transaction values; do not acquire another writer/gate lease. Rebind cancellation after WithoutCancel without losing transaction values. Validate before each call's writes. No assertion that policy errors cannot occur between sibling app-state commits is made.

### Durable Recovery

Application migration 2 adds at most one `device_repair` row per saved device/known collection; see [data-model.md](data-model.md). Commit the marker before app-state progress/MAC or bulk contact-snapshot changes. Any later error/crash retains uncertainty; never pretend earlier commits rolled back. Device credentials and message/chat records are outside the repair target.

After an authorized connection, one worker verifies startup-pending collections with the existing FetchAppState(fullSync=true, onlyIfNotSynced=false). At most one automatic attempt per collection per owner generation; five collections are the protocol-derived bound. Reuse the ten-second operation deadline per attempt. Use a dedicated app-state HTTP client with the reused ten-second/16-MiB body profile, TLS verification and redirect rejection. No gate lease is held across network waits. Oversized/slow real collections remain pending with an actionable failure, not silent truncation or increased limits.

The SDK takes a context-blind appStateSyncLock before consulting ctx. Establish startup recovery admission before NewClient/Connect: while required repair is pending, untagged app-state progress/MAC/bulk-snapshot operations for all collections fail fast before shared admission/SQL/HTTP, so automatic SDK sync cannot occupy that lock with a long fetch ahead of repair. Tagged owned repair and essential sync-key delivery remain allowed. Expected deferral is not a write fault; coalesce ordinary sync demand in the fixed five-name bitset and replay it incrementally after required repair, not as additional full-resync attempts.

Bound the caller with result/deadline selection; ctx alone does not preempt Mutex.Lock. Keep one owner-wide FetchAppState invocation slot across timeout/retirement until that call returns; no replacement or late clear. This does not serialize SDK-spawned key-request jobs: the pin detaches them with WithoutCancel, uses its existing 75-second response wait/24-hour per-key dedup, and stop must Disconnect to clear waiters while retired guards prevent late SQL. Do not invent a ten-second key-tail context/slot timer. Link BackgroundEventCtx to generation lifetime for non-detached work. Guarded-write faults pause/cancel the generation for restart-based recovery. Missing keys retain pending state while normal bounded key crypto/delivery remains allowed; inactive repair tickets block protected progress/clear, not those protocol operations. No new limit/retry or exception expansion; see R23.

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

`tasks.md` retains completed implementation history and unchecked compatibility and unsafe-run follow-ups. Requirements checklist remains local/ignored. Local source/backlog documents carry the owner decisions; portable approval and review history are in verification.md.

### Source Destinations for Implementation

```text
cmd/whatsapp-mcp/          # commands, injected factory, explicit offline recovery, safe stop
internal/wa/
  client.go               # narrow contracts/types
  owner.go                # state, epochs, health, monitoring and stop
  session.go              # pinned adapter, payload callback, login/logout
  pairing.go              # one QR attempt and current-code lifecycle
  version.go              # discovery/immutable candidate
  http.go                 # bounded discovery/app-state HTTP clients
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

The unsafe-run barrier additionally requires fresh and schema-1/2 -> 3 migration, real writer synchronization verification, process-crash boundary/reopen tests, failed cleanup and ambiguous commit tests, first-pair-without-a-row recovery, blocked supervisor restart, explicit interactive recovery/authorization tests and schema-3/legacy read-only status. Power-loss/native-platform claims require separate evidence.

Run formatting, vet, bounded-time Go tests, module integrity, CI-pinned vulnerability/libc checks, five CGO-free cross-builds and native Windows/Linux/macOS tests. No unperformed platform control is passed. Preserve heavy-store-test opt-in. Real-account acceptance is manual, separately permissioned and redacted; no command in planning executes it. Evidence goes in verification.md, not in assertions that baseline reproduction proves a fix.

Readiness workflow: task generation is complete. Re-run `/speckit-analyze` after covered artifact changes and obtain a current committed owner-judge design review against constitution 2.0.0 before `/speckit-implement`. A historical PASS does not cover the governance alignment; CONCERNS requires an explicit owner decision. Stop/reevaluate if the exception expires or a trigger occurs.

## Complexity Tracking

| Deviation | Why accepted / simpler alternative rejected | Control and owner |
| --- | --- | --- |
| V/X: approved temporary deviation for pinned upstream receive expansion/retained work | Constitution 2.0.0 expressly authorizes the narrowly scoped deferral; the user selected temporary acceptance after source research and 21 synthetic baseline measurements. An upgrade alone does not fix it; this is not proof of enforced receive bounds. | Existing E003-RX-01 approval, tiger-seo, expires 2026-12-02; unchanged pin/scope/controls, earlier review and backlog; not F09 closure or automatic approval of this revised design. |
| Added durable repair metadata | User chose restart-safe recovery over accepting indefinitely stale partial app-state/contact sync. Existing sibling methods have no whole-sync transaction seam. | One bounded migration, mark-before-write, ticket-fenced clear, bounded full resync and failure evidence; no key/chat deletion. |

The inherited feature 002 sidecar-creation deviation remains in its plan. It does not enlarge this exception. No other constitution deviation or new account action is authorized.
