# Tasks: Reusable Session After a Proven Quiet Stop

**Input**: Design documents in `specs/004-shutdown-drain/`: spec.md, plan.md, research.md (R1–R10), data-model.md, contracts/quiet-stop.md, contracts/cli.md and quickstart.md.

**Prerequisites**: A passing owner-judge **design** review of the committed feature directory (the hook refuses `/speckit-implement` without it). The constitution is 2.0.0. `docs/specification.md` §8.11 must carry the 2026-11-07 decision (T002).

**Tests**: Required (constitution VI, spec abuse cases, SC-001 to SC-007). Write each test before the code it covers and observe it fail for the right reason. Use channels, barriers and `testing/synctest` for owner timing, not sleeps. Keep the simulator, SQLite and OS fixtures outside synctest bubbles. Simulator suites that measure the pass rate always use the production `runtime.Stack` observation (contract "Test seams"). Automated tests never touch a real account.

**Organization**: Setup, a foundational phase that builds the pieces and the measurement spike (it ends at an owner gate), then US1, US2, US3 in spec priority order, then polish and evidence. No new setting, flag, dependency, fork or `replace`. Stage 2 (405 budget, startup retries, ordinary reconnect, other 515s, same-process re-pair) is out of scope and stays denied.

**Decisions already made (do not reopen without the user)**: option A, no fork; stage 1 only; unresolved pairing keeps the marker unsafe; a failed check after a linked `pair` exits 0 with the recovery notice and a storage fault still exits 1; pass-rate bar of 90% per kind; the two deadlines are measured, then approved by the owner (T013). No commit carries attribution lines other than the session trailer the environment requires.

## Format: `[ID] [P?] [Story] Description`

- Every task has a checkbox, a sequential ID and repository-relative file paths.
- `[P]` means different files and no dependency on an unfinished task.
- `[US1]` to `[US3]` mark story tasks. Setup, foundational and polish tasks have no label.
- Constraints quoted from data-model.md and the contracts are binding.

## Phase 1: Setup

**Purpose**: Confirm the gates and prepare the evidence file. No production code.

- [ ] T001 Recheck E003-RX-01 in `specs/003-whatsapp-pairing-status/verification.md` against the real UTC date (expiry at the start of 2026-12-02 UTC), the pinned `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`, and the scope (private, local, single owner). Record owner, pin, scope, expiry, "no trigger fired" and "reusable sessions make long connected runs possible" in a new "E003-RX-01 recheck (implementation start)" section of `specs/004-shutdown-drain/verification.md` (research R10). Stop for an owner decision if it has expired or a trigger fired. Do not infer real-account permission.
- [ ] T002 Confirm `docs/specification.md` §8.11 carries the 2026-11-07 decision that supersedes the 2026-11-04 fail-closed limitation and the same-generation form of the 2026-11-06 post-pair approval (FR-013). If `docs/` is missing or lacks the entry, stop and ask the user for it; do not write the decision yourself. Record the check in `specs/004-shutdown-drain/verification.md`.
- [ ] T003 Create `specs/004-shutdown-drain/verification.md` with these sections: Handoff (branch, commands, environment notes), E003-RX-01 recheck, Measurement and deadline approval, Simulator results, Pass-rate run, Platform evidence, Real-account journey, Not verified, and the "Owner reviews" table with the header row from `AGENTS.md`. Leave the review rows empty; the owner-judge agent adds them.

---

## Phase 2: Foundational — pieces and measurement spike (test-only wiring)

**Purpose**: Build the network fence, the refusal counts, the dropped-event count and the observation, measure them on the simulator, and stop at the owner's deadline approval. Nothing in this phase clears the marker. Depends on T001–T003.

**⚠️ CRITICAL**: No user story may wire `QuietStop` into the marker before T013 is ticked.

