# Contract: Generation Retirement and the Quiet-Stop Check

Internal contract between the connection owner (`internal/wa/owner.go`), the session adapter
(`internal/wa/session.go`), the network fence (`internal/wa/fence.go`), the observation (`internal/wa/quiet.go`) and
the store's device barrier (`internal/store/device.go`, with `MarkRetiring` and `Refused` in `internal/store/repair.go`). It replaces, for the post-pair reconnect and for clearing the
marker, feature 003's rule that no Connect may follow the retirement of an unproven generation
(`specs/003-whatsapp-pairing-status/contracts/connection-owner.md`, proof gate). Every other rule of that contract
stands.

## Two contexts, not one

Today the generation lifetime is cancelled first, and the attempt context derived from it is also the context the
client library gives its detached node handlers. Cancelling it while the socket is live makes every in-flight handler's
next store call fail with `context.Canceled`. That error is not a refusal, so a lost key or session write would look
like a quiet stop. The design therefore separates:

- **the program's own work**: a Connect that has not returned, the pairing window, the repair pass. These are cancelled
  at the start of retirement, as they must be for a prompt stop; they are resumable by design (repair markers) or
  have no handlers yet;
- **the library context** of a Connect that has returned (the context its node handlers run with). It is cancelled
  only after the network fence and the storage barrier are in place (step 9).

Any guarded call that still fails because of a context error, a stale lease or a refusal after retirement began is
counted (step 2), so the separation is a speed measure and the count is the safety measure.

## Retirement order (one generation)

1. Mark the generation retiring. New owner admissions for this epoch are refused, as in feature 003.
2. Set the session's retire bit and call `DeviceBarrier.MarkRetiring()`. From now on:
   - every guarded device call of this generation that fails with `ErrRetired`, a context error, a stale lease or any
     other error raised by the guard (not a result the database itself returns, such as "not found") is counted;
   - every call from the program's own code to the session (Connect, QR channel, repairs, Disconnect from any caller
     except this sequence) is refused and counted;
   - every event that the session would forward to the owner is dropped and counted, except `Disconnected`;
   - the event handler stays registered.
3. End any pairing window of this generation, so its QR channel context ends.
4. Cancel the program's own work: a Connect attempt that has not returned and the repair pass. Do not cancel the
   library context of a Connect that has returned.
5. Wait for the repair pass, as in feature 003.
6. `client.Disconnect()`: the library's own orderly close (this sequence's call, not counted). It writes the close
   frame and sets the library's socket to nil.
7. **Close the network fence.** Close all wrapped connections; from now on refuse and count every dial and every
   write.
8. **Retire the store barrier.** Refuse and count every later guarded call, and wait for admitted calls to finish.
   The existing context and the stop bound apply. A barrier that does not drain fails as today
   (`owner_drain_failed`, unsafe).
9. Cancel the library context (the generation lifetime). Storage is already blocked, so a handler that is released
   now can only be refused and counted.
10. **Observe** with `min(approved deadline, remaining stop bound)` (see "Stop bound"). The deadline counts from the
    start of this step, after the barrier has drained and the library context is cancelled; the result's `Duration`
    still covers the whole retirement from step 1. Poll the bounded goroutine dump until two observations at least
    `quietObservationGap` apart both show zero client-library goroutines after removing allow-listed work, or until
    that deadline.
11. Decide the `QuietStopResult` (data model) and remove the event handler.
12. `FinishRevocation` and the storage-fault check, as in feature 003.

Steps 7 and 8 must not swap, and step 9 must not move before step 8. A test asserts that storage is never blocked
while the fence is open and that the library context is not cancelled while a handler can still write, and fails when
either order is reversed (spec SC-004).

## What each count can and cannot see

- **Storage count** (step 2): sees refusals, context errors, stale leases and guard errors of this generation after
  retirement began. It is the evidence for a lost write.
- **Network fence** (step 7): sees dials and writes on the fenced transports after the fence closed. After step 6 the
  library's own socket is nil, so a late library send returns `ErrNotConnected` inside the library and never reaches
  the fence. The fence therefore proves isolation (a retired generation cannot reach any network, including the
  media and pre-login clients), not quiet. Evidence that no library sender is still running comes from the
  observation.
