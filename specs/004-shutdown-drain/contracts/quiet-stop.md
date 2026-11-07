# Contract: Generation Retirement and the Quiet-Stop Check

Internal contract between the connection owner (`internal/wa/owner.go`), the session adapter
(`internal/wa/session.go`), the network fence (`internal/wa/fence.go`), the observation (`internal/wa/quiet.go`) and
the store's device barrier (`internal/store/device.go`). It replaces, for the post-pair reconnect and for clearing the
marker, feature 003's rule that no Connect may follow the retirement of an unproven generation
(`specs/003-whatsapp-pairing-status/contracts/connection-owner.md`, proof gate). Every other rule of that contract
stands.

## Retirement order (one generation)

1. Mark the generation retiring. New owner admissions for this epoch are refused, as in feature 003.
2. Set the session's retire bit. From now on, callbacks are refused and every dropped event except the self-caused
   `Disconnected` is counted. The event handler stays registered.
3. Cancel the generation lifetime, which is also the library's `BackgroundEventCtx`. End any pairing window of this
   generation, so its QR channel context ends.
4. Wait for the generation's repair pass, as in feature 003.
5. `client.Disconnect()`: the library's own orderly close.
6. **Close the network fence.** Close all wrapped connections; from now on refuse and count every dial and every
   write.
7. **Retire the store barrier.** Refuse and count every later guarded call, and wait for admitted calls to finish.
   The existing context and the 10-second stop bound apply. A barrier that does not drain fails as today
   (`owner_drain_failed`, unsafe).
8. **Observe** with the deadline the caller passes (`quietStopDeadline` or `postPairQuietDeadline`). Poll the bounded
   goroutine dump until two observations at least `quietObservationGap` apart both show zero client-library
   goroutines after removing allow-listed work, or until the deadline.
9. Decide the `QuietStopResult` (data model) and remove the event handler.
10. `FinishRevocation` and the storage-fault check, as in feature 003.

Steps 6 and 7 must not swap. A test asserts that storage is never blocked while the fence is open, and fails when the
order is reversed (spec SC-004).

## Inputs and outputs

```go
// Session
func (s *Session) QuietStop(ctx context.Context, deadline time.Duration) QuietStopResult
// called after Retire has returned nil; a Session that never attempted Connect returns Reason "not_checked", Passed true

// store
func (b *DeviceBarrier) Refused() uint64 // calls refused with ErrRetired for this device session
```

The result is a value. It never holds a reference to the dump, the client or the device.

## Owner use

- `retireGeneration(gen, deadline)` runs steps 1–10 and records a failed result in `unsafeStop`, logging
  `quiet_stop_failed`. A passed result logs `quiet_stop_passed`. A generation that never attempted Connect is not
  checked.
- `finishStop` passes `quietStopDeadline`.
- The post-pair path, in this order:
  1. a 515 after `PairSuccess`, or `PairSuccess` releasing a pending 515;
  2. take the attempt slot;
  3. `retireGeneration(pairGen, postPairQuietDeadline)`;
  4. if that check failed, take the incomplete-link path (final, unsafe);
  5. otherwise open a new generation with `LoadDevice`, then run one `attemptPostPair` under the 30-second watchdog;
  6. release the slot after that attempt and its teardown drain, as in feature 003.
- No other path opens a generation after one that attempted Connect. Startup retries, 405, ordinary reconnect and
  same-process re-pair stay denied.

## Invariants

- I1: No retired generation's dial or write reaches the network after its fence closed.
- I2: No guarded storage call of a retired generation runs after its barrier drained.
- I3: Within one process, a fresh generation exists only after the previous generation's check passed.
- I4: A clean marker is written only after every checked generation of the run passed, and never while a check is
  pending.
- I5: The dump stays inside `quiet.go`. Nothing derived from it except counts and the reason leaves the function.
- I6: Every failure or doubt yields `Passed == false`: an error, a timeout, a cut-off dump, any refusal or any drop.
- I7: The allow-list holds exact function names only, and its `ReviewedFor` equals the `go.mod` whatsmeow version.

## Test seams

- The observation function and the clock are injectable for unit tests: a fake dump source makes the result
  deterministic.
- `internal/wasim` supplies the real client path. A test-only hook can hold a pinned handler, inject a late send
  after the fence, or a late store call after the barrier.
- The production observation is always `runtime.Stack`. Tests must not replace it in the simulator suites that
  measure the pass rate.
