# Verification: Reusable Session After a Proven Quiet Stop

Evidence for feature 004. Task T003 adds the other sections (handoff, E003-RX-01 decision, measurement and deadline
approval, simulator results, pass-rate run, platform evidence, real-account journey, not verified).

## E003-RX-01 (implementation start)

Facts collected 2026-11-07T21:53Z from `specs/003-whatsapp-pairing-status/verification.md`:

- Exception: E003-RX-01, F09 limited to upstream receive expansion; approved by `tiger-seo` on 2026-11-02 for
  feature 003's private, local, single-owner deployment.
- Expiry: the start of 2026-12-02 UTC, no automatic renewal. Not expired on 2026-11-07.
- Pin: `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b` in `go.mod`; it matches the exception.
- Incidents: none recorded in the available project evidence.
- This feature makes long connected `stdio` and `serve` runs possible for the first time (today every connect leaves
  the marker unsafe). That is the exception's own trigger ("before another feature materially expands the receive
  workload"), and constitution X allows no inheritance by another feature.

**Owner decision, 2026-11-07 (`tiger-seo`):** E003-RX-01 is extended to feature 004 as proposed:

1. Scope: feature 004 only, in the same private, local, single-owner deployment. Message ingest (005), a listener or
   any shared deployment needs a new decision.
2. Same pin and same expiry (start of 2026-12-02 UTC, no renewal). Feature 004's code review must happen before then.
3. Earlier trigger: a resource incident (memory or goroutine growth in a long run) or a pin change stops continued
   operation until the owner decides again.
4. Compensating control: the exception's long-run monitoring is exercised during this feature's verification, with a
   short soak run run as T046 and recorded in the roll-up (T047). The simulator cannot show real receive volume; that stays "not
   verified".
5. Not covered: stop and drain ordering. It is what this feature implements, so it is its own acceptance gate.

Editorial note, 2026-11-08: item 4 originally read "a short soak run recorded in the roll-up (T046)". After the
design reviews the soak run became its own task T046 (4 hours, simulator) and the roll-up became T047, so the
reference was renumbered. The substance of the decision is unchanged.

This is a feature-level extension recorded by the owner. It does not close F09 and does not amend the constitution.

## Handoff

### Design review round 6 (PASS at ea9234b): notes for implementation

Low findings and observations the reviewer left open. They did not block, and the feature documents were not edited
after the PASS so that the verdict stays current. Take them into account when implementing:

1. Contract "Three contexts", the bullet on the work context still says `gen.work` covers "the abort of a Connect that
   has not returned". Follow step 4, the pairing paragraph and the data-model row instead: `gen.work` never reaches a
   Connect context. Fix the bullet when the contract is next touched.
2. Window (b): recognise "aborted through the recorded abort" with `context.WithCancelCause` and a sentinel cause
   (`errors.Is(context.Cause(ctx), errAttemptAborted)`), never with `ctx.Err() != nil` or a sticky flag, which would
   hide a Connect after step 9. Add a negative T013 (b) variant: a Connect after step 9 is counted.
3. On the post-pair path `accountWork` is set early in the owner's critical section, so a stop in window (b) on the
   fresh generation is still checked (conservative). State one rule for both paths or limit the "not checked"
   assertion to the startup and pairing Connect.
4. Window (a) is detectable (the owner finds the abort already called when it clears it). Consider failing the
   generation's check deterministically whenever it fires.
5. The QR emitter calls `cli.Disconnect()` itself when its context ends (`qrchan.go` near 125-131). T013's stop-in-open-
   pairing-window case and T014 should expect it.
6. T013's pairing-generation write case is synthetic. Add the realistic variant (`handlePair`'s `Store.Save(ctx)` and
   `sendNode(ctx)`, `pair.go` near 226 and 243) during an open window.
7. A third interleaving, the abort called before the retire bit is set when `Session.Connect` runs, fails closed (the
   generation is checked). Mention it next to (a) and (b).
