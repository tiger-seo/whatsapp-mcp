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

Failed revocation persistence blocks new generations in the current owner, not safe finalization forever: on explicit stop, close/unlock only after true quiescence and exit unsuccessfully with an unresolved-state warning. Remediation/reassessment is required before restart; no promise of persisted deletion is made when storage rejected it.

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

Code expiry is not final attempt timeout. Final timeout requires an explicit new request; terminal CLI uses Enter. EOF/cancellation exits unsuccessfully and does not start another attempt. Request wait-context expiry does not silently grant/cancel another caller's shared window; explicit owner cancellation controls the attempt.

A client generation is bound to its logical pairing window; retire/drain it before a different window so untagged old events cannot masquerade as the new attempt. Reload durable identity after drain before deciding to create fresh keys. Version recovery inside a still-approved window remains subject to the existing 405 budget, never authorization to reopen a terminal window.

## Version Selection and Recovery Budget

- Version is a value triple of uint32 components. Discovery accepts the pinned `2.3000.<uint32 decimal>` grammar, including revision zero; no invented version floor. Preserve bundled fallback and latest successful candidate in memory; no version persistence/migration.
- Handshake freezes one candidate and registration hash together in a fresh cloned payload. Library globals remain immutable. Refresh never changes an in-flight payload or disconnects a healthy socket.
- One version request in flight, 15 seconds, at most 1 MiB actual application-body consumption, complete response required, redirects/TLS errors rejected. Unknown-length bodies reaching the budget without proving completion are conservatively rejected, never accepted as prefixes.
- The 405 budget is three retries after the initial rejection, each five seconds and a refresh attempt; duplicate events share it. Only current Connected resets it. Ordinary network backoff is separate and cannot bypass terminal state.

## Durable Repair Metadata

Application migration 2 adds `device_repair`; device schema 16 and chats/messages remain unchanged. Fresh stores run migrations 1 then 2; existing schema 1 upgrades to 2. Inspection of an old readable schema remains read-only. No Go migration file is written during this planning phase.

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
