# Implementation Plan: Message Ingest

**Branch**: `005-message-ingest` | **Date**: 2026-11-13 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/005-message-ingest/spec.md`

**Status**: Phase 0 and Phase 1 complete; tasks generated and the cross-artifact analysis of 2026-11-13 applied. The
first design review (`61a63f4`) returned BLOCK, the second (`1b1d91f`) CONCERNS and the third (`84dfa32`) PASS with
three low findings; those, the owner's T004 answers and the placeholder decision are applied, including two fork
commits the owner approved (`d83e19e` and `bedcf01`, contract items 8 and 9). The race-detector condition is met for
each pin (`verification.md`). Next: a fourth design review of these edits, then `/speckit-implement`.

## Summary

Store supported live messages and WhatsApp-supplied history with their text, captions and media metadata, normalise
LIDs to phone identities, merge LID data at startup and when a mapping appears, and name chats without a group lookup
per message. Live messages are stored inside the library's event handler, with acknowledgements sent only after a
successful write. History is downloaded and stored by one bounded worker per generation. A message-store failure stops
the connected command with a report; a receive overload is reported as a possible gap. Ingest joins feature 004's
retirement order without moving any step. The library is the owner-approved fork revision
`tiger-seo/whatsmeow@bedcf01`, which enforces the receive bounds E003-RX-01 had deferred and sends nothing to the
user's phone on another account's behalf.

## Technical Context

**Language/Version**: Go 1.27 (toolchain `go1.27.4`); `CGO_ENABLED=0` for every build and test, except the one
race-detector run in WSL2 that the owner allowed.

**Primary Dependencies**: `go.mau.fi/whatsmeow`, required at upstream `f1b798561919` and replaced by
`github.com/tiger-seo/whatsmeow v0.0.0-20261113124132-bedcf0185245` (branch `receive-bounds`, commit `bedcf01`: `9209108`
plus the two phone-request gates, all approved by the owner on 2026-11-13, research R1); `modernc.org/sqlite` through `internal/store`. No new dependency.

**Storage**: The existing `whatsapp.db`; no migration. Five read-only store helpers (the fifth, `ChatName`, added at the US4 owner checkpoint on 2026-11-14 so the name cache
knows the stored name's quality) and two exported validation helpers (data model).

**Testing**: `go test` with fakes behind a narrow client interface, the `internal/wasim` simulator, and temporary
stores; quiet-stop pass-rate rerun with `SynchronousAck`; the race detector once in WSL2. No real account.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

**Project Type**: Single-process CLI that owns one WhatsApp session and one SQLite store.

**Performance Goals**: One group lookup per group while its name stays available (SC-005). The live write path takes
one store write plus, at most, one chat update per message; the fork's 2,048-item queue absorbs bursts. An initial
history sync with a concurrent live burst causes no `receive_overload` on the simulator (research R3, measured).

**Constraints**: Fork limits as approved (16 MiB frame after expansion, 64 levels, 65,536 nodes, 2,048 queued items,
4,096 retry counters, 128 MiB expanded history). Store limits from feature 002 (1,000 messages and 16 MiB per batch,
per-field limits, 16 active and 64 waiting operations, 10 s per operation). Application limits proposed in
[data-model.md](data-model.md): history queue 8, history flushes of 250 messages or 4 MiB, name cache 4,096, group
lookup set 64 with 10 s lookups and a 10-minute failure cache of 1,024 groups, LID hint set 1,024 with 5 attempts, 2 s
LID lookups, 2 s worker wait at stop. Feature 004's stop bound
of 10 s and its retirement order.

**Scale/Scope**: One account. History bounded per chunk by the fork; the store grows without a retention limit
(documented, F12).

## Constitution Check

*Checked against constitution 3.0.0 before Phase 0 and again after Phase 1.*

| Principle | Pre-design | Post-design | Evidence |
| --- | --- | --- | --- |
| I. Specification | PASS | PASS | Spec with four clarifications; §3.4, §3.5, P2, P3, M7, S3–S5, D18 mapped in research R3–R12. Edits are not stored (R5): §3.4 ignored them. |
| II. One binary | PASS | PASS | No new dependency, no cgo in builds; the race run is a test tool run in WSL2 with the owner's permission. |
| III. One owner | PASS | PASS | Same store file, no migration, no working-directory paths. |
| IV. Clean channel | PASS | PASS | Text and captions only at debug; keys and URLs never; stdout empty in `stdio` (contract). |
| V. Secure by default | PASS; condition met below | PASS; condition met below | Fork bounds approved and pinned (R1); application bounds for every queue, set, cache, worker, wait and batch (data model); limits enforced on what is consumed (store charges, fork expansion counts); overload and rejected history reported (R4, R10). |
| VI. Tested | PASS | PASS | Narrow `ingestClient` interface with a fake; store helpers tested on SQLite; quickstart scenarios. |
| VII. MCP tools | N/A | N/A | No tool in this feature. |
| VIII. Real accounts | PASS | PASS | No real-account step is authorized; quickstart step 7 needs permission per step. |
| IX. Simplicity | PASS | PASS | No user-facing option; `Live` is the hand-off FR-012 requires and is `nil` here. |
| X. Security per feature | PASS | PASS | F01, F03, F05, F09–F13 taken up (spec Security, research R9–R13); E003-RX-01 retired (R2); abuse cases in quickstart step 2. |

**Condition (Principle V and the Fork Acceptance Contract)**: the race-detector run, met on 2026-11-13 in WSL2. The
fork passed without a race. The project's session and command packages found one race in a feature 003 test's own
variable, which is fixed; production code and the fork were not affected (`verification.md`, "Race Detector (WSL2)").

**Accepted by the owner, not bounded**: four library maps keyed by account or group (R2; backlog
`005-whatsmeow-per-account-maps.md`).

**High findings**: F09 is resolved for this feature by the pinned bounds and the application bounds; no exception is
needed. No other High finding is enabled by this feature without a decision.

## Fork Acceptance Contract

Approved by the owner on 2026-11-13 for revision `9209108`; items 8 and 9 approved the same day after the first and
second design reviews, which moved the pin to `d83e19e` and then `bedcf01`. The fork bounds what another WhatsApp account can drive in
the client library. The list is closed: a new item needs an owner decision, not a review finding.

1. **Frame size.** One frame is limited after expansion, on the bytes actually produced. (`be96244`)
2. **Handler queue.** A full queue starts no further work. The connection is closed and a record that survives
   reconnects says so. Responses to our own requests still arrive while the queue is full, and a logout or
   replacement that arrives at that moment is still reported. (`54a9501`)
3. **Retry counters.** The per-message retry counters cannot grow without limit. (`6cabab6`)
4. **No silent loss.** When the library drops received data at a limit (frame size, node count, nesting depth, full
   queue), it closes the connection and leaves the same record. The retry counter cap drops no received data and does
   neither. (`54a9501`, `8e61d2a`)
5. **History expansion.** A history sync is limited after decompression, on the bytes actually produced. The library
   returns an error; it does not close the connection. (`a654373`)
6. **Work per message.** Handling a message from another account starts no goroutine that can wait on storage or the
   socket. Push-name and business-name updates run inside the handler (`9209108`). With `SynchronousAck`, the
   acknowledgement, receipt and retry-receipt work listed in "What `SynchronousAck` changes in the pin" runs inside it
   too.
7. **No regression.** Apart from items 6, 8 and 9 and `c05eb84` (the stream-error fix proposed upstream as #1337), the
   branch changes no acknowledgement, retry, history, reconnect or shutdown behaviour of upstream.
8. **No phone re-request unless enabled.** The first retry receipt for an undecryptable message asks the user's phone
   for it only when `AutomaticMessageRerequestFromPhone` is set, with synchronous acks as with asynchronous ones.
   Another account can make any stanza undecryptable, so without this the account would send a peer message on a
   stranger's behalf, and the handler would wait for it. (`d83e19e`)
9. **No phone request for a placeholder unless enabled.** An `unavailable` placeholder, which the server sends to a
   linked device when another account sends for example a view-once message, makes the library ask the user's phone
   for the message only when `AutomaticMessageRerequestFromPhone` is set, in both ack modes. The placeholder is still
   acknowledged; its content is not fetched, and ingest counts it (R5). (`bedcf01`)

Also on the branch and not part of the gate: waiter cleanup after a timeout (`4a01445`), an error instead of a panic
on short app-state values (`8f99711`), and a panicking handler counted as failed (`01da9e4`).

### What `SynchronousAck` changes in the pin

Inventory of every site in `bedcf01` that reads the setting or defers an ack on it (`grep
'SynchronousAck\|backgroundIfAsyncAck\|maybeDeferredAck'`), made after the first design review and completed after
the second. Each moves work from a new goroutine onto the handler goroutine, so the handler waits for it.

| Site | Trigger and author | With `SynchronousAck` | Bearing on this design |
| --- | --- | --- | --- |
| `message.go:504` | every decrypted message | ack, nack or delivery receipt sent after the handlers, skipped when a handler failed | the intended change (R3) |
| `message.go:441-449`, `retry.go:514` | an undecryptable stanza; any account | nack (`msmsg`) or retry receipt and ack inline; the phone re-request only when `AutomaticMessageRerequestFromPhone` is set (`d83e19e`; false here, so none) | contract item 8; retry receipts are capped per message ID |
| `message.go:334` | an `unavailable` placeholder; written by the server, caused by any account (for example a view-once message) | ack inline; the phone request only when `AutomaticMessageRerequestFromPhone` is set (`bedcf01`; false here, so none) | contract item 9; the placeholder is counted `unavailable` |
| `receipt.go:37` | a retry receipt from another account | `tryHandleRetryReceipt` inline; it resends only messages this client sent, and this feature sends none | none now; relevant to the send features |
| `receipt.go:121` (`maybeDeferredAck`), its callers in `receipt.go`, `notification.go`, `call.go:18` and `message.go:65`, `:85` | receipts, notifications, call events, newsletter messages | ack after dispatch, skipped when dispatch failed | the handler returns `true` for these; after the retire bit a newsletter message is not acknowledged, which is harmless |
| `notification.go:484` | `server_sync` app-state notification; written by the server | `FetchAppState` runs inline instead of in a goroutine | feature 004's queued-fetch stop test must be adapted (tasks); the handler waits for each fetch |

Application side, fixed by this design: `SynchronousAck = true`; `ManualHistorySyncDownload = true`; history downloads
through the fenced media client (16 MiB, 10 s per response); the overload record is read and reported (R10); history
over the limit is reported as incomplete (R4).

## Questions for the design review (answered by the owner, 2026-11-13, T004)

1. **Proposed application limits** in [data-model.md](data-model.md). They follow from the design, not from
   measurement. **Accepted as proposed**; a measurement in T047 or T048 that misses its target returns to the owner
   before the code review.
2. **Unsafe stops during history.** A stop while the history worker is inside a library call can leave the marker
   unsafe (R11). The rate is measured, not estimated. **Accepted**; T047's rate, including the `pair`-shaped case,
   returns to the owner before the code review.
3. **History download limits.** History blobs are downloaded through the media client: 16 MiB and 10 s per response.
   A normal chunk on a slow link may be reported as incomplete. Real chunk sizes have not been measured. **Kept**; a
   chunk that misses them is reported (`download`), and T056 records real chunk sizes.

Settled on 2026-11-13 and recorded in the spec's clarifications: the history gap after `pair` is accepted (a bounded
wait is in the backlog), merges are not undone when a LID is remapped, and a startup pass stopped by its budget is
continued by the running session. Settled after the second design review: the `unavailable` placeholder request is
gated in the fork (contract item 9) for every placeholder type, and the real-account check keeps the roadmap's history
step and records the gap (T056). Settled after the third design review: giving up recovery of ordinary placeholders is
accepted and recorded in the spec's clarifications; an opt-in request is in the backlog.

## Project Structure

### Documentation (this feature)

```text
specs/005-message-ingest/
├── spec.md
├── plan.md              # this file
├── research.md          # Phase 0: R1–R15, and the earlier investigation as an appendix
├── data-model.md        # Phase 1
├── contracts/
│   └── ingest.md        # Phase 1: session, ingest, owner, store and command contract
├── quickstart.md        # Phase 1: validation guide
├── verification.md      # evidence and owner reviews
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source Code (repository root)

