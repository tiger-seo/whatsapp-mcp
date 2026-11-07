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
| `droppedEvents` | events refused after retirement, except the self-caused disconnect | counted from the moment the retire bit is set |
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

`DeviceBarrier.Refused() uint64`: the number of guarded calls for this device session that returned `ErrRetired`.
It is monotonic, and is read only after `Retire` has drained the admitted work.

## QuietStopResult

| Field | Type | Meaning |
| --- | --- | --- |
| `Passed` | bool | true only when every condition of FR-002 holds |
| `Reason` | enum | `passed`, `work_running`, `network_refused`, `storage_refused`, `event_dropped`, `observation_incomplete`, `observation_error`, `not_checked` |
| `LibraryGoroutines` | int | client-library goroutines left at the last observation, after removing allow-listed work |
| `NetworkRefused` | uint64 | refused dials plus refused writes |
| `StorageRefused` | uint64 | the barrier's refusal count |
| `EventsDropped` | uint64 | dropped events (see Generation) |
| `Duration` | duration | from the start of retirement to the decision |

When several conditions fail, `Reason` names the first in this order: `observation_incomplete`,
`observation_error`, `storage_refused`, `network_refused`, `event_dropped`, `work_running`. The counts report all of
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
| `stackBufferMax` | the largest goroutine-dump buffer | 8 MiB (research R2) |

The existing constants are unchanged: the 30-second connect watchdog and the 10-second stop bound for writes.