8. Keep the lock order one way: the abort helper calls `MarkRetiring()` (barrier locks) while holding `o.mu`; no path
   may take `o.mu` while holding a barrier lock.
9. The 4bfd39e CONCERNS row has an empty decision column; later rows of the same stage supersede it.

## Spec check (T002)

2026-11-08: `docs/specification.md` §8.11 carries the 2026-11-07 entry "quiet-stop proof and fresh-client post-pair
reconnect (feature 004)". It supersedes the 2026-11-04 fail-closed limitation and the same-generation form of the
2026-11-06 post-pair approval. Checked by reading the section; nothing was written to `docs/`.

## Handoff (progress)

T013 evidence (2026-11-08): `quiet_order_test.go` covers the retirement order against the real adapter (trace of
work cancel, disconnect, fence, barrier, library cancel) with a checker that rejects swapped steps; the stop signal
leaving the library context alone; a failed startup attempt and a watchdog after Connect returned leaving the handler
context alone; the synchronous abort of an unreturned Connect with the refusal count started first; no Connect after
BeginStop; window (a) recorded (`afterConnect` seam); a handler holding a decrypt transaction when the stop arrives
finishing its app-state key write and the check passing; and the control (library context cancelled first: write fails,
counted, `storage_refused`). `wasim_held_test.go` covers a pairing Save held by the database lock when the stop
arrives (completes after release) and the forced drain path (bound exceeded, unsafe). Window (b) is covered at session
level (`session_quiet_test.go`). Not covered until T032 exists: the pairing generation's handover retirement at the 515.
Observed-failing-first was done for T004, T005, T006, T010 and T012 (compile failures); the T013 and T014 tests were
written after the implementation.

## Handoff (environment)

- Branch `004-shutdown-drain`. Go 1.27.3 on Windows 10, Git Bash.
- C: is nearly full: `GOCACHE`, `GOMODCACHE`, `GOTMPDIR`, `TEMP` and `TMP` point to `D:\tmp` for builds and tests.
- Commands: see `specs/004-shutdown-drain/quickstart.md`.

## E003-RX-01 recheck

Pending (T047).

## Measurement and deadline approval

T018, 2026-11-08. Command (exit code read directly, no pipe):
`WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopMeasurement -count=1 -v -timeout 30m`
Native Windows 10: exit 0, 93.5 s. WSL2 Ubuntu (Go 1.27.3, not native Linux): exit 0, 86.2 s. 50 orderly stops per kind
on `internal/wasim`, through the production owner and retirement order. Time is from the start of the observation
(library context cancelled) to the first dump with no client-library goroutine; `QuietStop(5 s)` was also run on each
real retired session.

| Platform | Kind | p50 | p95 | max | Never quiet | QuietStop(5 s) | Pass rate at 250 ms to 5 s |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Windows | pair (pairing generation at the 515) | 1.36 ms | 4.08 ms | 14.4 ms | 0 | 50 passed | 100% |
| Windows | stdio (saved identity, short hold) | 3.08 ms | 5.19 ms | 10.9 ms | 0 | 50 passed | 100% |
| Windows | serve (saved identity, longer hold) | 3.77 ms | 7.99 ms | 11.0 ms | 0 | 50 passed | 100% |
| WSL2 | pair | 2.07 ms | 7.51 ms | 13.0 ms | 0 | 50 passed | 100% |
| WSL2 | stdio | 2.46 ms | 5.59 ms | 11.6 ms | 0 | 50 passed | 100% |
| WSL2 | serve | 4.12 ms | 12.1 ms | 29.6 ms | 0 | 50 passed | 100% |

- Whole stop (BeginStop to Done), Windows p50 / max: pair 1.2 / 4.3 ms, stdio 0.6 / 2.5 ms, serve 0.8 / 3.6 ms.
- Simulator-server goroutines with a library frame at the end: 0 of 150 runs on each platform, so the numbers are the
  same with and without them. The measurement fails if the dump shows no library goroutine while connected.
