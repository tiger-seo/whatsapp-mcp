# Tasks: Reusable Session After a Proven Quiet Stop

**Input**: Design documents in `specs/004-shutdown-drain/`: spec.md, plan.md, research.md (R1–R10), data-model.md, contracts/quiet-stop.md, contracts/cli.md and quickstart.md.

**Prerequisites**: A passing owner-judge **design** review of the committed feature directory (the hook refuses `/speckit-implement` without it). The constitution is 2.0.0. `docs/specification.md` §8.11 must carry the 2026-11-07 decision (T002).

**Tests**: Required (constitution VI, spec abuse cases, SC-001 to SC-007). Write each test before the code it covers and observe it fail for the right reason. Use channels, barriers and `testing/synctest` for owner timing, not sleeps. Keep the simulator, SQLite and OS fixtures outside synctest bubbles. Simulator suites that measure the pass rate always use the production `runtime.Stack` observation (contract "Test seams"). Automated tests never touch a real account.

**Organization**: Setup, then a foundational phase that builds the pieces, puts the production retirement order in place and runs the measurement spike. It ends at an owner gate. Then US1, US2 and US3 in spec priority order, then polish and evidence. No new setting, flag, dependency, fork or `replace`. Stage 2 (405 budget, startup retries, ordinary reconnect, other 515s, same-process re-pair) is out of scope and stays denied.

**Decisions already made (do not reopen without the user)**:
- option A, no fork; stage 1 only;
- an unresolved pairing keeps the marker unsafe;
- a failed check after a linked `pair` exits 0 with the recovery notice, and a storage fault still exits 1;
- the pass-rate bar is 90% per kind;
- the two deadlines are measured, then approved by the owner together with the total stop budget (T019);
- whether E003-RX-01 covers this feature is the owner's decision (T001), not an assumption.

No commit carries attribution lines.

## Format: `[ID] [P?] [Story] Description`

- Every task has a checkbox, a sequential ID and repository-relative file paths.
- `[P]` means different files and no dependency on an unfinished task.
- `[US1]` to `[US3]` mark story tasks. Setup, foundational and polish tasks have no label.
- Constraints quoted from data-model.md and the contracts are binding.

## Phase 1: Setup

**Purpose**: Confirm the gates and prepare the evidence file. No production code.

