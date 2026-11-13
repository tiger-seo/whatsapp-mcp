# Implementation Plan: Message Ingest

**Branch**: `005-message-ingest` | **Date**: 2026-11-13 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/005-message-ingest/spec.md`

**Status**: Phase 0 and Phase 1 complete; tasks generated and the cross-artifact analysis of 2026-11-13 applied. The
constitution gate passes on the pinned fork; its condition, the race-detector run, was met on 2026-11-13
(`verification.md`, "Race Detector (WSL2)"). Next: the owner's design review.

## Summary

Store supported live messages and WhatsApp-supplied history with their text, captions and media metadata, normalise
LIDs to phone identities, merge LID data at startup and when a mapping appears, and name chats without a group lookup
per message. Live messages are stored inside the library's event handler, with acknowledgements sent only after a
successful write. History is downloaded and stored by one bounded worker per generation. A message-store failure stops
the connected command with a report; a receive overload is reported as a possible gap. Ingest joins feature 004's
retirement order without moving any step. The library is the owner-approved fork revision
`tiger-seo/whatsmeow@9209108`, which enforces the receive bounds E003-RX-01 had deferred.

## Technical Context

**Language/Version**: Go 1.27 (toolchain `go1.27.4`); `CGO_ENABLED=0` for every build and test, except the one
race-detector run in WSL2 that the owner allowed.

**Primary Dependencies**: `go.mau.fi/whatsmeow`, required at upstream `f1b798561919` and replaced by
`github.com/tiger-seo/whatsmeow v0.0.0-20261112064615-92091085667b` (branch `receive-bounds`, commit `9209108`,
approved by the owner on 2026-11-13, research R1); `modernc.org/sqlite` through `internal/store`. No new dependency.

**Storage**: The existing `whatsapp.db`; no migration. Four read-only store helpers and two exported validation
helpers (data model).

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

Approved by the owner on 2026-11-13 for revision `9209108`. The fork bounds what another WhatsApp account can drive in
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
   socket. Push-name and business-name updates run inside the handler (`9209108`); acknowledgements and retry
   receipts run inside it through `SynchronousAck` (contract).
7. **No regression.** Apart from item 6 and `c05eb84` (the stream-error fix proposed upstream as #1337), the branch
   changes no acknowledgement, retry, history, reconnect or shutdown behaviour of upstream.

Also on the branch and not part of the gate: waiter cleanup after a timeout (`4a01445`), an error instead of a panic
on short app-state values (`8f99711`), and a panicking handler counted as failed (`01da9e4`).

Application side, fixed by this design: `SynchronousAck = true`; `ManualHistorySyncDownload = true`; history downloads
through the fenced media client (16 MiB, 10 s per response); the overload record is read and reported (R10); history
over the limit is reported as incomplete (R4).

## Open questions for the design review

1. **Proposed application limits** in [data-model.md](data-model.md). They follow from the design, not from
   measurement.
2. **Unsafe stops during history.** A stop while the history worker is inside a library call can leave the marker
   unsafe (R11). The rate is measured, not estimated.

Settled on 2026-11-13 and recorded in the spec's clarifications: the history gap after `pair` is accepted (a bounded
wait is in the backlog), merges are not undone when a LID is remapped, and a startup pass stopped by its budget is
continued by the running session.

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