- Window (a) (the recorded abort called after Connect returned): 0 of 150 on each platform. Caveat: the harness stops
  only after the connection is confirmed, so stops during the connecting phase are not sampled.
- Not measured: the final stop of the `pair` kind (needs the fresh-client path, T032); `stdio` and `serve` share one
  harness at owner level and differ only in how long they stay connected.
- Candidate-deadline pass rate counts the first quiet poll plus the 100 ms observation gap against the deadline.

Owner approval (T019), 2026-11-08, `tiger-seo`, on the proposal below:

- `quietStopDeadline` = 1 s and `postPairQuietDeadline` = 2 s (about 100 times the measured maximum of 29.6 ms).
- Total stop budget against the 10 s `stopBound`: typical stop 1 to 6 ms plus up to the 1 s observation. Worst case,
  Disconnect (up to 5 s) plus barrier drain (up to 10 s) can exceed the bound on their own; then the observation
  deadline is capped at the time left, a decision after the bound fails with `stop_bound_exceeded` and the marker stays
  unsafe. A passed decision inside the bound may still be followed by `shutdown timed out` and exit 1 with a clean
  marker, which is safe. Reserve for the steps after the decision (marker write with fsync, refresh wait, store close,
  lock release): whatever the bound has left, with the observation capped so a passed check normally leaves several seconds.
- Simulator-server goroutines: accept the measured number (0 of 150); no filtering in production code.
- No kind passed under 90%, so no keep, tune or fork decision arose.

## Simulator results

User story 1, 2026-11-08, Windows 10, Go 1.27.3, `CGO_ENABLED=0`, `TEMP` on `D:	mp`. Exit codes were read from the
test command itself, not through a pipe.

| Command | Result |
| --- | --- |
| `go test ./internal/wa -count=3 -run 'Simulator\|Handler\|Watchdog\|Window\|Retir\|Stop\|Quiet\|Fence\|RunGuard\|Logout\|Refused'` | exit 0, 86.7 s |
| `go test ./internal/store -run 'Refused\|RunGuard\|Retire\|Device' -count=3` | exit 0, 55.1 s |
| `go test ./internal/wa -count=1` (whole package) | exit 0, 40.9 s |
| `go test ./cmd/whatsapp-mcp -count=1` | exit 0, 145.4 s |

User story 2, 2026-11-08, same environment (fresh-client post-pair path; `PostPairReconnect` removed):

| Command | Result |
| --- | --- |
| `go test ./internal/wa ./internal/store -run 'PostPair\|Quiet\|Fence\|Refused\|RunGuard\|Logout' -count=3 -timeout 20m` | exit 0 (wa 21.7 s, store 16.6 s) |
| `go test ./cmd/whatsapp-mcp -count=1 -timeout 20m` | exit 0, 120.9 s |
| `go test ./... -count=1 -timeout 30m` | exit 0 (cmd 126 s, store 210 s, wa 32 s, wasim 4 s) |
| `go vet ./...` | exit 0 |
| `go vet` and `go test ./internal/wa/testdata/cmd/session-check` | exit 0 |
| `grep -rn PostPairReconnect --include=*.go .` | no matches (exit 1) |

Tests: `owner_postpair_test.go` and `owner_postpair_recheck_test.go` (one scripted session per generation, `testing/synctest`;
the handover, the 515 waiting rules, failed check, final failures, the slot held across retire and connect, the stop at
each of the four interleavings, the server's close during the handover); `wasim_owner_test.go` (fresh client with the
saved-identity payload and the selected version, late calls from the retired client through the real library and its
media client, server close right after the 515, a stop between creation and Connect dials nothing, link then stop clears
the marker). Changed expectations: `TestRetirementOrderOfAConnectedRun` now expects two generations in turn;
`TestSimulatorDeviceRemovedAfterConnected` expects a clean marker (the pairing was resolved by the fresh link, the
checks passed and the revocation cleanup finished); the "same-client post-pair link" unsafe case is gone.

