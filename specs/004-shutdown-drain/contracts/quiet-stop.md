# Contract: Generation Retirement and the Quiet-Stop Check

Internal contract between the connection owner (`internal/wa/owner.go`), the session adapter
(`internal/wa/session.go`), the network fence (`internal/wa/fence.go`), the observation (`internal/wa/quiet.go`) and
the store's device barrier (`internal/store/device.go`, with `MarkRetiring` and `Refused` in `internal/store/repair.go`). It replaces, for the post-pair reconnect and for clearing the
marker, feature 003's rule that no Connect may follow the retirement of an unproven generation
(`specs/003-whatsapp-pairing-status/contracts/connection-owner.md`, proof gate). Every other rule of that contract
stands.

## Three contexts, not one

Today every generation lifetime is a child of the owner context (`o.ctx`, which the lifecycle's stop signal cancels
through `BeginStop`), and the attempt context derived from the lifetime is also the context the client library gives
its detached node handlers (`BackgroundEventCtx` and the handler context). A stop signal, a connect watchdog or the
start of retirement therefore cancels every in-flight handler while the socket is live, and its next store call fails
with `context.Canceled`. That error is not a refusal, so a lost key or session write would look like a quiet stop.
The design separates three contexts per generation:

- **the owner context** (`o.ctx`): cancelled by the stop signal. It reaches only the program's own work;
- **the work context** (`gen.work`, a child of `o.ctx`): the program's own work, namely the abort of a Connect that
  has not returned, the pairing window and QR channel, and the repair pass. It is cancelled by the stop signal and at
  the start of retirement, as it must be for a prompt stop. The QR channel and the repair pass use it, never the
  library context;
- **the library context** (`gen.lib`, created as `context.WithCancel(context.WithoutCancel(o.ctx))`, because `WithoutCancel` alone has no `Done` channel and could not be cancelled at step 9): it is the library's
  `BackgroundEventCtx` and the parent of the attempt context that the library gives its handlers. Nothing cancels it
  except step 9 (and the forced path when the barrier cannot drain). The stop signal, the work context and the
  watchdogs do not reach it.

The attempt context passed to `ConnectContext` is a child of `gen.lib`. Its cancel function is the **attempt abort**:
the owner records it in the generation under `o.mu` before `ConnectContext` is called, and clears it under `o.mu` when
`ConnectContext` returns. The stop signal (`BeginStop`), a connect watchdog and retirement abort a Connect that has
not returned only by calling the recorded abort function, **synchronously and under `o.mu`**, through one helper that
calls `MarkRetiring()` first. There is no `AfterFunc` (it runs its function in a new goroutine, so the abort would
come after a scheduling delay and a Connect could start dialling after the stop began). The start of an attempt checks
that no stop has begun and records the abort in the same critical section of `o.mu`, so a stop either sees the abort
and calls it or is seen by the attempt and prevents the Connect.

The pairing generation's Connect (the `pair` command's pairing window) uses exactly the same mechanism: its context is a
child of `gen.lib`, the stop check and the recorded abort share one critical section of `o.mu`, and its watchdog only
calls `Disconnect()` once the Connect has returned. Only the window's QR channel and its diagnostics are `gen.work`.
The stop signal during an open pairing window, and retirement step 4 for the pairing generation at the 515, therefore
never cancel the context the pairing socket's handlers run with.

Once `ConnectContext` has returned, nothing cancels the attempt context: the watchdog calls `Disconnect()` and the
existing `if !connected { cancel() }` defer in `attempt` does not cancel it (the attempt context is released with
`gen.lib` at step 9). A startup attempt that ends in a remote disconnect, a stop or a timeout after `ConnectContext`
returned therefore leaves the handler context alone. Every other former use of the generation lifetime moves to the
work context: the version refresh, the QR drain, the storage-fault watcher and the post-pair slot wait.

**Known windows.** `ConnectContext` returning cannot be made atomic with `o.mu`: the library starts its handler loop
before it returns, and the owner clears the recorded abort only afterwards. Two interleavings follow, both fail
closed and both are counted:

- (a) the abort is still recorded when `ConnectContext` has returned, and the stop or a watchdog calls it. The handler
  context of a live socket is cancelled before the fence. `MarkRetiring()` came first, so a guarded call that fails
  is counted and the check fails with `storage_refused`;
- (b) the stop calls the abort and retirement sets the retire bit before the attempt goroutine calls
  `Session.Connect`. Nothing was dialled. `Session.Connect` does not count this as a late call when the attempt was
  already aborted through the recorded abort; the generation never attempted a Connect and is not checked.

In both cases the marker is never cleared by a false pass. The measurement (T018) records how often (a) occurs.

Counting backs the separation. `DeviceBarrier.MarkRetiring()` is called at the first of: `BeginStop`, the start of
retirement, or any cancellation of the attempt context (including a watchdog or stop that aborts a Connect that has not
returned). From then on a guarded call that fails with a context error, a stale lease or a refusal is counted (step 2).
The separation is the speed and safety measure, the count is the evidence.

## Retirement order (one generation)

0. `BeginStop` (the stop signal) calls `MarkRetiring()` on the current generation **before** it cancels `o.ctx`, then
   cancels `o.ctx`. Because of the separation above, this reaches only `gen.work`.
