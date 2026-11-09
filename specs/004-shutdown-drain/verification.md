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

**Owner decision, 2026-11-09 (`tiger-seo`):** E003-RX-01, as extended to feature 004 on 2026-11-07, covers the new pin
`go.mau.fi/whatsmeow v0.0.0-20261109152745-f1b798561919` (commit `f1b7985`). The 20 commits since `1eec1136a50b` add
no receive-resource bounds, so F09 and the exception's scope are unchanged; the `readPump` fix removes one goroutine
leak and is not a bound. Same scope (feature 004, private, local, single-owner), same expiry (start of 2026-12-02 UTC,
no renewal), same triggers and compensating controls. The automatic-reconnect paths added upstream stay off
(`EnableAutoReconnect = false`).

The owner accepted the old-pin soak run (T046) for the new pin on 2026-11-09 (section "E003-RX-01 recheck").

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

T047, 2026-11-08, at commit 409c304, against the owner's extension of 2026-11-07 (`tiger-seo`):

1. **Scope.** Still feature 004 only, in the private, local, single-owner deployment. No listener, no shared deployment
   and no message ingest were added; `stdio` and `serve` still have no MCP tools.
2. **Date and pin.** This recheck happens before the start of 2026-12-02 UTC. `git diff main --stat -- go.mod go.sum` is
   empty and `go.mod` pins `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`, the pin the exception covers. The
   owner-judge code review follows this recheck; its row is in "Owner reviews".
3. **Resource incident.** None. Run 2 of the soak run showed no sustained growth of heap, goroutines, files or working
   set (see "Soak run"). The real-account journey (T045) was short and showed none either.
4. **Soak result (T046).** Run 2 passed (exit 0): heap 4.1 to 5.4 MB, goroutines 16 to 17 while connected, database and
   WAL files unchanged, working set 30.6 to 22.6 MB; the stop check passed and the marker was clean. Run 1 failed on a
   test defect and an unexplained leftover-goroutine count that did not reproduce. **Deviation to decide:** the machine
   slept for about 17 minutes, so the effective load was about 3 h 43 min, not the 4 h of T046. The owner may accept
   that or ask for another run.

Accepted risks and limits: simulator traffic only (no real receive volume), native Windows only for the soak run,
run 1's 38 leftover goroutines unexplained.

Update at the second code review, 2026-11-09, at `3591f17`:

1. **Scope.** Unchanged: feature 004 only, private, local, single-owner; no listener, no ingest, no MCP tools.
2. **Date and pin.** Before the start of 2026-12-02 UTC. The pin is now `go.mau.fi/whatsmeow
   v0.0.0-20261109152745-f1b798561919` (upstream `f1b7985`); the owner reassessed the exception for it on 2026-11-09
   (section "E003-RX-01 (implementation start)"). No other module changed.
3. **Resource incident.** None. `internal/wa` passed 10 of 10 repeated runs on the new pin, where the old pin's
   `readPump` leak failed about 40% of runs.
4. **Soak result.** Accepted by the owner as it stands (below), not repeated on the new pin.

**Owner decision, 2026-11-09 (`tiger-seo`): the soak run is accepted** as the compensating control of E003-RX-01 for
feature 004, as it stands: run 2's effective load of about 3 h 43 min instead of 4 h (the machine slept), run 1's 38
unexplained leftover goroutines, and the fact that it ran on the old pin `1eec1136a50b`, not on `f1b7985`. It is not
repeated on the new pin.

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

## Pin change to upstream f1b7985

2026-11-09. Upstream did not merge tulir/whatsmeow#1335; the maintainer closed it with his own commit `f1b7985`
("socket: check context when sending completed frames to channel", 2026-11-09 15:27 UTC). Its `framesocket.go` change
is the same `select` as the fork's `dce60de`, without the fork's comment and without its test (upstream has a single
test file, `client_test.go`). The owner decided on 2026-11-09 ("let's use f1b7985") to move the pin to it instead of
using the fork.

- `go get go.mau.fi/whatsmeow@f1b798561919a9106f284e39078b39a57982625e` and `go mod tidy`, both exit 0. `go.mod` now pins
  `go.mau.fi/whatsmeow v0.0.0-20261109152745-f1b798561919`; no other module changed; `go.sum` changed only for
  whatsmeow. No `replace`, no fork.