Deviations to review:

- T028–T030 tests were written together with the code, not observed failing first (the owner change was made before the
  tests). They were checked against the behaviour they describe, and the interleaving tests fail if the stop check or
  the retire-before-open order is removed; this was not run as a mutation test.
- T030 was finished with US3 (see the User story 3 block); the lines below describe the state at the end of US2.
  category on the recovery lines) do not exist yet and belong to T035/T037. Only the switch test was replaced.
- T032 asks for `accountWork` to be set early in the owner's critical section. `accountWork` is set inside
  `Session.Connect`, and `attempt` already makes the stop check and records the abort in one critical section, so a
  stop in window (b) on the fresh generation finds an unchecked generation; the pairing is unresolved so the marker is
  unsafe anyway. No early set was added.
- A test seam `afterPostPairPublish` was added to `OwnerConfig` (tests only).

User story 3, 2026-11-08, same environment. The owner records the first failed check's category and whether a check
passed in `Health` (`QuietReason`, `QuietPassed`); the commands print it (`cmd/whatsapp-mcp/quiet_output.go`).

| Command | Result |
| --- | --- |
| `go test ./cmd/whatsapp-mcp -run 'ShellStop\|PairSavedSessionPassed\|PairLinked\|PairDeniedFollowUp' -count=1` | exit 0 |
| `go test ./internal/wa -run TestQuietCheckLeaksNothing -count=1 -v` | exit 0, 4 subtests (pass, work_running, over-large dump, dump error) |
| `go vet ./...` | exit 0 |
| `go test ./... -count=1 -timeout 30m` (second run) | exit 0 (cmd 137.7 s, store 218.2 s, wa 39.0 s) |
| `go test ./internal/wa -count=1` twice | exit 0 both |

Flake to review: the first `go test ./...` run after adding these tests failed seven simulator tests in `internal/wa`
(`no reconnect: context deadline exceeded`, `StopError = unsafe run`). The log showed `work_running` with one
`whatsmeow/socket.(*FrameSocket).readPump` goroutine blocked on `frameComplete` for about three minutes, left by an
earlier test in the same process. The quiet check counts library goroutines process-wide (research R6), so a leaked
goroutine from any earlier simulator test fails a later check. It did not reproduce in three further runs (two
package-only, one full), and the earlier full run of US2 passed. I did not find which test leaks. The pass-rate run
(T041) should count it; do not treat the package as flake-free.

Scope notes: the human lines are `stdio: offline recovery required (<category>)`, `stdio: stopped. The saved session
can be reused.` (same for `serve`), and for `pair` `linked.` or `connected. The saved session can be reused.`, the
recovery notice with the category, and the incomplete-link line with the category in parentheses. The secrecy test
covers the pass, `work_running`, an over-large dump and a dump error through the real session and owner, planting
markers in the dump; it does not cover a held handler or every reason (the other reasons come from counters, not from
text). `status` was not changed.

What the tests show (`owner_quiet_test.go`, `wasim_quiet_test.go`, `run_guard_test.go`, `store_test.go`):

- a passed check clears the marker and a failed one of any reason keeps it unsafe, with `quiet_stop_failed` and
  `run_guard_recovery_required`; a generation that never attempted Connect is not checked; a passed check does not
  override an unresolved pairing, a storage fault, a same-client post-pair link or an earlier failed generation;
- the check runs with the stop-bound deadline and the approved observation time; `upstream_drain_unproven` is gone;
- on the simulator a connected run stops with a clear marker and the next start reuses the saved identity (no
  registration, no QR window, connected again); a store call after the barrier, a media request after the fence, and a
  late Connect, QR or repair call during the check each keep the marker unsafe and cause no new dial; a remote
  disconnect and a receipt do not fail it; a late pinned logout during the check keeps it unsafe;