```text
cmd/whatsapp-mcp/
├── commands.go          # startup LID reconciliation; wait on owner.Terminal(); exit 1 on ingest failure
internal/wa/
├── session.go           # SynchronousAck, success-status handler, overload record, ingest wiring
├── owner.go             # Terminal(), Health fields, ingest stop and wait in retirement
├── ingest.go            # HandleMessage, terminal failure, counters
├── ingest_extract.go    # eligibility and field extraction for live and history
├── ingest_history.go    # history queue and worker, batching
├── ingest_names.go      # name cache, group lookup set and worker
├── ingest_lid.go        # normalisation, hint set and reconcile worker, startup pass
├── quiet_test.go        # allow-list check against the replace target
└── *_test.go            # fake client, simulator and abuse tests
internal/store/
├── validate.go          # ValidateMessage, MessageFieldBytes (exported)
├── device_lids.go       # mapping-write notification to the session
├── lids.go              # PhoneForLID, MappedLIDsWithData
└── contacts.go          # ContactNames
OPERATIONS.md            # what is stored, retention, privacy
```

**Structure Decision**: The existing single module. Ingest lives in `internal/wa` beside the session, because it needs
the generation's lifecycle and the library types, and talks to storage only through `internal/store`'s exported API.

## Complexity Tracking

No deviation from the constitution.
