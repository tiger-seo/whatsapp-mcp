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
- T030 is not done: the new human lines of `contracts/cli.md` (`linked. The saved session can be reused.`, the reason
  category on the recovery lines) do not exist yet and belong to T035/T037. Only the switch test was replaced.
- T032 asks for `accountWork` to be set early in the owner's critical section. `accountWork` is set inside
  `Session.Connect`, and `attempt` already makes the stop check and records the abort in one critical section, so a
  stop in window (b) on the fresh generation finds an unchecked generation; the pairing is unresolved so the marker is
  unsafe anyway. No early set was added.
- A test seam `afterPostPairPublish` was added to `OwnerConfig` (tests only).

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

Pending (T041).

## Platform evidence

Pending (T042, T043).

## Soak run

Pending (T046).

## Real-account journey

Not performed. Needs the owner's permission for each step (T045).

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