- the two `cmd` tests that expected an unsafe marker after every refused or stalled Connect now expect exit 0 and
  `quiet_stop_passed`, which is the intended change.

Defects found while running these suites: `TestFencesAreIndependent` (T005) leaked a connection that blocked its
listener cleanup and hung the package intermittently; fixed. One intermittent `StopError = unsafe run` in a
`-count=2` run of the two connected-stop simulator tests was seen once and did not recur in 25 repeats of those tests
or in the runs above; the cause is not established (a process-wide observation can see another test's leftover library
goroutines). Recorded as not verified.

Not covered by tests yet: a pinned handler held across retirement (scenario 9) beyond the held-Save and decrypt
transaction cases, and a process kill or forced exit (the store's existing kill tests cover the unsafe marker after a
crash). The `pair`-run interim rule is tested at owner level only.

## Pass-rate run

2026-11-08, Windows 10, Go 1.27.3, `CGO_ENABLED=0`, `TEMP` on `D:\tmp`. `WA_MEASURE=1 go test ./internal/wa -run
TestQuietStopPassRate -count=1 -v` exit 0 (`TestQuietStopPassRate` in `quiet_measure_test.go`). It runs the production
owner, store and adapter against the simulator and counts the owner's own decisions (`Health().QuietPassed` and
`QuietReason`) with the approved deadlines (1 s ordinary stop, 2 s pairing generation). `stdio` holds the connection
20 ms before the stop, `serve` 400 ms.

| Kind | Runs | Passed | Rate | Failures | Passed but marker not clean |
| --- | --- | --- | --- | --- | --- |
| pair: the pairing generation's check before the fresh connection | 50 | 50 | 100.0% | none | 0 |
| pair: the stop check after the fresh link | 50 | 50 | 100.0% | none | 0 |
| stdio: stop check | 50 | 50 | 100.0% | none | 0 |
| serve: stop check | 50 | 50 | 100.0% | none | 0 |

All are above the 90% bar. Limits: one machine, a quiet run in an otherwise idle process, simulator only. The same
quiet check failed seven simulator tests once in a parallel `go test ./...` run (see the User story 3 block: a goroutine
leaked by an earlier test), so the rate in a loaded or contaminated process is lower than this table. The old
`TestQuietStopMeasurement` pair kind was removed because it waited for the denied reconnect that no longer exists.

## Platform evidence

2026-11-08, exit codes read from the commands themselves.

| Gate | Result |
| --- | --- |
| `gofmt -l .` | no output, exit 0 |
| `go vet ./...` | exit 0 |
| `go mod verify` | all modules verified, exit 0 |
| `git diff main --stat -- go.mod go.sum` | empty: `go.mod` and `go.sum` unchanged |
| `CGO_ENABLED=0 go test ./... -count=1 -timeout 30m` (Windows, native) | exit 0: cmd 128.5 s, store 208.2 s, wa 33.5 s, wasim 3.0 s |
| `go build ./cmd/whatsapp-mcp` for windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 | five exit 0 |
| `go vet`, `go test` and `go build` of `./internal/wa/testdata/cmd/session-check` | three exit 0 |
| `govulncheck@v1.9.0 ./...` | "No vulnerabilities found", exit 0 |
| WSL2 Ubuntu, Go 1.27.3: `CGO_ENABLED=0 go test ./... -count=1 -timeout 10m` | exit 0: cmd 118.2 s, store 179.5 s, wa 33.5 s |
| WSL2: `go test ./internal/wa/testdata/cmd/session-check` | exit 0 |
| `git ls-files --eol` | no tracked file shows `w/crlf`; none of the modified or new files does either |

The WSL2 run is WSL2, not native Linux hardware. Not run: CI on Linux, Windows and macOS runners (no push was made),
native macOS, native Linux. These are not verified.