- **Call count** (step 2): sees the program's own late calls to the session.
- **Event count** (step 2): sees owner-relevant events only. A remote `Disconnected` after retirement, such as the
  server closing after the 515, is expected and is not counted; the library's `Disconnect` emits none. Receipts,
  presence and similar events carry no owner state and are not counted. Feature 005 must revisit this rule when
  messages are ingested.
- **Observation** (step 10): sees library goroutines still running.

## Stop bound

The owner receives the shutdown bound (10 s, the same constant the lifecycle uses; not a setting) and starts a timer at
`BeginStop`. The decision at step 11 is a failure with reason `stop_bound_exceeded` if the bound has already passed,
and the observation deadline is capped at the remaining time. The marker is never cleared after the bound: the
lifecycle reports `shutdown timed out` and exit 1 in that case, so a clean marker would contradict it. Steps 6 and 8
already take up to 5 s and 10 s in the worst case; the owner's approval of the deadlines (task T019) states the
total budget.

The post-pair handover is not part of the shutdown bound. A stop signal that arrives during it enters `finishStop`
and gets the bound from that moment.

## Inputs and outputs

```go
// Session
func (s *Session) QuietStop(ctx context.Context, deadline time.Duration) QuietStopResult
// called after Retire has returned nil; a Session that never attempted Connect returns Reason "not_checked", Passed true

// store
func (b *DeviceBarrier) MarkRetiring()
func (b *DeviceBarrier) Refused() uint64 // guarded calls that failed after MarkRetiring (refusal, context error, stale lease, guard error)
```

The result is a value. It never holds a reference to the dump, the client or the device.

## Owner use

- `retireGeneration(gen, deadline)` runs steps 1–12 and records a failed result in `unsafeStop`, logging
  `quiet_stop_failed`. A passed result logs `quiet_stop_passed`. A generation that never attempted Connect is not
  checked.
- `finishStop` passes `quietStopDeadline` and the bound described above.
- The post-pair path, in this order:
  1. a 515 after `PairSuccess`, or `PairSuccess` releasing a pending 515;
  2. take the attempt slot;
  3. `retireGeneration(pairGen, postPairQuietDeadline)`, which ends the pairing window (step 3);
  4. if that check failed, take the incomplete-link path (final, unsafe);
  5. otherwise open a new generation with `LoadDevice`, then run one `attemptPostPair` under the 30-second watchdog;
  6. release the slot after that attempt and its teardown drain, as in feature 003.
- No other path opens a generation after one that attempted Connect. Startup retries, 405, ordinary reconnect and
  same-process re-pair stay denied.

## Invariants

- I1: No retired generation's dial or write reaches the network after its fence closed.
- I2: No guarded storage call of a retired generation runs after its barrier drained.
- I3: Within one process, a fresh generation exists only after the previous generation's check passed. While feature
  003's same-client post-pair reconnect still exists (between the US1 and US2 tasks), a generation that used it keeps
  the marker unsafe whatever its check shows.
- I4: A clean marker is written only after every checked generation of the run passed, within the stop bound, and
  never while a check is pending.
- I5: The dump stays inside `quiet.go`. Nothing derived from it except counts and the reason leaves the function.
- I6: Every failure or doubt yields `Passed == false`: an error, a timeout, a cut-off dump, any refusal, interruption,
  late call or drop.
- I7: The allow-list holds exact function names only, and its `ReviewedFor` equals the `go.mod` whatsmeow version.
- I8: The library context of a connected generation is not cancelled before the fence is closed and the barrier has
  drained.

## Test seams

- The observation function and the clock are injectable for unit tests: a fake dump source makes the result
  deterministic.
- `internal/wasim` supplies the real client path and counts frames per connection. A test-only hook can hold a
  pinned handler, make a handler write after retirement began, inject a late send through the real client after
  `Disconnect`, or a late store call after the barrier.
- The production observation is always `runtime.Stack`. Tests must not replace it in the simulator suites that
  measure the pass rate. The simulator runs in the same process, so its own websocket server goroutines match the
  package rule; the measurement records how many and the owner decides at T019 how to treat them (research R6).