1. Mark the generation retiring. New owner admissions for this epoch are refused, as in feature 003.
2. Set the session's retire bit and call `DeviceBarrier.MarkRetiring()` if it has not been called. From now on:
   - every guarded device call of this generation that fails with `ErrRetired`, a context error, a stale lease or any
     other error raised by the guard (not a result the database itself returns, such as "not found") is counted;
   - every call from the program's own code to the session (Connect, QR channel, repairs, Disconnect from any caller
     except this sequence) is refused and counted;
   - every event that the session would forward to the owner is dropped and counted, except a disconnect: the raw
     `Disconnected` event and the `SessionDisconnected` that the session maps from it or from `StreamReplaced`;
   - the event handler stays registered.
3. End any pairing window of this generation, so its QR channel context ends.
4. If a Connect attempt has not returned, call the recorded attempt abort under `o.mu` (through the `MarkRetiring`
   helper). Then cancel `gen.work`, which ends the repair pass and the pairing window's QR channel. `gen.work` does not
   reach any Connect context. Do not cancel `gen.lib` or the attempt context of a Connect that has returned.
5. Wait for the repair pass, as in feature 003.
6. `client.Disconnect()`: the library's own orderly close (this sequence's call, not counted). It writes the close
   frame and sets the library's socket to nil.
7. **Close the network fence.** Close all wrapped connections; from now on refuse and count every dial and every
   write.
8. **Retire the store barrier.** Refuse and count every later guarded call, and wait for admitted calls to finish.
   The existing context and the stop bound apply. A barrier that does not drain fails as today
   (`owner_drain_failed`, unsafe).
9. Cancel `gen.lib`, which cancels the library's handler context. Storage is already blocked, so a handler that is released
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
- **Network fence** (step 7): fails the check on a dial through the fenced transports after the fence closed. A write on a fenced connection after the close is refused and counted but does not fail the check: every tracked connection is already closed, so the write cannot reach the network, and the TLS and HTTP/2 layers write on it themselves while they tear down (decision of 2026-11-08 after the real-account stop in `verification.md`). After step 6 the
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
and the observation deadline is capped at the remaining time. A marker is never cleared by a decision that was made
after the bound. The guarantee is one way: a passed decision made inside the bound can still be followed by the
marker write (an fsync), the refresh wait, the store close and the lock release, and the lifecycle's timer covers all
of them, so the command may print `shutdown timed out` and exit 1 with a clean marker. That is safe, because the
check passed and the stop completed in order. The owner's approval of the deadlines (task T019) states the total
budget and the reserve left for those steps. Steps 6 and 8 already take up to 5 s and 10 s in the worst case.

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
- `finishStop` passes `quietStopDeadline` and the bound described above. It retires the current generation, then takes
  the attempt slot, then reads `o.gen` again under `o.mu` and retires whatever generation exists now (and repeats until
  none is left), and only then releases the slot. A generation published while the stop was in progress is therefore
  always retired and checked before storage closes and the lock is released.
- The post-pair path, in this order:
  1. a 515 after `PairSuccess`, or `PairSuccess` releasing a pending 515. At the moment the 515 is accepted (in
     `tryManualReconnect` and `releasePending515`, under `o.mu`), the owner marks the generation as in handover and sets
     `PhaseConnecting`; a remote `Disconnected` from the pairing socket that arrives afterwards cannot move the owner to
     `PhaseDisconnected`. The server's close can follow the 515 within milliseconds, before the attempt slot is taken;
  2. take the attempt slot;
  3. `retireGeneration(pairGen, postPairQuietDeadline)`, which ends the pairing window (step 3);
  4. if that check failed, take the incomplete-link path (final, unsafe);
  5. otherwise open a new generation with `LoadDevice` and publish it under `o.mu` together with a check that no stop
     has begun; if one has, discard the unpublished generation without connecting and take the stopped path (the
     marker stays unsafe: the pairing is unresolved). Discarding it retires its barrier and stops its diagnostics
     goroutine, because `OpenSession` has already loaded the device. Immediately before `ConnectContext`, re-check under `o.mu` that
     no stop has begun, and set `accountWork` in the same critical section;
  6. run one `attemptPostPair` under the 30-second watchdog;
  7. release the slot after that attempt and its teardown drain, as in feature 003.
- No Connect begins after `BeginStop`. A stop that interleaves at any point of the post-pair path (during the
  pairing generation's retirement, between retire and open, between open and Connect, during the attempt) ends with
  every generation that exists retired and checked, or never connected.
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
  drained. The stop signal, the work context, the connect watchdog and the `attempt` defer never cancel the attempt
  context once `ConnectContext` has returned, except in the two windows below, which are counted.
- I9: No Connect begins after `BeginStop` (on every path because the stop check and the recording of the attempt abort
  share one critical section of `o.mu`, and `BeginStop` calls the abort synchronously; on the post-pair path also
  because of the atomic publish and re-check), and `finishStop` retires every generation that exists after it holds the
  attempt slot, so the store never closes and the lock is never released while a generation's socket may be live.

## Test seams

- The observation function and the clock are injectable for unit tests: a fake dump source makes the result
  deterministic.
- `internal/wasim` supplies the real client path and counts frames per connection. A test-only hook can hold a
  pinned handler, make a handler write after retirement began, inject a late send through the real client after
  `Disconnect`, or a late store call after the barrier.
- The production observation is always `runtime.Stack`. Tests must not replace it in the simulator suites that
  measure the pass rate. The simulator runs in the same process, so its own websocket server goroutines match the
  package rule; the measurement records how many and the owner decides at T019 how to treat them (research R6).