- [ ] T004 [P] Write `internal/store/device_refused_test.go`: for every site that returns `ErrRetired` after `Retire` (admission in `internal/store/device.go`, `Save`, the deferred `Delete`, `DoDecryptionTxn`, the nested paths, and the repair paths in `internal/store/repair.go`), `DeviceBarrier.Refused()` rises by exactly one per refused call. It is zero before retirement, it is monotonic, and calls refused before `Retire` returns are counted too. Run it and observe it fail to compile (no `Refused` yet).
- [ ] T005 [P] Write `internal/wa/fence_test.go` against a local `net.Listener`: dials before `Close` succeed and are wrapped; `Close` closes every wrapped connection; later dials return an error and add to `refusedDials`; later writes on a wrapped connection return an error, send nothing (the listener reads zero bytes) and add to `refusedWrites`; reads failing because the fence closed the connection are not counted. Two fences are independent (closing one leaves the other's connections working). Cover the websocket, pre-login and media HTTP clients built from the same fenced transport.
- [ ] T006 [P] Write `internal/wa/quiet_test.go` with a fake dump source and fake clock. Cases (each asserts `Passed`, `Reason` and the counts of data-model `QuietStopResult`):
  - zero library goroutines on two observations at least `quietObservationGap` (100 ms) apart passes;
  - one library goroutine at the deadline fails with `work_running`;
  - a goroutine with a `go.mau.fi/whatsmeow`, `go.mau.fi/libsignal` or `github.com/coder/websocket` frame, or a `created by` line in those packages, is counted;
  - a goroutine in `go.mau.fi/util` only is not counted;
  - a dump that fills the 8 MiB `stackBufferMax` fails with `observation_incomplete`;
  - a dump source error fails with `observation_error`;
  - a clean first observation followed by a dirty second one fails;
  - the checking goroutine is excluded;
  - an allow-listed exact function name is ignored but a different function in the same package is not;
  - the reason precedence is `observation_incomplete`, `observation_error`, `storage_refused`, `network_refused`, `event_dropped`, `work_running`, and the counts report all of them;
  - the result struct and every error string contain no dump text, function names, argument words or keys.
- [ ] T007 [P] Write the allow-list pin test in `internal/wa/quiet_test.go`: the list starts empty; every entry names one exact function (no entry may end in `*`, `/`, `.` or name a package without a function); every entry's `ReviewedFor` equals the `go.mau.fi/whatsmeow` version in `go.mod` (read `go.mod` in the test); the test fails when they differ (FR-010, invariant I7).
- [ ] T008 Add `Refused() uint64` to `DeviceBarrier` in `internal/store/repair.go` (beside `StorageFault`) and an atomic refusal counter on the device session in `internal/store/device.go`, incremented at every `return ErrRetired` site listed in T004 (`device.go` lines near 250, 455, 462 and 552; `repair.go` near 128 and 228) and at any other site `go vet`-clean search finds. The count is in memory only; no schema change. Make T004 pass.
- [ ] T009 Create `internal/wa/fence.go`: a per-generation `fence` that owns a `net.Dialer`, wraps each dialled `net.Conn` so `Write` is refused and counted after `Close`, tracks its connections, and exposes `Close()`, `RefusedDials()`, `RefusedWrites()` and `HTTPClient(base *http.Client) *http.Client` returning a clone whose transport dials through the fence (wrap a test-injected transport instead of replacing it). Make T005 pass.
- [ ] T010 In `internal/wa/session.go`, build the three fenced HTTP clients for every generation from the library defaults (or the injected `SessionConfig.PreLoginHTTP` and `WebsocketHTTP`) and install them with `SetWebsocketHTTPClient`, `SetPreLoginHTTPClient` and `SetMediaHTTPClient` (research R3). Keep the event handler registered after the retire bit is set, add an atomic `droppedEvents` counter that counts every event dropped after retirement except the `Disconnected` caused by the session's own `Disconnect`, and change `finishRetire` so the handler is removed only when the check ends (research R4). Write the tests first in `internal/wa/session_test.go`: a late `LoggedOut` after retirement counts as dropped; the self-caused `Disconnected` does not.
- [ ] T011 Create `internal/wa/quiet.go`: `QuietStopResult` (fields and reasons exactly as in data-model.md), the exact-function allow-list (empty, with `Function`, `ReviewedFor`, `Evidence`), constants `quietObservationGap = 100 * time.Millisecond` and `stackBufferMax = 8 << 20`, and the bounded observation: `runtime.Stack(buf, true)` into a buffer that starts at 1 MiB and doubles up to 8 MiB, a full 8 MiB buffer is incomplete, parse in memory, count goroutines with a frame or `created by` line in `go.mau.fi/whatsmeow`, `go.mau.fi/libsignal` or `github.com/coder/websocket` (and their subpackages), exclude the caller, remove allow-listed work, poll until two consecutive zero observations at least 100 ms apart or the deadline. The dump never leaves this file (invariant I5). Make T006 and T007 pass.
- [ ] T012 Add `func (s *Session) QuietStop(ctx context.Context, deadline time.Duration) QuietStopResult` to `internal/wa/session.go` per `contracts/quiet-stop.md`: callable only after `Retire` returned nil; a session that never attempted Connect returns `Reason: not_checked, Passed: true`; it combines the observation, `fence` refusals, `DeviceBarrier.Refused()` and `droppedEvents` into one result; it removes the event handler when done. Add `accountWork` (set before `ConnectContext`) to the session or generation if it is not already tracked. Test in `internal/wa/session_test.go` that each refusal source fails the result with its reason. Do not call it from the owner yet.
- [ ] T013 Write the test-only measurement `TestQuietStopMeasurement` in `internal/wa/quiet_measure_test.go` (build tag or `WA_MEASURE=1` guard so it does not run in the normal suite). Using `internal/wasim`, run at least 50 orderly stops of each kind: `pair` with the post-pair reconnect (the pairing generation before the fresh one, using the same-client path that still exists), `stdio` connected and `serve` connected. It runs the real retire order of the contract (Disconnect, fence, store barrier, observation) without touching the marker. For each kind it prints p50, p95 and max time until library work disappears, each failure category, and the pass rate under candidate deadlines (for example 250 ms, 500 ms, 1 s, 2 s, 5 s). Run it as in quickstart §1 (`CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopMeasurement -count=1 -v -timeout 30m`), once natively on Windows and once under WSL2, and paste the tables into `specs/004-shutdown-drain/verification.md` under "Measurement and deadline approval" with commands, dates and exit codes read without a pipe.
- [ ] T014 **OWNER GATE.** The owner reads the T013 tables and approves `quietStopDeadline` and `postPairQuietDeadline` in `specs/004-shutdown-drain/verification.md` (value, date, evidence). If any kind passes under 90%, the owner first decides whether to keep option A, tune it (including a reviewed allow-list entry with evidence) or move to the fork (SC-005); record the decision. Then add the two approved constants to `internal/wa/quiet.go`. Only the owner ticks this task. **Blocks T015 and every later production use of `QuietStop`.**

**Checkpoint**: The fence, the counts and the observation exist, are tested and are measured. The marker is untouched.

---

## Phase 3: User Story 1 — Restart without offline recovery after a clean stop (Priority: P1) 🎯 MVP

**Goal**: A normal stop of `stdio` or `serve` clears the unsafe-run marker when every connected generation passed the quiet-stop check, and the next start reuses the saved session. Every doubt keeps the marker unsafe.

**Independent Test**: On the simulator, connect a saved session, stop normally and restart: the second run connects with no recovery and no QR. Repeat with leftover work, a refused network call, a refused storage call, a dropped event, a cut-off observation, a held handler, the late-logout ordering, a crash and a forced exit; each keeps the marker unsafe.

### Tests for User Story 1 (write first, observe failing)

- [ ] T015 [P] [US1] Write `internal/wa/quiet_order_test.go` for the retirement order invariant (SC-004, SEC-002, invariant I1/I2): with a recording fence and barrier, assert the sequence Disconnect, fence close, store barrier retire, observe, event handler removal; assert the store is never blocked while the fence is open; make the test fail when the fence and barrier steps are swapped (include a negative variant that swaps them through a test seam and expects the order check to report a violation). Also assert that a message being decrypted when retirement starts completes its guarded writes (the network cannot deliver more input before storage closes).
- [ ] T016 [P] [US1] Write `internal/wa/owner_quiet_test.go` (with `testing/synctest`, fake observation): `finishStop` clears the marker after a passed check for a generation that attempted Connect; a failed check of any reason keeps it unsafe, logs `quiet_stop_failed` with the fields of `contracts/cli.md` and `run_guard_recovery_required`; a generation that never attempted Connect is not checked and today's safe-clear rules apply unchanged (scenario 7); a storage fault, an unresolved pairing (QR final timeout, cancellation, pairing success without a confirmed follow-up connection), a latched logout or client deletion without durable cleanup, and a refused low-disk clear each keep the marker unsafe even when the check passed; a latched logout with durable cleanup and a passed check clears; a passing check never overrides a logout; `upstream_drain_unproven` is never emitted; two generations in one run keep the marker unsafe if either failed (invariant I4); the check never lengthens the 10-second stop bound for writes (FR-011) and a failed check is not a failed shutdown (orderly close, lock released last).
- [ ] T017 [P] [US1] Write `internal/wa/wasim_quiet_test.go` (real pinned client, one simulator at a time, outside synctest bubbles; `quiet_wasim_test.go` in the plan's list): a connected `stdio`-shaped run and a `serve`-shaped run stop and the marker is clear; reopening connects with zero QR codes, zero recoveries and zero registrations (SC-001); a held pinned message handler across retirement fails the check until it returns and passes only if it made no call after the blocks (scenario 9); the late-logout ordering of `TestRunGuardLatePinnedLogoutAfterRetirement` (`internal/wa/run_guard_test.go`) fails the check and commits no clean marker while that work is pending (scenario 8); an injected late send after the fence is refused, counted and fails the check; an injected late store call after the barrier is refused, counted, changes no row and fails the check; a fake over-large dump fails; a crash (process kill helper), a forced exit and a stop beyond its bound leave the marker unsafe (scenario 6); a stop signal during the check keeps the drain order.
- [ ] T018 [P] [US1] Write the command-level tests in `cmd/whatsapp-mcp/run_guard_test.go` (rewrite the cases that expect unsafe after every production Connect; keep the unsafe-marker-at-start and legacy-marker refusal cases): `stdio` and `serve` exit 0 with a clean marker after a passed check and the next start connects; after a failed check they exit 1 with `run_guard_recovery_required` and the reason; `status` after a passed stop is identical to `status` after a run with no production connection (FR-012); a run without a saved identity is unchanged.
- [ ] T019 [US1] Run T015–T018 and record the failing observations (which assertion failed and why) in the Handoff section of `specs/004-shutdown-drain/verification.md`. A test that passes before the code exists is wrong; fix the test first.

### Implementation for User Story 1

- [ ] T020 [US1] In `internal/wa/session.go` make the `Retire` order match contract steps 5–7: `Disconnect()`, then close the fence, then retire the store barrier, each in that order and only that order (the existing `Retire` currently disconnects and retires the barrier; insert the fence close between them). Keep the existing context and 10-second stop bound. Make T015 pass.
- [ ] T021 [US1] In `internal/wa/owner.go` change `retireGeneration(gen *genState)` to `retireGeneration(gen *genState, deadline time.Duration)`: after `Retire` returns nil, call `Session.QuietStop(ctx, deadline)` before `FinishRevocation`; on a failed result set `o.unsafeStop`, log `quiet_stop_failed` (error; fields `epoch`, `reason`, `library_goroutines`, `network_refused`, `storage_refused`, `events_dropped`, `duration_ms`); on a pass log `quiet_stop_passed` (info; `epoch`, `duration_ms`). Skip the check for a generation with `accountWork == false`. The result lives in the generation (`quiet`, written once). Never log dump text, function names, keys, QR material or account identifiers.
- [ ] T022 [US1] In `internal/wa/owner.go` `finishStop` (near line 476): pass `quietStopDeadline` to `retireGeneration`; remove the `UpstreamDrainUnproven` branch (near line 544) and its `upstream_drain_unproven` event; keep every other clear condition (`unresolved`, `unsafeStop`, `health.StorageFault`, latched-logout cleanup, `ClearRunGuard` low-disk admission) exactly as in the data-model "Run outcome". Remove `UpstreamDrainUnproven()` from `Session` (`internal/wa/session.go` near line 170) and its interface assertion, and delete or rewrite `internal/wa/session_reconnect_test.go` cases that asserted it. Make T016, T017 and T018 pass.
- [ ] T023 [US1] In `cmd/whatsapp-mcp/commands.go` and `lifecycle.go` keep the exit codes and messages of `contracts/cli.md` for `stdio` and `serve` (exit 0 after a passed check, exit 1 with `run_guard_recovery_required` after a failed one); stdout in stdio mode stays protocol-only. No new flag or setting.
- [ ] T024 [US1] Run the US1 suites with `-count=3` as in quickstart §2 (`CGO_ENABLED=0 go test ./internal/wa ./internal/store -run 'Quiet|Fence|Refused|RunGuard|Logout' -count=3 -timeout 20m`) and `CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 20m`; record commands, dates, counts and exit codes (read directly, no pipe) in `specs/004-shutdown-drain/verification.md` "Simulator results".

**Checkpoint**: US1 works on its own. `stdio` and `serve` clear the marker after a proven quiet stop. `pair` still uses the same-client post-pair reconnect behind its switch.

---

## Phase 4: User Story 2 — Pair once and keep the device (Priority: P1)

**Goal**: The connection WhatsApp requests right after pairing runs on a fresh client and generation, only after the pairing generation passed the check. Late traffic from the pairing session cannot reach it. `PostPairReconnect` is removed.

**Independent Test**: On the simulator, pair, receive the restart request, and confirm that late sends from the pairing generation never reach the fresh connection, that the fresh connection uses a new client and generation with the saved identity and the selected version, and that a clean stop leaves the marker clear for the next `stdio` start.

### Tests for User Story 2 (write first, observe failing)

- [ ] T025 [P] [US2] Rewrite `internal/wa/owner_postpair_test.go` and `internal/wa/owner_postpair_recheck_test.go` for the fresh-client path (remove `ot.o.cfg.PostPairReconnect = true`; the path is part of `pair`). Cases:
  - the 515 after `PairSuccess` retires the pairing generation with `postPairQuietDeadline`, then opens exactly one new generation;
  - a 515 before `PairSuccess` waits within the 30-second watchdog and is refused on `PairError`, logout, stop or the watchdog;
  - duplicate 515s share one attempt;
  - a failed check starts no connection and takes the incomplete-link path (final, unsafe);
  - a failed fresh connection, a stall past 30 s and a cancellation are final with no second attempt;
  - the fresh generation exists only after the previous check passed (I3);
  - the attempt slot is held across retire, load, connect and teardown drain;
  - the recheck-after-disconnect, stop-during-disconnect and recheck-after-slot cases keep their meaning.
- [ ] T026 [P] [US2] Extend `internal/wa/wasim_owner_test.go` (`TestOwnerPostPairReconnectReachesConnectedAgainstSimulator`, `TestOwnerDeniesPostPairReconnectByDefaultAgainstSimulator`, `TestOwnerPostPairReconnectWaitsForHeldSave`) and add simulator cases: the fresh client logs in with the saved-identity payload (no registration) and the currently selected WhatsApp Web version and Linux/Chrome identification, asserted on the wasim handshake (FR-004); injected late sends from the pairing generation after its fence (a session announcement, a receipt, a key share, a resent request, a disconnect) never reach the fresh connection, are refused and counted for the pairing generation (scenario 7, SC-003); a late `Disconnect()` from the pairing error tail or the QR emitter reaches only the retired client and the fresh connection stays up; the pairing window's QR context ends before the pairing generation is retired; a stop signal during the post-pair handover keeps the drain order.
- [ ] T027 [P] [US2] Update `cmd/whatsapp-mcp/pair_test.go`, `pair_retry_test.go` and `run_guard_test.go` (replace `TestProductionOwnerEnablesPostPairReconnectOnlyForPair`, which tested the switch, with a test that `stdio` and `serve` have no reconnect and no pairing path): `pair` exit codes and stderr per `contracts/cli.md` — linked and passed exits 0 with `whatsapp-mcp: pair: linked. The saved session can be reused.` and a clean marker; linked but the stop failed its check exits 0 with the existing recovery notice plus the reason category and an unsafe marker; the pairing generation failing its check before the fresh connection exits 1 with the existing "linking could not be completed safely" line plus the category; a failed, stalled or cancelled fresh connection exits 1; QR final timeout, cancellation, Enter or EOF exit 1 and stay unsafe; a clean saved identity connects once and stops with `connected. The saved session can be reused.`; a storage fault or any other stop error exits 1.
- [ ] T028 [US2] Run T025–T027 and record the failing observations in `specs/004-shutdown-drain/verification.md` Handoff.

### Implementation for User Story 2

- [ ] T029 [US2] In `internal/wa/pairing.go` add an explicit way to end the pairing window of a generation (cancel the QR channel context so the emitter goroutine in whatsmeow `qrchan.go` can return) and call it from retirement step 3 of `contracts/quiet-stop.md`. Keep the window's existing diagnostics and unresolved-pairing accounting.
- [ ] T030 [US2] In `internal/wa/owner.go` rewrite `runPostPairReconnect` (near line 861) and `tryManualReconnect` (near line 785) per `contracts/quiet-stop.md` "Owner use": on a 515 after `PairSuccess`, or `PairSuccess` releasing a pending 515 (`releasePending515`), take the attempt slot, run `retireGeneration(pairGen, postPairQuietDeadline)`, take the incomplete-link path if that check failed, otherwise `openGeneration` a new client (`LoadDevice` from the durably saved identity, current selected version, Linux/Chrome properties, no registration), run exactly one `attemptPostPair` under the 30-second watchdog, and release the slot after that attempt and its teardown drain. Any failure, stall, cancellation or failed check is final. Keep the 515 waiting rules and duplicate-request sharing. Make T025 and T026 pass.
- [ ] T031 [US2] Remove `OwnerConfig.PostPairReconnect` (`internal/wa/owner.go` near line 46) and every use: the `!o.cfg.PostPairReconnect` early return near line 791, `cmd/whatsapp-mcp/commands.go` near line 184 (`PostPairReconnect: !deps.shell`), `internal/wa/testdata/cmd/session-check/main.go` near line 97 and its `postPair` environment field, and the test rigs (`internal/wa/wasim_owner_test.go` near line 50). Keep `denyReconnect` for every other reconnect path and its existing `reconnect_denied` diagnostic (FR-006). Make T027 pass.
- [ ] T032 [US2] Run the US2 suites (quickstart §2 with `PostPair`) at `-count=3` and `CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 20m`; record results in `specs/004-shutdown-drain/verification.md` "Simulator results". Confirm with `grep -rn PostPairReconnect --include=*.go .` (expect no matches) and record it.

**Checkpoint**: `pair` links on a fresh client, and US1 and US2 both work. The switch is gone.

---

## Phase 5: User Story 3 — Know why the session cannot be reused (Priority: P2)

**Goal**: A failed check is reported in plain words with one category and counts; a passed one says the session can be reused. No diagnostic leaks observation data.

**Independent Test**: Force each failure category on the simulator and read the stop diagnostic and the `status` output. No diagnostic at any log level contains library state, arguments, keys or account identifiers.

### Tests for User Story 3 (write first, observe failing)

- [ ] T033 [P] [US3] Write `cmd/whatsapp-mcp/quiet_output_test.go`: for each reason (`work_running`, `network_refused`, `storage_refused`, `event_dropped`, `observation_incomplete`, `observation_error`) a forced failure prints exactly one stderr line naming that category, the counts, and "offline recovery is required" in operator wording; a pass prints that the session can be reused; stdout in stdio mode stays protocol-only; `status` afterwards reports the marker state as today and stores nothing new (scenarios 1–3).
- [ ] T034 [P] [US3] Write the secrecy tests in `internal/wa/quiet_secrecy_test.go` (SC-006, SEC-004): capture logs at debug, info, warn and error through the production logger bridge while forcing each failure and a pass, plus a held-handler case and an over-large dump; assert zero occurrences of dump text, `goroutine ` headers, function names from the dump, argument words, key bytes, QR material or full account identifiers (use synthetic markers planted in goroutine names and device data). Also assert the result value and every returned error contain none of them.

### Implementation for User Story 3

- [ ] T035 [US3] In `cmd/whatsapp-mcp/commands.go` (and `lifecycle.go` if the stop messages live there) add the human lines of `contracts/cli.md`: `reusable` after a pass and `offline recovery required (<category>)` after a failure, for `pair`, `stdio` and `serve`; the `pair` lines are exactly those in the contract table. Wire the reason from `QuietStopResult` through the owner's `StopError`/health value without ever carrying dump text. Make T033 and T034 pass.
- [ ] T036 [US3] Add the pass-rate recorder to `internal/wa/quiet_measure_test.go` (reuse the T013 harness with the approved deadlines and the production `retireGeneration` path): at least 50 simulated orderly stops each of `pair` with post-pair reconnect, `stdio` and `serve`; print and record the pass rate per kind and the failure categories (SC-005). The bar is 90% for `stdio` and `serve` and 90% for the pairing generation check; below either, record the numbers and ask the owner for the decision before the code review. Paste the result in `specs/004-shutdown-drain/verification.md` "Pass-rate run".
- [ ] T037 [US3] Run the output and secrecy suites and record commands, dates, counts and exit codes in `specs/004-shutdown-drain/verification.md`.

**Checkpoint**: All three stories work and are explained to the operator.

---

## Phase 6: Polish and Cross-Cutting Concerns

**Purpose**: Documentation, full gates, platform evidence and the permitted real-account journey. No pull request until the code review passes.

- [ ] T038 [P] Update `OPERATIONS.md`: a reusable session after a passed quiet-stop check, offline recovery still required after a failed one or an unresolved pairing, the new stop wording and the `quiet_stop_passed` and `quiet_stop_failed` events, `upstream_drain_unproven` gone, and the split between implemented and verified. Update `AGENTS.md` "Current state" to describe feature 004 as built (do not call it merged until it is) and the removal of `PostPairReconnect`. Keep both files LF.
- [ ] T039 [P] Check the doc comments in `internal/wa/owner.go`, `session.go`, `fence.go` and `quiet.go` for stale text about `PostPairReconnect`, "upstream drain unproven" and the same-client reconnect; fix them. Do not cite FR-, SC-, US, research or finding IDs in code comments (constitution, user rule).
- [ ] T040 Run the full gates of quickstart §4 and record each exit code read directly: `gofmt -l .`, `go vet ./...`, `go mod verify`, `CGO_ENABLED=0 go test ./... -count=1 -timeout 30m`, the five cross-builds, and `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`. Confirm `go.mod` and `go.sum` are unchanged (SEC-007) with `git diff main -- go.mod go.sum`. Keep `TEMP` and `TMP` on `D:\tmp`; C: is nearly full.
- [ ] T041 Run the whole suite under WSL2 as in `AGENTS.md` (`wsl -d Ubuntu --cd /mnt/d/projects-mou/whatsapp-mcp-next --exec env CGO_ENABLED=0 go test ./... -count=1 -timeout 10m`) and record it as WSL2, not native Linux. Push the branch for CI only if the user asks, and record the CI result per OS when it exists; anything not run is "not verified".
- [ ] T042 Check LF endings with `git ls-files --eol` and confirm no tracked file shows `w/crlf` after the scripted and tool edits.
- [ ] T043 **Permitted real-account journey (SC-007), only with the owner's explicit permission for each step** (quickstart §5): `pair` into a new empty directory (for example `D:\tmp\wa-004a`) with `--log-level debug`; a normal stop; restart with the saved session through `stdio` or the `session-check -action connect` helper without recovery; the owner's cleanup (unlink on the phone, then offline recovery or delete). Record redacted results: each check's pass or fail, timings between `PairSuccess`, the 515 and the fresh connection, exit codes and the phone's linked-device entry. A step not performed is recorded as not verified. If the fresh-client reconnect does not complete the link, stop and ask the owner for the decision named in the spec's Assumptions (fix, keep the same-client reconnect with the marker unsafe, or stop the feature). Do not act on a real account without that permission.
- [ ] T044 Roll up `specs/004-shutdown-drain/verification.md`: the E003-RX-01 recheck at code review (expiry still ahead, pin unchanged), the measured pass rates, the approved deadlines, platforms covered, accepted risks and every "Not verified" item (another account or phone, native Linux or macOS runs, power loss). Then commit the work and the evidence and run the owner-judge agent in **code** mode; add its row to the "Owner reviews" table and commit it.

---

## Dependencies and Execution Order

- **Phase 1** (T001–T003) first. T001 and T002 stop the feature if the exception expired or §8.11 lacks the decision.
- **Phase 2** depends on Phase 1. Tests T004–T007 are parallel (different files); T008 follows T004; T009 follows T005; T010 follows T009; T011 follows T006 and T007; T012 follows T008, T010 and T011; T013 follows T012; **T014 is the owner gate and blocks everything that calls `QuietStop` from the owner**.
- **US1** (T015–T024) depends on T014. US1 is the MVP.
- **US2** (T025–T032) depends on US1's T021 and T022 (it reuses `retireGeneration` with a deadline) and on T014. T029 can start after T014 in parallel with US1 implementation only if the files differ (`pairing.go`); the rest follow US1.
- **US3** (T033–T037) depends on US1 and US2 for the messages; T034 can be written after T012.
- **Polish** (T038–T044) depends on all stories. T043 needs the owner's permission and runs before T044.
- Within a story: tests, then the failing-observation task, then implementation.

### Parallel opportunities

- Phase 2 tests: T004, T005, T006 and T007 together.
- US1 tests: T015, T016, T017 and T018 together (distinct files).
- US2 tests: T025, T026 and T027 together.
- US3 tests: T033 and T034 together.
- Polish: T038 and T039 together.

### Parallel example (Phase 2 tests)

```text
Task: "Write internal/store/device_refused_test.go (T004)"
Task: "Write internal/wa/fence_test.go (T005)"
Task: "Write internal/wa/quiet_test.go cases (T006) and the pin test (T007)"
```

## Implementation Strategy

1. **MVP = Phase 1 + Phase 2 + US1.** After T014 the owner can see a stopped `stdio` or `serve` session reused, which is what unblocks message ingest. `pair` keeps its current same-client reconnect until US2.
2. Add US2 to remove the switch and move the post-pair reconnect onto a fresh client.
3. Add US3 for the operator wording and the pass-rate record.
4. Finish with documentation, the full gates, the optional real-account journey and the code review.
5. If a measured pass rate is under 90%, stop at the owner decision (keep, tune, or fork) before the code review; do not widen the allow-list on your own.

## Notes

- Total tasks: 44 (Setup 3, Foundational 11, US1 10, US2 8, US3 5, Polish 7).
- Keep each commit to one logical change; Conventional Commits that explain why; no attribution other than the session trailer the environment requires.
- Tick tasks `[X]` as they are done and record verification results with commands, dates and counts in `verification.md`.
- No task changes `go.mod`, adds a setting, adds a reconnect path or reopens decisions D1–D18.
