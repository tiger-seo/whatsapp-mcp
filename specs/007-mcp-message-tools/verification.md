# Verification: MCP Message Tools

## Planning Evidence

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-22 | `.specify/scripts/bash/setup-plan.sh --json` (Git Bash on Windows) | Exit 0; resolved branch `007-mcp-message-tools` and generated the plan template in this directory. |
| 2026-11-22 | Read-only audit of `internal/store`, `internal/mcp`, the feature spec and the 002/006 baselines | Two independent research passes identified the indexed seek paths and the existing reply/lifecycle bounds. Research decisions are recorded in [research.md](research.md). No SQLite measurements, runtime tests or operator-account actions were run. |
| 2026-11-22 | Task checklist validation with `git grep --no-index` and PowerShell | Exit 0; 36 sequential unchecked tasks with correct story labels and file paths. `git show --check 0d4c7fa` and `git ls-files --eol -- specs/007-mcp-message-tools/tasks.md` passed. Documentation checks only. |
| 2026-11-22 | Git Bash `-n .claude/hooks/owner-review-gate.sh` and isolated `/d/tmp/kilo/whatsapp-007-gate-check.sh` | Exit 0 for both. Seven gate fixtures passed: existing design PASS unchanged, missing/pending/uncommitted evidence denied, accepted CONCERNS denied, committed Verified evidence plus a current PASS allowed, and edited evidence made the PASS stale. No production or account actions. |
| 2026-11-22 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 90s -run '^TestResult(HTMLMetacharacterCharges\|SDKEncodingEscapedBudget\|ActualTwoMiBBoundary\|NearLimitPassesFramePreflight)$'` | Exit 0, four existing MCP reply-size regressions, Windows/amd64 (package 1.115 s). This verifies the encoder baseline, not feature 007 context assembly. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 90s -run '^(TestToolReadDoesNotSucceedAfterCancellation\|TestToolWALWriteCancelsWithoutReleasing\|TestWriterFenceDuringExistingDeviceLease)$'` | Exit 0, three existing reader and writer-fence regressions, Windows/amd64 (package 10.873 s). Not a test of the proposed message query path. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 90s -run '^(TestServerSDKExplicitSchemasAndDispatch\|TestGuardOfficialInMemory\|TestCommonSchemaDefaults)$'` | Exit 0, three existing MCP registration, transport and default regressions, Windows/amd64 (package 0.365 s). |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 120s -run '^TestMCPIngestPressureWALFence$' -v` | Exit 0, one existing native Windows/amd64 WAL-fence test (4.65 s). Not evidence for the proposed message statements. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 120s` | Exit 0, existing MCP package suite, Windows/amd64 (96.461 s). Feature 007 tools are not wired in. |
| 2026-11-23 | `CGO_ENABLED=0 go vet ./internal/mcp ./internal/store` | Exit 0, Windows/amd64. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 90s -run '^Test(MCPPipeNative\|MCPStdioIntegratedDrain)$'` | Exit 0, existing native pipe and integrated drain tests, Windows/amd64 (package 91.963 s). |
| 2026-11-23 | `CGO_ENABLED=0 GOPROXY=off GOFLAGS=-mod=readonly go run D:/tmp/kilo/wa007_batch_probe.go` | Exit 0 on Windows/amd64; nine synthetic SQLite search trials and one connection-return trial passed. [Measured results and limitations](feasibility.md#bounded-batch-probe-2026-11-23). |
| 2026-11-23 | `git diff --check -- specs/007-mcp-message-tools`; `git ls-files --eol -- specs/007-mcp-message-tools`; `git grep -E -o '^[-] \[[ xX]\] T[0-9]{3}' -- specs/007-mcp-message-tools/tasks.md` with sequential ID validation | Exit 0 for all three checks; no whitespace errors, 10 LF files, and 36 sequential IDs. Documentation checks only, before the S6 decision below; no new runtime evidence was collected. |
| 2026-11-23 | `go run D:/tmp/kilo/wa007_batch_probe.go D:/tmp/kilo/wa007_seek_fold_probe.go` | Exit 0 on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`, `GOFLAGS=-mod=readonly`: 12 search/limit trials, six filtered batch checks, four plan-only checks, and one standalone cancellation trial. See [seek and Unicode expansion results and gaps](feasibility.md#seek-and-unicode-expansion-probe-2026-11-23). |
| 2026-11-23 | `go test -count=1 -timeout 120s -v ./internal/store -run '^(TestToolReadDoesNotSucceedAfterCancellation\|TestToolWALWriteCancelsWithoutReleasing\|TestMCPIngestPressureWALFence)$'` | Exit 0; three existing regressions passed on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`, `GOFLAGS=-mod=readonly` (4.633 s). This does not run the proposed query under writer pressure. |
| 2026-11-23 | `go test -count=1 -timeout 120s -v ./internal/mcp -run '^TestResult(SDKEncodingEscapedBudget\|SuccessfulNearLimitAllocations\|NearLimitPassesFramePreflight)$'` | Exit 0; three existing encoder and frame regressions passed on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`, `GOFLAGS=-mod=readonly` (1.886 s). This does not run message context assembly. |
| 2026-11-23 | `go run -mod=readonly D:/tmp/kilo/wa007_alias_probe.go` | Exit 0; one deep alias page and latest-group/direct comparison passed on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`. See [scratch results and limitations](feasibility.md#alias-paging-and-guarded-reply-probes-2026-11-23). |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_mcp_overlay.json ./internal/mcp -count=1 -timeout 45s -v -run '^TestScratch007GuardedContext$'` | Exit 0; two synthetic subtests, 98 guarded calls, all 50 identities recovered in each subtest on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`. No SQLite projection or native framing in this probe. |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_wal_overlay.json ./internal/store -count=1 -timeout 45s -v -run '^TestScratch007CandidateWAL$'`, then the same command with `-count=3` | Both exited 0 on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`; four passes. See [WAL pressure results and limitations](feasibility.md#admitted-query-under-wal-pressure-2026-11-23). |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_mcp_overlay.json ./internal/mcp -count=1 -timeout 60s -v -run '^TestScratch007SQLiteGuardedContext$'` | Exit 0 on Windows/amd64 with `CGO_ENABLED=0`, `GOPROXY=off`; two SQLite-backed fixtures and 100 guarded requests including both terminal empty pages. See [continuation results and limitations](feasibility.md#sqlite-to-guarded-mcp-continuation-2026-11-23). |
| 2026-11-23 | `git diff --check -- specs/007-mcp-message-tools`; `git ls-files --eol -- specs/007-mcp-message-tools`; `git grep -E -o '^[-] \[[ xX]\] T[0-9]{3}' -- specs/007-mcp-message-tools/tasks.md` with PowerShell sequential ID validation | Exit 0 for all three checks: no whitespace errors, 10 LF files, and 36 sequential task IDs. Documentation checks only for the context lookahead, unavailability and WAL acceptance refinements; no new runtime evidence. |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_mcp_overlay.json ./internal/mcp -count=3 -timeout 60s -v -run '^TestScratch007ContextRowCap$'` | Exit 0; three runs each charged exactly 2,000 row touches and matched both renderings against seeded history. |
| 2026-11-23 | `go run -mod=readonly D:/tmp/kilo/wa007_batch_probe.go D:/tmp/kilo/wa007_seek_fold_probe.go` | Exit 0; 12 search/limit trials, six filtered batch checks, four plan-only checks and one standalone cancellation check. |
| 2026-11-23 | `go run -mod=readonly D:/tmp/kilo/wa007_fold_bound_probe.go` | Exit 0; 1,112,064 singleton scalars and eight long patterns measured, not a proof of an arbitrary-string bound. |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_mcp_overlay.json ./internal/mcp -count=1 -timeout 60s -v -run '^TestScratch007SQLiteGuardedContext$'` | Exit 0; two fixtures, 100 guarded requests. |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_wal_overlay.json ./internal/store -count=3 -timeout 60s -v -run '^TestScratch007CandidateWAL$'` | Exit 0; three admitted reader cancellation and writer fence runs. |

The five scratch commands above ran on Windows/amd64 with `CGO_ENABLED=0` and `GOPROXY=off`. See [measurements and remaining work](feasibility.md#context-row-cap-and-probe-reruns-2026-11-23). Linux and macOS were not run.

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-23 | `go run -mod=readonly D:/tmp/kilo/wa007_unicode_proof.go` | Exit 0; measured the two scalar inequalities across all 1,112,064 Unicode scalars and checked six repeated-scalar cases on the Go 1.27.4 Unicode 17.0 tables. |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_integrated_overlay.json ./internal/mcp -count=1 -timeout 120s -v -run '^TestScratch007Integrated$'` | Exit 0 on two runs after mapping the scratch work-limit sentinel to `store.ErrResourceLimit`. The final run (66.368 s package time) covered six search/limit requests, 100 guarded continuation requests, two sets of eight writer commits and sampled process memory. Earlier attempts failed the work-limit assertion; no production code changed. |

Both commands ran on Windows/amd64 with `CGO_ENABLED=0` and `GOPROXY=off`. See the [integrated results and remaining gaps](feasibility.md#integrated-admission-and-fold-envelope-2026-11-23).

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-23 | `go run -mod=readonly D:/tmp/kilo/wa007_lookup_close.go` | Exit 0; exact pair/no-fallback, bounded ID-only 2/100/101/102 cases, 20 deep-offset alias matches and latest direct/group reference comparison. |
| 2026-11-23 | `go test -mod=readonly -overlay D:/tmp/kilo/wa007_integrated_overlay.json ./internal/mcp -count=1 -timeout 120s -v -run '^TestScratch007Integrated$'` | Exit 0; 61.074 s package time; eight search/limit requests and 100 guarded continuation requests. Fixed 256-key batch and scratch-only 32 MiB hard SQLite process limit. |

Both runs used Windows/amd64 with `CGO_ENABLED=0` and `GOPROXY=off`. See [fixed-ceiling evidence](feasibility.md#fixed-ceiling-verification-2026-11-23) for the design allocation bounds and limitations.

The owner approved the S6 duplicate-ID overflow rule on 2026-11-23: list all matching chats for 2–100; at 101 or more, require `chat_jid` without returning a chat list or selecting a message. See the [S6 amendment](../../docs/specification.md#85-storage). This resolves the documentation conflict, not the feasibility or design-review gate.

## Runtime and Security Evidence

Not verified. The indexed query and driver-return feasibility checks, actual heap and reply bounds, SDK acceptance and native stdio tests are required as described in the [store contract](contracts/store-and-lifecycle.md) and [quickstart](quickstart.md). No security exception or real-account verification is in scope.

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 2m -run '^TestToolMessage(Fixture\|Reader)'` | Exit 0; three synthetic fixture/scaffolding tests on Windows/amd64 (package 0.380 s). Snapshot isolation, local alias resolution, field-size fixtures and current facade method names were checked; message projection validation and feature lifecycle remain unverified. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 2m -v -run '^TestToolMessage'` | Exit 0; eight top-level synthetic tests, including two projection-validation subtests, Windows/amd64 (package 2.308 s). Checks include snapshot return after cancellation, nearest-neighbour ordering and the 2,000-touch cap. These are foundation tests, not delivered tool acceptance. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 2m -v -run '^(TestMessageArgumentsFoundation\|TestMessageProjectionAndSizingFoundation\|TestResultHTMLMetacharacterCharges\|TestResultSDKEncodingEscapedBudget\|TestCommonSchemaDefaults)$'` | Exit 0; five top-level tests on Windows/amd64 (package 0.515 s); strict-argument and output-sizing helpers and existing encoding/default regressions. Message handlers and integration were not exercised. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 2m -run '^(TestToolMessage\|TestGateSeventeenthWaits\|TestGateWaiterCap\|TestReaderPoolConnections\|TestToolGatePreservesOrdinaryCapacity\|TestToolFacadeRetainsLease\|TestToolStoreDeadlineIsCancellationNotRelease)$'` | Exit 0; five exact-name existing capacity and deadline tests, Windows/amd64 (package 5.485 s). The two prefix alternatives did not select tests due to the final anchor. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 2m -run '^(TestGuardAdmissionAndRetirement\|TestGuardWriteFailureRetainsCredit\|TestGuardHeldWriteKeepsToolCredit\|TestGuardToolDeadlineCancelsSDKHandler\|TestGuardDeadlineRetainsPendingAndCredit\|TestGuardSDKBlockedOutputDeadlineRetainsCredit\|TestFramingWriteCancellationAndFailure)$'` | Exit 0; seven existing guarded-transport tests, Windows/amd64 (package 10.336 s). Not end-to-end evidence for message tools. |
| 2026-11-23 | `CGO_ENABLED=0 go vet ./internal/store ./internal/mcp` | Exit 0 on Windows/amd64, including after the final projection validation refinement; full feature and platform verification remain outstanding. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 3m -v -run '^TestToolMessage'` | Exit 0; 11 top-level synthetic tests on Windows/amd64 (package 4.710 s). The 2,000-touch context case took 3.44 s. A first attempt with an inlined repeated byte-budget expression failed the 5 s store cancellation deadline; the bounded one-row materialized budget expression passed on rerun. This is not evidence for the 50-anchor acceptance fixture or native Linux/macOS. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 3m -v -run '^(TestMessage\|TestGuardFourOutstanding\|TestGuardDeadlineRetainsPendingAndCredit\|TestGuardToolDeadlineCancelsSDKHandler\|TestGuardSDKBlockedOutputDeadlineRetainsCredit\|TestFramingWriteCancellationAndFailure\|TestFramingWriteCompletionAndGuardStop)'` | Exit 0; 14 top-level tests on Windows/amd64 (package 10.383 s), including strict argument, sizing, synthetic message-shaped transport credit, deadline, failed-output and protocol-redaction tests. New message handlers were not registered or exercised end-to-end. An initial strict-argument test incorrectly expected JSON `1.0` to be rejected; corrected to the JSON Schema integer semantics before this passing run. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 3m -v -run '^(TestToolMessage.*\|TestGateSeventeenthWaits\|TestGateWaiterCap\|TestReaderPoolConnections\|TestToolGatePreservesOrdinaryCapacity\|TestToolGateExclusiveAndRetainsSlot\|TestToolStoreDeadlineIsCancellationNotRelease\|TestToolFacadeRetainsLeaseUntilReadReturns\|TestToolFacadeRetainsLeaseDuringConnectionCleanup)$'` | Exit 0; 22 top-level tests on Windows/amd64 (package 10.430 s), covering exact-pair snapshot and sizing, context reduction, projection budget boundaries, admission capacity and cancellation versus actual return. The 2,000-touch fixture took 3.20 s. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 3m -v -run '^(TestMessage.*\|TestGuardFourOutstanding\|TestGuardDeadlineRetainsPendingAndCredit\|TestGuardToolDeadlineCancelsSDKHandler\|TestGuardSDKBlockedOutputDeadlineRetainsCredit\|TestFramingWriteCancellationAndFailure\|TestFramingWriteCompletionAndGuardStop\|TestFramingSlowConsumerDeadline)$'` | Exit 0; 18 top-level tests on Windows/amd64 (package 15.924 s). Includes a test-only guarded SDK handler using the synthetic SQLite exact-pair facade and synchronous MCP sizing policy, plus shared transport credit and deadline tests. It does not exercise the future production message handlers. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 5m` | Exit 0; full MCP package regression suite on Windows/amd64 (package 97.775 s); test count not captured by the non-verbose run. |
| 2026-11-23 | `CGO_ENABLED=0 go vet ./internal/store ./internal/mcp`; `gofmt -l` on the seven changed Go files; `git diff --check` | Exit 0 for each on Windows/amd64; no formatting or whitespace output. Feature acceptance, other native platforms and complete resource-share measurements remain unverified. |

## Phase 3 Progress (2026-11-23)

Phase 3 has synthetic store and SDK evidence, but the feature is not verified for delivery. All runs used synthetic fixtures on Windows/amd64 with `CGO_ENABLED=0`; no real account was used. Earlier deadline failures are retained below. Prepared-statement reuse and the large-field validation fast path were added after those failures.

| Command | Result |
| --- | --- |
| `go test ./internal/mcp -run '^TestMessage(List\|Budget)' -count=1 -timeout 5m -v` | Exit 0; eight top-level tests, including continuation, context-row accounting, maximum offset and SDK argument/representation cases; package 162.324 s. Run before the final one-match search adjustment. |
| `go test ./internal/mcp -count=1 -timeout 5m` | Exit 0; full MCP package, 257.245 s; test count not captured. Run before the final search query changes. |
| `go test ./internal/store -run '^TestToolMessageSearch' -count=1 -timeout 3m -v` | Latest run exit 1: 7 top-level tests, with `TestToolMessageSearchOlderAfterLargeFields` and the `not-present` 400,000-row subtest hitting the store's 5-second deadline. Sparse 400,000-row subtest passed in 4.47 s. Earlier runs passed both scale subtests at 4.40 s and 4.39 s, and the large-field fixture separately, but that does not establish reliable deadline compliance. |
| `go vet ./internal/store ./internal/mcp` | Exit 0 before final one-match search adjustment. |
| `git diff --check` | Exit 0 before final one-match search adjustment. |
| `go test ./internal/store -run '^TestToolMessageSearch' -count=1 -timeout 3m -v` | Exit 0; seven top-level tests. Sparse and empty 400,000-row searches took 3.02 s and 3.97 s; older match after 128 maximum-size nonmatches passed. Run before the final key-materialization guard. |
| `go test ./internal/store -run '^TestToolMessageSearch(Keys\|RejectsOversizedKey\|Scale)$' -count=1 -timeout 3m -v` | Exit 0 after the key-materialization guard; three top-level tests, 400,000-row sparse 3.24 s and empty 3.03 s. Includes the 400,002-row exhaustion check. |
| `go test ./internal/mcp -run '^TestMessage(List\|Budget)' -count=1 -timeout 5m -v` | Exit 0; eight top-level tests, package 50.071 s. Run before the final key-materialization guard and before explicit request timing was added. |
| `go test ./internal/mcp -run '^TestMessageBudgetContinuation$' -count=1 -timeout 3m -v` | Exit 0 after the guard; one top-level test, two 50-anchor continuation fixtures. Initial guarded default-context reply, including test result validation, took 435.1164 ms ordinary and 234.1418 ms escape-heavy. Both recovered all 50 identities. |
| `go vet ./internal/store ./internal/mcp`; `git diff --check` | Exit 0 for each after the key-materialization guard. |

The independent review identified oversized-key materialization, malformed UTF-8 and batch overscan risks. The current implementation includes fixes and synthetic regressions, but the feature still needs its owner code review. Native Linux/macOS, delivered SDK 400,000-row timing, and the remaining feature-wide verification have not been run. The existing `.gitignore` modification predates this work and was not changed.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-22 | 0d4c7fa | CONCERNS | |
| design | 2026-11-23 | 7d150f9 | PASS | |
| design | 2026-11-23 | e45dbd6 | PASS | |
| code | 2026-11-24 | ab8555e | BLOCK | |
| design | 2026-11-27 | 35b447d | PASS | |

- **Code BLOCK (`ab8555e`):** The native macOS named suite did not return the required sparse search result within the 5-second store deadline. The judge reviewed the committed diff and verification record; its local Go commands were denied, so the verdict relies on the recorded CI evidence and source review, not a judge rerun. It also flagged the one-result first-page search's one-row refills as an unverified scale risk, not a separate blocker. The unrelated local `.gitignore` change was excluded from this committed review.

- **Current design PASS (`e45dbd6`):** Covers the committed Verified feasibility evidence and fixed ceilings. At delivery, run the 400,000-row fixtures early on native runners because of limited headroom under the 5-second store deadline. Confirm the production implementation does not retain rejected projections or batch text bodies. Neither observation blocks design approval.

- **Earlier design PASS (`7d150f9`):** Did not clear implementation because feasibility was pending at the time. The row-cap and expansion-heavy Unicode follow-ups were addressed in the subsequent scratch evidence.

- **Implementation gate check (2026-11-23):** Piped a synthetic `PreToolUse` event for `Skill(speckit-implement)` into `.claude/hooks/owner-review-gate.sh` using Git Bash. Exit 0 with no denial or other output. This checked the recorded verdict and committed evidence without starting implementation.

## Phase 4 Context Tests (2026-11-23 UTC)

- Windows/amd64, synthetic fixtures only: `$env:CGO_ENABLED='0'; go test ./internal/store ./internal/mcp -run 'TestMessageContext|TestMessageExact' -v -count=1 -timeout 10m` exited 0. Four named tests passed: `TestMessageContext`, `TestMessageContextLookup`, `TestMessageContextNeighbours`, and `TestMessageContextSDK`. Package times were 0.932 s and 1.659 s respectively.
- The complete `internal/mcp` regression run with `CGO_ENABLED=0`, `go test ./internal/mcp -json -count=1 -timeout 10m`, exited 0 with 474 passing test events including subtests and zero failures.
- `gofmt -l` on the Phase 4 Go files printed nothing; `git diff --check` exited 0. Native Linux and macOS controls and the feature-wide gates remain unverified in this phase.

## Phase 5 Interaction Tests (2026-11-23 UTC)

- Windows/amd64, synthetic fixtures only: `$env:CGO_ENABLED='0'; go test ./internal/store ./internal/mcp -run '^(TestToolMessageInteraction.*|TestMessageInteraction.*)$' -v -count=1 -timeout 5m` exited 0. Seven named tests passed: `TestToolMessageInteraction`, `TestToolMessageInteractionUnverified`, `TestToolMessageInteractionSnapshotAndPolicies`, `TestToolMessageInteractionSeekPlans`, `TestMessageInteractionStoreSDK`, `TestMessageInteractionSDK`, and `TestMessageInteractionLimits`. Package times were 0.933 s and 2.333 s.
- `$env:CGO_ENABLED='0'; go test ./internal/mcp -skip '^TestMessageInteractionStoreSDK$' -count=1 -timeout 10m` exited 0 in 156.872 s; the omitted SQLite-backed SDK test passed in the focused run above. `$env:CGO_ENABLED='0'; go vet ./internal/store ./internal/mcp` exited 0.
- An earlier broader store test run failed both existing `TestToolMessageSearchScale` subcases at the five-second store deadline while work was concurrent; its exact command was not retained. The scale test was not rerun here and remains a feature-wide verification concern. An initial integrated run also failed because the new SDK fixture's message was older than an existing shared fixture message; after correcting its timestamp, the final focused run passed.
- Formatting checks on Phase 5 Go files printed nothing; `git diff --check` exited 0. Native Linux and macOS controls and the feature-wide gates remain unverified.

## Phase 6 Preliminary Evidence (2026-11-23 UTC)

- Windows/amd64, synthetic fixtures: `$env:CGO_ENABLED='0'; go test ./internal/store -run '^TestToolMessageLifecycle' -v -count=1 -timeout 5m` exited 0: five named tests passed in 1.972 s. A real pinned message snapshot crossed 135,725,192 WAL bytes after concurrent ingest; the fence held a later writer until actual reader return, then a checkpoint restored admission.
- The first guarded SDK acceptance run (`go test ./internal/mcp -run '^TestMessageAcceptance' -count=1 -v -timeout 20m`, `CGO_ENABLED=0`) exited 1: both 400,000-row searches timed out around 5.01 s while other fixtures ran concurrently, and closed-store errors lacked local-session guidance. After adding guidance, the isolated rerun `$env:CGO_ENABLED='0'; go test ./internal/mcp -run '^TestMessageAcceptance(ClosedStore|Scale)$' -v -count=1 -timeout 10m` exited 0: the three closed-store subtests passed, sparse and empty requests took 4.559 s and 4.900 s through reply receipt, respectively. The tests were subsequently renamed with the quickstart's `TestMCPMessage` prefix. Timing under concurrent load remains a concern.
- Windows native message pipe probe (`CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -run '^TestMessageStdioNative$' -count=1 -v -timeout 90s`) passed one named test and three subtests before its subsequent `TestMCPMessage` prefix rename. It does not yet establish absence of all tool-originated files, writes and recovery; non-Windows native execution remains unverified.
- The documented Windows acceptance command `$env:CGO_ENABLED='0'; go test -count=1 -timeout 5m -v ./internal/store ./internal/mcp ./cmd/whatsapp-mcp -run '^Test(MCPMessage|ToolMessage)'` exited 1. Named tests ran in all three packages: store passed in 67.126 s, and `TestMCPMessageStdioNative` passed its three subtests in the command package (24.594 s package time). The MCP package failed in 66.206 s because both `TestMCPMessageAcceptanceScale` requests timed out at approximately 5.001 s. This was a concurrent package run; it does not establish the cause of the slowdown. The scale result does not satisfy the acceptance gate.
- `$env:CGO_ENABLED='0'; go vet ./...` and `$env:CGO_ENABLED='0'; go mod verify` each exited 0; `gofmt -l .` and `git diff --check` printed nothing and exited 0. These checks do not replace the remaining full regression, native-matrix, vulnerability, or owner-review gates.
- After the initial Phase 6 commit, `$env:CGO_ENABLED='0'; go test ./internal/mcp -run '^TestMCPMessageBudgetContinuation$' -count=1 -v -timeout 10m` exited 0 on Windows/amd64: both maximum-field fixtures recovered all 50 identities through continuations. The first guarded replies, with the default limit omitted, took 502.846 ms (ordinary) and 265.833 ms (escape-heavy); the test took 34.471 s including setup and continuations.
- Windows/amd64: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 30m` exited 1. `TestMCPStdioOfflineRead` still expected five tools; both guarded SDK scale searches and the store scale search also timed out. The discovery expectation was updated to eight; `$env:CGO_ENABLED='0'; go test ./cmd/whatsapp-mcp -run '^TestMCPStdioOfflineRead$' -count=1 -v -timeout 5m` then passed all four subtests. Other failures remain relevant until the full suite passes.
- The ASCII validation shortcut was replaced with the existing four-field UTF-8 validator after source and fold length checks, without changing work ceilings. `$env:CGO_ENABLED='0'; go test ./internal/mcp -run '^TestMCPMessageAcceptanceScale$' -count=1 -v -timeout 10m` exited 0 in isolation: sparse and empty replies took 2.674 s and 2.973 s. A focused store regression covering aliases, malformed UTF-8, oversize keys, large fields and interaction ordering exited 0 (`go test ./internal/store -run '^(TestToolMessageSearchKeys|TestToolMessageSearchRejects.*|TestToolMessageSearchFullPageBeforeLargeRemainder|TestToolMessageSearchOlderAfterLargeFields|TestToolMessageInteraction.*)$' -count=1 -v -timeout 10m`, `CGO_ENABLED=0`).
- The documented concurrent Windows acceptance command was rerun with that change and exited 1: the store package and native pipe test passed, as did the empty SDK scale search (3.069 s) and both maximum-field default-context continuations (initial replies 855.735 ms and 237.390 ms); the sparse SDK scale search still timed out at 5.001 s. Performance headroom remains insufficiently verified and the acceptance gate remains open.
- Before the UTF-8 query change, five CGO-free cross-builds (Windows/amd64, Linux/amd64, Linux/arm64, Darwin/amd64 and Darwin/arm64) passed; `CGO_ENABLED=0 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` exited 0 with no vulnerabilities found. Rerun both on the final code; cross-builds are not native runtime evidence.

## Phase 6 Follow-up (2026-11-23 UTC)

All runs used `CGO_ENABLED=0` and synthetic fixtures. No real account was used.

| Revision and environment | Check | Result |
| --- | --- | --- |
| `b92520c35ac369f028fd15bde2d56b89244a002b`, [CI run 45249980803](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45249980803) | `go test -timeout 30m ./...` on Windows/amd64, Linux/amd64 and macOS/arm64 | Exit 0 on all three, ten passing packages per runner; test names and fixture timings were not logged by this invocation. Separate native diagnostic-shutdown and integrated pipe steps passed on Linux and macOS. |
| Same CI run, Linux/amd64 check job | `gofmt`, `go vet ./...`, `go mod verify`, `govulncheck`, Unix data-directory denial, five target builds | All steps passed; no vulnerabilities found. Scheduled heavy store job was skipped on this push. |
| Working tree based on `b92520c`, Windows/amd64 | `go test -count=1 -timeout 5m -v ./internal/store ./internal/mcp ./cmd/whatsapp-mcp -run '^Test(MCPMessage\|ToolMessage)'` | Exit 0: 30 store, four MCP and one native pipe top-level tests; all three pipe subtests passed. Both 400,000-row SDK requests returned exact results: sparse 3.395 s, empty 2.869 s. The pinned snapshot test crossed 135,725,192 WAL bytes. |
| Same Windows working tree | `go test ./... -count=1 -timeout 30m -json` | Exit 0: ten packages passed; 720 store, 485 MCP and 305 command passing test events including subtests. Sparse/empty guarded replies took 3.828 s and 4.627 s. Default 50-anchor ordinary/escaped first replies took 518.499 ms and 309.609 ms; both continuation tests recovered all 50 identities. |
| Same Windows working tree | `gofmt -l .`; `go vet ./...`; `go mod verify`; `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`; `GOOS=<os> GOARCH=<arch> go build -o /dev/null ./cmd/whatsapp-mcp` | All exited 0; no formatting output, modules verified, no vulnerabilities found. Windows/amd64, Linux/amd64, Linux/arm64, Darwin/amd64 and Darwin/arm64 builds passed; cross-builds are not native runtime evidence. |

The strengthened native pipe fixture checks fake-owner pairing calls, data-directory entries between initialization and live replies, and the final file names and hashes across offline, cancellation and stalled-output cases. These observations cover the fixture's data directory; they are not a system-wide filesystem trace. The earlier scale timeouts remain relevant: even the passing Windows full run left less than half a second before the 5-second store deadline in the empty case.

## Native Acceptance (2026-11-24 UTC)

[CI run 45296592563, attempt 1](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45296592563/attempts/1) tested `ba3b26ff5c82c1bf122e85f0ec018e9885979746` with `CGO_ENABLED=0` and synthetic fixtures only. The named command was `go test -count=1 -timeout 5m -v ./internal/store ./internal/mcp ./cmd/whatsapp-mcp -run '^Test(MCPMessage|ToolMessage)'`. Each passing named run confirmed 30 store, four MCP and one command top-level test, plus 33 passing subtests (no skips). These included actual WAL drain, guarded SDK scale and context continuation, and native stdio offline, cancellation and stalled-output cases.

| Native runner (UTC start) | Named command | Guarded 400,000-row replies (sparse / empty) | First default-context replies (ordinary / escape-heavy) |
| --- | --- | --- | --- |
| Windows/amd64, 00:28:53 | Exit 0, 35 top-level passes | 2.225 s / 2.189 s | 243.559 ms / 170.858 ms |
| Linux/amd64, 00:26:14 | Exit 0, 35 top-level passes | 2.051 s / 1.777 s | 238.540 ms / 138.871 ms |
| macOS/arm64 | Skipped after the full-suite failure | Full-suite attempt returned `timeout` at 5.002 s / 5.001 s; neither was a valid reply | Not measured by the named command in this attempt |

The full `go test -timeout 30m ./...` command exited 0 on Windows and Linux and exited 1 on macOS (`TestMCPMessageAcceptanceScale`, both subcases). The separate native diagnostic-shutdown, integrated pipe, and unsafe-run controls still passed on macOS. CI's check job passed formatting, vet, module verification, the vulnerability scan, the Unix privacy check and all five cross-builds. Cross-builds are not native runtime evidence. No real account was used.

[Attempt 2](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45296592563/attempts/2) repeated the macOS full-suite failure on the same commit: exit 1, with guarded sparse and empty requests returning `timeout` at 5.002 s and 5.002 s. Named acceptance was again skipped. The workflow now runs named acceptance even after a full-suite failure and serializes package execution for macOS full regression; it does not change the named acceptance command, within-package concurrency tests, fixtures or production deadlines.

[CI run 45298905633](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45298905633) tested `eb0657a8dba00d86a2c25d2860d200bd71adf7c3` under the same synthetic, CGO-free acceptance command:

| Native runner (UTC start) | Named command | Guarded 400,000-row replies (sparse / empty) | First default-context replies (ordinary / escape-heavy) |
| --- | --- | --- | --- |
| Linux/amd64, 00:43:17 | Exit 0, 35 top-level and 33 subtest passes | 2.120 s / 1.875 s | 242.206 ms / 136.348 ms |
| Windows/amd64, 00:46:07 | Exit 0, 35 top-level and 33 subtest passes | 2.090 s / 2.074 s | 252.966 ms / 156.106 ms |
| macOS/arm64, 00:48:34 | Exit 1, 34 top-level and 32 subtest passes; 1 top-level and 1 subtest failure | `timeout` at 5.003 s / correct empty reply in 3.344 s | 209.375 ms / 134.951 ms |

On macOS `go test -count=1 -p 1 -timeout 30m ./...` passed; the native pipe, shutdown, and integrated controls passed. The separate unsafe-run summary step was skipped in this run after named acceptance failed; it passed on macOS in attempt 1 at `ba3b26f` with identical Go test and production code. Windows and Linux full regressions and the CI check job passed. **T033 remains open:** the exact named command ran on all three platforms, but the macOS sparse fixture did not return a valid result under that command. No other OS/architecture received native acceptance coverage.

### Search Aggregation Follow-up

On Windows/amd64 (2026-11-24 UTC, `CGO_ENABLED=0`), `go test -count=1 -timeout 5m -v ./internal/mcp -run '^TestMCPMessageAcceptanceScale$'` exited 1 on `ab8555e` (sparse timed out at 5.002 s; empty passed in 4.421 s). Removing the intermediate SQLite table for the length checks, while retaining bounded key materialization and checking the aggregate charge before UTF-8 validation and matching, gave exit 0 on the same command (one top-level test, both subtests passed; sparse 2.866 s, empty 3.007 s). These are individual observations, not a throughput guarantee.

[CI run 45301552497](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45301552497) tested `b23357c165ebd1709f24b8da2622a196d1f8990a` with the same named command, `CGO_ENABLED=0`, and synthetic fixtures:

| Native runner (UTC start) | Named command | Guarded 400,000-row replies (sparse / empty) | First default-context replies (ordinary / escape-heavy) |
| --- | --- | --- | --- |
| Linux/amd64, 01:03:32 | Exit 0, 35 top-level and 33 subtest passes | 1.791 s / 1.553 s | 238.917 ms / 130.660 ms |
| Windows/amd64, 01:03:52 | Exit 0, 35 top-level and 33 subtest passes | 0.940 s / 0.889 s | 136.924 ms / 94.063 ms |
| macOS/arm64, 01:09:57 | Exit 1, 34 top-level and 32 subtest passes; 1 top-level and 1 subtest failure | `timeout` at 5.002 s / correct empty reply in 2.314 s | 188.179 ms / 94.340 ms |

Windows and Linux full regressions and CI's check job passed. The macOS serialized full regression also failed: sparse and empty requests timed out at 5.002 s and 5.002 s. CI restores the standard regression command because serializing packages did not reliably clear this failure, and still runs named acceptance independently. T033 and T036 remain open. A synthetic PR-create hook event on 2026-11-24 returned `deny` for the recorded code BLOCK; no actual PR operation was performed.

The [native macOS job at `594425c`](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45303570180/job/138141806181) reproduced both guarded search timeouts in named acceptance (5.005 s sparse, 5.003 s empty; exit 1), plus the store-only sparse timeout. On Windows/amd64 on 2026-11-24, `CGO_ENABLED=0 go test -count=1 -timeout 5m -v -cpuprofile D:/tmp/kilo/feature007-cpu.pprof -o D:/tmp/kilo/feature007-mcp.test.exe ./internal/mcp -run '^TestMCPMessageAcceptanceScale$'` exited 0 (one top-level and two subtest passes, 3.563 s sparse and 3.540 s empty). SQLite stepping accounted for 6.23 s of the 6.50 s of sampled CPU in the batch search path across both requests. The Windows profile does not establish the macOS-specific cause.

### Single-Pass Candidate (2026-11-24 UTC)

[CI run 45307912654](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45307912654) ran the unchanged named acceptance and full regression commands with `CGO_ENABLED=0` at `c411ac96efbe5575f95f1f5bee5856e7375aa348`. The candidate uses a single indexed walk where worst-case batch bytes fit, with field guards before body reads and the exact-charge fallback otherwise. The Windows focused `go test -count=1 ./internal/store -run '^TestToolMessageSearch(BatchGuardsBeforeBody|ReservedSeekPlans|RejectsUnverifiedPage)$'` passed three top-level cases, including production plan checks for six index shapes. One Windows candidate acceptance run passed before the final guard simplification; a later local run failed the store scale, context row-cap and sparse guarded cases. These are not reliable deadline guarantees.

| Native runner | Named acceptance | Guarded 400,000-row replies (sparse / empty) | Full regression |
| --- | --- | --- | --- |
| Windows/amd64 | Exit 0; 37 top-level and 39 subtest passes | 2.128 s / 2.121 s | Exit 0 |
| Linux/amd64 | Exit 0; 37 top-level and 39 subtest passes | 1.018 s / 1.013 s | Exit 0 |
| macOS/arm64 | Exit 1; 36 top-level and 38 subtest passes | `timeout` at 5.002 s / correct empty reply in 3.994 s | Exit 1; sparse guarded search timed out at 5.003 s |

CI's check job passed. On the same macOS runner, the separate `go test -count=1 -v -timeout 5m -cpuprofile "$profile" -o "$binary" ./internal/mcp -run '^TestMCPMessageAcceptanceScale$'` exited 0 (one top-level and two subtest passes, 4.491 s sparse, 2.827 s empty). `go tool pprof -top -cum -focus=searchMessageBatch` showed 2.92 of 3.19 sampled search CPU seconds in the SQLite `pread` stack. This identifies the dominant sampled operation in the isolated diagnostic, not the wall-time breakdown of the failed acceptance or whether most reads came from the database or WAL. No deadline or acceptance limit changed. T033 and T036 remain open.

### Backing-File Diagnostic (2026-11-24 UTC)

[Native macOS/arm64 job 138176239366](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45313981225/job/138176239366) ran at `252045ca8a37c7f8a1b1e61c8e2da6c34eec079b`. The opt-in command `CGO_ENABLED=0 go test -count=1 -timeout 8m -v -tags messageiodiagnostic ./internal/mcp -run '^TestSQLiteScaleBackingReads$'` exited 0 (one top-level test, four subtests, eight measured requests). Each subtest independently seeded 400,000 synthetic rows via the same writer as acceptance, then issued the same guarded SDK query twice. Only the diagnostic checkpointed copy ran `PRAGMA wal_checkpoint(TRUNCATE)`. The hook counted actual bytes returned by VFS `pread` through reply receipt; it does not count memory-mapped reads or distinguish physical disk I/O from OS cache hits. A timed-out row is a partial read ledger, not a successful search.

| Search / setup | Trial | Reply | Main reads / bytes | WAL reads / bytes |
| --- | --- | --- | --- | --- |
| Sparse / post-ingest | First | Exact, 4.547 s | 25,655 / 105,082,880 | 141 / 577,536 |
| Sparse / post-ingest | Repeat | Exact, 2.690 s | 25,650 / 105,062,400 | 140 / 573,440 |
| Sparse / checkpointed | First | Exact, 4.771 s | 25,796 / 105,660,416 | 0 / 0 |
| Sparse / checkpointed | Repeat | Exact, 4.203 s | 25,790 / 105,635,840 | 0 / 0 |
| Empty / checkpointed | First | Exact, 4.855 s | 25,742 / 105,439,232 | 0 / 0 |
| Empty / checkpointed | Repeat | Timeout, 5.003 s | 20,449 / 83,759,104 | 0 / 0 |
| Empty / post-ingest | First | Timeout, 5.002 s | 24,421 / 100,028,416 | 139 / 569,344 |
| Empty / post-ingest | Repeat | Timeout, 5.001 s | 16,337 / 66,916,352 | 138 / 565,248 |

There were no unattributed reads. Main-database reads accounted for more than 99.4% of read bytes in the completed post-ingest cases. Checkpointing eliminated measured WAL reads, did not reduce the completed sparse request's total bytes, and did not reliably prevent timeouts. This is evidence against changing checkpoint policy for this fixture. Run-to-run variance and diagnostic-hook overhead limit timing comparisons; first and repeat requests were not forced into cold/warm OS cache states.

The same tagged diagnostic passed under WSL2 Linux/amd64 (`-buildvcs=false`, `TMPDIR=/var/tmp`, exit 0, one top-level/four subtest passes; seven exact replies and one checkpointed timeout). Its completed requests also read roughly 105 MB from the main database, with less than 0.6 MB from WAL or none after checkpointing. Tagged `go vet` on WSL2 and the `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -tags messageiodiagnostic -c` cross-build both exited 0. The latter was compile-only.

The unchanged native named acceptance in this CI run still failed on macOS (exit 1; sparse `timeout` at 5.002 s, exact empty reply in 3.990 s); full regression failed there too. Windows and Linux passed their CI jobs. The diagnostic is not acceptance evidence. Production code, fixture inputs and deadlines were unchanged; T033 and T036 remain open. The next candidate should reduce main-database page reads or their overhead within the existing resource bounds, measured against this baseline before changing production policy.

### Mapped-Read Candidate (2026-11-26 UTC)

The candidate sets `PRAGMA mmap_size=536870912` on read-only connections through a connection hook that exists only under `-tags messageiodiagnostic`. Production connection settings, queries, fixtures and deadlines are unchanged. The diagnostic now compares it with the current settings over three rounds of the sparse and empty searches, alternating which arm runs first. Each of the twelve cases seeds its own 400,000 synthetic rows and issues the same guarded SDK request twice. It records time spent inside `pread`, process fault, block and CPU counters, resident memory (`ps`) and the SQLite heap peak. The earlier checkpointed arm was removed.

Local checks before the push, `CGO_ENABLED=0`: `gofmt -l .`, tagged `go vet` and `go test -c` for darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 exited 0. The first WSL2 Linux/amd64 run exited 1 because the harness expected all four reader connections to be open after startup; the pool opens them on demand. After that correction, tagged vet for darwin/arm64 and linux/amd64, untagged vet of `./internal/mcp` on Windows, and one WSL2 round (`-run '^TestSQLiteScaleBackingReads$/.*/round-1'`) exited 0.

[CI run 45893820917](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45893820917) tested `23610eb123bfe94d28fa519cfa4013cc7ee913c7`. On native macOS/arm64 (image `macos-26-arm64` 20261117.0516.1, Go 1.27.4) the command `CGO_ENABLED=0 go test -count=1 -timeout 8m -v -tags messageiodiagnostic ./internal/mcp -run '^TestSQLiteScaleBackingReads$'` exited 0: one top-level test, twelve subtests, 24 measured requests. Every exact reply held the expected IDs in order. A passing diagnostic subtest permits a `timeout` reply.

| Query | Round | Trial | Baseline reply | Baseline main reads / MB | Baseline time in main `pread` | Mapped reply | Mapped major faults |
| --- | ---: | ---: | --- | --- | ---: | --- | ---: |
| Sparse | 1 | 1 | timeout, 5.004 s | 18,984 / 77.8 | 2.596 s | timeout, 5.003 s | 11,982 |
| Sparse | 2 | 1 | exact, 4.541 s | 25,655 / 105.1 | 2.105 s | exact, 4.474 s | 7,176 |
| Sparse | 3 | 1 | exact, 3.971 s | 25,655 / 105.1 | 1.619 s | exact, 4.198 s | 7,218 |
| Sparse | 1 | 2 | timeout, 5.002 s | 16,922 / 69.3 | 3.388 s | exact, 2.470 s | 789 |
| Sparse | 2 | 2 | exact, 4.184 s | 25,650 / 105.1 | 1.784 s | exact, 2.190 s | 10 |
| Sparse | 3 | 2 | exact, 3.916 s | 25,650 / 105.1 | 1.493 s | exact, 2.645 s | 13 |
| Empty | 1 | 1 | timeout, 5.004 s | 23,720 / 97.2 | 2.862 s | timeout, 5.001 s | 9,802 |
| Empty | 2 | 1 | timeout, 5.005 s | 20,313 / 83.2 | 3.256 s | timeout, 5.010 s | 10,404 |
| Empty | 3 | 1 | exact, 4.960 s | 25,603 / 104.9 | 2.761 s | exact, 4.899 s | 10,915 |
| Empty | 1 | 2 | timeout, 5.004 s | 20,530 / 84.1 | 3.228 s | exact, 3.015 s | 2,434 |
| Empty | 2 | 2 | timeout, 5.001 s | 22,237 / 91.1 | 3.066 s | exact, 2.596 s | 1,451 |
| Empty | 3 | 2 | timeout, 5.002 s | 19,732 / 80.8 | 3.268 s | exact, 1.946 s | 0 |

- **First requests did not improve.** Each arm returned three exact replies and three timeouts. Exact first replies took 3.971-4.960 s with the current settings and 4.198-4.899 s mapped. The named acceptance's sparse request is a first request.
- **Repeat requests improved.** Mapped repeats were all exact (6 of 6, 1.946-3.015 s); current-setting repeats returned 2 of 6 (3.916-4.184 s).
- **Read calls.** The mapped arm made no main-database `pread` calls during measured requests. WAL reads were unchanged on first requests.
- **Memory.** Resident size was 36-49 MB with the current settings. A mapped request raised it to 352-394 MB, retained while that connection stayed open (one connection was mapped in each case). The SQLite heap peak fell from 4.5-5.1 MB to 3.1-3.6 MB; the mapping is outside the SQLite heap limit.
- **Time in `pread`.** This is the first wall-clock measurement of it: 1.493-2.761 s, or 38-56%, of exact baseline requests. The other 2.2-2.4 s of those requests was outside `pread`. In timed-out requests it averaged 121-200 µs per read; at the rate of the slowest one, the full read set would need about 5.1 s. That figure is an extrapolation, and wall time inside a call includes any scheduling delay. The block-input counter read 0 for every request, so these counters do not show whether reads reached storage.
- **Page set.** `dbstat` reported 14,323 pages for `messages` and 11,415 for `messages_time`, 25,738 together. Exact baseline requests made 25,603-25,655 main and 139-141 WAL reads, so each reads about both B-trees once.

**Decision: the mapped-read candidate is rejected and was not applied to production.** It gives no headroom on a first request, raises resident memory by more than 300 MB, and would turn a read error on a mapped page into a process fault (SQLite's documented behaviour; not tested here).

The same CI run's unchanged named acceptance command:

| Native runner (UTC start) | Named command | Guarded 400,000-row replies (sparse / empty) | Full regression |
| --- | --- | --- | --- |
| Windows/amd64, 20:56:40 | Exit 0; 37 top-level and 39 subtest passes | 2.819 s / 2.711 s | Exit 0 |
| Linux/amd64, 20:53:46 | Exit 0; 37 top-level and 39 subtest passes | 2.292 s / 2.005 s | Exit 0 |
| macOS/arm64, 20:54:08 | Exit 1; 36 top-level and 38 subtest passes, 1 top-level and 1 subtest failure | `timeout` at 5.005 s / correct empty reply in 4.905 s | Exit 1; both guarded searches timed out (5.002 s and 5.004 s) |

CI's check job passed. The macOS native shutdown and integrated pipe steps passed, and the post-failure profile rerun passed (3.480 s sparse, 2.654 s empty).

Two local observations, neither acceptance evidence:

- A WSL2 Linux/amd64 CPU profile of `TestMCPMessageAcceptanceScale` at `23610eb` (exit 0; 2.29 s and 2.23 s) attributed 3.62 of 4.30 sampled search seconds to SQLite's statement execution (`_sqlite3VdbeExec` cumulative) and 0.22 s to page fetching. In the WSL2 diagnostic, time inside main-database `pread` was 66-82 ms per request.
- A scratch overlay test on Windows/amd64 (`go test -overlay`, exit 0, one test, 28.4 s; file kept outside the repository) created two covering indexes in a temporary database before seeding the same rows. The `messages` and `messages_time` page counts matched macOS exactly. An index on `(time, chat_jid, id)` plus the four text fields took 10,512 pages in ascending key order and 18,971 in descending order; SQLite planned both as covering seeks. No timing was measured and nothing in the project changed.

No small candidate tested within the current design gives repeatable macOS headroom. A design revision is proposed in the [backlog item](../../docs/backlog/007-message-search-page-reads.md) and awaits the owner's decision. T033 and T036 remain open.

### Covering-Index Prototype (2026-11-26 UTC)

The owner agreed to a diagnostic-only prototype with a bar set beforehand: every first request with the index returns exact results in 4.0 s or less, over at least three rounds. The diagnostic replaces `messages_time` in each temporary database with an ascending index on `(time, chat_jid, id, text, caption, text_fold, caption_fold)` under the same name, before seeding, and confirms through `EXPLAIN QUERY PLAN` that the search shape uses it as a covering index. Production schema and queries are unchanged, and the mapped-read arm was removed. It runs three rounds of four fixtures (sparse and empty on 400,000 short rows; one old match behind 128 newer rows with 256 KiB ASCII or expanding Unicode text and caption) under both schemas: 24 cases, 48 requests. The large rows go through the ordinary writer, so these are not the store test fixtures.

Local checks, `CGO_ENABLED=0`: `gofmt -l .`, `git diff --check`, tagged `go vet` for darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 and untagged vet of `./internal/mcp` on Windows exited 0. The full diagnostic passed on WSL2 Linux/amd64 (exit 0, 48 exact replies; not native evidence).

[CI run 45899617254](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45899617254) tested `a80f368896d60ca73b2f713b2a34c080406f2681`. On native macOS/arm64 the diagnostic command (now with `-timeout 20m`), run alone after the acceptance steps, exited 0 with 48 exact replies.

| First request, three rounds each | Today | Covering index |
| --- | --- | --- |
| Sparse | 1.370 / 1.625 / 1.877 s | 1.292 / 1.156 / 1.232 s |
| Empty | 1.420 / 3.427 / 1.456 s | 1.057 / 1.747 / 1.173 s |
| Large ASCII (range) | 0.221-0.312 s | 0.244-0.390 s |
| Large expanding (range) | 0.774-0.913 s | 0.810-1.064 s |

| Cost or resource | Today | Covering index |
| --- | --- | --- |
| Main reads, short rows | 25,598-25,655 | 10,425-10,482 |
| Time in main `pread`, short first requests | 0.130-1.352 s | 0.032-0.390 s |
| Main bytes read, large ASCII / expanding, first request | 234.0 / 567.0 MB | 353.8 / 782.7 MB |
| Database size, short rows | 433.4 MB | 428.7 MB |
| Database size, large ASCII / expanding | 133.6 / 267.2 MB | 267.4 / 538.3 MB |
| Ingest, short rows | 10.8-11.7 s | 10.5-12.7 s |
| Ingest, large ASCII | 0.97-1.30 s | 1.84-2.20 s |
| Ingest, large expanding | 2.44-2.77 s | 4.19-5.10 s |
| SQLite heap peak | 4.6-8.2 MB | 4.6-10.0 MB |
| Resident memory after requests | 73 MB at most | 73 MB at most |

**The bar was met, but the run cannot show that the index gives headroom: today's schema met it too** (slowest first request 3.427 s). This runner was far faster than the one in run 45893820917, where today's schema needed 3.9 s or timed out, on the same image version.

The same job's unchanged named acceptance command, which runs three packages at once, still failed:

| Native runner (UTC start) | Named command | Guarded 400,000-row replies (sparse / empty) | Full regression |
| --- | --- | --- | --- |
| Windows/amd64, 21:44:26 | Exit 0; 37 top-level and 39 subtest passes | 1.116 s / 0.986 s | Exit 0 |
| Linux/amd64, 21:43:07 | Exit 0; 37 top-level and 39 subtest passes | 1.131 s / 0.972 s | Exit 0 |
| macOS/arm64, 21:43:25 | Exit 1; 36 top-level and 38 subtest passes, 1 top-level and 1 subtest failure | `timeout` at 5.002 s / correct empty reply in 3.264 s | Exit 1; sparse guarded search timed out at 5.001 s |

In that job the isolated profile rerun of the same scale test passed at 1.712 s (sparse) and 2.081 s (empty), and CI's check job passed. The difference points to load from the packages running beside the search; it does not separate CPU from disk contention.

A local scratch overlay test on Windows/amd64 also counted pages by arrival order for 400,000 short rows (not committed, no timing). It exited 1 at its 9-minute timeout during the third, shuffled order; the first two orders completed:

| Arrival order | `messages` + `messages_time` today | Covering index | Saving |
| --- | ---: | ---: | ---: |
| Oldest first (the acceptance fixture) | 25,738 | 10,512 | 59% |
| Newest first | 20,674 | 18,971 | 8% |

The mix of live and history arrival in a real database was not measured.

**Next measurement, criterion set before it runs:** the diagnostic step now runs while a loop repeats the named command's other two packages (`./internal/store ./cmd/whatsapp-mcp`, same `-run` pattern) on the same runner. The same 4.0 s bar applies to the index, and a run counts as evidence for it only if today's schema misses the bar. T033 and T036 remain open.

### Covering Index Under the Acceptance Load (2026-11-26 UTC)

[CI run 45902588798](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45902588798) tested `921ab1263445fd567648f5afdc13cc4a2da88b96`. On native macOS/arm64 (3 CPUs, 7.5 GB, the same image version) the diagnostic command exited 0 while the load loop completed 16 runs of the other two acceptance packages, all exit 0. All 24 cases ran; every exact reply held the expected IDs.

| First request, rounds 1 / 2 / 3 | Today | Covering index |
| --- | --- | --- |
| Sparse | timeout / timeout / timeout | 3.512 / 3.500 / 3.258 s |
| Empty | timeout / timeout / timeout | **4.560** / 2.297 s / **timeout** |
| Large ASCII (range) | 0.247-0.405 s | 0.270-0.618 s |
| Large expanding (range) | 0.825-1.257 s | 1.375-1.753 s |

- **The run counts, and the index fails the bar.** Today's schema missed it in all six short first requests. With the index, one empty search took 4.560 s and another timed out.
- Repeat requests on short rows: today returned 2 of 6 exact (3.325 and 4.942 s); with the index all 6 were exact (1.411-3.848 s).
- With the index, time per main `pread` in short first requests ranged from 98 to 347 µs. The timed-out one spent 3.284 s in `pread` for 9,457 of about 10,400 reads.
- Large messages with the index: ingest took 1.87-4.16 s against 1.05-1.26 s (ASCII) and 6.32-7.56 s against 2.69-2.99 s (expanding).

| Native runner (UTC start) | Named command | Guarded 400,000-row replies (sparse / empty) | Full regression |
| --- | --- | --- | --- |
| Windows/amd64, 22:11:26 | Exit 0; 37 top-level and 39 subtest passes | 1.528 s / 1.537 s | Exit 0 |
| Linux/amd64, 22:10:28 | Exit 0; 37 top-level and 39 subtest passes | 1.369 s / 1.355 s | Exit 0 |
| macOS/arm64, 22:10:31 | Exit 1; 36 top-level and 38 subtest passes, 1 top-level and 1 subtest failure | `timeout` at 5.002 s / correct empty reply in 3.550 s | Exit 1; both guarded searches timed out |

In the same macOS job the isolated profile rerun, with no other package running, also failed: both searches timed out at 5.002 s (exit 1). CI's check job passed.

Across runs on one image version, today's schema ranged from 1.4 s to a timeout on the same fixture. Running the search alone helps on some runners but did not on this one.

**Decision under the agreed plan: the covering index is not adopted, and optimisation stops.** Production schema, queries, limits and deadlines are unchanged. Native macOS acceptance is not met. The next step is the owner's requirement-level decision, listed in the [backlog item](../../docs/backlog/007-message-search-page-reads.md). T033 and T036 remain open.

### macOS Runner Survey: Criterion (2026-11-27)

The owner has no Apple Silicon Mac and directed the next step to GitHub's CI. A temporary `macos-survey` job runs on the standard runners `macos-26`, `macos-15`, `macos-26-intel` and `macos-15-intel`, three separate machines each. Every machine runs `go test -timeout 30m ./...` and then the unchanged named acceptance command three times, with `CGO_ENABLED=0` and synthetic data. The covering-index diagnostic step was removed from the workflow; its file stays behind its build tag. Product code, tests, fixtures and deadlines are unchanged.

Set before the first run: a runner type qualifies only if on all three machines the full tests exit 0, all nine acceptance runs exit 0, and every guarded 400,000-row reply takes 4.0 s or less. A qualifying type is a candidate, not an accepted gate: which runner provides the macOS evidence is the owner's decision, and the survey says nothing about slower machines than those it ran on.

### macOS Runner Survey: Results (2026-11-26 UTC)

[CI run 45905237579](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45905237579) tested `d559031a73cb63522f4f636f1a2b5caad0a1ab1b`. All twelve survey machines completed both steps. Each named run is `go test -count=1 -timeout 5m -v ./internal/store ./internal/mcp ./cmd/whatsapp-mcp -run '^Test(MCPMessage|ToolMessage)'` with `CGO_ENABLED=0`; no run skipped a test. Reply times are the guarded 400,000-row SDK searches over nine runs per runner type; ranges cover the replies under 5 s.

| Runner (image) | Hardware | Machines with passing full tests | Named runs passing | Sparse replies | Empty replies |
| --- | --- | --- | --- | --- | --- |
| `macos-26` (20261117.0516.1) | arm64, 3 CPUs, 7.5 GB | 0 of 3 | 1 of 9 | 8 timeouts; one at 4.712 s | 3 timeouts; 1.783-4.921 s |
| `macos-15` (20261118.0550.1, one 20261123.0558.1) | arm64, 3 CPUs, 7.5 GB | 0 of 3 | 0 of 9 | 9 timeouts | 1 timeout; 1.759-4.183 s |
| `macos-26-intel` (20260824.0517.1) | amd64, 4 CPUs, 15 GB | 1 of 3 | 6 of 9 | 3 timeouts; 2.928-4.538 s | 2 timeouts; 2.917-4.457 s |
| `macos-15-intel` (20260824.0482.1) | amd64, 4 CPUs, 15 GB | 2 of 2 (see correction) | 8 of 9 | 2.462-4.953 s, 2 over 4.0 s | 2.496-4.923 s, 2 over 4.0 s |

- **No runner type qualifies** under the criterion set beforehand.
- `macos-15-intel` came closest. Two of its machines passed all runs with replies of at most 3.274 s. The third failed one run at the store-level sparse search (5.00 s) and answered between 4.4 and 5.0 s in that run and the next.
- On the arm64 runners the sparse SDK search, the first timed request after seeding, timed out in 17 of 18 runs. The store-level sparse search of the same size passed in 17 of those 18. The cause of that difference is not established.
- One `macos-26-intel` machine also failed `TestToolMessageContextRowCap` in two runs, besides both scale tests.
- **Correction (2026-11-27):** this row first read 3 of 3. On the third `macos-15-intel` machine, `go test ./...` reported cached results for `internal/mcp`, `internal/store` and four small packages from the restored build cache, so those tests did not run there. The named runs use `-count=1` and are unaffected. The other survey machines and the standard jobs of runs 45893820917, 45899617254, 45902588798 and 45905237579 ran `internal/mcp`, `internal/store`, `internal/wa` and `cmd/whatsapp-mcp` rather than reusing results. The conclusion is unchanged.

The same run's standard jobs: the check job passed; Windows/amd64 (22:36:22) and Linux/amd64 (22:34:40) passed full regression and the named command (exit 0; 37 top-level and 39 subtest passes; sparse / empty 1.557 / 1.524 s and 1.129 / 0.878 s). macOS/arm64 (`macos-latest`, 22:35:05) failed both: the named command exited 1 (36 top-level and 38 subtest passes; sparse timeout at 5.008 s, empty 4.485 s) and full regression timed out both searches. Its isolated profile rerun passed (2.894 / 2.058 s).

The temporary survey job was removed afterwards. Native macOS acceptance is not met on any standard GitHub runner surveyed. T033 and T036 remain open, pending the owner's [decision](../../docs/backlog/007-message-search-page-reads.md#decision-2026-11-27-the-first-choice-below).

### Smaller macOS Sizes: Criterion (2026-11-27)

With the owner's agreement, a temporary `macos-size-survey` job measures 100,000 and 200,000 messages on `macos-26` and `macos-15` (both arm64), three separate machines for each size and runner. `.github/scripts/scale_variant.py` generates copies of `TestMCPMessageAcceptanceScale` and `TestToolMessageSearchScale` at that size, keeping 12 sparse matches at the same relative positions, and `go test -overlay` substitutes them. The store copy drops its closing work-limit check, which needs more rows than one search may visit. Repository tests, product code and deadlines are unchanged. Each machine runs `go test ./... -count=1 -timeout 30m` and the named acceptance command three times with the overlay, then the unchanged command once at 400,000 messages for context. Both variants passed on Windows/amd64 before the push (100,000: the whole named command, exit 0; 200,000: the two scale tests, exit 0), which is not macOS evidence.

Set before the first run: a size qualifies on a runner type only if on all three machines the full tests exit 0, all three named runs at that size exit 0, and every guarded SDK reply at that size takes 4.0 s or less. A size is proposed to the owner only if it qualifies on both runner types. The 400,000-message run does not count either way.

### Smaller macOS Sizes: Results (2026-11-26 UTC)

[CI run 45909879856](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45909879856) tested `28310b3033b2a4917f494a204e24eabce75974bf`. All twelve survey machines ran both commands at their size; none of the ten packages of any full run reused a cached result. Images were `macos26` 20261117.0516.1 and `macos15` 20261118.0550.1 (one 200,000 machine 20261123.0558.1). Reply times are the guarded SDK searches of nine named runs per row.

| Size | Runner | Machines passing full tests | Named runs passing | Sparse replies | Empty replies | Qualifies |
| --- | --- | --- | --- | --- | --- | --- |
| 100,000 | `macos-26` | 3 of 3 | 9 of 9 | 0.559-2.137 s | 0.462-0.968 s | **Yes** |
| 100,000 | `macos-15` | 3 of 3 | 9 of 9 | 0.349-1.113 s | 0.459-1.119 s | **Yes** |
| 200,000 | `macos-26` | 2 of 3 | 8 of 9 | 1.079-4.120 s and one timeout | 0.792-2.742 s | No |
| 200,000 | `macos-15` | 2 of 3 | 8 of 9 | 1.528-4.327 s | 1.129-2.881 s | No |

- **100,000 messages meets the criterion on both arm64 runner types.** The store-level searches at that size took 0.26-0.88 s.
- At 200,000, each runner type had one machine whose full tests timed out in `TestMCPMessageAcceptanceScale`, and a reply over 4.0 s in a named run.
- The unchanged 400,000-message run on the same machines passed once in 6 on `macos-26` and never in 6 on `macos-15`; its sparse search timed out in 11 of 12. The machines were therefore not unusually fast.
- Not measured: sizes between 100,000 and 200,000, either size on Intel macOS, and any physical Mac.

**A second, unrelated failure.** On the second 200,000 `macos-15` machine, `TestMCPMessageStdioNative/cancel` failed in one named run: the reply checks passed, then the server exited 1 where the test expects 0. Its diagnostics show the expected `busy` refusal, a `list_messages` failure of category `internal`, `shutting down` 5 ms later and a `transport_failure`. The cause is not established. It was the only failure of that test among the 84 macOS named runs of the two surveys.

The same run's standard jobs: the check job passed; Windows/amd64 (23:22:27) and Linux/amd64 (23:19:44) passed full regression and the named command (exit 0; 37 top-level and 39 subtest passes; sparse / empty 1.533 / 1.523 s and 1.453 / 1.370 s). macOS/arm64 (23:19:49) failed both: the named command exited 1 (36 top-level and 37 subtest passes, both searches timed out) and the full tests timed out the sparse search.

The temporary survey job and its script were removed afterwards. No repository test, product code or requirement changed. T033 and T036 remain open, pending the owner's [decision](../../docs/backlog/007-message-search-page-reads.md#decision-2026-11-27-the-first-choice-below).

### Owner Decision and Pre-Amendment Measurements (2026-11-27)

The owner approved a macOS guarantee of 100,000 messages (spec clarification, 2026-11-27). While preparing the amendment, these defects were measured on Windows/amd64 at `743e188` with `CGO_ENABLED=0` and scratch overlay tests kept outside the repository; all data synthetic. None of this is native acceptance evidence.

| Check | Result |
| --- | --- |
| First page of one match, `searchMessageKeys` on a checkpointed table of short rows | At 20,000 rows 1.46-2.07 s against 0.10-0.17 s for page sizes 2 and 50. At 100,000 rows all four one-match runs ended at the 5-second trigger (three with the deadline error, one with a storage I/O error) while sizes 2 and 50 took 0.52-0.60 s. |
| Errors at the deadline: 1,500 searches per page size with deadlines of 0.2-20 ms, raw error logged | Page size 1: 1,454 deadline, 46 storage I/O errors. Page size 50: 1,481 and 19. The raw cause of the storage errors was `sql: statement is closed`; no keys were returned. |
| `TestMCPMessageStdioNative/cancel` failure in CI run 45909879856 | Read from code and the recorded diagnostics, not reproduced: on end of input the framed reader closes the connection, and a write whose acknowledgment is not yet received returns a closed-connection error that the guard records as a transport failure. The test closes input as soon as it has read the last reply. This path is unchanged from feature 006. |

The amendment (spec, plan, research R5, contracts, quickstart, feasibility, tasks T037-T041) needs a new design review before any of it is implemented.

### Amended Design Review (2026-11-27)

The `owner-judge` design review of `35b447d` returned PASS (row in the Owner reviews table). It checked the amendment against the code at that commit and ran no code. Low observations, none blocking, and what happens to them:

| Observation | Handling |
| --- | --- |
| The end-of-input wait must cover the write's return, and a ready acknowledgment should win over closing; a response that starts writing between the close and the guard's stop can still be reported as a failure | T040 waits on the writer's lock, which a write holds until it returns, and prefers a ready acknowledgment. The narrower window for a response that was not yet being written is unchanged. |
| A read preempted by the store reports `cancelled` where the inherited contract uses `busy` | Unchanged behaviour in the usual case; left for the owner. |
| The `timeout` error gives no advice to narrow the search | Left for the owner; changing it is a contract change. |
| A feasibility criterion mentions matches beyond 100,200 visits, which a 100,000-row fixture cannot hold | Windows and Linux cover it; wording left to avoid staling this review. |
| T034 and T035 stay ticked although their gates run again | Fresh gate results and the T041 documentation are recorded with T033. |
| The cause of the macOS cancellation failure is inferred, and one `internal` diagnostic was unexplained | The `internal` line is the cancelled request's result: the server logs the `cancelled` code in that category. T033 repeats the native cancellation test on macOS. |

The review noted that Intel macOS and physical Macs are unmeasured at 100,000 messages, and that macOS users with larger histories may receive `timeout`; both are recorded in the plan and are the owner's to accept.
