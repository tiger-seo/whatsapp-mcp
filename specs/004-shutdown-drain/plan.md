# Implementation Plan: Reusable Session After a Proven Quiet Stop

**Branch**: `004-shutdown-drain` | **Date**: 2026-11-07 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/004-shutdown-drain/spec.md`, and the owner decision of 2026-11-07
(`docs/specification.md` §8.11).

## Summary

Make a stopped session reusable without an offline recovery, when the program can show that nothing from the
previous connection is still running. Each generation that attempted a production Connect is retired in a fixed
order (`contracts/quiet-stop.md`): the program's own work is cancelled, the library's own disconnect runs, a
per-generation **network fence** (refuse and count dials and writes) closes, the store's **device barrier** (refuse
and count guarded calls, including context errors) retires, the library's context is cancelled only then, and a
bounded **observation** of the client library's goroutines runs inside the 10 s stop bound. The marker clears only if every checked generation passed and feature 003's other conditions
hold. The post-pair reconnect moves from the pairing client to a **fresh client and generation**, started only after
the pairing generation passes. The `PostPairReconnect` switch is removed. No other reconnect is added.

The deadlines are measured by a test-only spike on the simulator and approved by the owner before any production
path clears the marker (research R6).

## Technical Context

**Language/Version**: Go 1.27.0 module, Go 1.27.3 toolchain, always `CGO_ENABLED=0`.

**Primary Dependencies**: Unchanged. whatsmeow `v0.0.0-20261027104441-1eec1136a50b`, coder/websocket v1.8.15,
libsignal v0.2.2, util v0.11.0, modernc SQLite v1.61.0. The standard library adds `runtime` (goroutine dump) and
`net` (connection wrapper). No new module, no fork, no `replace`.

**Storage**: Unchanged schema 3. No migration. The marker's format does not change (FR-012). `internal/store` gains
an in-memory refusal count on the device session.

**Testing**: Go `testing`, `testing/synctest` for owner timing with a fake observation, `internal/wasim` for the real
pinned client path (one simulator at a time, outside synctest bubbles), and the guarded SQLite store in temporary
directories. Fault injection is test-only: a held pinned handler, a late send after the fence, a late store call
after the barrier, and a fake dump that is too large. Automated tests never use a real account.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. `runtime.Stack` and
`net.Conn` wrapping are portable. Platform evidence: Windows native, WSL2 (recorded as such), and CI on three OSes.

**Project Type**: Single-module CLI/server. `pair`, `stdio` and `serve` change only in their stop outcome and
messages.

**Performance Goals**: The ordinary stop and the post-pair handover each wait at most their approved deadline for the
check. The stop bound for writes (10 s) and the connect watchdog (30 s) are unchanged. The simulator pass rate must
reach at least 90% for each kind of stop, or the owner decides (SC-005).

**Constraints**:
- The dump never leaves `quiet.go` (FR-008).
- Its buffer is capped at 8 MiB.
- Allow-list entries name exact functions only.
- The order of fence before storage is a tested invariant.
- No new automatic network activity.

**Scale/Scope**: One account, one generation at a time. At most two generations per `pair` run (pairing, then
fresh); one per `stdio` or `serve` run.

## Constitution Check

*Gate before Phase 0, rechecked after Phase 1. Constitution 2.0.0.*

| Principle | Disposition | Gate |
| --- | --- | --- |
| I. Source of truth | The owner decision is recorded in §8.11 (2026-11-07) and in the spec's Clarifications. It supersedes, for this path only, the 2026-11-04 limitation and the same-generation form of the 2026-11-06 approval. D1–D18 are not reopened. | PASS |
| II. One binary | No new module and no CGO. The standard library only. Five targets are built in the gates. | PASS |
| III. One owner | One generation at a time. The fresh generation opens only after the previous one is retired and checked (invariant I3). The lock and storage close order are unchanged. | PASS by design; tests pending |
| IV. Clean stdout | New events go to the stderr logger. No dump content at any level. | PASS by design; tests pending |
| V. Secure defaults | Bounded observation (8 MiB, deadline), fail closed in every case of doubt, no new listener, no new dependency. E003-RX-01 is an **approved temporary deviation** for feature 003's deployment. Whether it covers this feature, which makes long connected runs possible, is the **owner's decision** recorded at T001 (research R10); the plan does not assume it. | Pending the owner's E003-RX-01 decision; other rules PASS by design |
| VI. Tested | Unit tests with a fake observation, simulator tests with the real client, store refusal tests, command tests, and the measurement spike. Every abuse case in the spec has a test. | PASS by design; tests pending |
| VII. MCP tools | No tools in this feature. | N/A |
| VIII. Real accounts | Automated tests are synthetic. The quickstart's real-account journey needs the owner's permission for each step. | PASS |
| IX. Simplicity | Two small files (`fence.go`, `quiet.go`) and changes to the existing retire and post-pair paths. No setting, no flag, no fork. The switch is removed. | PASS |
| X. Security per feature | The spec has its Security section and finding dispositions. The owner decides at T001 whether E003-RX-01 covers this feature, and the exception is rechecked at code review. Its expiry (start of 2026-12-02 UTC) or a "not covered" answer blocks continuation without a new owner decision. | Pending the owner's decision; PASS otherwise |

No violation needs justification, so Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/004-shutdown-drain/
├── spec.md
├── plan.md              # this file
├── research.md          # R1–R10
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── quiet-stop.md    # retirement order, check, invariants (internal)
│   └── cli.md           # pair/stdio/serve outcomes and events
├── tasks.md             # /speckit-tasks
└── verification.md      # evidence, measurement, owner reviews, E003-RX-01 recheck
```