## Soak run

Pending (T046).

## Real-account journey

Performed with the owner's permission, step by step (T045). Binary built from `64ec8bc`
(`go build ./cmd/whatsapp-mcp`, windows/amd64). Windows 10, one account, one phone, new empty directory
`D:\tmp\wa-004a`. Account identifiers are not recorded here.

### Step 1: `pair --log-level debug` (permitted 2026-11-08, run by the owner at 02:33 UTC)

Command: `whatsapp-mcp pair --data-dir D:/tmp/wa-004a --log-level debug 2> D:/tmp/wa-004a-pair.log`. The owner scanned
the code on the phone. Result: exit status not read by the assistant (the owner ran it; the last stderr line is the
success line), final line `whatsapp-mcp: pair: linked. The saved session can be reused.`

| Moment (UTC, 2026-11-08) | Event | Since `PairSuccess` |
| --- | --- | --- |
| 02:33:51.957 | `pairing_requested` | |
| 02:34:10.323 | `PairSuccess` (`pairing_ended outcome=succeeded`) | 0 |
| 02:34:11.070 | server's 515 | 0.75 s |
| 02:34:11.339 | `quiet_stop_passed epoch=1`, `duration_ms=269` (pairing generation) | 1.02 s |
| 02:34:11.340 | fresh client dials | 1.02 s |
| 02:34:11.916 | authenticated with the saved identity (no registration) | 1.59 s |
| 02:34:14.604 | `connection_confirmed` (after the fresh client's prekey upload) | 4.28 s |
| 02:34:15.004 | `quiet_stop_passed epoch=2`, `duration_ms=398` (stop) | 4.68 s |
| 02:34:15.014 | `stopped` | 4.69 s |

Checks:

- two `quiet_stop_passed` events, no `quiet_stop_failed`, no `run_guard_recovery_required`, no `reconnect_denied`;
- one dial for the fresh connection, no second post-pair connection, no `version_refresh_failed`;
- `status --data-dir D:/tmp/wa-004a` (offline, run by the assistant, exit 0): `logged_in: true`, `connected: false`,
  `pairing_required: false`, no unsafe warning, so the marker is clean;
- the phone's linked-device entry: not yet reported by the owner (to be recorded).

Observation for review: the 515 arrived 0.75 s after `PairSuccess` (feature 003 saw about 0.6 s), and both checks passed
well inside their deadlines (269 ms and 398 ms against 2 s and 1 s). One run is one sample, not a pass rate.

### Step 2: restart with the saved session (permitted 2026-11-08, run by the assistant)

Binary from `64ec8bc`, `whatsapp-mcp stdio --data-dir D:/tmp/wa-004a --log-level debug`, stderr to a file.

- **First attempt, 02:37:23 UTC: not a valid run.** stdin was `/dev/null`, so the command stopped about 1 ms after
  start (`shutting down reason="stdin closed"`) before any dial. No connection was made, no check ran, exit 0. The
  account was not contacted.
- **Second attempt, 02:37:34 UTC, stdin held open 20 s: the quiet-stop check FAILED.** The client dialled at 02:37:35.078,
  authenticated with the saved identity at 02:37:35.722 and confirmed at 02:37:35.979 (no QR, no recovery), then ran its
  app-state sync. At 02:37:54.660 stdin closed and the stop began. Result at 02:37:54.995 (334 ms):
  `quiet_stop_failed epoch=1 reason=network_refused library_goroutines=0 network_refused=1 storage_refused=0
  calls_refused=0 events_dropped=0`, then `run_guard_recovery_required`, stderr line
  `whatsapp-mcp: stdio: offline recovery required (network_refused)`, exit 1. `status` afterwards reports the unsafe
  run state: **the marker is unsafe and offline recovery is required before the next connection on this directory.**
- Expected by the quickstart: `quiet_stop_passed` and a clean marker. **Not met.**
- What the log shows: the check failed closed as designed (no library goroutine left, no storage, call or event
  refusal; exactly one refused network operation). At 02:37:54.892, 1 ms before the stop's read pump exited, the
  library logged `Error sending close to websocket`. The same warning appears in the two passing step 1 checks and in the
  simulator logs, so it is not new. The cause of the single refusal is **not proven**: the likely source is a close
  frame written by the library after the fence closed (the fence counts a write after close as a refusal), but the
  counter does not say whether it was a dial or a write, and no test reproduced it. That reading is an inference.
- Evidence so far: 2 passes (step 1, both checks) and 1 failure (step 2) on the real account; on the simulator 200 of
  200 stops passed (T041). One real failure out of three is not a rate.
- The owner decides how to proceed (see the open question in the hand-off); no code was changed after this run.

### Investigation of the step 2 failure (2026-11-08, simulator and unit tests; no account contact)

- **Mechanism found, cause in the real run inferred.** A test that sends one HTTPS request through a fenced client,
  closes the fence and then lets the transport tidy up shows `refusedWrites` rising a moment later. The stack is
  `net/http.(*persistConn).readLoop` → `crypto/tls.(*Conn).Close` → `closeNotify` → `fenceConn.Write`: the TLS layer
  writes its close alert on a connection the fence has already closed, and `fenceConn.Write` counts that as a refused
  write. The count arrives asynchronously, so a check that has already looked can pass and a check that looks a few
  milliseconds later fails. The loopback simulator uses no TLS, so it could not show this and none of the 200 simulated
  stops did. The real run used TLS (and probably HTTP/2) connections; one refused write matches. That this was the
  source of the single refusal on 2026-11-08 02:37:54 is **inferred, not observed**: the production binary did not
  record what was refused.
- **Why it is not a network escape.** The fence closes every tracked raw connection before it counts anything, so a
  write through `fenceConn` after the close cannot reach the network. The count is a "late activity" signal, and TLS
  and HTTP/2 teardown produce it by design of those libraries.
- **Decision needed from the owner (changes the check's rules, so not done):** stop counting writes on connections the
  fence already closed (dials after close, gate refusals and every other source stay counted), or keep the strict rule
  and accept that a real-account stop can fail on harmless TLS teardown. TLS 1.3 alerts are encrypted, so they cannot
  be recognised by content. Counting only dials would also need the T005 unit test and the contract
  (`contracts/quiet-stop.md`, reason `network_refused`) to change.
- **A second failure mode, found by the stress run.** 60 simulated stops with a server stream error (`503`) sent at the
  same moment as `BeginStop`: 50 of 60 failed the check with `work_running` (one library goroutine); with no server
  action 0 of 60 failed, and with a server connection close at that moment 0 of 60 failed. The library starts work when
  the server ends the stream during a stop. A real server can do that at any time, so a stop that races a server
  disconnect will fail closed. Cause not investigated; same decision applies (the check is strict by design).
- The scratch instrumentation (a stack dump on each refusal) and the stress tests were removed, not committed.
- **Owner decision, 2026-11-08: option A.** Writes on connections the fence already closed no longer fail the check
  (`NetworkRefused` is refused dials only). They are still refused with zero bytes sent and still counted in
  `RefusedWrites`, which is not part of the check. Changed: `Session.QuietStop`, `fence.go` comment, `data-model.md`,
  `contracts/quiet-stop.md`. Tests: `TestQuietStopIgnoresWritesOnConnectionsTheFenceClosed` (fails with the old
  behaviour: `network_refused`, `NetworkRefused:1`; passes now) and `TestFenceTLSTeardownAfterClose...`
  (characterises the TLS source). Gates after the change: `gofmt`, `go vet ./...` and `go test ./... -count=1` exit 0,
  no CRLF. The WSL2 run, the pass-rate run and the helper checks of the Platform evidence section were **not repeated**
  after this change; the pass-rate run is unaffected on the simulator (no TLS), the real-account result is not yet
  re-verified. The state of the real-account journey stays "step 2 failed"; a retry needs the directory recovered
  first and the owner's permission.

### Retry after the fix `3882532` (permitted 2026-11-08, new directory `D:\tmp\wa-004b`)

The failed directory `wa-004a` was deleted by the owner, so the retry started with a new pairing. Binary built from
`3882532`.

- **Step 1 again** (run by the owner at 03:08 UTC, `pair --log-level debug`): `PairSuccess` 03:08:29.771, 515 at
  03:08:30.528 (0.76 s), `quiet_stop_passed epoch=1` 281 ms (pairing generation), fresh dial right after, authenticated
  03:08:31.377, `connection_confirmed` 03:08:33.817 (4.05 s), `quiet_stop_passed epoch=2` 392 ms, `stopped`, final line
  `pair: linked. The saved session can be reused.` No `quiet_stop_failed`, no `reconnect_denied`. Offline `status`:
  `logged_in: true`, no unsafe warning.
- **Step 2 again** (run by the assistant, `stdio --data-dir D:/tmp/wa-004b`, stdin held 20 s, exit 0 read from the
  command): dial 03:08:54.653, authenticated 03:08:55.224, `connection_confirmed` 03:08:55.877 with no QR and no
  recovery, stdin closed, `quiet_stop_passed epoch=1 duration_ms=440`, stderr
  `stdio: stopped. The saved session can be reused.`, `stopped` 03:09:14.954, stdout empty. Offline `status` afterwards:
  `logged_in: true`, no unsafe warning, so the marker is clean.
- **Tally on the real account:** the checks passed 4 of 5 times (3 before and 1 after the fix; the one failure was
  before the fix). The fix is **not proven** by one pass: the refusal it removes appeared once in three stops, so a
  single clean stop is consistent with the fix but also with chance. More stops would tell; none were run.
- **Five more `stdio` stops on the same saved session** (permitted by the owner 2026-11-08, run by the assistant,
  same binary `3882532`, 5 s apart, stdin held 2, 5, 10, 15 and 20 s). Each: exit 0, `connection_confirmed` once, no
  QR, stdout empty, one `quiet_stop_passed`, no `quiet_stop_failed`. Check durations: 372, 327, 285, 331 and 359 ms.
  Offline `status` afterwards: `logged_in: true`, no unsafe warning (marker clean).
- **Tally on the real account after the fix:** 7 of 7 checks passed (the step 1 retry's two, the step 2 retry's one and
  these five). Before the fix: 3 of 4 passed and one failed (`network_refused`). The refusal appeared once in four
  stops before the fix and in none of seven after it. That is consistent with the fix, not a proof: seven clean stops
  would also happen about 13% of the time (0.75^7) if the old rate was 1 failure in 4. The 2 s hold covers a stop early in the app-state sync; a
  server disconnect during a stop (the `work_running` case from the stress run) was not tried on the real account.
- Phone linked-device entry: the owner reported on 2026-11-08 that the `wa-004b` device is visible in Linked devices (the `wa-004a` entry was removed earlier by the owner). Not checked by the assistant.

### Step 3: cleanup (unlink on the phone, then recovery or delete)

Not performed; owned by the owner.

## Not verified

Another account or phone, native Linux or macOS runs, power loss, real-account receive volume.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-07 | 11ac064 | BLOCK | |
| design | 2026-11-07 | 14f648a | BLOCK | |
| design | 2026-11-07 | 8c32753 | CONCERNS | E003-RX-01 extended to feature 004 as proposed, 2026-11-07 (section "E003-RX-01 (implementation start)") |
| design | 2026-11-08 | 4bfd39e | CONCERNS | |
| design | 2026-11-08 | 39762a8 | BLOCK | |
| design | 2026-11-08 | ea9234b | PASS | |
