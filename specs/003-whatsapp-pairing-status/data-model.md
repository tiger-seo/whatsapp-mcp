# Data Model: WhatsApp Connection, Pairing and Status

**Date**: 2026-11-02. This is design, not an implemented schema or verified control.

## Device Session and Generation

Use the existing whatsmeow device row, keys and session-specific/global interfaces. No new copy of credentials is stored. Owner generations add only process-local control state:

| Field | Meaning and constraint |
| --- | --- |
| epoch | Monotonically distinct within one owner; stale epoch events never change current state. |
| device/client | At most one active pair; fresh device after confirmed revocation, same shared container. Never overlap a new client with unquiescent old access. |
| lifetime | Cancellable generation context, not a 30-second expiring connection context. |
| admission | Per-generation reject/drain plus the one shared store gate. Nested transaction calls reuse a live lease. |
| phase | idle, connecting, pairing, connected, disconnected, logged_out, retiring, storage_failed, stopping or stopped. |
| storage health | Separate from physical connection; startup repair pending, guarded-write fault and ready are internal, not extra login-tool fields. |

Only confirmed logout permits revocation cleanup/replacement of a saved identity. A new unpaired client may be created after the prior window drains and a reload confirms no saved identity exists. Ordinary disconnect preserves credentials and logged-in state. Save/storage failure cannot count as successful durable linking. Forced termination/disk failure is not described as clean recovery.

Guard interface fields are installed before client creation and stay stable throughout the generation. Initialized denotes interface wiring, not durable identity; ordinary pre-ready calls fail fast before gate admission/SQL until first Save succeeds, rather than exhausting slots waiting for Save. Private raw delegates may bind on that Save's validated JID without being published in device fields. Client delete requests retire the generation and return deferred cleanup rather than allowing the library to reset live fields; confirmed-revocation SQL cleanup happens after drain. Failed pairing preserves any saved identity for explicit verification rather than silently deleting it.

Failed revocation persistence blocks new generations in the current owner, not safe finalization forever: on explicit stop, close/unlock only after true quiescence and exit unsuccessfully with an unresolved-state warning. The previously armed unsafe-run marker blocks automatic account work on restart; no promise of persisted deletion is made when storage rejected it. Recovery must not infer that uncertainty proves revocation.

### Unsafe-Run Marker

Application migration 3 adds exactly one bounded store-level marker with clean/unsafe states and a five-bit set of repair collections whose device-specific markers would otherwise be lost on identity deletion. A new database known to have been created under the lock starts clean; an existing schema 1/2 database initializes unsafe because its previous run cannot be classified, even if no device row exists. If creation or migration was interrupted before the marker was committed, the reopened database is existing/unknown, never reconstructed as freshly created. Missing, malformed, unreadable or unsupported marker state is never clean. A committed unsafe state precedes all account-capable work, remains armed across all generations and is cleared only after true drain and durable completion of confirmed-revocation cleanup, or after separately authorized offline recovery removes the local identity if present. An arm error or ambiguous commit prevents automatic clear in that invocation; reopen and inspect the committed state. The marker is not proof of logout, identity validity or phone-side unlink; clean/unsafe and the pending repair bitset are independent.

The marker schema enforces one row with primary key `id = 1`, non-null state limited to `clean` or `unsafe`, and a non-null integer repair bitset from 0 through 31. The bit positions correspond exactly to the five existing supported `device_repair` collections; migration and tests must use one documented stable mapping. Status inspection validates the singleton and all constraints without writing. A missing or malformed schema-3 row is classified unsafe/corrupt and is not automatically recreated because the pending bits may be unknown.

An opened fresh-pairing attempt stays unresolved until it has both a durably saved identity and a current-generation Connected confirmation. Timeout, failure, cancellation or an orderly stop before both conditions retains unsafe state: the phone may have linked a device even when no local row exists. PairSuccess alone is insufficient. Subsequent automatic pairing or saved-identity reuse is blocked until offline recovery.