- The new pin is 20 upstream commits past `1eec1136a50b` (including `2fc844f`, named in `spec.md` as needing its own
  E003-RX-01 reassessment). Read from the module cache: the new retry paths (handshake timeout and closed socket made
  retryable in `ConnectContext`, keepalive using `ResetConnection`) all require `EnableAutoReconnect`, which
  `internal/wa/session.go` sets to false. `store/sqlstore` changed only in `store.go` (contact-name deduplication), not
  in its schema upgrades. `server_sync` notifications change only with `SynchronousAck`, which this project does not set.
- The allow-list is empty, so the pin test has nothing to revalidate.

| Gate (Windows 10, Go 1.27.3, `CGO_ENABLED=0`, `TEMP` on `D:\tmp`) | Result |
| --- | --- |
| `gofmt -l .` | no output, exit 0 |
| `go vet ./...` | exit 0 |
| `go mod verify` | all modules verified, exit 0 |
| `go test ./... -count=1 -timeout 30m` | exit 0: cmd 133.8 s, store 223.9 s, wa 36.3 s, wasim 3.8 s |
| `go test ./internal/wa -count=1` ten times in a row | 10 of 10 exit 0 (29.8 to 34.4 s); on the old pin about 40% of package runs failed |
| `go build ./cmd/whatsapp-mcp` for windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 | five exit 0 |
| `go vet`, `go test` and `go build` of `./internal/wa/testdata/cmd/session-check` | three exit 0 |
| `govulncheck@v1.9.0 ./...` | "No vulnerabilities found", exit 0 |
| `git ls-files --eol` | no `w/crlf` |

