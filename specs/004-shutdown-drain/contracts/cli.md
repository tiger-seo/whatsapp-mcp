# Contract: Command Behaviour After a Quiet-Stop Check

This contract changes feature 003's `specs/003-whatsapp-pairing-status/contracts/cli.md` only where it is listed
below. Everything else stays as it is: configuration, lock and private-storage rules, exit categories (0 success,
1 operational failure, 2 usage or configuration), offline recovery, `status` and the stop-signal rules. All logs go to
stderr, and `stdio` stdout stays protocol-only.

## Shared stop outcome

At an orderly stop, each generation that attempted a production Connect is checked (contract `quiet-stop.md`). The
run marker is cleared only when every such check passed and all of feature 003's other clear conditions hold. Those
conditions are: no unresolved pairing attempt, no storage fault, a durable cleanup of any latched logout or client
deletion, and low-disk admission for the clear.

| Event | Level | Fields | When |
| --- | --- | --- | --- |
| `quiet_stop_passed` | info | `epoch`, `duration_ms` | a check passed |
| `quiet_stop_failed` | error | `epoch`, `reason`, `library_goroutines`, `network_refused`, `storage_refused`, `calls_refused`, `events_dropped`, `duration_ms` | a check failed |
| `run_guard_recovery_required` | error | (unchanged) | the marker stays unsafe for any reason |
| `upstream_drain_unproven` | — | — | **removed**; the check's result replaces it |

`reason` is one of `work_running`, `network_refused`, `storage_refused`, `call_refused`, `event_dropped`,
`stop_bound_exceeded`, `observation_incomplete`, `observation_error`. No field carries dump text, function names, argument values, keys, QR material or account
identifiers, at any log level.

On stderr, the human line names the category only; the counts are in the `quiet_stop_failed` event, which is also on
stderr. Together they are the stop diagnostic of spec User Story 3.

## pair

| Situation | Exit | stderr (human line) | Marker after |
| --- | --- | --- | --- |
| Fresh link: the pairing generation passed, the fresh connection confirmed, and the stop passed | 0 | `whatsapp-mcp: pair: linked. The saved session can be reused.` | clean |
| Fresh link confirmed, but the stop failed its check | 0 | the existing `linked. Offline recovery is required before the next run: ...` line, with the reason category added | unsafe |
| Pairing generation failed its check before the fresh connection | 1 | the existing "linking could not be completed safely ..." line, with the reason category added | unsafe |
| Fresh connection failed, stalled past 30 s or was cancelled | 1 | the existing "linking could not be completed safely ..." line | unsafe |
| QR final timeout, cancellation, Enter or EOF | 1 | unchanged | unsafe (unresolved pairing) |
| Linked, check passed inside the bound, then `shutdown timed out` | 1 | `shutdown timed out` (from the lifecycle) | clean |
| Clean saved identity: connect once and stop | 0 when connected and the stop passed; otherwise as feature 003 | `connected. The saved session can be reused.`, or the recovery notice | clean or unsafe |
| Storage fault or any other stop error | 1 | unchanged | unsafe |

The 515 rules are unchanged:
- A restart request before `PairSuccess` waits for it within the 30-second watchdog.
- A `PairError`, logout, stop or the watchdog refuses it.
- Duplicate requests share one attempt.
- There is never a second post-pair connection.

## stdio and serve

| Situation | Exit | Marker after |
| --- | --- | --- |
| Clean marker, saved identity, connected or failed once, stop passed | 0 | clean; the next start connects with the saved session |
| Same, stop failed its check | 1 with `run_guard_recovery_required` and the reason | unsafe |
| Same, the check passed inside the stop bound but the later steps (marker write, store close, lock release) pushed the lifecycle past its 10 s timer | 1 with `shutdown timed out` | clean (safe: the check passed and the stop completed in order) |
| Same, the decision came after the stop bound | 1 with `shutdown timed out` and `quiet_stop_failed` (`stop_bound_exceeded`) | unsafe |
| No saved identity (never connects) | as feature 003 | as feature 003 |
| Unsafe or legacy marker at start | 1, offline recovery required, no account work | unchanged |

Still denied, with the existing `reconnect_denied` diagnostic: startup retries, 405 recovery, ordinary reconnect and
any 515. A shell never pairs.

## status

Unchanged. A clean marker after a passed check is indistinguishable from any other clean marker.