Recovery uses the explicit, interactive offline branch of `pair`: the operator verifies the named account/device and any orphaned link, performs separately authorized phone-side unlink if needed, and separately authorizes removal of the local identity if present. Removal and marker clearing have atomic or equivalent safe ordering; no network or fresh QR occurs in that recovery action, and no chats/messages are deleted. Refusal, EOF, unverified phone state or definitely failed persistence keeps the marker unsafe. An ambiguous commit exits unsuccessfully and requires reopening to inspect committed state; clean can appear only after the authorized prerequisites were met. A later explicitly approved pair is separate. Process/OS/power failure coverage requires verified durable synchronization, not WAL `synchronous(NORMAL)` alone; physical corruption, storage that violates flush guarantees and restored older backups remain outside the guarantee.

Before any genuine confirmed-revocation deletion or separately authorized offline identity removal, atomically union that device's pending `device_repair` collections into the store-level bitset in the same transaction as deletion, before foreign-key cascades remove the original rows. Retain the bitset across marker clearing, a missing identity and restart. When a new identity is saved, materialize the pending collections as new-device repair needs with unknown target and valid revision in the same transaction as first Save, or keep ordinary app-state admission closed until materialization commits. This closes the interval between Save and marker creation while still permitting the generation-valid key delivery needed for repair. Fence and clear each need only after successful current repair, never merely after pairing. Interrupted transfer/materialization cannot lose a bit or falsely declare recovery complete. Device session keys removed by separately authorized identity removal or genuine revocation are distinct from keys protected against deletion by app-state repair.

## Connection Status

| Field | Constraint |
| --- | --- |
| connected | True only after the current generation's Connected confirmation; false on disconnect/logout/stop. |
| logged_in | Locally saved identity unless confirmed revoked: false immediately on confirmed logout, preserved on ordinary disconnect. Revoked-device retirement/cleanup must finish before a new generation. Not proof of current network acceptance. Fresh PairSuccess may establish saved identity, but pair command still waits for Connected. |
| pairing_required | Derived: not logged_in AND current code exists AND now is before its expiry. False after code expiry even if a window awaits replacement. |
| wa_version | Selected immutable candidate rendered as a three-part version, not a claim of server negotiation. Standalone status uses the bundled version only and labels it unrefreshed. |

Return a copied coherent snapshot. Standalone status always connected=false/pairing_required=false, with an offline-credentials warning. Do not expose device JID, keys, QR or internal recovery ticket in ordinary status.

## Pairing Attempt

| Field | Constraint |
| --- | --- |
| attempt identity | Bound to the owner epoch; one attempt at a time. |
| approval | Explicit request opens one window including upstream replacement codes, never a new window automatically. |
| current code | At most 4 KiB; empty/malformed/over-limit/encoder-failed input cannot replace valid state. |
| current representation | At most one derived terminal/image representation; no QR history, files or logs. |
| expires_at | Earlier of upstream expiry and 60 seconds from receipt; use monotonic elapsed time internally. |
| result | active, succeeded, timed_out, cancelled or failed. Terminal outcomes invalidate code and late events. |
| consumer | Exactly one fast channel consumer; callers share the snapshot/update signal rather than reading the upstream channel. |

Code expiry is not final attempt timeout. An unresolved final timeout retains unsafe state and requires explicit offline recovery before a separately approved new pairing; Enter, EOF and cancellation do not authorize a new attempt or recovery. Request wait-context expiry does not silently grant/cancel another caller's shared window; explicit owner cancellation controls the attempt.

A client generation is bound to its logical pairing window; retire/drain it before a different window so untagged old events cannot masquerade as the new attempt. Reload durable identity after drain before deciding to create fresh keys. Version recovery inside a still-approved window remains subject to the existing 405 budget, never authorization to reopen a terminal window.

## Version Selection and Recovery Budget