Pass-rate run on the new pin, 2026-11-09 19:03 UTC at `a3fb844`, Windows, same command as "Pass-rate run"
(`WA_MEASURE=1 go test ./internal/wa -run TestQuietStopPassRate -count=1 -v`): exit 0, 50 of 50 passed for each of the
four kinds (pairing generation's check, `pair` stop check, `stdio`, `serve`), no failures, no passed check with a marker
left unclean.

After the review fixes (`a340199`, `cbb8093`): `go vet ./internal/wa` and `go test ./internal/wa -count=1` exit 0;
`TestFenceTLSTeardownAfterCloseIsNotADial` and `TestQuietStopIgnoresWritesOnConnectionsTheFenceClosed` at `-count=20`
exit 0.

WSL2 on the new pin: not done. The first attempt failed to start (`go` is not on the PATH under `wsl --exec`; the
toolchain is `~/sdk/go1.27.3`). The second, and a third at `ec2a0b7` with `-p 1` (2026-11-09 19:47 UTC), were stopped
by Claude Code during the test run because Windows ran low on memory (about 2.6 GB of 16 GB free); neither recorded a
result for any package.

WSL2 on the new pin, 2026-11-09, run by the owner in a WSL2 terminal at `480cd21` (Ubuntu, Go 1.27.3, `CGO_ENABLED=0`,
`TMPDIR=/var/tmp`), exit codes as printed by the owner's shell:

- `go test -p 1 ./... -count=1 -timeout 20m`: exit 1. Passed: buildinfo, config, lock, private, store (185.2 s), wa
  (28.1 s), walog. Failed: `cmd/whatsapp-mcp` (the binary build) and `internal/wasim` (`TestNotLinkedIntoTheBinary`,
  `go list`), both with `error obtaining VCS status: exit status 128`. Git in WSL refused the repository on `/mnt/d`
  as "dubious ownership"; no test assertion ran in either.
- After the owner added the repository to `safe.directory` in WSL: `go test -p 1 ./cmd/whatsapp-mcp ./internal/wasim
  ./internal/wa/testdata/cmd/session-check -count=1 -timeout 20m`: exit 0 (cmd 112.7 s, wasim 1.6 s, helper 0.8 s).

So every package and the helper passed under WSL2, in two invocations. WSL2 is not native Linux.

Final Windows gates before the code review, 2026-11-09 21:09 UTC at `3591f17` (Windows 10, Go 1.27.3,
`CGO_ENABLED=0`, `TEMP` on `D:\tmp`), each exit code read from the command: `gofmt -l .` no output, exit 0;
`go vet ./...` exit 0; `go mod verify` exit 0; `go test ./... -count=1 -timeout 30m` exit 0 (cmd 130.3 s, store
257.9 s, wa 36.1 s, wasim 5.6 s); the five cross-builds exit 0; `go vet`, `go test` and `go build` of the helper exit
0; `govulncheck@v1.9.0 ./...` "No vulnerabilities found", exit 0; `git ls-files --eol` no `w/crlf`.
`git diff main --stat -- go.mod go.sum` shows only the whatsmeow pin line and its two `go.sum` lines.

Not repeated on the new pin: the soak run (T046, accepted by the owner as it stands) and the real-account journey
(T045; the new pin is not verified on a real account). Those results belong to the old pin. The owner reassessed E003-RX-01 for the new pin on 2026-11-09 (section
"E003-RX-01 (implementation start)").

## Soak run

`TestQuietStopSoak` (`internal/wa/quiet_soak_test.go`), native Windows 10, Go 1.27.3, `CGO_ENABLED=0`, `TEMP` on
`D:\tmp`. A `serve`-shaped connection on the simulator, 10 synthetic events per second (receipt, presence, message
without payload, chat state), a burst of 2000 events and a `server_sync` notification every 10 minutes, a sample every
60 s, then an orderly stop. Command: `WA_SOAK=1 WA_SOAK_OUT=D:\tmp\soak-full.csv go test ./internal/wa -run
TestQuietStopSoak -count=1 -v -timeout 5h`.

### Run 1, 2026-11-08 12:03 to 16:04 UTC: FAILED (exit 1)

241 samples, 190,069 events sent, the connection held for 4 h with no loss.

| Quantity | Start | Peak | End | Slope per hour (all / second half) |
| --- | --- | --- | --- | --- |
| heap in use | 3.9 MB | 6.4 MB | 5.5 MB | +39 KB / +83 KB |
| goroutines (connected) | 16 | 20 | 19 | 0 / 0 |
| database file | 270 KB | 270 KB | 270 KB | 0 / 0 |
| WAL file | 20.6 KB | 20.6 KB | 20.6 KB | 0 / 0 |
| process working set | 23 MB | 73 MB | 38.6 MB | -2.4 MB / +0.1 MB |

The working set sat at 39 to 40 MB with two excursions to about 72 MB (a burst, then back); it did not grow. The stop
passed its check (`stop passed=true`). Two assertions failed:

1. `marker ""`, wanted `clean`: **a defect in the test.** The marker was read with the setup context, which has a
   2-minute limit and had expired. Fixed (a fresh context); a 4-minute run then reads `marker=clean`.
2. Goroutines after the stop: 38 against a baseline of 2 (1 of them the simulator's). **Cause not established.** The
   connected count was stable (16 to 20) and the check saw no library goroutine, so the extra ones appeared at or
   after the stop, or were still winding down when the dump was taken immediately after the owner finished. The test
   now waits up to 10 s and prints the remaining goroutines grouped by function. The 90 s and 4 min smoke runs ended
   at 5.

The log also holds about 47,000 `No LID found` warnings from the synthetic messages (noise, not a resource effect).
This run does not satisfy T046. Run 2 follows.

### Run 2, 2026-11-08 16:11 to 20:12 UTC: PASSED (exit 0, 14,400 s)

Same command with the output in `D:\tmp\soak-full2.csv`, after the two fixes above. 225 samples, 176,621 events sent.

| Quantity | Start | Peak | End | Slope per hour (all / second half) |
| --- | --- | --- | --- | --- |
| heap in use | 4.1 MB | 6.8 MB | 5.4 MB | +101 KB / +39 KB |
| goroutines (connected) | 16 | 20 | 17 | 0 / 0 |
| database file | 270 KB | 270 KB | 270 KB | 0 / 0 |
| WAL file | 20.6 KB | 20.6 KB | 20.6 KB | 0 / 0 |
| process working set | 30.6 MB | 74.4 MB | 22.6 MB | -7.4 MB / +0.5 MB |

The final stop passed its check (`stop passed=true`), the marker read `clean`, and the goroutines after the stop were 5
against a baseline of 2 (1 the simulator's), inside the allowance of 6 above the baseline. The working set rose to
about 72 MB in the first minutes and again in the first run's pattern, then settled between 22 and 40 MB; it did not
grow.

Caveats:

- **The machine slept for about 17 minutes.** Between the samples at 11,760 s and 12,794 s (1,034 s instead of 60 s)
  only 167 events were sent instead of about 10,000, and the sample count is 225 instead of 241. The owner confirmed that
  the system went to sleep. The connection survived it. The effective load time is about 3 h 43 min, not 4 h.
- **Run 1's 38 leftover goroutines did not reproduce.** Run 2 ended at 5. The cause of run 1's count was not
  established; it may have been goroutines still winding down when the dump was taken (run 2 waits up to 10 s), or a
  side effect of the expired setup context in run 1. Treated as unexplained, not as resolved.
- About 44,000 `No LID found` warnings came from the synthetic messages (noise).
- The simulator cannot show real receive volume: real-account receive volume stays "not verified". Native Windows
  only; the soak was not run under WSL2.
- No resource incident under the owner's extension of E003-RX-01: no sustained growth of heap, goroutines, files or
  working set.

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

### Check on the new pin `f1b7985` (permitted 2026-11-09, new directory `D:\tmp\wa-004c`)

Binary built from `8ba2550` (the proxy fix on the new pin), windows/amd64. One account, one phone, as before.

- **Step 1, `pair --log-level debug`** (run by the owner at 21:32 UTC; the owner reported success): pairing succeeded
  21:32:32.615; 515 at 21:32:33.209 (0.59 s); `quiet_stop_passed epoch=1` 229 ms (pairing generation); fresh dial
  21:32:33.440; authenticated with the saved identity 21:32:34.013; `connection_confirmed` 21:32:36.825 (4.21 s);
  `quiet_stop_passed epoch=2` 439 ms; final line `pair: linked. The saved session can be reused.` No
  `quiet_stop_failed`, `run_guard_recovery_required`, `reconnect_denied` or error line. Offline `status` (run by the
  assistant): exit 0, `logged_in: true`, no unsafe warning.
- **Step 2, five `stdio` stops planned** (permitted by the owner, run by the assistant; stdin held 2, 5, 10, 15 and 20 s).
  - **Run 1 (hold 2 s): the check FAILED.** Dial and authentication with no QR, `connection_confirmed` at
    21:33:50.086, the library's initial app-state sync running (a full sync completed at 21:33:50.632). stdin closed at
    21:33:50.848. At 21:33:51.081-.083 the read loop exited, the library's own `Disconnect` reported the close, and the
    library logged four failed initial app-state fetches and five failed syncs after server notifications, all in the
    same millisecond. Result: `quiet_stop_failed epoch=1 reason=storage_refused library_goroutines=0 network_refused=0
    storage_refused=8 calls_refused=0 events_dropped=0 duration_ms=346`, `run_guard_recovery_required`, stderr
    `stdio: offline recovery required (storage_refused)`, exit 1, stdout empty.
  - **Runs 2 to 5 did not connect.** Each refused to start on the unsafe marker (`unsafe or invalid run marker; offline
    recovery is required using pair`), exit 1, no dial in the log. The script should have stopped after run 1; these
    four runs had no effect on the account.
  - Offline `status` afterwards: exit 1, the store is in an unsafe run state; offline recovery is required.
- **Reading of the failure (inference, not observed):** the library's app-state fetches wait on its own
  `appStateSyncLock`. When the stop's `Disconnect` made the in-flight request fail, the queued fetches ran one after
  another; each first reads the stored app-state version (`GetAppStateVersion`) and then fails to send. Calls that
  reached the store after the barrier retired (step 8) were refused and counted. The production log keeps only format
  strings, so the refused calls and their errors were not recorded. If this is right, the refused calls are reads,
  not lost writes, and the check failed closed as designed. The same window existed on the old pin (its 2 s stop on
  2026-11-08 passed); whether the new pin makes it more likely is not known. One failure in one stop is not a rate.
- Tally on the new pin: 2 of 3 checks passed (both in step 1); 1 failed (`storage_refused`, a stop 0.76 s after the
  connection was confirmed, during the initial app-state sync).
- **Cleanup:** the owner unlinked the `wa-004c` device on the phone (reported 2026-11-09); the assistant then deleted
  `D:\tmp\wa-004c` (`whatsapp.db` and the lock file). The logs `D:\tmp\wa-004c-*.log` remain outside the repository.
- **Simulator reproduction, 2026-11-09** (`TestSimulatorStopDuringQueuedAppStateFetches` in
  `internal/wa/appstate_stop_test.go`, guarded by `WA_APPSTATE_STOP=1`): after a connected saved-session run, the
  simulator holds the first app-state request unanswered and sends five `server_sync` notifications, so the library's
  other fetches queue on its `appStateSyncLock`; then the stop begins. A recording wrapper around the device's
  app-state store logs every failed call next to the retirement steps.
  - New pin `f1b7985`, 3 runs: 3 of 3 failed with `storage_refused=4` (no library goroutine, no other refusal). In
    every run the four refused calls were `GetAppStateVersion` (a read), each refused with "device generation is
    retired", each after `barrier_retired`: the queued fetches ran once the in-flight request failed at `Disconnect`.
    No write was refused.
  - Old pin `1eec1136a50b`, the same test in a temporary worktree (removed): 3 of 3 failed the same way. The fetch
    path is the same in both pins (lock, then read the version); the new pin adds only a rate-limit check. So the new
    pin did not introduce this; the earlier journey's stops did not land while fetches were queued.
  - The test's 5 s stop duration comes from the held request blocking the close handshake; the real stop took 346 ms.
  - This confirms the mechanism inferred from the real-account log, and that the refused calls in the reproduction are
    reads. That the eight refused calls of the real stop were the same reads is very likely but was not recorded.
- **Owner decision, 2026-11-09 (`tiger-seo`): option 1.** A read refused only because the barrier has retired no
  longer fails the check; it is counted in `DeviceBarrier.RefusedReads()`. Reads failing on their context, a stale
  lease or another guard error, and every refused write, still count. Implemented in `b6c6d2d`; the documents
  (`spec.md`, `contracts/quiet-stop.md`, `data-model.md`, `OPERATIONS.md`, §8.11) follow.
  - Audit of the guard's `write` flag: every call marked as a read is a getter (`Get*`, `HasSession`,
    `IsTrustedIdentity`, `UploadedPreKeyCount`); the getters that can create rows (`GetOrGenPreKeys`, `GenOnePreKey`)
    are marked as writes; the nested path in `protected` is reached only by reads.
  - Store tests: `TestRefusedKeepsReadsAfterRetireApart` (two refused reads leave `Refused` at 0 and `RefusedReads` at
    2; a refused write raises `Refused`) and `TestRefusedCountsReadsFailingOnTheirContext` (a cancelled read still
    counts); `TestRefusedCountsEveryRetiredSiteExactlyOnce` now checks `Refused + RefusedReads`. They failed to compile
    before the change (no `RefusedReads`) and pass after it.
  - `TestSimulatorStopDuringQueuedAppStateFetches` is now a regular test: it requires a passed check, a clean marker,
    at least one refused read and no refused write. Against the old counting rule (old store code plus a stub
    `RefusedReads` returning 0) it failed with `storage_refused=4` and an unsafe marker; with the change it passes, 10
    of 10 with `WA_APPSTATE_STOP_RUNS=10` (exit 0).
  - `go test ./internal/wa -count=1` exit 0 (35.4 s); `go test ./internal/store -count=1` exit 0 (196.0 s).
  - Real-account check of the new rule: see the next block.

### Check of the refused-read rule (permitted 2026-11-09/10, new directory `D:\tmp\wa-004d`)

Binary built from `a68aea4` (new pin, proxy fix, refused-read rule), windows/amd64. One account, one phone.

- **Step 1, `pair --log-level debug`** (run by the owner, reported success): pairing succeeded 22:03:33.203 UTC; 515 at
  22:03:33.869 (0.67 s); `quiet_stop_passed epoch=1` 246 ms; fresh client authenticated 22:03:34.649 with the saved
  identity; `connection_confirmed` 22:03:37.273 (4.07 s); `quiet_stop_passed epoch=2` 399 ms; final line `pair: linked.
  The saved session can be reused.` No failed check, recovery request or error line. Offline `status`: exit 0,
  `logged_in: true`, no unsafe warning.
- **Step 2, five `stdio` stops** (permitted by the owner, run by the assistant; stdin held 2, 2, 3, 5 and 10 s, 5 s
  apart; the script would have stopped at the first non-zero exit). Each: exit 0 read from the command, one dial, no
  QR, stdout empty, one `quiet_stop_passed`, no `quiet_stop_failed`.

  | Run | Hold | Stop after `connection_confirmed` | Check |
  | --- | --- | --- | --- |
  | 1 | 2 s | 0.34 s | passed, 472 ms |
  | 2 | 2 s | 0.60 s | passed, 301 ms |
  | 3 | 3 s | 1.47 s | passed, 335 ms |
  | 4 | 5 s | 3.20 s | passed, 455 ms |
  | 5 | 10 s | 8.29 s | passed, 451 ms |

  Run 1 landed in the app-state sync: at the stop's `Disconnect` (22:04:20.887) the library logged five failed initial
  app-state fetches and five failed syncs after notifications within 2 ms, the same pattern as the failed stop of
  2026-11-09, and the check passed. That the refused calls were reads kept apart by the new rule is inferred from the
  pattern and the simulator reproduction; the production log does not count `RefusedReads`. Offline `status`
  afterwards: exit 0, `logged_in: true`, no unsafe warning (marker clean).
- Tally on the new pin with the new rule: 7 of 7 checks passed (two in step 1, five in step 2), including one stop
  during the initial app-state sync. One account and one phone; seven clean stops are consistent with the fix, not a
  proof of a rate.
- **Cleanup:** the owner unlinked the `wa-004d` device on the phone (reported 2026-11-10); the assistant deleted
  `D:\tmp\wa-004d`. The logs `D:\tmp\wa-004d-*.log` remain outside the repository.

### Gates before the third code review

2026-11-09 22:06 UTC at `2a6306c` (Windows 10, Go 1.27.3, `CGO_ENABLED=0`, `TEMP` on `D:\tmp`), each exit code read from
the command: `gofmt -l .` no output, exit 0; `go vet ./...` exit 0; `go mod verify` exit 0; `go test ./... -count=1
-timeout 30m` exit 0 (cmd 127.5 s, store 208.6 s, wa 39.9 s, wasim 4.0 s); the five cross-builds exit 0; `go vet`,
`go test` and `go build` of the helper exit 0; `govulncheck@v1.9.0 ./...` "No vulnerabilities found", exit 0;
`git ls-files --eol` no `w/crlf`; `go.mod`/`go.sum` differ from `main` only in the whatsmeow pin. WSL2 was not rerun
after the proxy fix and the refused-read rule; CI has not run (no push).

The owner asked for the third code review on 2026-11-10 without a separate design review of the changes made since the
design PASS (fence rule, pin, refused-read rule).

### Step 3: cleanup (unlink on the phone, then recovery or delete)

Done 2026-11-08. The owner unlinked the `wa-004b` device on the phone (the `wa-004a` entry had been removed earlier); the
assistant then deleted `D:/tmp/wa-004b` (`whatsapp.db` and the lock file). The log files `D:/tmp/wa-004b-*.log`
remain on disk, outside the repository (the `wa-004a` logs are already gone). They are debug output and were not
redacted, but the QR code is not in them: the library logs the format string, not the code (checked by grep). That the
phone no longer lists the device after unlinking was not checked by the assistant.

## Not verified

- Another account or phone (one account, one phone only).
- Native Linux and macOS runs, and the CI runners (no push yet); WSL2 is not native Linux.
- Power loss, and macOS `F_FULLFSYNC`.
- Real-account receive volume (the soak run used the simulator).
- The soak run under WSL2 or any platform other than native Windows.
- A server disconnect during a stop on the real account (the `work_running` case).
- The cause of run 1's 38 leftover goroutines.
- That the phone stopped listing the unlinked device (not checked by the assistant).
- The new pin `f1b7985` on a real account (the journey used the old pin), and the soak run on the new pin.
- CI on the three runners for the new pin (no push yet).

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-07 | 11ac064 | BLOCK | |
| design | 2026-11-07 | 14f648a | BLOCK | |
| design | 2026-11-07 | 8c32753 | CONCERNS | E003-RX-01 extended to feature 004 as proposed, 2026-11-07 (section "E003-RX-01 (implementation start)") |
| design | 2026-11-08 | 4bfd39e | CONCERNS | |
| design | 2026-11-08 | 39762a8 | BLOCK | |
| design | 2026-11-08 | ea9234b | PASS | |
| code | 2026-11-08 | 4d65199 | BLOCK | |
| code | 2026-11-09 | 2d0c0b0 | BLOCK | |
