# Data Model: Reusable Session After a Proven Quiet Stop

Nothing new is stored on disk. The durable run marker keeps the format of feature 003 (schema 3); a clean marker after
a passed check is the same row as a clean marker after a run with no connection (FR-012). Everything below lives in
memory for one process.

## Generation (extended)

One client instance and its connection: an owner `genState` plus its `Session`.

| Field | Meaning | Rule |
| --- | --- | --- |
| `epoch` | the owner epoch of this generation | unchanged from feature 003 |
| `accountWork` | a production Connect was attempted on this client | set before `ConnectContext`; when false, no check is needed |
| `fence` | the generation's network block | one per generation; never shared with another generation |
| `barrier` | the store's device barrier | unchanged; gains a refusal count |
| `droppedEvents` | owner-relevant events dropped after retirement, except `Disconnected` | counted from the moment the retire bit is set |
| `lateCalls` | calls from the program's own code to the session after the retire bit is set, except the retirement sequence's own Disconnect | counted from the same moment |
| `work` | the program's own work: an unreturned Connect's abort, the pairing window and QR channel, the repair pass | a child of the owner context; cancelled by the stop signal and at the start of retirement |
| `lib` | the library's `BackgroundEventCtx` and the parent of the attempt context its handlers run with | created from `context.WithoutCancel(o.ctx)`; cancelled only after the fence and the barrier (invariants I8); never by the stop signal, `work` or a watchdog once Connect has returned |
| `quiet` | the generation's `QuietStopResult` | written once, by retirement |

**States.** open → connecting / connected / pairing → retiring → retired (blocks closed, drained) → checked (result
known) → released. A generation never returns to an earlier state. A new Connect never runs on a retired generation.

## Network fence

| Field | Meaning | Rule |
| --- | --- | --- |
| `closed` | the fence is closed | set once, after the library's own Disconnect |
| `conns` | the wrapped connections dialled by this generation | all closed when the fence closes |
| `refusedDials` | dials attempted after closing | counted, never performed |
| `refusedWrites` | writes attempted on a wrapped connection after closing | counted, never sent |

Reads that fail after closing are not counted (research R3).

## Storage refusal count

`DeviceBarrier.MarkRetiring()` starts the count when retirement starts. `DeviceBarrier.Refused() uint64` is the number
of guarded calls for this device session that failed after that point for a reason that comes from the guard or the
context: `ErrRetired`, a context error, a stale lease or another guard error. A result the database itself returns
(for example "not found") is not counted. It is monotonic, and is read only after `Retire` has drained the admitted
work.

## QuietStopResult

| Field | Type | Meaning |
| --- | --- | --- |
| `Passed` | bool | true only when every condition of FR-002 holds |
| `Reason` | enum | `passed`, `work_running`, `network_refused`, `storage_refused`, `call_refused`, `event_dropped`, `stop_bound_exceeded`, `observation_incomplete`, `observation_error`, `not_checked` |
| `LibraryGoroutines` | int | client-library goroutines left at the last observation, after removing allow-listed work |
| `NetworkRefused` | uint64 | refused dials plus refused writes |
| `StorageRefused` | uint64 | the barrier's count (refusals, context errors, stale leases, guard errors) |
| `CallsRefused` | uint64 | late calls to the session (see Generation) |
| `EventsDropped` | uint64 | dropped owner-relevant events (see Generation) |
| `Duration` | duration | from the start of retirement to the decision |

When several conditions fail, `Reason` names the first in this order: `observation_incomplete`,
`observation_error`, `stop_bound_exceeded`, `storage_refused`, `network_refused`, `call_refused`, `event_dropped`,
`work_running`. The counts report all of
them. `not_checked` is used for a generation that never attempted a Connect. The result never holds dump text,
function names, argument values, keys or account identifiers.

## Allow-list entry

| Field | Meaning | Rule |
| --- | --- | --- |
| `Function` | one exact function name as `runtime.Stack` prints it | never a package or module prefix |
| `ReviewedFor` | the `go.mau.fi/whatsmeow` version the entry was reviewed against | must equal the `go.mod` require line, or a test fails |
| `Evidence` | why the work is harmless after both blocks | a reference to `verification.md` |

The list starts empty (research R5).

## Run outcome (owner)

The existing `finishStop` decision, with one more input:

```
clear = guardArmed
        && !unresolved            // no unresolved pairing attempt (FR-003, clarification 2026-11-07)
        && !unsafeStop            // set by a failed check of a generation that attempted Connect, among the existing causes
        && withinStopBound        // the decision is made before stopBound has passed
        && !health.StorageFault
then ClearRunGuard, which still enforces low-disk admission
```

`upstream_drain_unproven` no longer sets `unsafeStop` on its own. A failed check does, for that generation.

## Constants (owner-approved before use)

| Name | Meaning | Value |
| --- | --- | --- |
| `quietStopDeadline` | the observation deadline for an ordinary stop | measured by the spike (research R6), then approved |
| `postPairQuietDeadline` | the observation deadline for the pairing generation before the fresh connection | measured by the spike, then approved |
| `quietObservationGap` | the minimum gap between the two clean observations | 100 ms (research R1) |
| `stopBound` | the whole orderly stop, from `BeginStop` to the clear decision | 10 s, the lifecycle's shutdown timeout, passed in by the command; not a setting |
| `stackBufferMax` | the largest goroutine-dump buffer | 8 MiB (research R2) |

The existing constants are unchanged: the 30-second connect watchdog and the 10-second stop bound for writes. The
observation deadline is capped at the time left in `stopBound`; a decision after it fails with `stop_bound_exceeded`.
The owner's approval of the two deadlines records the total budget (Disconnect up to 5 s, barrier drain up to 10 s,
observation) against `stopBound`.