- Version is a value triple of uint32 components. Discovery accepts the pinned `2.3000.<uint32 decimal>` grammar, including revision zero; no invented version floor. Preserve bundled fallback and latest successful candidate in memory; no version persistence/migration.
- Handshake freezes one candidate and registration hash together in a fresh cloned payload. Library globals remain immutable. Refresh never changes an in-flight payload or disconnects a healthy socket.
- One version request in flight, 15 seconds, at most 1 MiB actual application-body consumption, complete response required, redirects/TLS errors rejected. Unknown-length bodies reaching the budget without proving completion are conservatively rejected, never accepted as prefixes.
- The 405 budget is three retries after the initial rejection, each five seconds and a refresh attempt; duplicate events share it. Only current Connected resets it. Ordinary network backoff is separate and cannot bypass terminal state.
- Saved-session shell startup recovery has a distinct budget of three retries after the initial failure/timeout, each after a cancellable five-second wait. Connected, shutdown, logout, storage fault or 405 handoff ends that sequence. Exhaustion retains identity and a disconnected shell; it never starts pairing or replenishes the 405 budget.
- Each owner-initiated initial, explicit, startup-retry, 405 or 515 connection attempt has a 30-second watchdog; timeout cancels and drains before another attempt. A still-undrained call retains the single attempt slot and ownership. A healthy connection is not bounded by that watchdog.

## Durable Repair Metadata

Application migration 2 adds `device_repair`; migration 3 adds the singleton unsafe-run marker. Device schema 16 and chats/messages remain unchanged. Fresh stores run migrations 1 through 3; an existing schema 1/2 database upgrades to schema 3 with unsafe state. Inspection of an old readable schema remains read-only and reports legacy safety unknown. No Go migration file is written during this planning phase.

| Column | Constraint |
| --- | --- |
| device_jid | Required, canonical saved device identity, at most 256 bytes; foreign key to whatsmeow_device.jid with ON DELETE CASCADE. |
| collection | Required, exactly one of critical_block, critical_unblock_low, regular_high, regular, regular_low. |
| revision | Required positive signed 64-bit integer; increments before each protected mutation; overflow fails without resetting live state. |
| target_version | Nullable fixed 8-byte big-endian encoding of the protocol uint64 version; NULL means reset/unknown rather than an integer-domain truncation. |
| repair_ticket | Nullable fixed 16-byte non-secret uniqueness token for an owner-controlled repair; do not log it. Ordinary mutations do not inherit another repair's ticket. |
| updated_at_ms | Required UTC Unix milliseconds, diagnostic only, never a correctness/ordering substitute for revision. |

Primary key is (device_jid, collection). At most one row per five known collections for the owned device; no message bodies, contact snapshots, credentials or unbounded error strings. Unknown collection/JID/input is rejected before marker or data writes. Marker fields do not define a new total database quota.

### Marker and Repair Transitions

1. Before a protected version/MAC/bulk-contact mutation, validate the call, commit an upsert/increment of its marker, then delegate under the same admitted operation. If recording fails, do not mutate the protected state. If later work fails or crashes, marker remains.
2. Separate sibling methods may already have committed. A marker records uncertainty; it does not roll them back. Ordinary successful updates may conservatively leave a marker for next-start verification.
3. After an authorized connection, process startup-pending/failed collections through one serialized worker, at most one automatic attempt per collection per owner generation. Do not consume a gate lease while waiting for the network. Reuse the 10-second operation budget and 16-MiB external-body budget.
4. Prepare a repair ticket; propagate it through the actual full-resync call. Track every guarded-write error even when upstream only logs it. Unrelated/interleaved mutations replace/invalidate the ticket/revision so an old completion cannot clear new work.
5. Clear by compare-and-delete only after successful call completion, no guarded errors, current generation and an admission fence proving no superseding/in-flight protected mutation. If fencing/CAS fails, retain the row; no silent second repair loop.
6. Timeout/failure keeps durable state and internal recovery-pending health. Operator restart after fixing the cause may retry. Never automatically unpair, evict cryptographic state, delete chats/messages or declare device-data readiness on Connected alone.