### Source Code (repository root)

```text
internal/wa/
├── fence.go             # new: per-generation dialer and conn wrapper, refusal counts
├── quiet.go             # new: bounded goroutine observation, allow-list, QuietStopResult
├── session.go           # fenced HTTP clients for every generation; retire order; late-call and dropped-event counts; QuietStop
├── owner.go             # retireGeneration(deadline); fresh-client post-pair path; switch removed; events
├── pairing.go           # end the window's QR context before retiring the pairing generation
├── fence_test.go, quiet_test.go, quiet_wasim_test.go, quiet_measure_test.go (spike), owner_postpair_test.go (rewritten)
└── testdata/cmd/session-check/main.go   # drop the PostPairReconnect field
internal/store/
├── device.go            # refusal count at every ErrRetired site; DeviceBarrier.Refused
└── device_refused_test.go
cmd/whatsapp-mcp/
├── commands.go          # remove PostPairReconnect; pair, stdio and serve messages per contracts/cli.md
└── *_test.go            # exit codes and messages
OPERATIONS.md, AGENTS.md # reusable session after a passed check; recovery after a failed one
```

**Structure Decision**: Keep the existing single module. The check lives in `internal/wa` next to the session it
observes. The store change is one counter in `internal/store`. No new package.

## Phases for `/speckit-tasks`

1. **Setup and gates.** Record the E003-RX-01 facts and obtain the owner's decision on whether it covers this
   feature (research R10). Confirm that §8.11 carries the 2026-11-07 decision.
2. **Measurement spike (test-only).** Build the fence, the store counts, the late-call and dropped-event counts and
   the observation without wiring them to the marker. Measure on the simulator (quickstart §1), driving the new retire
   order at the 515 for the pairing case, and record how many simulator-server goroutines the package rule counts.
   **Gate:** the owner approves `quietStopDeadline` and `postPairQuietDeadline` together with the total stop budget
   against the 10 s bound in `verification.md`; if a kind is under 90%, the owner decides first.
3. **US1: stop and reuse.** Wire `QuietStop` into `retireGeneration` and `finishStop`. Remove the
   `UpstreamDrainUnproven` latch. Write tests first for every reason category, the late-logout regression, a held
   pinned handler, the order invariant, an unresolved pairing and low disk.
4. **US2: fresh-client post-pair reconnect.** Rewrite the post-pair path (contract, owner use) and remove the switch
   from the owner, the commands and the helper. Write tests first: injected late sends, a late `Disconnect` from the
   pairing tail or QR emitter, saved-identity payload and selected version, the 515 waiting for `PairSuccess`, a
   failed check before reconnect, exit codes.
5. **US3: diagnostics.** Add the events and the human lines per `contracts/cli.md`. Run the secrecy tests at every log
   level.
6. **Polish and evidence.** Update `OPERATIONS.md` and `AGENTS.md`, run the full gates (quickstart §4) and the
   pass-rate run with the approved constants. The permitted real-account journey (quickstart §5) runs only with the
   owner's step-by-step permission, before the code review. If it fails, the owner decides as the spec's assumption
   says.

## Risks

| Risk | Effect | Handling |
| --- | --- | --- |
| The check rarely passes (the close frame or a websocket `AfterFunc` counted, a slow handler) | Recovery still needed after most stops | Measured in phase 2; under 90% the owner decides (tune, allow-list with evidence, or fork) |
| A false pass | Lost session or key writes; decryption failures later | Fail closed in every case of doubt, broad package match, two clean observations, refusal counts; tests for every fault |
| The real server dislikes the pause or the new client between `PairSuccess` and reconnect | Linking fails on the real account | Permitted real-account run before the code review; the owner decides on failure |
| A stop-the-world pause from the dump | A short pause during stop | Only during stops, a few polls, bounded buffer |
| E003-RX-01 expires before the code review, or the owner says it does not cover this feature | Work stops | Owner decision at T001; recheck at code review; owner decision before 2026-12-02 |
| A handler loses a write to a cancelled context | False pass, a lost key or session | The library context is cancelled only after the fence and barrier; every guard or context failure after retirement began is counted (research R4) |
| The stop passes the check after the 10 s bound | Exit 1 with a clean marker | The check fails with `stop_bound_exceeded` (contract "Stop bound") |
| The in-process simulator's server goroutines are counted as library work | Pass rate understated | Measured and decided at the gate (research R6) |

## Complexity Tracking

No constitution violations to justify.