- [X] T001 **OWNER DECISION.** Collect the E003-RX-01 facts from `specs/003-whatsapp-pairing-status/verification.md`: the real UTC date against the expiry (start of 2026-12-02 UTC), the pinned `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b` against `go.mod`, the scope (feature 003's private, local, single-owner deployment), any recorded incident, and that this feature makes repeated long connected runs possible. Write them in a new "E003-RX-01 (implementation start)" section of `specs/004-shutdown-drain/verification.md`, then ask the owner whether the exception covers feature 004 and record the answer and date there (research R10; constitution X: no inheritance by another feature). Do not write "no trigger fired" or any answer on the owner's behalf. If it has expired or the owner says it does not cover this feature, stop until the owner records a new decision. Only the owner ticks this task. Do not infer real-account permission.
- [X] T002 Confirm `docs/specification.md` §8.11 carries the 2026-11-07 decision that supersedes the 2026-11-04 fail-closed limitation and the same-generation form of the 2026-11-06 post-pair approval (FR-013). If `docs/` is missing or lacks the entry, stop and ask the user for it; do not write the decision yourself. Record the check in `specs/004-shutdown-drain/verification.md`.
- [X] T003 Extend `specs/004-shutdown-drain/verification.md` (created with the design review row) with these sections: Handoff (branch, commands, environment notes), E003-RX-01 recheck, Measurement and deadline approval, Simulator results, Pass-rate run, Platform evidence, Soak run, Real-account journey, Not verified, and the "Owner reviews" table with the header row from `AGENTS.md`. Leave the review rows empty; the owner-judge agent adds them.

---

## Phase 2: Foundational — pieces, production retirement order and measurement spike

**Purpose**: Build the network fence, the refusal counts, the dropped-event and late-call counts and the observation. Put the production retirement order of `contracts/quiet-stop.md` in place: the three contexts, the fence between Disconnect and the barrier, and the pairing window ended at retirement. Measure that production order on the simulator, then stop at the owner's deadline approval. Nothing in this phase calls `QuietStop` from the owner or clears the marker. The order changes keep feature 003's outcome, so every stop that attempted a production Connect still leaves the marker unsafe until US1. Depends on T001–T003.

**⚠️ CRITICAL**: No user story may wire `QuietStop` into the marker before T019 is ticked.

### Tests for the pieces (write first, observe failing)

- [X] T004 [P] Write `internal/store/device_refused_test.go`: after `DeviceBarrier.MarkRetiring()`, `DeviceBarrier.Refused()` rises by exactly one per guarded call that fails for a reason from the guard or the context. That covers `ErrRetired` at every site (admission in `internal/store/device.go`, `Save`, the deferred `Delete`, `DoDecryptionTxn`, the nested paths, and the repair paths in `internal/store/repair.go`), **and** a cancelled context (`context.Canceled`, `DeadlineExceeded`), `ErrStaleLease` and the `tok.ctx.Err()` returns near `device.go:204-214`. The count is zero before `MarkRetiring` and monotonic, and a result the database itself returns (for example "not found") is not counted. Add the case that matters most: a guarded `PutAppStateSyncKey`-shaped write issued with a context cancelled after `MarkRetiring` is counted. Run it and observe it fail to compile (no `MarkRetiring` or `Refused` yet).
- [X] T005 [P] Write `internal/wa/fence_test.go` against a local `net.Listener`:
  - dials before `Close` succeed and are wrapped, and `Close` closes every wrapped connection;
  - later dials return an error and add to `refusedDials`;
  - later writes on a wrapped connection return an error, send nothing (the listener reads zero bytes) and add to `refusedWrites`;
  - reads failing because the fence closed the connection are not counted;
  - two fences are independent: closing one leaves the other's connections working;
  - the websocket, pre-login and media HTTP clients built from the same fenced transport are all covered.

  This is the wrapper's unit test only. The fence cannot see a library send after `Disconnect`, because the library's socket is nil and the send returns `ErrNotConnected` inside the library (research R3). The abuse-case tests in T021 and T029 drive the real client path instead.
- [X] T006 [P] Write `internal/wa/quiet_test.go` with a fake dump source and fake clock. Cases (each asserts `Passed`, `Reason` and the counts of data-model `QuietStopResult`):
  - zero library goroutines on two observations at least `quietObservationGap` (100 ms) apart passes;
  - one library goroutine at the deadline fails with `work_running`;
  - a goroutine with a `go.mau.fi/whatsmeow`, `go.mau.fi/libsignal` or `github.com/coder/websocket` frame, or a `created by` line in those packages, is counted;
  - a goroutine in `go.mau.fi/util` only is not counted;
  - a dump that fills the 8 MiB `stackBufferMax` fails with `observation_incomplete`;
  - a dump source error fails with `observation_error`;
  - a clean first observation followed by a dirty second one fails;
  - the checking goroutine is excluded;
  - an allow-listed exact function name is ignored but a different function in the same package is not;
  - the reason precedence is `observation_incomplete`, `observation_error`, `stop_bound_exceeded`, `storage_refused`, `network_refused`, `call_refused`, `event_dropped`, `work_running`, and the counts report all of them;
  - a decision made after the stop bound fails with `stop_bound_exceeded` whatever the observation shows;
  - the observation deadline runs from the start of observation (contract step 10) and is capped at the time left in the bound;
  - the result struct and every error string contain no dump text, function names, argument words or keys.
- [X] T007 [P] Write the allow-list pin test in `internal/wa/quiet_test.go`: the list starts empty; every entry names one exact function (no entry may end in `*`, `/`, `.` or name a package without a function); every entry's `ReviewedFor` equals the `go.mau.fi/whatsmeow` version in `go.mod` (read `go.mod` in the test); the test fails when they differ (FR-010, invariant I7).

### Implementation of the pieces

- [X] T008 Add `MarkRetiring()` and `Refused() uint64` to `DeviceBarrier` in `internal/store/repair.go` (beside `StorageFault`) and an atomic counter on the device session in `internal/store/device.go`. After `MarkRetiring`, count every guarded call that fails with `ErrRetired` (sites near `device.go` 250, 455, 462 and 552; `repair.go` near 128 and 228, and any other site a search finds), with a context error, with `ErrStaleLease`, or with another guard error. Today `latchWriteFault` (`device.go` near 170) and `report` (`ops.go`) treat context errors as the caller's, and `device.go` near 204-214 returns `tok.ctx.Err()`: add the counting at those sites without changing their fault semantics. The count is in memory only; no schema change. Make T004 pass.
- [X] T009 Create `internal/wa/fence.go`: a per-generation `fence` that owns a `net.Dialer`, wraps each dialled `net.Conn` so `Write` is refused and counted after `Close`, tracks its connections, and exposes `Close()`, `RefusedDials()`, `RefusedWrites()` and `HTTPClient(base *http.Client) *http.Client`. `HTTPClient` returns a clone whose transport dials through the fence; it wraps a test-injected transport instead of replacing it. Make T005 pass.
- [X] T010 In `internal/wa/session.go`:
  - build the three fenced HTTP clients for every generation from the library defaults (or the injected `SessionConfig.PreLoginHTTP` and `WebsocketHTTP`), and install them with `SetWebsocketHTTPClient`, `SetPreLoginHTTPClient` and `SetMediaHTTPClient` (research R3);
  - keep the event handler registered after the retire bit is set, and change `finishRetire` so the handler is removed only when the check ends (research R4);
  - add the atomic counter `droppedEvents`: every event that `Session.handleEvent` would have forwarded to the owner and dropped after the retire bit, except a disconnect (the raw `Disconnected` event and the `SessionDisconnected` that `handleEvent` maps from it or from `StreamReplaced`). Read the forwarded set from `handleEvent`. The library's `Disconnect` emits no event, a remote `Disconnected` after the 515 is expected, and receipts and presence carry no owner state;
  - add the atomic counter `lateCalls`: every call from the program's own code to the session after the retire bit (`Connect`, which today returns `ErrSessionRetired` uncounted, `GetQRChannel`, `RunRepairs`/`fetchAppState` and `Disconnect`), except the retirement sequence's own Disconnect, which uses an internal method. `GetQRChannel` and `RunRepairs` gain the generation check they lack.

  Write the tests first in `internal/wa/session_test.go`:
  - a late `LoggedOut` after retirement counts as dropped;
  - a remote `Disconnected` and a receipt do not;
  - a late `Connect`, `GetQRChannel`, `RunRepairs` and external `Disconnect` each count and start no work;
  - the retirement sequence's own Disconnect does not count.
- [X] T011 Create `internal/wa/quiet.go`: `QuietStopResult` (fields and reasons exactly as in data-model.md), the exact-function allow-list (empty, with `Function`, `ReviewedFor`, `Evidence`), the constants `quietObservationGap = 100 * time.Millisecond` and `stackBufferMax = 8 << 20`, and the bounded observation:
  - `runtime.Stack(buf, true)` into a buffer that starts at 1 MiB and doubles up to 8 MiB; a full 8 MiB buffer is incomplete;
  - parse the dump in memory and count goroutines with a frame or `created by` line in `go.mau.fi/whatsmeow`, `go.mau.fi/libsignal` or `github.com/coder/websocket`, or their subpackages. Match the package path followed by `.` or `/`, never a bare string prefix;
  - exclude the caller and remove allow-listed work;
  - poll until two consecutive zero observations at least 100 ms apart, or until the deadline.

  The dump never leaves this file (invariant I5). Make T006 and T007 pass.
- [X] T012 Add `func (s *Session) QuietStop(ctx context.Context, deadline time.Duration) QuietStopResult` to `internal/wa/session.go` per `contracts/quiet-stop.md`:
  - it may be called only after `Retire` returned nil;
  - a session that never attempted Connect returns `Reason: not_checked, Passed: true`;
  - it combines the observation, the `fence` refusals, `DeviceBarrier.Refused()`, `lateCalls` and `droppedEvents` into one result, and removes the event handler when done;
  - `Session.accountWork` already exists (`session.go` near line 49); reuse it.

  Test in `internal/wa/session_test.go` that each source fails the result with its reason (`network_refused`, `storage_refused`, `call_refused`, `event_dropped`). Do not call it from the owner yet.

### Production retirement order (write the tests first, observe failing)

- [X] T013 [P] Write `internal/wa/quiet_order_test.go` for the retirement order invariant (SC-004, SEC-002, invariants I1, I2, I8). With a recording fence and barrier:
  - assert the sequence of `contracts/quiet-stop.md`: program work cancelled, Disconnect, fence close, store barrier retire, library context cancel, observe, event handler removal;
  - assert that the store is never blocked while the fence is open, and that the library context is not cancelled before the barrier has drained;
  - make the test fail when the fence and barrier steps are swapped or the library cancel moves earlier, with negative variants through a test seam that expect the order check to report a violation;
  - the lost-write case, driven through the real stop entry and not by calling `retireGeneration` directly: a pinned handler that has committed a decrypt transaction and is about to store an app-state key or sender key when the stop signal arrives (the owner is started with a context that the test cancels, so `BeginStop` calls `o.cancel()`). The handler's context is not cancelled, it completes the write, the key is in the store afterwards, and the check passes;
  - the same case for the connect watchdog: `ConnectContext` has returned, no connected event arrives, the 30-second watchdog fires (fake time). The watchdog disconnects and does not cancel the handler context, a handler writing in the gap completes, and nothing is lost;
  - the startup-disconnect case: a startup attempt whose `ConnectContext` returned and whose connection then ends with a remote `Disconnected` before the connected event (`outcomeFailed`), or that is stopped or times out (`outcomeStopped`, `outcomeTimeout`), does not cancel the handler context: a handler writing in that gap completes and nothing is lost;
  - the synchronous-abort case: a stop signal that arrives between the attempt's stop check and `ConnectContext` (barriers, no sleeps) either prevents the Connect or aborts it through the recorded abort at once; the simulator sees no dial after `BeginStop` returned;
  - the control cases: when the library context is cancelled first (by the owner context or by the watchdog), the same write fails with `context.Canceled`, which is counted from the first cancellation and fails the check with `storage_refused`;
  - a Connect that has not returned is still aborted promptly by the recorded attempt abort, called by the stop signal, retirement or the watchdog, and that cancellation starts the count;
  - the pairing generation: a pinned handler of the pairing socket writes (an app-state key or sender key) during the handover retirement at the 515 and during a stop signal in an open pairing window; the write completes, nothing is lost, and the pairing generation's check passes;
  - the two windows of the contract: (a) the stop calls the abort in the instant after `ConnectContext` returned and before the owner cleared it (barriers): a failed guarded write is counted and the check fails with `storage_refused`, the marker stays unsafe; (b) the stop calls the abort and retirement sets the retire bit before `Session.Connect` is called: no dial happens, no late call is counted, the generation is not checked and the marker follows feature 003's rules for a run with no production Connect. T018 records how often (a) happens.
- [X] T014 [P] Write the pairing-window case in `internal/wa/pairing_test.go`: retiring a generation with an active or just-succeeded pairing window ends the window's QR channel context at contract step 3, before Disconnect. The window's diagnostics and its unresolved-pairing accounting are unchanged, and no QR emitter goroutine of that window is left at the observation.
- [X] T015 In `internal/wa/session.go` make `Retire` follow contract steps 2 and 6–8:
  - add a separate session entry point that only sets the retire bit and calls `DeviceBarrier.MarkRetiring()`; the owner calls it at contract step 2, and contract steps 3-5 (end the pairing window, cancel `gen.work`, wait for the repair pass) run between it and `Retire`;
  - `Retire` then runs steps 6-8:
  - then `Disconnect()` (the sequence's own, uncounted);
  - then close the fence;
  - then retire the store barrier.

  Each step runs in that order and only that order. The existing `Retire` and `finishRetire` remove the event handler first, then disconnect and retire the barrier: insert the fence close between them, and keep the handler until the check ends. Keep the existing context and the 10-second stop bound. Make the session-level part of T013 pass.
- [X] T016 In `internal/wa/owner.go` and `internal/wa/pairing.go` separate the three contexts of `contracts/quiet-stop.md`. Today `gen.lifetime` is a child of the owner context (`o.ctx`, near line 222), `BeginStop` cancels it (near line 460), `retireGeneration` calls `gen.cancel()` first (near line 530), `attemptCtx` derives from `gen.lifetime` (near line 710) and is the context the library gives its node handlers, and the connect and pairing watchdogs cancel it (`owner.go` near 713-726, `pairing.go` near 183-193).
  - Create `gen.work` as a child of `o.ctx` for the program's own work, and use it for the pairing window's QR channel (`GetQRChannel`, near `pairing.go:174`) and the repair pass (`RunRepairs`, near `owner.go:922`).
  - Create `gen.lib` as `context.WithCancel(context.WithoutCancel(o.ctx))` (`WithoutCancel` alone cannot be cancelled at step 9) and use it as the library's `BackgroundEventCtx` and the parent of the attempt context. Only retirement step 9 (and the forced path when the barrier cannot drain) cancels it.
  - Make the attempt context a child of `gen.lib`. Record its cancel function in the generation as the attempt abort under `o.mu` before `ConnectContext` is called, in the same critical section as the check that no stop has begun, and clear it under `o.mu` when `ConnectContext` returns. `BeginStop`, the watchdogs and retirement abort an unreturned Connect only by calling the recorded abort synchronously under `o.mu` through one helper that calls `MarkRetiring()` first. Do not use `context.AfterFunc` (it runs in a new goroutine, so the abort would come late and a Connect could dial after the stop began).
  - Delete the cancel in the existing `defer ... if !connected { cancel() }` of `attempt` (near line 728-735) for every outcome reached after `ConnectContext` returned (`outcomeFailed`, `outcomeStopped`, `outcomeTimeout`): the attempt context is released with `gen.lib` at step 9. After `ConnectContext` returned, a watchdog calls `Disconnect()` and does not cancel the handler context.
  - Move the other uses of `gen.lifetime` to `gen.work`: `Versions.Refresh` (`owner.go` near 610, `pairing.go` near 166), `watchStorageFault` (near 252), the post-pair slot wait (near 864), the QR drain goroutine (`pairing.go` near 224), the reusable check (`pairing.go` near 106), the repair error check (`owner.go` near 929). `SessionConfig.Lifetime` feeds both `LoadDevice`/`ConfigureRecovery`/`PendingRepairs` (`session.go` near 86-95) and `BackgroundEventCtx` (near 124): split it into a work context and a library context. A stop during a pairing window's version refresh must not wait for the HTTP bound while holding the slot.
  - Call `MarkRetiring()` on the generation's barrier before the first of: `BeginStop` cancelling `o.ctx`, the start of retirement, and any cancellation of the attempt context.
  - The pairing generation's Connect is **not** in that list. Its attempt context (`pairing.go` near 180, passed to `Connect` near 241, a child of the lifetime today) is the handler context of the pairing socket: make it a child of `gen.lib` and give it the same mechanism as the startup attempt (stop check and recorded abort in one `o.mu` critical section; the pairing watchdog near 183-193 only calls `Disconnect()` once the Connect has returned). Only the window's QR channel and diagnostics use `gen.work`.
  - Retirement step 4 calls the recorded attempt abort first, then cancels `gen.work`; `gen.work` reaches no Connect context.
  - In `Session.Connect` (`session.go` near 155), do not count a late call when the attempt was already aborted through the recorded abort (window (b) of the contract); that generation never attempted a Connect.
  - A stop during a Connect that has not returned must still abort it promptly, through the recorded abort.

  `retireGeneration` keeps its feature-003 signature and outcome in this task (`UpstreamDrainUnproven` still marks unsafe). Make T013 pass, including the stop-signal and watchdog cases.
- [X] T017 In `internal/wa/pairing.go` add an explicit way to end the pairing window of a generation: cancel the QR channel context (a child of `gen.work`, never of `gen.lib`) so the emitter goroutine in whatsmeow `qrchan.go` can return. Call it from retirement step 3 of `contracts/quiet-stop.md`, and keep the window's existing diagnostics and unresolved-pairing accounting. Make T014 pass. Record the failing observations of T013 and T014 in the Handoff section of `specs/004-shutdown-drain/verification.md`.

### Measurement spike and owner gate

- [X] T018 Write the test-only measurement `TestQuietStopMeasurement` in `internal/wa/quiet_measure_test.go`, behind a build tag or a `WA_MEASURE=1` guard so it does not run in the normal suite. It depends on T015–T017, so it measures the production retirement order, not a harness copy of it.
  - Using `internal/wasim`, run at least 50 orderly stops of each kind: `pair` with the post-pair reconnect, `stdio` connected and `serve` connected.
  - For the `pair` kind, retire the pairing generation at the 515 after `PairSuccess` through the production `retireGeneration` path (window ended, generation retired), because that is the state `postPairQuietDeadline` governs. Measure the final stop separately.
  - For every kind, call `Session.QuietStop` after the production retirement, without touching the marker.
  - Count how often a stop lands in window (a) of the contract (the recorded abort still set after `ConnectContext` returned) and report it per kind.
  - Record how many goroutines at the observation belong to the in-process simulator's own websocket server (frames in `internal/wasim`), and the pass rate with and without them.
  - For each kind, print p50, p95 and max time until library work disappears, each failure category, and the pass rate under candidate deadlines (for example 250 ms, 500 ms, 1 s, 2 s and 5 s).

  Run it as in quickstart §1 (`CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopMeasurement -count=1 -v -timeout 30m`), once natively on Windows and once under WSL2. Paste the tables into `specs/004-shutdown-drain/verification.md` under "Measurement and deadline approval" with commands, dates and exit codes read without a pipe.
- [X] T019 **OWNER GATE.** The owner reads the T018 tables and records in `specs/004-shutdown-drain/verification.md` (value, date, evidence):
  - approval of `quietStopDeadline` and `postPairQuietDeadline`;
  - the total stop budget: `Disconnect` (up to 5 s) plus the barrier drain (up to 10 s) plus the observation, against the 10 s `stopBound`, the reserve left for the steps after the decision (marker write with fsync, refresh wait, store close, lock release), and what a late check means: a decision after the bound fails with `stop_bound_exceeded` and the marker stays unsafe; a passed decision inside the bound may still be followed by `shutdown timed out` and exit 1 with a clean marker, which is safe;
  - how the simulator-server goroutines are treated: accept the measured number, run the simulator out of process, or filter by `internal/wasim` frames in the measurement only (never in production code);
  - if any kind passes under 90%, first the decision whether to keep option A, tune it (including a reviewed allow-list entry with evidence) or move to the fork (SC-005).

  Then add the two approved constants to `internal/wa/quiet.go`. Only the owner ticks this task. **Blocks T020 and every later production use of `QuietStop`.**

**Checkpoint**: The fence, the counts and the observation exist and are tested. The production retirement order is in place and measured. The marker behaves as in feature 003.

---

## Phase 3: User Story 1 — Restart without offline recovery after a clean stop (Priority: P1) 🎯 MVP

**Goal**: A normal stop of `stdio` or `serve` clears the unsafe-run marker when every connected generation passed the quiet-stop check, and the next start reuses the saved session. Every doubt keeps the marker unsafe. Until US2 replaces the same-client post-pair reconnect, a `pair` run that used it never clears the marker.

**Independent Test**: On the simulator, connect a saved session, stop normally and restart: the second run connects with no recovery and no QR. Repeat with each fault below; each keeps the marker unsafe: leftover work, a refused network call, a refused storage call, a dropped event, a cut-off observation, a held handler, the late-logout ordering, a crash and a forced exit.

### Tests for User Story 1 (write first, observe failing)

- [X] T020 [P] [US1] Write `internal/wa/owner_quiet_test.go` (with `testing/synctest` and a fake observation). Clears and refusals:
  - `finishStop` clears the marker after a passed check for a generation that attempted Connect;
  - a failed check of any reason keeps it unsafe, and logs `quiet_stop_failed` with the fields of `contracts/cli.md` and `run_guard_recovery_required`;
  - a generation that never attempted Connect is not checked, and today's safe-clear rules apply unchanged (scenario 7);
  - each of these keeps the marker unsafe even when the check passed: a storage fault, an unresolved pairing (QR final timeout, cancellation, pairing success without a confirmed follow-up connection), a latched logout or client deletion without durable cleanup, and a refused low-disk clear;
  - a latched logout with durable cleanup and a passed check clears; a passing check never overrides a logout;
  - `upstream_drain_unproven` is never emitted;
  - two generations in one run keep the marker unsafe if either failed (invariant I4);
  - **a generation that made a same-client post-pair reconnect keeps the marker unsafe even when its check passed** (invariant I3, until US2 removes that path).

  The stop bound:
  - the check never lengthens the 10-second stop bound for writes (FR-011);
  - a failed check is not a failed shutdown (orderly close, lock released last);
  - the whole stop runs inside the 10 s `stopBound`, and the observation deadline is capped at the time left;
  - a decision reached after the bound fails with `stop_bound_exceeded`, logs `quiet_stop_failed` and keeps the marker unsafe, so a late decision never clears the marker (scenario 6). A passed decision inside the bound followed by `shutdown timed out` and exit 1 with a clean marker is allowed and tested as such.
- [X] T021 [P] [US1] Write `internal/wa/wasim_quiet_test.go` (real pinned client, one simulator at a time, outside synctest bubbles):
  - a connected `stdio`-shaped run and a `serve`-shaped run stop and the marker is clear; reopening connects with zero QR codes, zero recoveries and zero registrations (SC-001);
  - a held pinned message handler across retirement fails the check until it returns, and passes only if it made no call after the blocks (scenario 9);
  - the late-logout ordering of `TestRunGuardLatePinnedLogoutAfterRetirement` (`internal/wa/run_guard_test.go`) fails the check and commits no clean marker while that work is pending (scenario 8);
  - a retry receipt or key request issued through the real client after `Disconnect` returns `ErrNotConnected`, and the simulator sees zero frames from the retired generation. The fence does not see it; the observation covers a sender that is still running;
  - a dial or write through the generation's media or pre-login client after the fence is refused, counted, and fails the check;
  - a late `Connect`, QR-channel or repair call to the session is refused, counted, and fails the check with `call_refused`;
  - a remote `Disconnected` after the 515 and an ordinary receipt do not fail the check;
  - an injected late store call after the barrier is refused, counted, changes no row, and fails the check;
  - a fake over-large dump fails;
  - a crash (process kill helper), a forced exit and a stop beyond its bound leave the marker unsafe (scenario 6);
  - a stop signal during the check keeps the drain order.
- [X] T022 [P] [US1] Write the command-level tests in `cmd/whatsapp-mcp/run_guard_test.go`. Rewrite the cases that expect unsafe after every production Connect, and keep the unsafe-marker-at-start and legacy-marker refusal cases.
  - `stdio` and `serve` exit 0 with a clean marker after a passed check, and the next start connects;
  - after a failed check they exit 1 with `run_guard_recovery_required` and the reason;
  - `status` after a passed stop is identical to `status` after a run with no production connection (FR-012);
  - a run without a saved identity is unchanged;
  - until US2, a `pair` run that used the same-client post-pair reconnect still exits as in feature 003 with an unsafe marker.
- [X] T023 [US1] Run T020–T022 and record the failing observations (which assertion failed and why) in the Handoff section of `specs/004-shutdown-drain/verification.md`. A test that passes before the code exists is wrong; fix the test first.

### Implementation for User Story 1

- [X] T024 [US1] In `internal/wa/owner.go` change `retireGeneration(gen *genState)` to `retireGeneration(gen *genState, deadline time.Duration)` and follow contract steps 1–12, using the three contexts from T016:
  - after `Retire` returns nil, cancel the library context, then call `Session.QuietStop(ctx, min(deadline, time left in stopBound))` before `FinishRevocation`. A decision after the bound is `stop_bound_exceeded`;
  - the owner starts the `stopBound` timer at `BeginStop`;
  - on a failed result set `o.unsafeStop` and log `quiet_stop_failed` (error; fields `epoch`, `reason`, `library_goroutines`, `network_refused`, `storage_refused`, `calls_refused`, `events_dropped`, `duration_ms`);
  - on a pass log `quiet_stop_passed` (info; `epoch`, `duration_ms`);
  - skip the check for a generation with `accountWork == false`;
  - give every caller a deadline: `finishStop` and the other non-pairing callers (`owner.go` near 184, 274 and the logout retire near 361) pass `quietStopDeadline` (a logout with durable cleanup can still clear the marker after a passed check, scenario 5); the pairing callers (`pairing.go` near 85, 112, 237 and 419) pass `quietStopDeadline` and their outcome stays unresolved and unsafe; only the post-pair retire passes `postPairQuietDeadline`. Read each call site and list the result in the Handoff section of `specs/004-shutdown-drain/verification.md`;
  - the result lives in the generation (`quiet`, written once).

  Never log dump text, function names, keys, QR material or account identifiers.
- [X] T025 [US1] In `internal/wa/owner.go` `finishStop` (near line 476):
  - pass `quietStopDeadline` to `retireGeneration`;
  - remove the `UpstreamDrainUnproven` branch (near line 544) and its `upstream_drain_unproven` event;
  - after `finishStop` retires the current generation and takes the attempt slot, re-read `o.gen` under `o.mu` and retire whatever generation exists, repeating until none is left, and only then release the slot (invariant I9);
  - keep every other clear condition (`unresolved`, `unsafeStop`, `health.StorageFault`, latched-logout cleanup, `ClearRunGuard` low-disk admission) exactly as in the data-model "Run outcome";
  - while the same-client post-pair path still exists, set `unsafeStop` for a generation whose `reconnects > 0` (it made a same-client post-pair reconnect), whatever its check shows. T034 removes this rule together with that path;
  - remove `UpstreamDrainUnproven()` from `Session` (`internal/wa/session.go` near line 170) and its interface assertion, and delete or rewrite the `internal/wa/session_reconnect_test.go` cases that asserted it.

  Make T020, T021 and T022 pass.
- [X] T026 [US1] In `cmd/whatsapp-mcp/commands.go` and `lifecycle.go`, pass the lifecycle's shutdown timeout to the owner as `OwnerConfig.StopBound` (10 s, the constant `lc.timeout` uses; not a setting). Keep the exit codes and messages of `contracts/cli.md` for `stdio` and `serve`: exit 0 after a passed check, exit 1 with `run_guard_recovery_required` after a failed one. stdout in stdio mode stays protocol-only. No new flag or setting.
- [X] T027 [US1] Run the US1 suites with `-count=3` as in quickstart §2 (`CGO_ENABLED=0 go test ./internal/wa ./internal/store -run 'Quiet|Fence|Refused|RunGuard|Logout' -count=3 -timeout 20m`), and `CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 20m`. Record commands, dates, counts and exit codes (read directly, no pipe) in `specs/004-shutdown-drain/verification.md` "Simulator results".

**Checkpoint**: US1 works on its own. `stdio` and `serve` clear the marker after a proven quiet stop. `pair` still uses the same-client post-pair reconnect behind its switch, and a run that used it always leaves the marker unsafe (T025), so this interim state never clears the marker on the path the 2026-11-07 decision replaced. Do not run the real-account journey (T045) in this state.

---

## Phase 4: User Story 2 — Pair once and keep the device (Priority: P1)

**Goal**: The connection WhatsApp requests right after pairing runs on a fresh client and generation, only after the pairing generation passed the check. Late traffic from the pairing session cannot reach it. `PostPairReconnect` is removed.

**Independent Test**: On the simulator, pair, receive the restart request, and confirm three things: late sends from the pairing generation never reach the fresh connection; the fresh connection uses a new client and generation with the saved identity and the selected version; a clean stop leaves the marker clear for the next `stdio` start.

### Tests for User Story 2 (write first, observe failing)

- [X] T028 [P] [US2] Rewrite `internal/wa/owner_postpair_test.go` and `internal/wa/owner_postpair_recheck_test.go` for the fresh-client path (remove `ot.o.cfg.PostPairReconnect = true`; the path is part of `pair`). Cases:
  - the 515 after `PairSuccess` retires the pairing generation with `postPairQuietDeadline`, then opens exactly one new generation;
  - a 515 before `PairSuccess` waits within the 30-second watchdog and is refused on `PairError`, logout, stop or the watchdog;
  - duplicate 515s share one attempt;
  - a failed check starts no connection and takes the incomplete-link path (final, unsafe);
  - a failed fresh connection, a stall past 30 s and a cancellation are final with no second attempt;
  - the fresh generation exists only after the previous check passed (I3);
  - the attempt slot is held across retire, load, connect and teardown drain;
  - the recheck-after-disconnect, stop-during-disconnect and recheck-after-slot cases keep their meaning;
  - after a fresh-client post-pair link and a passed stop, the marker clears (the T025 same-client rule no longer applies);
  - a stop signal at each interleaving, all with `testing/synctest` barriers: during the pairing generation's retirement, between its retirement and the fresh generation's creation, between creation and `ConnectContext`, and during the attempt. In each, no Connect begins after `BeginStop`, every generation that exists is retired and checked (or never connected) before storage closes and the lock is released, the unpublished generation is discarded without connecting, and the marker stays unsafe because the pairing is unresolved (invariant I9).
- [X] T029 [P] [US2] Extend `internal/wa/wasim_owner_test.go` (`TestOwnerPostPairReconnectReachesConnectedAgainstSimulator`, `TestOwnerDeniesPostPairReconnectByDefaultAgainstSimulator`, `TestOwnerPostPairReconnectWaitsForHeldSave`) and add simulator cases:
  - the fresh client logs in with the saved-identity payload (no registration), the currently selected WhatsApp Web version and the Linux/Chrome identification, asserted on the wasim handshake (FR-004);
  - injected late sends from the pairing generation through the real client after its `Disconnect` and fence (a session announcement, a receipt, a key share, a resent request, a disconnect) never reach the fresh connection (scenario 7, SC-003). They return `ErrNotConnected` inside the library, the simulator counts zero frames from the retired generation on the fresh connection, and any dial or write through the retired generation's media or pre-login clients is refused and counted. Do not prove this by writing to the wrapped connection directly;
  - a late `Disconnect()` from the pairing error tail or the QR emitter reaches only the retired client, and the fresh connection stays up;
  - a server close of the pairing socket right after the 515, before and during the pairing generation's retirement: the command never sees `PhaseDisconnected` in a normal handover and still links;
  - a stop signal at each point of the post-pair handover (T028's interleavings, on the real client): the drain order holds, the simulator sees no dial from a fresh client after the stop began, and the store closes only after every generation is retired.

  The window-ending case is covered by T014.
- [X] T030 [P] [US2] Update `cmd/whatsapp-mcp/pair_test.go`, `pair_retry_test.go` and `run_guard_test.go`. Replace `TestProductionOwnerEnablesPostPairReconnectOnlyForPair`, which tested the switch, with a test that `stdio` and `serve` have no reconnect and no pairing path. Then check the `pair` exit codes and stderr of `contracts/cli.md`:
  - linked and passed exits 0 with `whatsapp-mcp: pair: linked. The saved session can be reused.` and a clean marker;
  - linked, but the stop failed its check, exits 0 with the existing recovery notice plus the reason category, and an unsafe marker;
  - the pairing generation failing its check before the fresh connection exits 1 with the existing "linking could not be completed safely" line plus the category;
  - a failed, stalled or cancelled fresh connection exits 1;
  - QR final timeout, cancellation, Enter or EOF exit 1 and stay unsafe;
  - a clean saved identity connects once and stops with `connected. The saved session can be reused.`;
  - a storage fault or any other stop error exits 1.
- [X] T031 [US2] Run T028–T030 and record the failing observations in `specs/004-shutdown-drain/verification.md` Handoff.

### Implementation for User Story 2

- [X] T032 [US2] In `internal/wa/owner.go` rewrite `runPostPairReconnect` (near line 861) and `tryManualReconnect` (near line 785) per `contracts/quiet-stop.md` "Owner use". When the 515 is accepted after `PairSuccess`, or `PairSuccess` releases a pending 515 (`tryManualReconnect` and `releasePending515`), first mark the generation as in handover and set `PhaseConnecting` under `o.mu`, before the attempt slot is taken. The server's close can follow the 515 within milliseconds; a remote `Disconnected` that dispatch sees after that point must not move the owner to `PhaseDisconnected` and make `waitSavedConnection(afterPairing=true)` report a failed link. Then:
  1. take the attempt slot;
  2. run `retireGeneration(pairGen, postPairQuietDeadline)`, which ends the pairing window through T017;
  3. if that check failed, take the incomplete-link path;
  4. otherwise `openGeneration` a new client (`LoadDevice` from the durably saved identity, the currently selected version, Linux/Chrome properties, no registration) and publish it under `o.mu` together with a check that no stop has begun. If one has, discard the unpublished generation without connecting (retire its barrier and stop its diagnostics goroutine, since `OpenSession` already loaded the device) and take the stopped path (the pairing is unresolved, so the marker stays unsafe). Immediately before `ConnectContext`, re-check under `o.mu` that no stop has begun, record the attempt abort and set `accountWork` in the same critical section (`attempt`, near line 706, makes no such check today);
  5. run exactly one `attemptPostPair` under the 30-second watchdog;
  6. release the slot after that attempt and its teardown drain. `finishStop` (T025) retires a generation published meanwhile.

  Any failure, stall, cancellation or failed check is final. Keep the 515 waiting rules and duplicate-request sharing. Make T028 and T029 pass.
- [X] T033 [US2] Remove `OwnerConfig.PostPairReconnect` (`internal/wa/owner.go` near line 46) and every use:
  - the `!o.cfg.PostPairReconnect` early return near line 791;
  - `cmd/whatsapp-mcp/commands.go` near line 184 (`PostPairReconnect: !deps.shell`);
  - `internal/wa/testdata/cmd/session-check/main.go` near line 97 and its `postPair` environment field;
  - the helper's own tests in `internal/wa/testdata/cmd/session-check/main_test.go`, which assert "connect does not enable the post-pair reconnect" and "pair enables it". Rewrite them for the fresh-client path: `pair` links through it, and `connect` never pairs;
  - the test rigs (`internal/wa/wasim_owner_test.go` near line 50).

  Keep `denyReconnect` for every other reconnect path, with its existing `reconnect_denied` diagnostic (FR-006). Make T030 pass. Run `go vet` and `go test ./internal/wa/testdata/cmd/session-check` (outside `./...`) and record the results.
- [X] T034 [US2] Remove the T025 rule that marks a same-client post-pair reconnect unsafe, since that path no longer exists. Run the US2 suites (quickstart §2 with `PostPair`) at `-count=3`, and `CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 20m`. Record the results in `specs/004-shutdown-drain/verification.md` "Simulator results". Confirm with `grep -rn PostPairReconnect --include=*.go .` (expect no matches, including under `testdata`) and record it.

**Checkpoint**: `pair` links on a fresh client, and US1 and US2 both work. The switch is gone.

---

## Phase 5: User Story 3 — Know why the session cannot be reused (Priority: P2)

**Goal**: A failed check is reported in plain words with one category, and its counts are on stderr; a passed one says the session can be reused. No diagnostic leaks observation data.

**Independent Test**: Force each failure category on the simulator and read the stop diagnostic and the `status` output. No diagnostic at any log level contains library state, arguments, keys or account identifiers.

### Tests for User Story 3 (write first, observe failing)

- [X] T035 [P] [US3] Write `cmd/whatsapp-mcp/quiet_output_test.go`. For each reason (`work_running`, `network_refused`, `storage_refused`, `call_refused`, `event_dropped`, `stop_bound_exceeded`, `observation_incomplete`, `observation_error`), a forced failure prints:
  - exactly one human stderr line naming that category and saying, in operator wording, that offline recovery is required;
  - the `quiet_stop_failed` event with the counts, also on stderr (`contracts/cli.md`).

  Also check: a pass prints that the session can be reused; stdout in stdio mode stays protocol-only; `status` afterwards reports the marker state as today and stores nothing new (scenarios 1–3).
- [X] T036 [P] [US3] Write the secrecy tests in `internal/wa/quiet_secrecy_test.go` (SC-006, SEC-004).
  - Capture logs at debug, info, warn and error through the production logger bridge while forcing each failure and a pass, plus a held-handler case and an over-large dump.
  - Assert zero occurrences of: dump text, `goroutine ` headers, function names from the dump, argument words, key bytes, QR material, full account identifiers. Use synthetic markers planted in goroutine names and device data.
  - Assert that the result value and every returned error contain none of them either.

### Implementation for User Story 3

- [X] T037 [US3] In `cmd/whatsapp-mcp/commands.go` (and `lifecycle.go` if the stop messages live there), add the human lines of `contracts/cli.md` for `pair`, `stdio` and `serve`: `reusable` after a pass, `offline recovery required (<category>)` after a failure. The `pair` lines are exactly those in the contract table. Wire the reason from `QuietStopResult` through the owner's `StopError`/health value without ever carrying dump text. Make T035 and T036 pass.
- [X] T038 [US3] Run the output and secrecy suites and record commands, dates, counts and exit codes in `specs/004-shutdown-drain/verification.md`.

**Checkpoint**: All three stories work and are explained to the operator.

---

## Phase 6: Polish and Cross-Cutting Concerns

**Purpose**: Documentation, the pass-rate evidence, full gates, platform evidence and the permitted real-account journey. No pull request until the code review passes.

- [X] T039 [P] Update `OPERATIONS.md`: a reusable session after a passed quiet-stop check; offline recovery still required after a failed one or an unresolved pairing; the new stop wording; the `quiet_stop_passed` and `quiet_stop_failed` events; `upstream_drain_unproven` gone; and the split between implemented and verified. Update `AGENTS.md` "Current state" to describe feature 004 as built (do not call it merged until it is) and the removal of `PostPairReconnect`. Update `docs/backlog/003-post-pair-reconnect-and-unsafe-marker.md` and `docs/backlog/INDEX.md` to say that debts 1, 2 and 4 are delivered and that stage 2 stays open (docs are local and not in the repository; report the change to the user). Also add the extension of E003-RX-01 to feature 004 to the follow-up `docs/backlog/003-whatsmeow-receive-resource-bounds.md`. Keep the repository files LF.
- [X] T040 [P] Check the doc comments in `internal/wa/owner.go`, `session.go`, `pairing.go`, `fence.go` and `quiet.go` for stale text about `PostPairReconnect`, "upstream drain unproven" and the same-client reconnect, and fix them. Do not cite FR-, SC-, US, research or finding IDs in code comments (constitution, user rule).
- [X] T041 Add the pass-rate recorder to `internal/wa/quiet_measure_test.go`. Reuse the T018 harness with the approved deadlines and the production `retireGeneration` path. Run at least 50 simulated orderly stops each of `pair` with the post-pair reconnect, `stdio` and `serve`, and print and record the pass rate per kind and the failure categories (SC-005). The bar is 90% for `stdio`, for `serve`, and for the pairing generation's check. Below either bar, record the numbers and ask the owner for the decision before the code review. Paste the result into `specs/004-shutdown-drain/verification.md` "Pass-rate run".
- [X] T042 Run the full gates of quickstart §4 and record each exit code read directly:
  - `gofmt -l .`, `go vet ./...`, `go mod verify`;
  - `CGO_ENABLED=0 go test ./... -count=1 -timeout 30m`;
  - the five cross-builds;
  - `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`;
  - for the helper, which `./...` skips: `go vet ./internal/wa/testdata/cmd/session-check`, `go test ./internal/wa/testdata/cmd/session-check` and its build.

  Confirm `go.mod` and `go.sum` are unchanged (SEC-007) with `git diff main -- go.mod go.sum`. Keep `TEMP` and `TMP` on `D:\tmp`; C: is nearly full.
- [X] T043 Run the whole suite under WSL2 as in `AGENTS.md` (`wsl -d Ubuntu --cd /mnt/d/projects-mou/whatsapp-mcp-next --exec env CGO_ENABLED=0 go test ./... -count=1 -timeout 10m`), plus the helper's test, and record it as WSL2, not native Linux. Push the branch for CI only if the user asks, and record the CI result per OS when it exists; anything not run is "not verified".
- [X] T044 Check LF endings with `git ls-files --eol` and confirm no tracked file shows `w/crlf` after the scripted and tool edits.
- [ ] T045 **Permitted real-account journey (SC-007), only after US2 and only with the owner's explicit permission for each step** (quickstart §5).
  - Steps: `pair` into a new empty directory (for example `D:\tmp\wa-004a`) with `--log-level debug`; a normal stop; a restart with the saved session through `stdio` or the `session-check -action connect` helper, without recovery; the owner's cleanup (unlink on the phone, then offline recovery or delete).
  - Record redacted results: each check's pass or fail, the timings between `PairSuccess`, the 515 and the fresh connection, exit codes, and the phone's linked-device entry. A step not performed is recorded as not verified.
  - If the fresh-client reconnect does not complete the link, stop and ask the owner for the decision named in the spec's Assumptions: fix it, keep the same-client reconnect with the marker unsafe, or stop the feature.

  Do not act on a real account without that permission.
- [ ] T046 Write and run the soak test `TestQuietStopSoak` in `internal/wa/quiet_soak_test.go` (E003-RX-01 compensating control, condition 4 of the owner's extension; quickstart §6). Guard it with `WA_SOAK=1` so it never runs in the normal suite. It uses the production owner and store on the simulator. It stays connected for 4 hours in a `serve`-shaped run while `internal/wasim` sends synthetic traffic at a steady rate (receipts, presence, app-state notifications, messages) with a burst every 10 minutes. It samples every 60 s, reusing the owner's monitor samples: heap in use, goroutine count, WAL and database file sizes, and process resident memory read from the operating system. It ends with an orderly stop, then asserts `quiet_stop_passed`, a clear marker and the goroutine count back at the pre-connect baseline. It records start, peak and end per quantity and the growth slope. Run it natively on Windows with `TEMP` on `D:\tmp` (the database and WAL need room), and under WSL2 if wanted, and paste the table and the commands, dates and exit codes (read directly, no pipe) into `specs/004-shutdown-drain/verification.md` "Soak run". Sustained unexpected growth is a resource incident under the owner's extension: stop and ask the owner. The simulator cannot show real receive volume: record real-account receive volume as "not verified" unless the owner separately permits a real-account run for it.
- [ ] T047 Roll up `specs/004-shutdown-drain/verification.md`: the E003-RX-01 recheck at code review against the owner's extension of 2026-11-07: (1) scope is still feature 004 only and still the private, local, single-owner deployment; (2) the code review happens before the start of 2026-12-02 UTC and the pin is unchanged; (3) no resource incident in the soak run or any long run; (4) the soak result of T046; the measured pass rates, the approved deadlines, platforms covered, accepted risks, and every "Not verified" item (another account or phone, native Linux or macOS runs, power loss). Then commit the work and the evidence and run the owner-judge agent in **code** mode; add its row to the "Owner reviews" table and commit it.

---

## Dependencies and Execution Order

- **Phase 1** (T001–T003) runs first. T001 and T002 stop the feature if the exception expired or does not cover it, or if §8.11 lacks the decision.
- **Phase 2** depends on Phase 1.
  - Tests T004–T007 are parallel (different files).
  - T008 follows T004; T009 follows T005; T010 follows T009; T011 follows T006 and T007; T012 follows T008, T010 and T011.
  - T013 and T014 can be written in parallel once T012 exists.
  - T015 follows T013; T016 follows T015; T017 follows T014 and T016.
  - T018 follows T015–T017, so it measures the production order.
  - **T019 is the owner gate and blocks everything that calls `QuietStop` from the owner.**
- **US1** (T020–T027) depends on T019. US1 is the MVP.
- **US2** (T028–T034) depends on US1's T024 and T025, which it reuses (`retireGeneration` with a deadline and the same-client rule that T034 removes), and on T019.
- **US3** (T035–T038) depends on US1 and US2 for the messages; T036 can be written after T012.
- **Polish** (T039–T047) depends on all stories. T041 needs T019's approved deadlines. T045 needs US2 and the owner's permission. T046 (soak) needs US1 and US2. T045 and T046 run before T047.
- Within a story: tests, then the failing-observation task, then implementation.

### Parallel opportunities

- Phase 2 tests: T004, T005, T006 and T007 together; then T013 and T014 together.
- US1 tests: T020, T021 and T022 together (distinct files).
- US2 tests: T028, T029 and T030 together.
- US3 tests: T035 and T036 together.
- Polish: T039 and T040 together.

### Parallel example (Phase 2 tests)

```text
Task: "Write internal/store/device_refused_test.go (T004)"
Task: "Write internal/wa/fence_test.go (T005)"
Task: "Write internal/wa/quiet_test.go cases (T006) and the pin test (T007)"
```

## Implementation Strategy

1. **MVP = Phase 1 + Phase 2 + US1.** After T019 and US1, the owner can see a stopped `stdio` or `serve` session reused, which is what unblocks message ingest. `pair` keeps its same-client reconnect until US2, and a run that used it stays unsafe (T025).
2. Add US2 to remove the switch and move the post-pair reconnect onto a fresh client.
3. Add US3 for the operator wording.
4. Finish with documentation, the pass-rate run, the full gates, the real-account journey (after US2, with permission) and the code review.
5. If a measured pass rate is under 90%, stop at the owner decision (keep, tune or fork) before the code review. Do not widen the allow-list on your own.

## Notes

- Total tasks: 47 (Setup 3, Foundational 16, US1 8, US2 7, US3 4, Polish 9).
- Keep each commit to one logical change; Conventional Commits that explain why; no attribution lines.
- Tick tasks `[X]` as they are done and record verification results with commands, dates and counts in `verification.md`.
- No task changes `go.mod`, adds a setting, adds a reconnect path or reopens decisions D1–D18.