The marker-admission/clear fence must not deadlock nested Signal transactions or hold a transaction/SQL writer across network calls. Fault-injection and interleaving tests are part of the acceptance contract, not assumed proof.

### Repair Coordinator

| Field | Constraint |
| --- | --- |
| admission_mode | Before connecting with pending repair, ordinary untagged app-state progress/MAC and bulk-snapshot calls fail fast; tagged repair and sync-key delivery remain permitted. Resume ordinary sync only after required recovery succeeds. |
| actual_call | At most one owner-invoked FetchAppState call is outstanding across client generations; timeout/retirement does not release its slot before that invocation returns. |
| deadline_outcome | Ten-second caller wait; cancel/invalidate the repair ticket, retain the marker, discard late results, and start no replacement FetchAppState call while the prior invocation remains outstanding. |
| deferred_sync | Coalesce deferred ordinary sync demand in a five-collection bitset; replay through the same FetchAppState slot after repair, never as another full-resync retry or an unbounded per-notification goroutine. |

The SDK's appStateSyncLock is a plain mutex before its context-aware calls. A context alone cannot bound that wait. Startup admission mode is established before NewClient/Connect, not merely after an error: untagged background FetchAppState then fails its first guarded progress call before SQL/HTTP, rather than holding that mutex across network sync ahead of repair. Expected deferral does not become a storage fault or consume a repair attempt. Never block sync-key delivery needed by a tagged repair. A dirty marker created during otherwise successful ordinary sync does not by itself switch admission mode.

The controller waits on result/cancellation/deadline, not directly on an uninterruptible SDK call; its one actual-call lease remains held until that call returns. Late work sees an inactive ticket/cancelled context and cannot clear a marker or mutate through a retired guard. Deferred ordinary requests are coalesced/replayed as ordinary incremental sync after repair, not extra full-resync attempts; failure leaves demand pending for a new notification/connection event, never a tight autonomous retry loop. A guarded-write fault during active use pauses/cancels that generation and requires an operator-authorized restart, whose pending markers activate startup admission before competing SDK work. Missing sync keys are transient: retain pending state and allow key delivery, without repeatedly retrying full resync.

The SDK spawns missing-key requests with context.WithoutCancel: their ctx has no caller deadline/cancellation. They are SDK-owned protocol jobs, not outstanding FetchAppState invocations; do not invent an owner tail timer or claim the ten-second caller budget bounds them. The pin has a default 75-second message-response wait, per-key 24-hour request dedup and disconnect-cleared response waiters/retryFrame connection wait up to five seconds; these are component behaviours, not a hard total-tail/RSS guarantee. On stop disconnect before finalization and deny/cancel all old-generation device operations even for detached contexts. Inactive repair tickets forbid protected progress/clear, not necessary generation-valid key-request crypto/key delivery, which still uses fresh shared operation budgets. Never free a FetchAppState slot before that actual invocation returns or clear on late success.

## Store and Stop States

| State | Invariant |
| --- | --- |
| open | Existing accepted shared budgets apply to device and application operations. |
| generation retiring | Old client cannot admit new calls; admitted calls drain. Shared store remains available for controlled revocation/fresh generation only after old drain. |
| store closing | Shared admission permanently closed; reject new calls; contexts cancelled after grace. Pools remain open while work is active. |
| quiescent | Zero admitted operations and no future admissions; safe to close pools. Completion signal closes once. |
| closed | Pools closed after quiescence; lock release may follow. Close errors are reported; timeout alone never establishes this state. |

Reuse 16 active/64 waiters, five-second admission, ten-second operation, 16-MiB input/32-MiB output and existing disk reserve/resume profile. Reuse appropriate field/aggregate budgets, not an unrelated total-address-book count. Contact/LID device views and session migration retain no private raw cache; raw migratedPNSessionsCache is not an excepted device-store control. Unused all-contact/all-key exports fail explicitly rather than truncate or materialize unlimited results.

E003-RX-01 applies only to documented upstream receive resource exposure. It does not relax these store-state or marker invariants.
