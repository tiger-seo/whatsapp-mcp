# Verification: MCP Chat and Contact Tools

## Current State

See [tasks.md](tasks.md#current-state) for the current implementation status and remaining work. The passing Windows store and focused shutdown runs are recorded under [Runtime Gates](#runtime-gates). Earlier failures and blockers are dated evidence, not current status. The [owner reviews](#owner-reviews) and the binding [isolation amendment](#owner-accepted-amendment-to-the-isolation-policy-2026-11-18) are preserved below.

The T045/T046 sequencing revision is design-only. `owner-judge` reviewed `42827be` and returned `CONCERNS`. The design amendment addresses both findings without accepting the earlier verdict, and incorporates the follow-up draft pre-review findings. A current design verdict on the committed amendment is still required; no runtime or CI workflow changes may start before the design gate clears. The `889ff65` design PASS is historical and does not cover this revision.

## Planning Evidence

| Date | Check / source | Result |
| --- | --- | --- |
| 2026-11-17 | `.specify/scripts/bash/setup-plan.sh --json`, via Git Bash | Exit 0; resolved branch `006-mcp-stdio-tools`, spec and plan under `specs/006-mcp-stdio-tools`. |
| 2026-11-17 | Feature spec, constitution 3.0.0, source section 8.11 and security review F01-F16 | Owner scope/read decisions loaded; no unresolved product clarification; no new security exception. |
| 2026-11-17 | Official SDK v1.8.0 metadata/module manifest and pinned source | Stable release/Go compatibility and API/queue/I/O behavior inspected, sources in research R1-R4. No module graph/build/vulnerability verification performed. |
| 2026-11-17 | Existing store, command lifecycle and owner/fake test seams | Query deltas, admission limits and stdio-only device-fault integration inspected, references in research R5-R7. No tests executed. |
| 2026-11-17 | `git diff --check`; `git diff --cached --check` | Both exit 0; documentation whitespace validation only. |
| 2026-11-17 | `git ls-files --eol -- specs/006-mcp-stdio-tools` | Exit 0; all nine tracked/staged feature files show `i/lf w/lf`, no CRLF. |
| 2026-11-17 | Internal read-only artifact consistency pass (not owner-judge) | Corrected seven design gaps: pre-handler credit retirement, blocked stderr, numeric-ID precision, SDK error sanitization, subscription defaults, valid page zero and notification boundary. No runtime tests or owner verdict inferred. |
| 2026-11-17 | `.specify/scripts/bash/setup-tasks.sh --json`, via Git Bash | Exit 0; resolved feature `specs/006-mcp-stdio-tools`, active task template and all four optional design-document groups. No implementation or runtime tests executed. |
| 2026-11-17 | Initial PowerShell task-format validator using `rg` | Exit 1 before validation: `rg` is not available on this shell PATH. Replaced only the search executable with Git's no-index search; no task result inferred from this failure. |
| 2026-11-17 | PowerShell assertions over `git grep --no-index -h -E '^- \[[ xX]\]' -- specs/006-mcp-stdio-tools/tasks.md` | Exit 0; 42 sequential unchecked IDs, 8 US1/6 US2 labels, 14 parallel markers, checklist format and file references present on every task; no story labels outside story phases. Artifact validation only. |
| 2026-11-17 | Internal read-only task coverage check (not owner-judge) | One missing diagnostic-emission obligation identified and added explicitly to T017/T018/T025: bounded tool/component name, time, outcome and secret-free category, including pre-dispatch and terminal faults. No circular setup dependency found; no runtime tests or approval inferred. |
| 2026-11-17 | Task-generation `git diff --check`, `git diff --cached --check` and `git ls-files --eol -- specs/006-mcp-stdio-tools` | Exit 0; task-related staged patch is whitespace-clean; all ten tracked/staged feature files show `i/lf w/lf`, no CRLF. Runtime gates remain not run. |
| 2026-11-19 | User-requested T024 store-observer split; task-marker/dependency inspection, `git diff --check`, and `git ls-files --eol -- specs/006-mcp-stdio-tools/tasks.md specs/006-mcp-stdio-tools/contracts/store-and-lifecycle.md specs/006-mcp-stdio-tools/verification.md` | 44 task entries with unchanged existing IDs/completion markers; new T043/T044 unchecked in the foundation, no T024 prerequisite for their work, and T024 retains later MCP revalidation. Both Git checks exit 0; all three documents show `i/lf w/lf`. Documentation validation only; no runtime tests, commit or renewed review performed. |
| 2026-11-19 | Diagnostic shutdown split; read-only dependency inspection, PowerShell marker comparison using `git grep -h -o -E` against HEAD and the worktree, `git diff --check HEAD`, and `git ls-files --eol -- specs/006-mcp-stdio-tools` | The topological order in `tasks.md` separates reporter seams from checkbox closure; neither T024 nor the full native matrix supplies foundation evidence. Marker comparison and Git checks exit 0 on Windows: all 44 original IDs and markers preserved, only unchecked T045/T046 added, feature files remain LF. Design validation only, not an owner verdict or runtime result. |

## Runtime Gates

### Guarded MCP Foundation (2026-11-19)

Native Windows/amd64, `CGO_ENABLED=0`, pinned SDK v1.8.0, synthetic inputs only. The guarded server now stops the connection when the SDK error latch or diagnostic-delivery latch fires. The public session wait joins SDK work before disposing of unwritten request credits, and checks both latches after joining so a disconnect cannot hide failure. This is a package-level result; the command-to-store drain remains T024 work. No production tool or real account was used.

**No-response paths on the pinned SDK:** Only requests with valid, unique synthetic IDs are admitted. The MCP preempter handles cancellation notifications but returns `ErrNotHandled` for calls (`mcp/transport.go:259-272`); there is no asynchronous-response tool registration, and the guard refuses subscriptions before dispatch. For queued cancellation the SDK's `handleAsync` skips the handler and calls `processResult` (`internal/jsonrpc2/conn.go:670-679`). The same result path runs after entered handlers return. It constructs and attempts to write a response independently of the cancelled request context (`conn.go:704-724`). If construction fails, `OnInternalError` reaches the payload-free SDK latch (`mcp/transport.go:233-235`), which stops the guard. The SDK can skip `Write` only when already closing or after a read/write error (`conn.go:151-168,750-764`): the managed session stops the guard before joining, and a guard I/O error stops it before returning. A stopped reader cancels incoming requests (`conn.go:541-559`); SDK `Wait` joins incoming work before guarded credits are discarded. There is no guessed cancellation grace time and no inference that a cancellation request or blocked-pipe timeout is a completed drain. Recheck this argument if the SDK pin or dispatch surface changes.

| Command | Direct result |
| --- | --- |
| `go test -p 1 ./internal/mcp -run '^TestServerSDKMarshalFailureLatches$' -count=1 -v -timeout 20s` | Exit 1 before session latch wiring: one expected failure, no skips. The preceding terminal attempt returned no test exit. |
| `go test -p 1 ./internal/mcp -run '^TestServer' -count=1 -v -timeout 35s` | Exit 0 after latch wiring: eight tests and nine subtests, no skips. |
| `go test -p 1 ./internal/mcp -run '^(TestGuardAdmissionAndRetirement\|TestGuardFourOutstanding\|TestServerSDKFailureWaitsForHeldHandler)$' -count=1 -v -timeout 20s` | Exit 1 before the tool-overload fix: one expected failure, two passes, no skips. |
| `go test -p 1 ./internal/mcp -run '^(TestGuardCancellationBeforeSDKHandlerRetiresCredit\|TestGuardAdmissionAndRetirement\|TestGuardFourOutstanding)$' -count=1 -v -timeout 20s` | Exit 0: three tests, no skips. The queued cancellation does not enter the handler, writes a correlated error, and permits reuse of the external ID. |
| `go test -p 1 ./internal/mcp -run '^TestServerSDKExplicitSchemasAndDispatch$' -count=1 -v -timeout 15s` | Exit 1 before middleware auditing: five subtests passed, but the parent found no mandatory audit for structured argument errors. |
| `go test -p 1 ./internal/mcp -run '^TestGuardRejectsNotificationMethodsWithRequestIDs$' -count=1 -v -timeout 15s` | Exit 1 before the fix: one expected failure, no skips. Notification methods sent as calls were reaching SDK dispatch. |
| `go test -p 1 ./internal/mcp -count=1 -json -timeout 5m` | Exit 0 on final source: 128 parent tests, 152 subtests, zero skips, 88.827 s; prior checkpoints also passed (127 parents, 152 subtests, zero skips). Includes actual Windows pipe tests, structured busy replies, read-only discovery annotations, latch-to-session shutdown and a held handler that keeps its credit until actual SDK join. |
| `go test -p 1 ./internal/wa -run '^TestMCPActual(HistoryIncompleteAtLogLevels\|ReceiveOverloadAtLogLevels\|MessageDebugRedaction)$' -count=1 -v -timeout 6m` | Exit 0: three tests and eight subtests, no skips; 2.533 s. Exercises real producer paths using synthetic data and the simulator, not a real account. |
| `gofmt -l .`; `go vet ./...`; `git diff --check`; `go build -o D:\tmp\kilo\mcp-foundation-<GOOS>-<GOARCH> ./cmd/whatsapp-mcp` | Formatting, vet and whitespace checks passed. Each build exited 0: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. Cross-builds are not native runtime evidence. |

T011/T012/T014 and T017/T018 are complete at the foundation boundary. T015/T016 remain open. Read-only source inspection confirmed the prior dependency cycle: `cmd/whatsapp-mcp/commands.go` still calls `runShell` with stdin-discard and no reporter/SIGPIPE setup; the existing T044 observer watches the store latch, not diagnostic delivery. `internal/mcp/server.go` stops its guarded session on diagnostic failure, and `TestDiagnosticsStderrSubprocess` exercises the reporter in a helper process, not `runStdio`. Neither proves how the command exits after cleanup. Under the old plan this required T024 wiring, which itself waited for T015/T016 to close. The repository-wide delivery suite and coverage gates were not run in this slice.

| Gate | Evidence still required |
| --- | --- |
| Foundation: T045/T046, then T015/T016 closure | Intended local red command cases, then passing stdio diagnostic-only cases: startup/refusal and ordinary log-level change on one reporter, timely acknowledgment with clean exit, failure already latched or raised during operation/drain, EOF and signal races, normal exit 1 after actual ordered owner/store cleanup and lock release, and no wait for diagnostic I/O in cancellation or cleanup or change to marker ownership. Rerun focused producer/reporter and lifecycle regressions. Record both focused T046 CI run links and tested commit, date, native OS/architecture, required named cases, counts/skips and test/child exit outcomes for blocked/closed stderr, SIGPIPE and first/forced-second signals. Zero matches and required-case skips fail the gate. Not verified. |
| Phase 3: T024/T026 | Install actual MCP transport and revalidate both latches with actual handler/SDK join, correct owner/store/lock order, and exit 1 despite EOF. Existing package-level guarded-session evidence and pre-MCP command evidence cannot prove this. Not verified. |
| Delivery: T034-T039/T041 | Retained bounds, sustained ingest and store CI evidence; full integrated native Windows/Linux/macOS subprocess matrix including stdin/stdout, stderr and signal cases; wider command regression and delivery gates. Not verified. |

Record each native run's command, date, OS/architecture, counts/skips and direct exit statuses, including expected child exit 1. Do not infer native Linux/macOS evidence from Windows, WSL2 or cross-builds. No new runtime tests were run for the sequencing revision.

### T010 Result-Encoding Bounds (2026-11-19)

Native Windows/amd64, `CGO_ENABLED=0`, SDK v1.8.0. `go test ./internal/mcp -run '^TestResultNearLimitPassesFramePreflight$' -count=1` exited 1 before the fix: a valid 5,900,806-byte SDK frame was rejected because the framer charged already-encoded raw JSON twice. `go test -p 1 ./internal/mcp -count=1 -json -timeout 5m` on the final patch exited 0 in 87.645 s: 125 parent tests, 152 subtests, zero skips. This includes T009, the near-limit write with newline, and raw-frame preflight boundaries for HTML and Unicode line separators. The intermediate package run exited 1 on the former factor-two test boundary (123 parent and 152 subtest passes, zero skips); the updated boundary keeps the 8 MiB cap and envelope reserve. `go vet ./...`, `gofmt -l .`, `git diff --check` and LF checks passed. `go build -o D:\tmp\kilo\t010-<GOOS>-<GOARCH> ./cmd/whatsapp-mcp` exited 0 for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 (the first loop timed out before the last result, which was rerun separately). Cross-builds are not native runtime coverage.

**Allocation-bound audit:** The DTO preflight charges fields, rows, escaping and fixed keys before marshaling, then checks the actual structured length. The ASCII renderer checks exact size before allocating one string backing buffer. Accepted input caps both representations at 2 MiB each. `TextContent.MarshalJSON` escapes quotes/backslashes at most twice in the ASCII text (HTML and controls were already escaped), so its encoded payload is at most 4 MiB plus fixed keys. `CallToolResult.MarshalJSON` embeds structured content as raw JSON; its inner buffer and the outer `NewResponse`/`marshalToRaw` copies each encode at most 6 MiB plus fixed fields. `EncodeMessage` embeds the raw result again, not as a quoted string; the framer reserves raw bytes, remaining escape expansion, ID and envelope before encoding, then checks actual bytes including the newline before write. This route requires the untyped SDK `Server.AddTool` handler with a pre-encoded DTO result and no added result metadata; the typed wrapper's additional marshal and schema round trip are not covered. The one-tool credit and writer serialize in-flight outputs; later integration must preserve those assumptions.

The near-limit benchmark sample had structured/text lengths of 2,080,768 bytes each, SDK result length 4,162,078 bytes and frame length 4,162,124 bytes including newline. Its measured encoding chain used 30,568,820 bytes/op and 39 allocations/op; rejected preflight used 392 bytes/op and six allocations/op. These are sample allocation totals, not peak live heap. The source audit bounds result-dependent encoded copies; buffer capacity rounding, `encoding/json` pool retention and feature-wide peak heap with other live work are not measured bounds here. T034 covers retained-state delivery evidence; the store-to-DTO story mapping is outside T010.

### T004/T005 Completion (2026-11-19)

Native Windows/amd64, SQLite v1.62.0, `CGO_ENABLED=0`. The complete `go test -p 1 ./internal/store -parallel 1 -count=1 -json -timeout 30m` run exited 0 in 735.829 s: 322 parent tests, 305 subtests, 11 skips (child-only entries, opt-in test, or unavailable Windows symlink privilege), zero failures. Query plans, scale, mapped identity ceilings, device priority and WAL readmission passed. Both per-contact memory children passed with unchanged limits: `LastInteraction` used 93,592 bytes in 2.0008 ms (even) and 942.6 us (phone). `-parallel 1` serialized parallel test fixtures only; the concurrency schedules within the tests still ran.

`go test -p 1 ./internal/store -run '^TestPerContactMemory$/^even$' -count=1 -v -timeout 8m` also passed (one parent, one subtest, one child execution, zero skips; 144.636 s). `go test -p 1 ./internal/store -run '^TestToolGatePrioritizesWaitingDevice$' -count=10 -v -timeout 2m` passed ten times, zero skips, in 3.315 s. Earlier drafts of the new schedule failed because the fixture used an unguarded seed device; they are not evidence of a production admission failure. The passing schedule uses a guarded device write and parks it after waiter registration, outside the gate mutex. It asserts cancellation of the active tool, refusal with one free slot and a waiting device, no added tool waiters, and readmission after the device completes. T043/T044 separately supply the command failure and drain evidence. This is not a feature-wide or native Linux/macOS acceptance result.

### T004/T005 Mapped Identities and Regression Runs (2026-11-19)

Native Windows/amd64, SQLite v1.62.0, `CGO_ENABLED=0`, synthetic data only. The new mapping test uses both alias directions. The phone-mapping case also asserts zero contact folds. The guard counts the domain suffix in the JID ceiling and does not slice a rejected source contact identity. Fixture batching preserves the plan data and assertions. The pressure test's retry window covers the existing checkpoint cooldown and asserts successful admission and WAL recovery, not a new production deadline.

| Command | Direct result |
| --- | --- |
| `go test -p 1 ./internal/store -run '^TestToolMappedIdentitySourceCeiling$' -count=1 -v -timeout 1m` | Exit 1 before the fix: both subtests failed, no skips. Search accepted the oversized mapped phone and folded six values. |
| `go test -p 1 ./internal/store -run '^(TestToolMappedIdentitySourceCeiling|TestToolContactAggregatePlan|TestToolDirectChatPointPlans|TestToolFiveReadsExpectedResults|TestToolContactCeilingsZeroFolds)$' -count=1 -v -timeout 2m` | Exit 0 after the source guards: five tests and six subtests passed, no skips; 6.244 s. |
| `go test -p 1 ./internal/store -run '^(TestTool|TestSearchContacts|TestDirectChat|TestContactChats)' -count=1 -json -timeout 5m` | Exit 1 while the initial full rerun was also running: 50 parent and 77 child passes, one helper skip. The at-ceiling contact case hit a deadline and the maximum-batch case timed out waiting for WAL recovery before the retry fix. |
| `go test -p 1 ./internal/store -run '^TestTool' -count=1 -timeout 5m` | Exit 0 on the final patch, 34.866 s. Nonverbose output did not report counts. |
| `go test -p 1 ./internal/store -count=1 -json -timeout 30m` (three runs) | All exited 1. The earlier T043/T044 checkpoint run finished with 319 parent and 279 child passes, 11 skips, and one `TestQueryPlans` fixture-seeding failure (809.953 s). After fixture batching: 320 parent and 305 child passes, 11 skips, one `TestQueryScale` full-size text-search deadline (859.491 s). On the final patch: 320 parent and 304 child passes, 11 skips and two failure events for `TestPerContactMemory/even` and its parent (578.602 s); `TestQueryPlans`, `TestQueryScale`, the mapped identity cases and WAL readmission all passed. The captured final output did not retain the memory child's detailed assertion. |
| `go vet ./...`; `git diff --check`; `git ls-files --eol` on changed tracked files | All exit 0; no vet findings, whitespace errors or CRLF in the changed tracked files. |

These earlier failures precede the passing serialized Windows run above. No production timing or resource limit was relaxed.

### T043/T044 Stdio Store Failure (2026-11-19)

Native Windows/amd64, `CGO_ENABLED=0`, SQLite v1.62.0; temporary stores and fake owners only for the new tests. `go test -p 1 ./cmd/whatsapp-mcp -run '^TestMCPStoreFailureShutdown$' -count=1 -v -timeout 90s` exited 1 before the observer: six subtests failed and the serve control passed, with zero skips. An earlier tool invocation timed out before yielding a test result.

After the observer, `go test -p 1 ./cmd/whatsapp-mcp -run '^(TestMCPStoreFailureShutdown|TestMCPHarnessHeldOwnerAndCooperativeStreams|TestShellIngestFailureStopsTheCommand|TestShellLifecycleStopWithoutTerminalSignalIsUnchanged|TestShellIngestFailureAfterPairLink|TestStopStepsInReverseOrder|TestStoreNotQuiescentWaitsBeforeLock|TestStoreCloseErrorAfterDrainReleasesLock)$' -count=3 -v -timeout 2m` exited 0 (24 parent and 27 child executions, zero skips; 30.702 s). This pairs injected command-latch and real lock/owner ordering tests with store-side `TestToolFacadeControlFailureRetainsLease` and `TestToolFacadeRetainsLeaseDuringConnectionCleanup` from the focused v1.62.0 store run below. An injected latch does not prove SQLite cancellation. The command observer joins before return and checks the latch after cleanup, without installing MCP or the diagnostic reporter.

`go test -p 1 ./internal/wa -run '^(TestQuietOrder|TestOwnerTerminal)' -count=1 -v -timeout 4m` exited 0 (two owner terminal tests, zero skips; 0.668 s). `go test -p 1 ./internal/wa -run '^(TestRetirementOrderOfAConnectedRun|TestRetirementOrderCheckerRejectsEveryViolation|TestRetirementOrderCheckerRejectsIngestStepsOutOfPlace)$' -count=1 -v -timeout 2m` exited 0 (three tests, zero skips; 0.842 s). `go vet ./cmd/whatsapp-mcp` passed.

The broader `go test -p 1 ./cmd/whatsapp-mcp -run 'TestMCPStoreFailureShutdown|TestMCPHarness|TestShell|TestStopSteps|Test.*Lifecycle|Test.*Lock' -count=3 -v -timeout 5m` exited 1: one saved-session `serve` case reported `work_running` on quiet stop (later repetitions of that case passed), then the overall five-minute timeout fired during another `serve` case. The new observer cases passed on all three repetitions. This broader run is not a passing package regression.

### Post-rebase SQLite Check (2026-11-19)

On rebased tip `ca86799` with the existing uncommitted store edits, native Windows/amd64 and `CGO_ENABLED=0`, `go test -p 1 ./internal/store -run '^TestTool' -count=1 -json -timeout 5m` ran against `modernc.org/sqlite v1.62.0` and exited 0 in 35.623 s: 44 parent tests and 74 subtests passed, with zero skips or failures. The finite ingest fixture completed 120 light writes and twelve 64-message maximum-byte batches without ingest errors. The maximum-byte run observed 16,332 successful reads, 41,374 busy refusals, zero cancelled reads and a sampled post-write WAL peak of 67,984,152 bytes.

On the same platform, `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/cancel-wal.go` exited 0 (one synthetic scenario, zero skips). While a cancelled later `Next` was held in a scalar callback, the writer completed 1,000 inserts; the passive checkpoint reported 336 log frames and zero checkpointed frames. After releasing the callback, closing rows and ending the transaction, the truncate checkpoint reported zero WAL bytes. In v1.62.0, `stmt.go` and `sqlite.go` still scope the query's interrupt/progress handler to the initial query call; `rows.go` adds context-aware lock waits on later steps, not a general later-step interruption mechanism.

These reruns supported the first-step design but did not repeat the full T001 spike, which used v1.61.0. See the completion and shutdown entries above for later Windows evidence and the review table for the renewed design verdict.

### T004/T005 Store Follow-up (2026-11-18/19)

Native Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`; synthetic temporary stores only. Dates below are local (UTC+02:00). Existing uncommitted changes in `bench_test.go`, `tool_reader_test.go`, `tasks.md` and this file were preserved. No implementation commit, account action or optional heavy/soak/measurement run was performed.

The additional tests exposed missing source-byte checks for latest-message IDs/sender identities, listed chat identities and device-contact identities. SQL now uses `octet_length` before selecting those values or folding contact fields. An oversized identity gives `ErrResourceLimit`, never a partial successful result; metadata-only `GetChat` still avoids latest-message work. The contact precheck retains the same entry/name budgets and runs in the same first-step statement. Contact aggregate equivalence now also covers REAL and NULL legacy names, alongside controls/NUL, integers, BLOBs and invalid UTF-8 bytes. The contact-chat aggregate checks identity length before hex encoding as well.

`TestToolFirstStepCancellationDuringContactFolding` cancels the caller inside a real SQLite folding callback, compares observed folding with an independently measured complete-read count, checks zero retained gate/reader resources, then performs a successful fresh read. The initial 1,000-fold cutoff was invalid because one contact can cause multiple folds; it failed under full-suite load with 1,579 folds. The corrected test uses 10,000 synthetic entries and the measured complete workload rather than an arbitrary callback count or duration. Twenty repeated runs and the complete tool-test group passed after the correction; no full-suite pass after that correction is claimed.

`TestToolConcurrentIngestAndReadmission` now has light and maximum-byte subtests. Each maximum batch contains 64 messages and charges exactly 16 MiB through `MessageFieldBytes`, including identity bytes, by shortening the last message's text within its existing field ceiling. Twelve such batches (768 messages) are written while back-to-back facade reads run. The test checks ingest errors, sampled post-write WAL against 128 MiB plus one observed batch, successful post-ingest readmission and recovery to at most 64 MiB. It is finite pressure evidence with fast metadata reads, not a sustained worst-case grouping/heap/temp test or a proof that every guard threshold was reached. The separately existing held-snapshot test covers actual threshold cancellation and recovery.

| Date | Command | Direct result |
| --- | --- | --- |
| 2026-11-18 | `go test -p 1 ./internal/store -run '^(TestToolLatestIdentitySourceCeilings|TestToolListChatIdentitySourceCeiling)$' -count=1 -v -timeout 2m` | Exit 1 before guards: two failed parents and three failed identity subtests; over-limit values incorrectly succeeded. |
| 2026-11-18 | `go test -p 1 ./internal/store -run '^(TestToolLatestIdentitySourceCeilings|TestToolListChatIdentitySourceCeiling|TestToolLatestSender|TestToolGetChatMetadataOnly|TestToolListChatsFirstStepPlans)' -count=1 -v -timeout 2m` | Exit 0 after guards; six parents/five subtests passed, zero skips, 1.506 s. |
| 2026-11-18 | `go test -p 1 ./internal/store -run '^(TestToolContactIdentitySourceCeiling|TestToolFirstStepCancellationDuringContactFolding|TestToolContactAggregateLegacyValues)$' -count=1 -v -timeout 2m` | Exit 1 before the contact identity guard: one expected failed parent; two parents/six legacy subtests passed, zero skips, 0.584 s. |
| 2026-11-18 | `go test -p 1 ./internal/store -run '^(TestToolContactIdentitySourceCeiling|TestToolFirstStepCancellationDuringContactFolding|TestToolConcurrentIngestAndReadmission|TestToolContactChatsFirstStepPlan|TestToolContactAggregatePlan)$' -count=1 -v -timeout 5m` | Exit 0; five parents/two subtests passed, zero skips, 15.124 s. Light: 120 writes, 1,668 reads, WAL peak 4,717,432 B. Maximum batches: 12 writes, 16,619 reads, WAL peak 34,228,992 B, max observed growth 33,870,520 B. Zero busy/cancelled reads and zero ingest errors in this run. |
| 2026-11-19 | `go test -p 1 ./internal/store -count=1 -json -timeout 30m` (first attempt; terminal timeout 600,000 ms) | Interrupted at the terminal limit before final counts/exit were printed; no direct test exit or full-suite acceptance inferred. Both contact-memory children printed PASS in this partial attempt. |
| 2026-11-19 | `go test -p 1 ./internal/store -count=1 -json -timeout 30m` (retry; terminal timeout 1,900,000 ms) | Direct exit 1, 1,332.900 s; 317 parent and 302 child/seed passes, 11 skip events, four failure events. Failures: `TestPerContactMemory/even` and its parent (89.9519 ms versus existing 50 ms threshold; memory 93,592 B), `TestQueryScale` (full text-search deadline), and the new cancellation assertion described above. The phone memory child passed at 25.064 ms. Seven skipped parents are helper-only or opt-in; four skipped symlink subtests lack Windows privilege. Counts come from JSON events with `$LASTEXITCODE` captured after the test pipeline, not from its last output-filter status. |
| 2026-11-19 | `go test -p 1 ./internal/store -run '^TestToolFirstStepCancellationDuringContactFolding$' -count=20 -v -timeout 3m` | Exit 0 after the test correction; 20 parent executions passed, zero skips, 11.689 s. |
| 2026-11-19 | `go test -p 1 ./internal/store -run '^TestTool' -count=1 -json -timeout 5m` | Direct exit 0; 44 parents/74 subtests passed, zero skips/failures, 90.029 s. Light: 120 writes, 2,169 reads, WAL peak 5,018,192 B. Maximum batches: 12 writes, 14,926 reads, WAL peak 34,228,992 B, maximum observed batch growth 33,870,520 B. Zero ingest errors, zero busy/cancelled reads in this run. |
| 2026-11-19 | `go test -p 1 ./internal/mcp -count=1 -timeout 5m` | Direct exit 0, 89.981 s; nonverbose run does not independently report test/skip counts. Store/SDK compatibility evidence only, not tool/lifecycle feature acceptance. |
| 2026-11-18/19 | `gofmt -w internal/store/tool_reader_test.go internal/store/queries.go internal/store/contacts.go`; later `gofmt -l .` | Exit 0; final listing printed nothing. |
| 2026-11-19 | `go vet ./...` | Direct exit 0; no findings. |
| 2026-11-19 | `go build -o D:\tmp\kilo\t004-t005-<GOOS>-<GOARCH> ./cmd/whatsapp-mcp`, with `GOOS`/`GOARCH` set for each approved target | Direct exit 0 individually for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. Cross-builds only; no native Linux/macOS runtime coverage inferred. |
| 2026-11-19 | `git diff --check`; `git ls-files --eol` | Both exit 0; no whitespace errors or tracked `w/crlf`. |

Read-only inspection of the non-`Store.read` pool users found their reader declarations present (`RunGuard`, recovery identity, `LoadDevice`, and recovery methods inside device leases), including nested reader accounting without a second active lease, and post-write WAL checks on the inspected device/run-marker paths. This is static evidence, not the missing per-path stress matrix. The Go ignore rules were checked and `/vendor/` added. Direct inspection of the ignored `.specify/extensions.yml` found only optional before/after implementation commit hooks; neither was executed. The initial glob did not find this ignored file, so pre-hook discovery was late; no mandatory implementation hook was registered or missed.

The focused Windows pass at this checkpoint was not sufficient to close T004/T005. The later completion and T043/T044 shutdown evidence are recorded above; sustained ingest and native Linux/macOS acceptance remain open.

### Resume Check (2026-11-18)

Before the interim checkpoint commit, `CGO_ENABLED=0 go test -p 1 ./... -run '^$' -count=1 -timeout 5m` exited 0 on native Windows/amd64; all 10 packages compiled, with zero tests run. This is compilation evidence only, not feature or phase acceptance.

Native Windows/amd64, `CGO_ENABLED=0`: `go test -p 1 ./internal/store -run '^TestTool' -count=1 -v -timeout 5m` exited 0 with 13 parent tests and two subtests, zero skips, package time 3.520 s. `go test -p 1 ./internal/mcp -count=1 -v -timeout 5m` exited 0 with 74 parent tests, zero skips, package time 46.002 s. The latter includes T009's control, invalid-UTF-8, omission/null, timestamp, overflow, no-truncation and preflight cases. These checks do not establish the missing store-isolation schedules, transport/lifecycle integration, allocation bounds or native platform gates.

`go mod verify` with `CGO_ENABLED=0` exited 0 and reported all modules verified during the phase 2 work. `go version` exited 0 and reported `go1.27.4 windows/amd64` for the active toolchain.

`go test -p 1 ./internal/config ./internal/lock ./internal/walog -count=1 -timeout 5m` with `CGO_ENABLED=0` exited 0 on native Windows/amd64, with package times 2.851 s, 2.194 s and 1.043 s respectively. The non-verbose run did not report test or skip counts; it is not a substitute for the final feature gate.

`go test -p 1 ./internal/config -count=1 -v -timeout 5m` with `CGO_ENABLED=0` exited 0 on native Windows/amd64 in 4.492 s, with three skips: Unix-only file permissions, symlink privilege and Windows FIFO absence. The verbose output includes individual tests and fuzz seeds; its pass count was not independently tallied.

`go test -p 1 ./internal/lock -count=1 -v -timeout 5m` with `CGO_ENABLED=0` exited 0 in 1.426 s: 14 parent tests and two subtests passed; three parent tests and three subtests were skipped for helper-only execution, unavailable symlink privilege, or Unix-only permission checks.

`go test -p 1 ./internal/walog -count=1 -v -timeout 5m` with `CGO_ENABLED=0` exited 0 in 1.107 s: three tests passed, zero skips, including the bridge's argument-drop and template-bounding checks.

`go test -p 1 ./internal/private ./internal/buildinfo -count=1 -v -timeout 5m` with `CGO_ENABLED=0` exited 0 on native Windows/amd64. `internal/private` had 15 passes and four skips because symlink creation requires an unavailable privilege; Windows junction and access checks passed. `internal/buildinfo` had three parent passes and three subtest passes, zero skips. Package times were 1.476 s and 1.384 s respectively.

An earlier phase 2 integration check on the same date/platform, `go test -p 1 ./internal/mcp -count=1 -v -timeout 5m`, exited 1 in 88.528 s with two failing parent tests and zero skips. `TestGuardToolDeadlineCancelsSDKHandler` timed out waiting for cancellation; `TestServerKnownToolFailureAuditIdentity` observed `tool=unknown` for `list_chats`. Other exercised tests passed. Both cases passed in the later focused checkpoint below; the full package has not yet been revalidated after all ongoing changes.

T004 legacy contact aggregate follow-up: `go test -p 1 ./internal/store -run '^TestToolContactAggregateLegacyValues$' -count=1 -v -timeout 5m` exited 1: the control and integer subtests passed, while a legacy BLOB name failed with storage I/O error. After converting the already size-checked selected name to TEXT in the JSON aggregate, `go test -p 1 ./internal/store -run '^TestToolContactAggregateLegacyValues$|^TestToolContactSourceFieldCeiling$|^TestSearchContacts' -count=1 -v -timeout 5m` exited 0 with six parent tests passed, one child-only parent skipped, three subtests passed and the heap-limit subprocess passed, package time 1.777 s. `gofmt -l internal/store/tool_reader_test.go internal/store/contacts.go` exited 0 with no output. Other non-TEXT values and first-step isolation requirements remain to be proven.

An invalid-UTF-8 BLOB follow-up to the same aggregate test exited 1: JSON decoding replaced a legacy byte with U+FFFD. The selected, size-checked name now crosses the JSON aggregate as hex and is decoded back into its original bytes. The identical combined regression command exited 0 with six parent tests passed, one child-only parent skipped, four legacy-value subtests passed and the heap-limit subprocess passed, package time 1.565 s; the same `gofmt -l` check exited 0 with no output. T004 remains incomplete; no feature-wide safety claim is made.

`go test -p 1 ./internal/store -count=1 -timeout 30m` with `CGO_ENABLED=0` on native Windows/amd64 printed `ok` in 312.026 s. The background-process monitor did not report a direct process exit code or test/skip counts, so this run is only provisional regression evidence and does not satisfy the final command-recording gate.

### T003 Command Harness (2026-11-18)

Native Windows/amd64, `CGO_ENABLED=0`: `go test ./cmd/whatsapp-mcp -run '^TestMCPHarnessHeldOwnerAndCooperativeStreams$' -count=20 -v -timeout 5m` exited 0 with 20 passes and zero skips; the same command with `-count=1` exited 0 with one pass and zero skips after formatting. `gofmt -l cmd/whatsapp-mcp/mcp_test_helpers_test.go` exited 0 with no output. These exercise controlled fake-owner completion and cooperative stream teardown only.

The initial `go test ./internal/mcp -run '^TestHarnessFixtureAndSDK$' -count=1 -v -timeout 2m` exited 1 before compilation because the first SDK import required module-file updates. An alternate temporary module file run exited 0 with one test and zero skips. After resolving the six already-reviewed transitive dependencies into `go.mod`, `go test -mod=mod ./internal/mcp -run '^TestHarnessFixtureAndSDK$' -count=1 -v -timeout 2m` exited 0 with one test and zero skips. `go test ./internal/mcp ./cmd/whatsapp-mcp -run '^TestHarnessFixtureAndSDK$|^TestMCPHarnessHeldOwnerAndCooperativeStreams$' -count=1 -v -timeout 5m` exited 0 with two tests and zero skips (package times 7.828 s and 17.406 s); `go mod verify` exited 0. The synthetic fixture contains 235 chats and contacts, local alias mapping and latest messages, and initializes the official in-memory SDK without account networking. At that checkpoint the common server-end guard and metadata-only store method did not yet exist.

After those seams became available, the counted fixture was changed to use `Store.ToolReader()` and the SDK server end to use `GuardTransport`. On native Windows/amd64 with `CGO_ENABLED=0`, `go test -p 1 ./internal/mcp -run '^TestHarnessFixtureAndSDK$' -count=1 -v -timeout 2m` exited 0 with one test and zero skips in 0.981 s. This is harness verification, not full guard or store safety acceptance.

### Foundation In Progress (2026-11-18)

Native Windows/amd64, `CGO_ENABLED=0`: T006 adds the five-method narrow read interface and public DTO definitions. `go test -v ./internal/mcp -count=1 -timeout 10m` exited 0, one existing harness test passed; `go build ./cmd/whatsapp-mcp` exited 0, and `gofmt -l internal/mcp/results.go` exited 0 with no output. These establish compilation, not DTO rendering or budget correctness; those are T009/T010. DTO construction must enforce provenance, counts, non-nil arrays, timestamps and media types.

T004 has a first negative test, `go test ./internal/store -run '^TestToolContactSourceFieldCeiling$' -count=1 -v -timeout 5m`, which exited 1 with one intended failure and zero skips: an oversized contact source returned nil instead of `ErrResourceLimit`. The remaining T004 schedules and T005 implementation are not complete.

Further T004 red run: `go test ./internal/store -run '^TestTool(ContactSourceFieldCeiling|ReadDoesNotSucceedAfterCancellation)$' -count=1 -v -timeout 5m` exited 1 with two intended failures and zero skips: the oversized source was accepted and a read reported success after cancellation. No isolation or completion guarantee is inferred.

An early partial gate continuation: `go test ./internal/store -run '^TestToolGate|^TestToolReadDoesNotSucceedAfterCancellation$' -count=1 -v -timeout 5m` exited 0 with seven parent tests and two subtests, zero skips. At that checkpoint the cancellation case passed but the contact source-ceiling test still failed and the tool facade, bounded queries and WAL/disk checkpoint policy were incomplete; later checkpoints below record subsequent changes. This early result alone does not approve the gate for MCP use.

Later 2026-11-18 Windows checkpoints: `go test -p 1 ./internal/store -run '^TestToolContactChats(FirstStepPlan|WalkCeiling)$' -count=1 -v -timeout 5m` exited 1 before the aggregate rewrite with two expected failures. `go test -p 1 ./internal/store -run '^TestToolLatestSenderChatNameSourceCeiling$' -count=1 -v -timeout 5m` exited 1 before the source guard, and `go test -p 1 ./internal/store -run '^TestToolFacadeGetChatAdmission$' -count=1 -v -timeout 5m` exited 1 against the initial refusing facade. After those changes, `go test -p 1 ./internal/store -run '^TestTool|^TestListChats|^TestGetChat|^TestDirectChat|^TestContactChats|^TestSearchContacts' -count=1 -v -timeout 5m` exited 0 with 38 parent tests and 11 subtests passed, one child-only parent skipped and its heap-limit subprocess passed. `go test -p 1 ./internal/wa ./internal/mcp -run '^$' -count=1 -timeout 5m` exited 0 with no tests run, compilation only. The tests used `CGO_ENABLED=0`; isolation stress, native platform coverage and complete shutdown evidence remain open.

Later sender-name guard on 2026-11-18, native Windows/amd64 and `CGO_ENABLED=0`: `go test -p 1 ./internal/store -run '^TestToolLatestSenderNameSourceCeiling$' -count=1 -v -timeout 5m` exited 1 before the guard, as the oversized contact name was accepted. `go test -p 1 ./internal/store -run '^TestTool' -count=1 -v -timeout 5m` subsequently exited 0 with 15 parent tests and six subtests, zero skips. This does not complete the other first-step queries or isolation schedules.

T015/T016 are incomplete. Before a diagnostic filtering prototype, `go test ./internal/mcp -run '^TestDiagnosticHandlerLevelFiltering$' -count=1` exited 1: one test failed, with the `ERROR` subtest rejecting mandatory WARN records. The isolated retry `go test internal/mcp/diagnostics.go internal/mcp/diagnostics_test.go -run '^TestDiagnosticHandlerLevelFiltering$' -count=1 -v` exited 0 with one test and four subtests passed, zero skips. The post-edit package test exited 1 during concurrent edits to `tools.go`; it did not execute the test. An initial isolated attempt timed out at 120 s without a direct exit result. `gofmt -l internal/mcp/diagnostics.go internal/mcp/diagnostics_test.go` exited 0 with no output. At that checkpoint the prototype was synchronous and not suitable for integration; see the subsequent checkpoint below.

Later diagnostic checkpoint on native Windows with `CGO_ENABLED=0`: `go test -p 1 ./internal/mcp -run '^TestDiagnostics' -count=1 -v -timeout 45s` exited 0 with 15 tests and four subtests, zero skips. `go test -p 1 ./internal/wa -run '^TestMCPActual(HistoryIncomplete|ReceiveOverload)AtLogLevels$' -count=1 -v -timeout 6m` exited 0 with two tests and eight subtests, zero skips; this exercises the real history producer and a synthetic simulator, not an account. The earlier isolated structured-attributes/human-failure and late-acknowledgment tests each failed before their fixes and passed afterward. T015/T016 and command integration remain incomplete.

The diagnostic edge-case repeat `go test -p 1 ./internal/mcp -run '^TestDiagnostics(LevelRouting|QueueOverflowAndFinish|SinkFailure|BoundEventAttribute|PriorityAndOrdinaryBound|AuditMetadata|PartialWriteAcknowledgment|OrdinarySinkFailureThenMandatory|OversizedMandatoryLatches|OrdinaryStructuredAttributes|HumanFailure|OrdinarySeverityIdentity|TerminalFailureIdentity|LateAcknowledgmentLatches)$' -count=20 -timeout 60s` exited 0 (14 tests repeated 20 times). `go test -p 1 ./internal/wa -run '^TestMCPActual(HistoryIncomplete|ReceiveOverload)AtLogLevels$' -count=3 -timeout 6m` exited 0 (two tests and eight subtests repeated three times). These do not prove native subprocess or command lifecycle behavior.

An independent review identified two false-failure races when a timely mandatory-write acknowledgment was buffered but not yet accounted at timer expiry or `Finish`. Both new buffered-ack tests failed before the fix. On native Windows/amd64, `CGO_ENABLED=0 go test -p 1 ./internal/mcp -run '^TestDiagnostics(BufferedAcknowledgmentAtFinish|FinishRejectsLateOrFailedAcknowledgment|BufferedAcknowledgmentAtExpiry)$' -count=1 -v -timeout 2m` then exited 0 with three parent tests and two subtests, zero skips, in 0.381 s; late and failed acknowledgments still latch failure. This does not prove subprocess/lifecycle integration or race-detector coverage.

Further diagnostics work on 2026-11-18: `go test -p 1 ./internal/mcp -run '^TestDiagnosticsOrdinaryPrivateAttributes$' -count=1 -v -timeout 60s` exited 1 before the attribute-filter change, and `go test -p 1 ./internal/mcp -run '^TestDiagnosticsLevelRouting$' -count=1 -v -timeout 60s` exited 1 with four expected loss-warning failures before that change. `go test -p 1 ./internal/mcp -run '^TestDiagnostics' -count=1 -v -timeout 60s` then exited 0 with 17 parent tests and four subtests, zero skips. General redaction of ordinary message text, allowlisted values and group names was not established and requires further work. All tests used `CGO_ENABLED=0` on native Windows.

Redaction follow-up on the same platform: `go test -p 1 ./internal/mcp -run '^TestDiagnosticsOrdinaryUntrustedText$' -count=1 -v -timeout 60s` exited 1 before fixed message/value/group filtering, then exited 0 with one test and zero skips. `go test -p 1 ./internal/mcp -run '^TestDiagnostics' -count=1 -v -timeout 60s` exited 0 with 18 tests and four subtests; `go test -p 1 ./internal/wa -run '^TestMCPActual(HistoryIncompleteAtLogLevels|ReceiveOverloadAtLogLevels|MessageDebugRedaction)$' -count=1 -v -timeout 6m` exited 0 with three tests and eight subtests, zero skips. Ordinary numeric attributes are omitted; known fixed producer identities and log levels remain. Command integration and actual inherited-stderr verification remain open.

An early T007/T008 checkpoint: `go test ./internal/mcp -run '^TestCommon' -count=1` initially exited 1 on three behavioral tests covering 21 invalid-input cases, and a subsequent run exited 0 before the error-result test was added. The isolated `go test internal/mcp/tools.go internal/mcp/results.go internal/mcp/validation_test.go -run '^TestCommon' -count=1 -v -timeout 5m` exited 0 with four tests and 21 subtests, zero skips. A concurrent package run failed to compile due to work in progress in `connection.go`, and an earlier isolated attempt had a transient import failure. At that checkpoint schema coverage and acceptance of safe integral numeric spellings were incomplete; see the follow-up below.

T007/T008 follow-up: `go test ./internal/mcp -run '^TestCommon(ExactJSONNumbers|SchemaValidation)$' -count=1 -v -timeout 5m` exited 1 on three intended safe-integral-number cases. A required-string schema red run, `go test ./internal/mcp -run '^TestCommon(RequiredStringSchemas|MetadataOnlyAndUnknownFields)$' -count=1 -v -timeout 5m`, exited 1 with four required-string subtests failing as intended. After both fixes, `go test -p 1 ./internal/mcp -run '^TestCommon' -count=1 -v -timeout 5m` exited 0 with 11 top-level tests and 71 subtests, zero skips, package time 0.254 s. Metadata-only assertions here cover argument values, not the store query; integration and store error mapping remain later work.

T008 low-disk error construction: `go test ./internal/mcp -run '^TestCommonLowDiskError$' -count=1 -v -timeout 5m` exited 1 with one intended failure before the fixed unavailable reason was added; `go test ./internal/mcp -run '^TestCommon' -count=1 -v -timeout 5m` then exited 0 with 12 tests and 71 subtests, zero skips. The later handlers must map the actual store refusal to this fixed error without echoing private details; that integration is not yet tested.

T009/T010 remain incomplete. `go test ./internal/mcp/results.go ./internal/mcp/results_test.go -run '^TestResultEmptyPageArray$' -count=1 -v` exited 1 with one expected failure (empty array encoded null); `go test ./internal/mcp -run '^TestResult' -count=1 -v` subsequently exited 0 with seven tests, zero skips. A rejected synthetic preflight case measured 392 bytes/op and six allocations/op; that is not a successful-response peak-memory bound. SDK source audit identifies further copies in `TextContent.MarshalJSON`, `CallToolResult.MarshalJSON`, `jsonrpc2.NewResponse`/`marshalToRaw` and `EncodeMessage`; typed `AddTool` marshals and replaces structured content before the guard. Exact limits, final frame integration and complete SDK allocation bounds remain unverified.

T009/T010 later checkpoint: `go test ./internal/mcp/results.go ./internal/mcp/results_test.go -run '^TestResultSemanticValidation$' -count=1 -v` exited 1 on the expected invalid-value cases. `go test -p 1 ./internal/mcp -run '^TestResult' -count=1 -v -timeout 2m` subsequently exited 0 with 14 tests and nine subtests, zero skips. Samples construct 2 MiB boundary-sized values and check all five output schemas. A successful 2,080,768-byte synthetic structured/text sample allocated 35,432,767 bytes/op across 47 allocations for the measured encoding chain; it is not a live peak or global bound. Go's `encoding/json` pooled buffers and incomplete one-tool integration leave retained SDK heap unproven; these tasks remain unchecked. The timestamp conversion helper is not yet wired to store-to-DTO mapping.

On 2026-11-18, native Windows/amd64 with `CGO_ENABLED=0`, `go test -p 1 ./internal/mcp -run '^TestResultSDKEncodingEscapedBudget$' -count=1 -v -timeout 5m` exited 0 with one test and zero skips, package time 0.992 s. The test checks structured and safe-text charges against actual SDK result marshaling and the complete JSON-RPC envelope for a heavily escaped synthetic result. It does not establish SDK peak heap or credit lifetime.

`go test -p 1 internal/mcp/results.go internal/mcp/tools.go internal/mcp/results_test.go internal/mcp/validation_test.go -run '^TestResultHTMLMetacharacterCharges$' -count=1 -v -timeout 5m` with `CGO_ENABLED=0` exited 0 on native Windows/amd64, with one parent test and three subtests, zero skips, in 0.308 s. The current preflight charges six encoded bytes for each `<`, `>` and `&`, matching `encoding/json` HTML escaping before marshaling. This particular test was added after that behavior was already present, so no failing-first result was observed for it.

`go vet internal/mcp/results.go internal/mcp/tools.go internal/mcp/results_test.go internal/mcp/validation_test.go` with `CGO_ENABLED=0` exited 0 with no output on the same platform and date; this isolated vet does not replace the full project gate.

`go test -p 1 internal/mcp/results.go internal/mcp/tools.go internal/mcp/results_test.go internal/mcp/validation_test.go -count=1 -v -timeout 5m` with `CGO_ENABLED=0` exited 0: 27 parent tests and 80 subtests passed, zero skips, in 3.633 s. The successful near-limit encoding sample reported 40,755,534 bytes/op and 53 allocs/op; this remains total allocations rather than a peak-memory bound.

The same isolated command after adding the HTML-escape test exited 0 in 4.515 s with 28 parent tests and 83 subtests passed, zero skips. The successful near-limit sample measured 44,768,610 bytes/op and 58 allocs/op; it is still not a retained-heap measurement.

The pinned SDK source was also inspected at `mcp/content.go:34-47` (text content marshal), `mcp/protocol.go:372-387` (result marshal), and `internal/jsonrpc2/messages.go:118-120,148-155` (response and frame marshaling). These paths allocate serialized intermediates before the framing connection sees the response. The DTO preflight bounds represented structured bytes and safe-text bytes to 2 MiB each, and charges twice the text plus the structured form and envelope for the 8 MiB frame; this limits represented payload size but does not by itself prove a bound on Go allocator or pooled-buffer retention. Registration must use a path that preserves the preflighted result and leaves arbitrary SDK metadata/input requests unset.

T011/T012 remain incomplete: isolated behavioral red `go test -vet=off -p 1 ./internal/mcp/connection_test.go -run '^TestGuard' -count=1 -v -timeout 60s` exited 1 with three expected failures; `go test -vet=off -p 1 ./internal/mcp/connection.go ./internal/mcp/connection_test.go -run '^TestGuard' -count=1 -v -timeout 60s` subsequently exited 0 with five tests and 12 subtests, zero skips. The prototype does not yet cover the complete pre-dispatch, notification, response-retirement and in-memory acceptance obligations and must not be integrated as complete. T013/T014 likewise remain incomplete; their prototype must not be integrated until framing deadlines, preflight and the remaining bounds are verified.

Guard continuation: `go test -vet=off -p 1 ./internal/mcp/connection.go ./internal/mcp/connection_test.go -run '^TestGuard(MalformedParamsAndNotifications|NotificationLimit)$' -count=1 -v -timeout 60s` exited 1 (one intended failure, one pass); `go test -p 1 ./internal/mcp -run '^TestGuard' -count=1 -v -timeout 2m` then exited 0 with 12 tests and 12 subtests, zero skips. The tool deadline, no-response termination, full shape validation, mandatory diagnostics and actual integration remained open. The SDK can suppress response writes on marshal failure, so a successful focused run does not establish credit retirement.

Guard ID-reuse checkpoint on 2026-11-18: the guard's ID-reuse regression exited 1 before synthetic internal ID mapping. `go test -p 1 ./internal/mcp -run '^TestGuard' -count=1 -v -timeout 2m` with `CGO_ENABLED=0` then exited 0 with 18 parent tests and 12 subtests, zero skips. The guard translates cancellation IDs and restores external response IDs; a direct deterministic test verifies distinct internal IDs after external reuse and the SDK error latch is exercised by an in-memory marshal failure. A delayed cancellation through the SDK scheduler, the tool deadline and lifecycle observation of the latch remain unverified.

Phase 2 continuation, 2026-11-18, native Windows/amd64, `CGO_ENABLED=0`: `go test -p 1 ./internal/mcp -run '^TestServerSDKExplicitSchemasAndDispatch$' -count=1 -v -timeout 60s` exited 0, one parent test and five subtests, zero skips. `go test -p 1 ./internal/mcp -count=1 -v -timeout 3m` exited 0, 123 parent tests and their subtests, zero skips (88.257 s). This includes the previously failing deadline and known-tool audit identity tests; it does not establish an SDK no-response completion signal, owner lifecycle integration, store isolation acceptance, or native Linux/macOS behavior.

Phase 2 store continuation, 2026-11-18, native Windows/amd64, `CGO_ENABLED=0`: `go test -p 1 ./internal/store -run '^(TestToolConcurrentIngestAndReadmission|TestToolSQLiteCloseCancellationRaces|TestToolFiveReadsExpectedResults|TestToolWALActualWritesCancelSnapshot)$' -count=1 -v -timeout 5m` exited 0, four parent tests and 50 subtests, zero skips (16.586 s). The concurrent test recorded 120 synthetic ingest writes, 1,538 successful reads, zero busy/cancelled reads, sampled peak WAL 4,705,072 bytes and maximum observed batch growth 321,360 bytes. This finite, light run does not establish maximal-ingest bounds, the full T004/T005 checkpoint, or native Linux/macOS behavior. A prior monitor run passed but did not expose a direct exit code and is not counted as this evidence.

T004 follow-up, 2026-11-18, native Windows/amd64, `CGO_ENABLED=0`: added `TestToolFacadeRetainsLeaseDuringConnectionCleanup` and `TestToolCheckpointWaitingForWriterExcludesToolRead`. The first holds the reader cleanup hook before `Conn.Close`, cancels the caller and checks the gate retains the lease and refuses replacement; the second holds the actual writer connection, triggers recovery from an oversized synthetic WAL, and checks the checkpoint retains the tool place and excludes a tool read. `go test -p 1 ./internal/store -run '^(TestToolFacadeRetainsLeaseDuringConnectionCleanup|TestToolCheckpointWaitingForWriterExcludesToolRead)$' -count=1 -v -timeout 2m` exited 0, two tests, zero skips; the same command with `-count=10` exited 0, 20 test executions, zero skips (9.784 s). These were added against the existing implementation, so no pre-implementation failing run is claimed. `go test -p 1 ./internal/store -run '^TestTool' -count=1 -v -timeout 5m` exited 0, 40 parent tests and 67 subtests, zero skips (53.998 s). The run includes a finite 120-write concurrent-ingest fixture, not maximal sustained pressure. The command's unsuccessful exit on the tool-store control-failure latch depends on the unimplemented T024 observer and has not been verified; T004 remains unchecked.

Full store regression, 2026-11-18, native Windows/amd64, `CGO_ENABLED=0`: `go test -p 1 ./internal/store -count=1 -v -timeout 30m` exited 1 in 462.126 s. The output reported 312 passing top-level tests/fuzz seed groups, one failed parent (`TestQueryPlans`) with four failed and 20 passing subtests, seven skipped parent tests and four skipped Windows symlink subtests (missing symlink privilege); 292 subtests/seeds passed overall. The old plan checker rejected the reviewed page-aggregate sort and bounded primary-key ceiling count in four cases. After narrowly recognizing these two plan lines only for the bounded aggregate SQL and adding negative checker tests, `go test -p 1 ./internal/store -run '^(TestQueryPlans|TestPlanProblemsRejectsBadPlans|TestBoundedChatPlanKeepsUnsafeOperations|TestToolListChatsFirstStepPlans|TestToolContactChatsFirstStepPlan)$' -count=1 -v -timeout 8m` exited 0, five parent tests and 26 subtests, zero skips, in 126.427 s. This targeted rerun is not a passing full store suite; a full rerun and remaining isolation evidence are still required.

Full store rerun after the checker correction, 2026-11-18, native Windows/amd64, `CGO_ENABLED=0`: `go test -p 1 ./internal/store -count=1 -timeout 30m` exited 1 in 454.553 s. The nonverbose run did not report total test or skip counts; its reported failure was `TestPerContactMemory/phone`, whose child measured `LastInteraction` at 161.0353 ms against its existing 50 ms threshold. The earlier full run measured the corresponding phone case at 2.5585 ms. An isolated `go test -p 1 ./internal/store -run '^TestPerContactMemory$' -count=1 -v -timeout 8m` then exited 0 in 98.989 s: one parent, two subtests and two child executions passed, zero skips, with phone 9.0041 ms and even 3.9979 ms. This suggests timing sensitivity but does not establish its cause or replace a passing full suite; no threshold was changed.

Query-plan checker follow-up, 2026-11-18, native Windows/amd64, `CGO_ENABLED=0`: `go test -p 1 ./internal/store -run '^TestBoundedChatPlanKeepsUnsafeOperations$' -count=1 -v -timeout 1m` exited 1, one parent test, zero skips: duplicate primary-key scans and aggregate sorts were wrongly accepted. After restricting each exception to one occurrence, `go test -p 1 ./internal/store -run '^(TestBoundedChatPlanKeepsUnsafeOperations|TestPlanProblemsRejectsBadPlans|TestToolListChatsFirstStepPlans|TestToolContactChatsFirstStepPlan)$' -count=1 -v -timeout 2m` exited 0, four parents and two subtests, zero skips (0.564 s). `go test -p 1 ./internal/store -run '^TestQueryPlans$' -count=1 -v -timeout 8m` exited 0, one parent and 24 subtests, zero skips (124.196 s). The full suite was not rerun after this test-only change.

SDK cancellation source inspection, 2026-11-18, pinned `github.com/modelcontextprotocol/go-sdk v1.8.0`: `internal/jsonrpc2/conn.go:670-679,704-724` constructs a response even when cancellation precedes handler entry; cancellation alone is not evidence of a suppressed response. Result marshaling can fail before transport `Write`, and SDK shutdown can also refuse a write (`internal/jsonrpc2/messages.go:116-120`, `internal/jsonrpc2/conn.go:709-724,750-764`). The public middleware does not cover every pre-handler path or response serialization, and the SDK logger is a global error signal, not a per-request completion callback. The guard retains credits after cancellation, but without a response or observed terminal latch it cannot generally distinguish running work from completed work that produced no write. Owner observation of the latch and actual handler/session drainage remain unverified. Do not invent a post-cancellation deadline or interpret cancellation as completion; a source-specific proof of all reachable paths or an owner-reviewed termination policy is still needed.

The real SDK deadline regression `go test -p 1 ./internal/mcp -run '^TestGuardToolDeadlineCancelsSDKHandler$' -count=1 -v -timeout 30s`, `CGO_ENABLED=0`, exited 1 with one intended failure: a held handler was not cancelled by 11 seconds. The proposed guard-side reader pump must also preserve the one-frame/no-read-ahead bound and deadline coverage while a synchronous overload reply is blocked. No deadline guarantee is claimed from the other passing guard tests.

Later guard integration checkpoint on native Windows with `CGO_ENABLED=0`: `go test -p 1 ./internal/mcp -run '^(TestGuard|TestServer|TestFraming)' -count=1 -v -timeout 120s` exited 0 with 56 parent tests and 42 subtests, zero skips, before an additional blocked-output case was added. `go test -p 1 ./internal/mcp -run '^(TestGuardSDKBlockedOutputDeadlineRetainsCredit|TestGuardDeadlineRetainsPendingAndCredit|TestGuardBusyWriteDeadlineRetainsCredit|TestGuardFramingHeldMessagePreventsReadAhead|TestServerKnownToolFailureAuditIdentity)$' -count=20 -timeout 60s` exited 0, five tests repeated 20 times, zero skips. The new demand-driven guard reader and held-frame acknowledgment, SDK cancellation and typed tool identity need further audit: no-response watchdog, actual handler/store drainage and cancellation versus already-writing response have not been proven.

T013/T014 isolated red: `go test -p 1 internal/mcp/transport.go internal/mcp/transport_test.go -run '^TestFramingInput$' -count=1` exited 1 because oversized and truncated frames were accepted. Subsequent `go test -p 1 internal/mcp/transport.go internal/mcp/transport_test.go -run '^TestFraming' -count=1 -v -timeout 60s` exited 0 with three tests and 12 subtests. A full-package attempt failed on a transient vet import error during concurrent edits. Frame timer start, pre-marshal output bounds, duplicate keys and the rest of the specified matrix remained unverified at that checkpoint.

Later native Windows framing checkpoint: `go test -p 1 internal/mcp/transport.go internal/mcp/transport_test.go -run '^TestFraming(InheritedPipes|SerializedFrameBoundary)$' -count=1 -v -timeout 45s` exited 0 with two tests and three subtests, zero skips. The child inherited OS pipes; its blocked partial input and blocked 1 MiB output returned after approximately 10.02 s and 5.23 s respectively, and it exited without joining the blocked pumps. SDK-serialized frames including newline at 8 MiB minus one, equal, and plus one were checked against the final-size helper; the production preflight conservatively rejects these large frames, so this is not proof of successful near-boundary transport. Encoding CPU deadline and whole-process memory peak remain unproven.

On 2026-11-18, native Windows and `CGO_ENABLED=0`, `go test -p 1 internal/mcp/transport.go internal/mcp/transport_test.go -run '^TestFraming(NoFurtherWritesAfterClose|CancelledWriteNeverStarts)$' -count=1 -v -timeout 30s` exited 1 before the pump change: one test observed 32 writes after close, one passed. After the fix, `go test -p 1 internal/mcp/transport.go internal/mcp/transport_test.go -run '^TestFraming' -count=1 -v -timeout 90s` exited 0 with 19 tests and 23 subtests, zero skips. `go vet internal/mcp/transport.go internal/mcp/transport_test.go` and `gofmt -l internal/mcp/transport.go internal/mcp/transport_test.go` both exited 0. SDK guard/session integration and tool deadline enforcement remain unverified.

The expanded native Windows framing suite `go test -p 1 internal/mcp/transport.go internal/mcp/transport_test.go -run '^TestFraming' -count=1 -v -timeout 120s` exited 0 with 23 tests and 23 subtests, zero skips, in 71.298 s. It includes slow partial writes, an uncancellable write interrupted by connection close, idle input and a partial-frame deadline that is not reset by later bytes. T013's test-writing work is complete; tool-admission cancellation and actual SDK handler drainage remain unverified, so T014 remains open.

T017/T018 are incomplete: `go test ./internal/mcp -run '^TestServerConstruction$' -count=1 -v -timeout 5m` first exited 1 with one expected failure for default SDK capabilities, then exited 0 with one test and zero skips after a server-construction prototype. `gofmt -l internal/mcp/server.go internal/mcp/server_test.go` exited 0 with no output. The test used an unguarded in-memory connection and is not acceptance evidence; pre-dispatch guarding and mandatory diagnostics are not integrated, and no tools are registered.

Later server tests on 2026-11-18, native Windows and `CGO_ENABLED=0`: `go test -p 1 ./internal/mcp -run '^TestServerSDKFailureIsAudited$' -count=1 -v -timeout 2m` exited 1 with one intended failure because an SDK unknown-tool error was not audited. A revised `TestServerConstruction` exposed that the SDK accepted `resources/subscribe` despite disabled capabilities. The guard and SDK logger latch are being addressed; T017/T018 remain open.

### T002 Dependency Review (2026-11-18)

Native Windows/amd64; `CGO_ENABLED=0`. The following commands returned exit 0 directly:

| Command | Result |
| --- | --- |
| `go mod download -json github.com/modelcontextprotocol/go-sdk@v1.8.0` | Downloaded the exact SDK; its module requires Go 1.25.0. |
| `go get github.com/modelcontextprotocol/go-sdk@v1.8.0` | Added the SDK; no existing module version, WhatsApp fork, libsignal or toolchain change in the resulting `go.mod`. |
| `go list -mod=mod -deps github.com/modelcontextprotocol/go-sdk/mcp` | Resolved the SDK package dependency graph with CGO disabled and added transitive checksums. |
| `go mod verify` | All downloaded modules verified, both before and after SDK package dependency resolution. |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | No vulnerabilities found in currently imported project packages; the SDK is not yet imported by project code. |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 github.com/modelcontextprotocol/go-sdk/mcp` | No vulnerabilities found in the SDK package scan. |
| `go test ./internal/store -run '^TestListChatsSearch$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic-store test passed, zero skips, package time 0.609 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestListChats' -count=1 -v -timeout 5m` | Exit 0; three existing synthetic-store tests passed, zero skips, package time 2.336 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestListChatsLatest$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic-store test passed, zero skips, package time 0.930 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestListChatsSortAndPage$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic-store test passed, zero skips, package time 1.750 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestGetChat$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic-store test passed, zero skips, package time 2.816 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestSearchContacts' -count=1 -v -timeout 5m` | Exit 0; four parent tests passed, one child-only entry skipped in the parent and passed in its subprocess, package time 2.475 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestDirectChat|^TestContactChats' -count=1 -v -timeout 5m` | Exit 0; three parent tests and three subtests passed, zero skips, package time 6.390 s. Baseline only, not feature acceptance. |
| `go test ./internal/store -run '^TestAdmissionLimitsOnTheRealStore$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic-store admission test passed, zero skips, package time 1.121 s. Baseline only, not the new tool isolation policy. |
| `go test ./internal/store -run '^TestGate' -count=1 -v -timeout 5m` | Exit 0; eight existing gate tests passed, zero skips, package time 1.347 s. Baseline only, not the new tool isolation policy. |
| `go test ./internal/store -run '^TestGateQuiesceOutcome$' -count=1 -v -timeout 5m` | Exit 0; one existing gate test passed, zero skips, package time 0.541 s. Baseline only. |
| `go test ./internal/store -run '^TestCancellationFreesTheSlot$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic-store test passed, zero skips, package time 1.229 s. Baseline only. |
| `go test ./cmd/whatsapp-mcp -run '^TestShellLifecycleStopWithoutTerminalSignalIsUnchanged$' -count=1 -v -timeout 5m` | Exit 0; one existing fake-owner lifecycle test passed, zero skips, package time 24.263 s. Baseline only, not MCP acceptance. |
| `go test ./cmd/whatsapp-mcp -run '^TestShellIngestFailureStopsTheCommand$' -count=1 -v -timeout 5m` | Exit 0; one parent test and two serve/stdio subtests passed, zero skips, package time 9.491 s. Baseline only, not MCP acceptance. |
| `go test ./internal/wa -run '^TestOwnerTerminalIgnoresADeviceStorageFault$' -count=1 -v -timeout 5m` | Exit 0; one existing owner test passed, zero skips, package time 1.293 s. Baseline only. |
| `go test ./internal/wa -run '^TestOwnerTerminal' -count=1 -v -timeout 5m` | Exit 0; two existing owner tests passed, zero skips, package time 1.563 s. Baseline only. |
| `go test ./internal/wa -run '^TestQuietStopReportsDroppedMessagesWithoutFailing$' -count=1 -v -timeout 5m` | Exit 0; one existing owner test passed, zero skips, package time 1.157 s. Baseline only. |
| `go test ./internal/wa -run '^TestIngestLiveStoreErrorIsTerminal$' -count=1 -v -timeout 5m` | Exit 0; one existing synthetic ingest test passed, zero skips, package time 2.390 s. Baseline only. |
| `go test ./internal/wa -run '^TestQuietStopPassesAfterACleanRetirement$' -count=1 -v -timeout 5m` | Exit 0; one existing owner test passed, zero skips, package time 0.831 s. Baseline only. |
| `go test ./internal/wa -run '^TestQuietStopEachSourceFailsWithItsReason$' -count=1 -v -timeout 5m` | Exit 0; one existing parent test and four subtests passed, zero skips, package time 1.999 s. Baseline only. |
| `go test ./internal/wa -run '^TestQuietStopNeverAttemptedIsNotChecked$|^TestQuietStopAfterTheStopBoundFails$' -count=1 -v -timeout 5m` | Exit 0; two existing owner tests passed, zero skips, package time 0.923 s. Baseline only. |
| `go test ./internal/wa -run '^TestIngestHistoryFailuresAreReportedAndNotTerminal$' -count=1 -v -timeout 5m` | Exit 0; one parent test and three subtests passed, zero skips, package time 0.544 s. Baseline only; mandatory MCP diagnostics are not implemented. |
| `go vet ./...` | Exit 0 on the existing project packages; baseline only, before MCP implementation. |
| `gofmt -l .` | Exit 0 with no output; baseline only, before MCP implementation. |

The SDK package imports six newly selected transitive modules: `github.com/google/jsonschema-go v0.4.3`, `github.com/segmentio/encoding v0.5.4`, `github.com/segmentio/asm v1.1.3`, `github.com/yosida95/uritemplate/v3 v3.0.2`, `golang.org/x/oauth2 v0.35.0`, and `golang.org/x/time v0.15.0`. Their module Go versions are 1.14 through 1.25; the SDK requires 1.25 and existing selected `x/sys` and `x/sync` require 1.26, all within the project's Go 1.27 baseline. The Windows dependency graph has no cgo files; `segmentio/asm` includes architecture-specific assembly, so CGO-free does not mean Go source exclusively.

An independent review on Windows with `CGO_ENABLED=0` and `GOTOOLCHAIN=local` (Go 1.27.3, not the configured 1.27.4) reported direct exit 0 for `go list -mod=readonly -m all`, `go mod graph`, `go mod verify`, and the SDK dependency and module-version listings. `go build -mod=readonly github.com/modelcontextprotocol/go-sdk/mcp` exited 0 separately for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. These are cross-compiles of the SDK package, not native execution or application builds. An interrupted combined SDK/application build produced no usable evidence. No implementation or feature acceptance is inferred from these checks.

The two listing commands were:

```powershell
go list -mod=readonly -deps -f '{{if not .Standard}}{{.ImportPath}}|{{.Module.Path}}|{{.Module.Version}}|cgo={{.CgoFiles}}{{end}}' github.com/modelcontextprotocol/go-sdk/mcp
go list -mod=readonly -m -f '{{.Path}} {{.Version}} go={{.GoVersion}}' github.com/modelcontextprotocol/go-sdk github.com/google/jsonschema-go github.com/segmentio/asm github.com/segmentio/encoding github.com/yosida95/uritemplate/v3 golang.org/x/oauth2 golang.org/x/time golang.org/x/sys golang.org/x/sync
```

| Gate | Status |
| --- | --- |
| Supported store-isolation/query-resource/completion prerequisite | Policy specified from the T001 spike (native Windows/amd64 and a WSL2 race run); design review outstanding; implementation evidence pending (T004/T034/T035 on all three CI platforms). |
| Five tools through official in-memory transport | Not run; no implementation yet. |
| Schema/default/output consistency and all acceptance scenarios | Not run. |
| Input/dispatch/notification/output and memory bounds | Not run; fixed values are review proposals, not measured guarantees. |
| Flooded reads plus synthetic ingest/device writes, no admission-induced stop | Not run. Requires supported isolation/bounds argument from T001 as well as workload evidence; backlog remains open. |
| Mandatory diagnostic admission/acknowledgment and failure fallback | Not run. Ordinary dropping no longer applies to mandatory loss/audit/fault records. |
| EOF/cancellation/terminal device faults and serve regressions | Not run. |
| Native Windows/Linux/macOS blocked/closed pipe and SIGPIPE tests | Not run on every platform. Cross-compilation is not native evidence. |
| Gofmt, vet, module integrity, full tests, vulnerability scan and five builds | Not run for implementation; no new implementation is present. |
| Real account | Not required for automated feature acceptance; not authorized or run. |

No runtime PASS, platform protection claim, compliance claim, exception or owner review verdict is inferred from this document. Populate implementation evidence with exact commands, dates, counts/skips and direct exit codes. Follow [quickstart.md](quickstart.md).

## Analysis Remediation (2026-11-17)

The user authorized documentation fixes after the read-only analysis. No constitution, pin, database, production code, resource-budget approval or owner verdict is changed by this remediation.

| Finding | Document action | Status / remaining evidence |
| --- | --- | --- |
| C1 - mandatory reports discarded | SEC-006, stdio contract/R3, plan/DTO state and T015/T016/T018/T036/T037 separate mandatory records from ordinary logs; fixed acknowledgment/failure latch makes overflow/failed delivery an unsuccessful normal stop. Outer Handler.Enabled admits WARN/ERROR at error level, then mandatory routing precedes the ordinary configured filter; audit uses a typed independent reporter. | Design corrected; delivery/overflow/loss-warning/severity/EOF-race tests not run. No claim that an undelivered record was recorded downstream. |
| U1 - ingest isolation not enforced | Withdraw opportunistic shared-gate proof and PASS; contracts/R6/T001 require a supported admission/resource/control argument, exact ceilings and adversarial/native evidence before design PASS or implementation. No implicit extra lease/reader, pin change or ErrBusy retry. | **OPEN at 2026-11-17**; policy specified 2026-11-18 (contracts/store-and-lifecycle.md), design review pending. Static inspection identifies relevant driver limitations, not a verified solution. Stop for an explicit owner prerequisite decision if existing architecture cannot meet FR-017. |
| I1 - mapped-LID fallback | FR-007/cross-story acceptance and T027 now explicitly prefer exact phone chat then exact mapped LID; phone/JID/LID inputs return the LID row when only it exists. | Artifact inconsistency corrected; runtime acceptance not run. |
| A1 - timer called completion ceiling | Contracts/plan/R6/tasks distinguish 1 s/10 s cancellation requests and 5 s public output-wait termination from actual SQL/transaction/pump completion. Real resource accounting stays until return. | Wording corrected; establishing actual supported store completion bounds remains part of OPEN U1/T001, not an assumed one-second drain. |
| I2 - US1 requires US2 discovery | US1 independently discovers two chat tools; a separate cross-story acceptance/T031 requires exactly five after both stories. | Story/dependency inconsistency corrected; no reduced final feature scope. |
| A2 - formatted phone search ambiguous | T027 gives explicit matching/nonmatching fixtures; query formatting remains literal, identity inputs still normalize. | Expected result specified; tests not run. |
| C2 - default 235-chat traversal absent | T020/quickstart explicitly traverse all 235 chats with omitted limit (30), 1 and 200, asserting completeness and uniqueness. | Test assignment corrected; tests not run. |

Static source research used the actual `go.mod` SQLite pin v1.61.0 and existing store admission/SQL; it ran no cancellation/isolation experiment. No module was downloaded/updated. The implementation permission and quiet-stop/marker requirements remain unchanged. Design approval must cover the corrected full feature directory after the prerequisite is resolved; artifact consistency review is not owner-judge.

## U1 Scratch Experiment: Later Next (2026-11-17)

Native Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`, repository module with `modernc.org/sqlite v1.61.0`. Synthetic WAL database and scratch harness: `D:\tmp\kilo\u1-spike\main.go` and `synthetic.db`; no real account data. One cancelled later-step case, one uncancelled comparison and one cancelled initial-query case; no skipped cases in these three runs. All commands exited 0 directly:

```powershell
$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\main.go'; exit $LASTEXITCODE
$env:CGO_ENABLED='0'; $env:U1_BASELINE='1'; go run 'D:\tmp\kilo\u1-spike\main.go'; exit $LASTEXITCODE
$env:CGO_ENABLED='0'; $env:U1_INITIAL='1'; go run 'D:\tmp\kilo\u1-spike\main.go'; exit $LASTEXITCODE
```

The harness opens one `database/sql` connection, begins a context-owned transaction, and runs `SELECT 1 UNION ALL SELECT sum(x + spike_mark() * 0) FROM (WITH RECURSIVE r(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM r WHERE x<2000000) SELECT x FROM r)`. After consuming the first row, the second `Next` runs in a goroutine. The public registered scalar `spike_mark` signals its first invocation within the aggregate, before cancellation; the callback itself runs once per aggregate row and affects measured runtime. The harness waits for actual `Next`, closes rows, rolls back or observes automatic rollback, then performs a write using the same single-connection pool. The retained scratch source contains the full reproduction and a 20 s failure watchdog; watchdog termination is not considered a successful drain.

| Case | Query return / entered | Request / actual return | Close and reuse |
| --- | --- | --- | --- |
| Cancel | Query 0.559 ms; aggregate entered 1.107 ms | Cancel at 1.107 ms; `Next=true`, `rows.Err()=context canceled` at 2.477 s, 2.476 s after cancellation. | `rows.Close` nil at 2.478 s; rollback reports already rolled back; write nil at 2.479 s, pool in-use 0. |
| Baseline | Query 0.622 ms; aggregate entered 1.172 ms | No cancel; `Next=true`, nil error at 1.724 s, 1.723 s after entry. | Close and rollback nil; write nil at 1.729 s, pool in-use 0. |
| Initial query | Aggregate entered 1.094 ms | Cancel at 1.094 ms; `QueryContext` returned `context canceled` at 1.636 ms, 0.542 ms later. | Rollback reports already rolled back; write nil at 9.812 ms, pool in-use 0. |

These are individual observations, not calibrated upper bounds. The scratch query and callback do not establish the duration or memory of production queries, SQLite VM-only cancellation, or all transaction-close races. Broader initial-query cancellation, folding, connection-acquisition cancellation, query plans, resource/WAL bounds, admission schedules and native Linux/macOS remain not verified. U1 stays BLOCKED; no completion policy or owner approval is inferred.

Documentation checks on 2026-11-17: `git diff --check` exit 0; `git ls-files --eol -- specs/006-mcp-stdio-tools` exit 0, all eleven tracked feature files `i/lf w/lf`. These checks do not verify the runtime mechanism.

## U1 Follow-up: Query Plans and WAL (2026-11-17)

Native Windows/amd64, Go 1.27.4, pinned SQLite driver, `CGO_ENABLED=0`; synthetic data only. Direct command results:

| Command | Result |
| --- | --- |
| `$env:CGO_ENABLED='0'; go test ./internal/store -run '^TestQueryPlans$' -count=1 -v -timeout 5m; exit $LASTEXITCODE` | Exit 0; one test with 24 passing subtests, zero skips, 57.139 s package time. Representative existing plans assert activity/name ordered scans, keyed get/direct lookups, indexed contact-chat membership probes, and contact-search GROUP BY/ORDER BY temporary B-trees. Assertions cover these fixtures only and do not prove scan/resource ceilings. |
| `$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\wal.go'; exit $LASTEXITCODE` | Exit 0; initial scratch version explicitly disabled autocheckpoint and held a snapshot across 1,000 synthetic 1 KiB inserts. Passive checkpoint reported busy/log/checkpointed `0,360,0`, WAL 1,483,232 bytes; after rollback, truncate checkpoint `0,0,0`, WAL zero. The retained harness was subsequently extended to 4,000 inserts and an environment-controlled default autocheckpoint. |
| `$env:CGO_ENABLED='0'; $env:U1_DEFAULT_CHECKPOINT='1'; go run 'D:\tmp\kilo\u1-spike\wal.go'; exit $LASTEXITCODE` | Exit 0; default autocheckpoint 1,000 pages; snapshot saw zero rows before and after 4,000 inserts in 40 transactions, 111.474 ms write time. Passive checkpoint `0,1465,0`, WAL 6,035,832 bytes; after rollback truncate checkpoint `0,0,0`, WAL zero. |

The WAL harness uses two public `database/sql` connections against a separate synthetic database, not the actual Store admission layer or its full production pragmas. It explicitly runs a passive checkpoint and later a truncate checkpoint; no checkpoint-duration, disk-ceiling, writer-isolation or worst-case claim follows. It demonstrates that a small read result and automatic checkpoint setting do not by themselves release a held snapshot's WAL. At that stage no batch prototype, admission schedule or cross-platform runtime evidence had been established; see the subsequent activity-keyset experiment below.

## U1 Follow-up: Activity Keyset (2026-11-17)

Native Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`, `modernc.org/sqlite v1.61.0`; scratch source `D:\tmp\kilo\u1-spike\keyset.go` and synthetic database `keyset.db`. No production code or account data. Both executions of `$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\keyset.go'; exit $LASTEXITCODE` exited 0. Between runs the NULL continuation was edited to add the explicit `last_message_time IS NULL` predicate; the retained source is the second version.

The fixture has 235 distinct JIDs, five repeated non-NULL timestamps and NULL timestamps. It compares the complete activity-order traversal against `ORDER BY (last_message_time IS NULL), last_message_time DESC, jid`. Batch sizes 1, 7, 50 and 200 respectively used 242, 42, 11 and 6 SQL queries; all returned exactly 235 entries equal to baseline. Each traversal also counted 102 JIDs containing `00`; that count is not a paging/filter acceptance test. The full traversal is materialized by the scratch harness and is not a production bounded-memory implementation.

| Query shape | Reported plan on final run |
| --- | --- |
| First non-NULL candidates | `SEARCH chats USING COVERING INDEX chats_last_activity (<expr>=?)` |
| Equal timestamp, later JID | `SEARCH chats USING COVERING INDEX chats_last_activity (<expr>=? AND last_message_time=? AND jid>?)` |
| Lower timestamps | `SEARCH chats USING COVERING INDEX chats_last_activity (<expr>=? AND last_message_time<?)` |
| First NULL candidates | `SEARCH chats USING COVERING INDEX chats_last_activity (<expr>=?)` |
| Later NULL JID, explicit `last_message_time IS NULL` | `SEARCH chats USING COVERING INDEX chats_last_activity (<expr>=? AND last_message_time=? AND jid>?)` |

The first version's later-NULL query omitted the explicit null predicate and reported only `(<expr>=?)`, despite `jid>?`; it did not demonstrate a JID range seek. Plan output describes planner strategy, not measured VM steps or a guarantee for other query shapes/statistics. The experiment has no page/offset, source-size, candidate-exhaustion, concurrent write, SQLite heap, WAL or actual-completion measurements. The query strategy, numeric limits, contact grouping and full admission policy remain unverified; U1 is still BLOCKED.

## U1 Focused Follow-up: List Chats Paging (2026-11-17)

Native Windows/amd64, Go 1.27.4, pinned `modernc.org/sqlite v1.61.0`, `CGO_ENABLED=0`. Synthetic scratch sources and database under `D:\tmp\kilo\u1-spike`; no account data or production code. Both commands exited 0 directly:

```powershell
$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\keyset.go'; exit $LASTEXITCODE
$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\page.go'; exit $LASTEXITCODE
```

`keyset.go` rebuilds the 235-chat fixture. `page.go` keeps a single read transaction, walks activity-index candidates in batches, filters synthetic JIDs in Go, and compares pages against full SQL ordering/filtering. Twelve cases ran, zero skips: nine successful baseline comparisons and three expected candidate-budget errors with a nil result, not a partial page.

| Case | Filter / offset / limit / batch / budget | Inspected / returned | Outcome |
| --- | --- | --- | --- |
| 1 | empty / 0 / 30 / 7 / 235 | 30 / 30 | Matches baseline |
| 2 | empty / 100 / 30 / 7 / 235 | 130 / 30 | Matches baseline |
| 3 | empty / 234 / 30 / 7 / 235 | 235 / 1 | Matches baseline |
| 4 | `00` / 0 / 30 / 1 / 235 | 53 / 30 | Matches baseline |
| 5 | `00` / 30 / 30 / 50 / 235 | 130 / 30 | Matches baseline |
| 6 | `00` / 100 / 30 / 7 / 235 | 235 / 2 | Matches baseline |
| 7 | `0234` / 0 / 20 / 7 / 235 | 235 / 1 | Matches baseline |
| 8 | `absent` / 0 / 20 / 7 / 235 | 235 / 0 | Matches baseline |
| 9 | empty / 100 / 30 / 7 / 110 | 110 / 0 | Budget error, no partial result |
| 10 | `absent` / 0 / 20 / 50 / 234 | 234 / 0 | Budget error, no partial result |
| 11 | `00` / 0 / 30 / 200 / 10 | 10 / 0 | Budget error, no partial result |
| 12 | empty / 0 / 30 / 7 / 30 | 30 / 30 | Matches baseline at exact budget |

Counts are candidates inspected by Go, not SQLite VM steps: `rows.Next` is called before checking whether the candidate budget is exhausted, and may execute SQLite work for one additional row; SQL itself may do more than one unit of work per returned row. This model does not implement folded name/JID matching, latest messages, source-length checks, resource accounting, real admission, cancellation or concurrent writes. Results do not clear T001.

### Candidate Budget Boundary Follow-up (2026-11-17)

The scratch `page.go` was subsequently revised in place: each statement's SQL `LIMIT` is `min(batch size, remaining candidate budget)`, the iteration loop stops at that limit, and the next statement is not issued when no candidate budget remains. Thus the retained source reproduces this run, not the earlier pre-check version. Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`, SQLite v1.61.0, same 235-row synthetic fixture. Command `$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\page.go'; exit $LASTEXITCODE` exited 0; **13 cases, zero skips**: the previous nine successful baseline comparisons and three no-partial budget failures, plus one exact-budget failure.

The successful full page at budget 30 inspected and returned 30 candidates. With an absent filter and budget 235, after inspecting all 235 fixture rows the model returned a budget error with no rows because it did not probe for end-of-input; at budget 236 the same empty page succeeded. The former is a deliberate conservative false-positive resource error, not proof that it is the final approved API policy. SQL execution/VM work can exceed the number of returned candidates, and no timing, heap, WAL, actual cancellation, production query or ingest-isolation bound follows from these cases. T001 stays BLOCKED.

## U1 Focused Cancellation and Concurrent Writes (2026-11-17)

Native Windows/amd64, Go 1.27.4, pinned SQLite v1.61.0, `CGO_ENABLED=0`. Synthetic scratch program `D:\tmp\kilo\u1-spike\cancel-wal.go` uses a separate synthetic WAL database, two `database/sql` connections and a scalar callback deliberately held by a channel. No real account or Store admission path was used. Command `$env:CGO_ENABLED='0'; go run 'D:\tmp\kilo\u1-spike\cancel-wal.go'; exit $LASTEXITCODE` exited 0; one controlled scenario, no skips.

After a read transaction's first result established a snapshot, a later `Next` entered the held callback. The context was cancelled and the handler was confirmed still running at 100.312 ms. While held, 1,000 synthetic 1 KiB rows were committed; passive checkpoint returned `busy=0, log=336, checkpointed=0`, WAL 1,384,352 bytes at 123.970 ms. The callback was then released; `Next=true` with `rows.Err()=context canceled`; rows close returned nil and rollback reported the transaction already rolled back. After actual return, truncate checkpoint returned `0,0,0` and WAL size zero.

The callback is intentionally blocked to make the schedule deterministic; it does not establish a normal folding duration, production query timing, a maximum retained WAL, or an ingest error. Writes succeeded in this run. It demonstrates that cancellation and a successful concurrent writer do not by themselves prove snapshot return. The source-derived near-capacity gate schedule and decision alternatives in research.md are reasoning, not an executed stress test. T001 remains BLOCKED; native Linux/macOS, actual store/device contention and a complete enforcement mechanism remain not verified.

## U1 First-Step Aggregation Prototype (2026-11-17)

Native Windows/amd64, Go 1.27.4, pinned `modernc.org/sqlite v1.61.0` (SQLite 3.54.1), `CGO_ENABLED=0`. Scratch program `D:\tmp\kilo\u1-spike\firststep.go`, run from the repository root so the module's pin is used; it builds `firststep.db` (172 MB) on first run: 300,000 synthetic chats plus 300 chats with 8,000-byte names, using the production `chats` schema and both indexes; 100,000 synthetic `whatsmeow_contacts` entries (30% `@lid`, half of them mapped) plus 5 group entries; one 64 MiB text value. Memory statistics are switched on before SQLite initialises, as `internal/store/sqlite.go` does. `fold_s` (Go `strings.ToLower`, one Go callback per value) stands in for `mcp_fold`; the contact SQL is otherwise the current `SearchContacts` text and the chat SQL the current `ListChats` text. No account data or production code.

Command `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/firststep.go` exited 0 directly (output kept in `firststep.out`); `done failures=0`. An earlier run without the memory-statistics step exited 1 on the two heap cases: `hard_heap_limit` is accepted but not enforced unless statistics are on (`DEFAULT_MEMSTATUS=0` in `PRAGMA compile_options`; separate check `heaplimit.go`, exit 1, 0 of 3 over-limit queries refused). This matches the store's existing handling and is not a store defect.

The prototype wraps each page query as `SELECT count(*), json_group_array(json_array(...) ORDER BY <page order>) FROM (<current query> LIMIT ? OFFSET ?)`, so every SQLite step of the read runs inside the first `step`, where the pinned driver's interrupt watcher and 1,000-instruction progress handler are installed (`stmt.go:287-299`, `sqlite.go:80-139`).

| Check | Result |
| --- | --- |
| Equivalence, wrapped versus current SQL | 23 of 23 pages identical: 13 chat pages (activity and name order, empty/rare/no-match/Unicode/`@g.us` filters, offsets up to 300,250) and 10 contact pages (empty, rare, name, phone digits, Unicode, no match, offsets up to 99,990). |
| Plans | Wrapped queries keep the current plans (index scan for chats; GROUP BY and ORDER BY temp B-trees for contacts) and add one `USE TEMP B-TREE FOR json_group_array(ORDER BY)` over at most the page's rows. |
| Cancellation, current chats SQL, later `Next` scanning to a rare match | Uncancelled median 268 ms; cancelled at 89 ms; 5 runs, cancel-to-return median 168 ms, max 280 ms: the step is not interrupted and runs on. |
| Cancellation, wrapped chats | Uncancelled median 383 ms; cancelled at 128 ms; 5 runs, `context canceled`, cancel-to-return reported as 0 s in every run, below this host's timer resolution. |
| Cancellation, current contacts SQL (sort in first step) | Uncancelled median 994 ms; cancelled at 331 ms; 5 runs, median 8.6 ms, max 9.2 ms. |
| Cancellation, wrapped contacts | Uncancelled median 1,020 ms; cancelled at 340 ms; 5 runs, median 8.0 ms, max 8.7 ms. |
| After each cancelled run | Rows close and rollback returned immediately; a write on a second connection then succeeded in at most 5.0 ms. |
| SQLite heap high-water (process-wide, includes open connections' page caches of about 2.3 MB) | Chats pages, current or wrapped: 2.27–2.34 MB. Contacts over all 100,000 entries, current or wrapped: 12.5–12.6 MB. Wrapping adds no measurable heap. |
| `sqlite.Limit(reader, SQLITE_LIMIT_LENGTH, 1 MiB)` | Wrapped page of 100 long chats (804,001 JSON bytes) succeeds; 200 long chats fails in the first step with `string or blob too big (18)`. On the reader `zeroblob(2000000)` fails; on another connection it succeeds: the limit is per connection. |
| 64 MiB text under a 32 MiB `hard_heap_limit` | `octet_length(v)` returns 67,108,864 and `CASE WHEN octet_length(v) <= 4096 THEN fold_s(v) END` returns the fallback without calling the fold on the value, both without error; `length(v)` and `SELECT v` fail with `out of memory (7)`. `octet_length` does not load the value. |

Limits: single host, single runs of five samples, synthetic data and a stand-in fold; timings are not bounds. Cancellation latency inside the first step is bounded by 1,000 VM instructions plus one Go callback, which the length guard bounds; the measurements agree but do not prove a maximum. Not covered: GetChat/DirectChat/ContactChats wrappers, latest-message retrieval, folded JID filtering, the Store admission path, concurrent ingest during a wrapped read, WAL retention under the wrapped form, other native targets. T001 remains BLOCKED pending those and the owner decisions in spike.md.

## U1 First-Step Prototype, Part 2: Remaining Tools, WAL and Rollback (2026-11-17)

Same host, toolchain, pin and stand-in fold as part 1. `D:\tmp\kilo\u1-spike\firststep2.go` extends `firststep.db` (now 294 MB) with the production `messages` schema and its sender indexes: two messages in each of 30,000 groups, one in every tenth direct chat, a target contact writing as a phone in the newest group and as its mapped LID in an old group, its direct chats, and one chat whose latest message is 64 MiB (impossible through the store's 256 KiB write limit; stands for legacy or damaged data). `D:\tmp\kilo\u1-spike\rollback.go` reuses the fixture.

| Command (repository root) | Exit | Result |
| --- | --- | --- |
| `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/firststep2.go` | 0 | `done failures=0`; output in `firststep2.out`. |
| `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/rollback.go` | 1 (expected) | Four pooled-connection forms clean; two dedicated-connection forms left transactions open (below). Output in `rollback.out`. |

| Check | Result |
| --- | --- |
| ContactChats keys, wrapped versus streamed | 14 of 14 identical: current (direct plus group-only) and planned (direct plus any chat) membership; both identities, phone only, LID only, unrelated and unknown contacts; limits and offsets. |
| Latest message, `octet_length`-guarded versus current SQL | 44 of 44 chats identical. Plan: point lookups only (`chats` primary key, `messages_chat_time`, contact and LID-map keys). |
| ContactChats plans | Streamed and wrapped both scan `chats_last_activity` with covering per-chat probes of `messages_chat_sender_phone` and `messages_chat_sender_jid`; wrapping adds one sort over at most the page. The walk is proportional to the chat count, not the page. |
| Cancellation, ContactChats current key walk | Streamed: uncancelled median 221 ms, 5 runs cancel-to-release median 61 ms, max 82 ms. Wrapped: uncancelled 352 ms, median 0 s, max 34 µs. |
| Cancellation, ContactChats planned key walk | Streamed: uncancelled 1,324 ms, median 121 ms, max 508 ms. Wrapped: uncancelled 948 ms, median 0 s, max 125 µs. Release is measured to `Conn.Close` returning. |
| WAL under concurrent writes, planned key walk, 3 runs each | A writer committed 1 KiB rows throughout (169–260 writes per read, max write 4.6–18.5 ms, no write error). At cancel the WAL held 1.16–1.78 MB. TRUNCATE checkpoint issued at cancel completed `0,0,0` with WAL 0 bytes after 441, 599, 682 ms streamed and 5.0, 4.1, 3.3 ms wrapped. |
| 64 MiB latest message under a 32 MiB SQLite heap limit | Current latest-message query fails `out of memory (7)`; guarded query succeeds, text NULL, `octet_length` 67,108,864, so the store can return `resource_limit` without loading it. |
| Cancelled reads in the store's pattern (pooled `Conn` per read, `BeginTx(ctx, nil)`, deferred `Rollback`, `Conn.Close`) | 400 runs (streamed and wrapped, default and read-only transactions), deadlines spread over 5–95% of a warm run: no transaction left open. |
| Cancelled reads on one `sql.Conn` held across reads | Probe `ROLLBACK` after each read found the old transaction still open in 33 of 100 streamed and 72 of 100 wrapped runs. An earlier `firststep2.go` version that held one connection failed the same way: `cannot start a transaction within a transaction`. Cause, from Go 1.27.4 `database/sql/sql.go:2214-2226,2136-2158`: on context cancellation `Tx.awaitDone` marks the transaction done and rolls back in its own goroutine, so the caller's `Rollback` returns `ErrTxDone` before SQLite has rolled back; only `Conn.Close` waits for it. |
| Success after the deadline | Streamed forms returned a complete, error-free result after their deadline had passed in 2–10 of 100 runs per form (an uninterruptible later step finished first). Wrapped forms: 0 in every form. |

Interpretation and limits: wrapping removes the later-step problem for all four multi-row reads; GetChat and DirectChat are already single-row point lookups whose only gap was materialising oversized values, closed by the guard. Snapshot release coincides with `Conn.Close`, so the tool reader must obtain and close a connection per read, as `Store.read` does, and must check its context after a successful read. Not covered: the real `mcp_fold`, folded JID filtering, the Store gate and device paths, other native targets, and memory or walk ceilings (ContactChats and filtered ListChats still walk every chat; contact grouping grows with the contact count). T001 remains BLOCKED pending those and the owner decisions in spike.md.

## U1 Walk and Grouping Ceilings (2026-11-18)

Same host, toolchain and pin (Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`, SQLite 3.54.1). Scratch `D:\tmp\kilo\u1-spike\ceiling.go` reuses `firststep.db` (300,303 chats, messages from part 2) and builds `ceiling-contacts.db` (266 MB, six synthetic contact variants, each its own device). `mcp_fold` is registered with an exact copy of `internal/store/fold.go` (NFC plus full case folding), not a stand-in. Readers use the store's read pragmas (`temp_store(MEMORY)`, `hard_heap_limit(268435456)`, `query_only(1)`) and a fresh pooled connection per measured read. Heap figures are SQLite's process-wide high-water above the level before the read, with memory statistics on.

| Command (repository root) | Exit | Result |
| --- | --- | --- |
| `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/ceiling.go` (first version) | 0 | `done failures=0`. Equivalence held, but the plans showed `USE TEMP B-TREE FOR ORDER BY` in the outer page query: a sorter of up to offset+limit rows (7.6 MB at offset 100,000 with 20-byte names; proportional to name size). |
| `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/probe/probe.go` | 0 | Plans of five forms: the outer sort disappears when the walk exposes its first order term as a column (`last_message_time IS NULL AS k0`, `name_quality = 0 AS k0`) and the outer query orders by `k0, ...`; the outer `ORDER BY` stays in the SQL. Without the alias, or with a materialised CTE, SQLite sorts. |
| `CGO_ENABLED=0 go run D:/tmp/kilo/u1-spike/ceiling.go` (alias form, final) | 0 | `done failures=0`; output in `ceiling.out`. |

**Chat walk ceiling.** `list_chats` and `get_contact_chats` walk at most C chats in page order (`... FROM chats ORDER BY <index order> LIMIT C`), filter, page and aggregate in the first step. A third column, evaluated only when the page is short, counts the table up to C+1 rows on the primary key. Rule: a full page is exact; a short page is exact only if the table has at most C chats; otherwise `resource_limit`, no partial page. Tested C = 1,000, 100,200, 150,500 and 400,000 against the current SQL.

| Check | Result |
| --- | --- |
| `list_chats`, 16 cases (both orders, empty/rare/no-match/Unicode/`@g.us`/long-name filters, offsets to 100,000) × 4 ceilings | 39 identical to the current SQL, 25 `resource_limit`; no refusal when the table fits in the walk, no success that differs. |
| `get_contact_chats` (planned membership), 7 cases × 4 ceilings | 11 identical, 17 `resource_limit`; no refusal when the table fits. |
| Plans, final form | Walk: `SCAN chats USING INDEX chats_last_activity` / `chats_name` (covering for contact chats, with the two per-chat `messages_chat_sender_*` probes); the only temp B-tree is `json_group_array(ORDER BY)` over at most the page; the size check is `SCAN chats USING COVERING INDEX sqlite_autoindex_chats_1` inside a scalar subquery. |
| Work at C = 100,200 (5 runs each): activity/name no-match, name long-name filter, unfiltered offset 100,000, contact-chats full walk | Median 86–325 ms, max 468 ms (contact chats). SQLite heap above base: 29–79 KB, and 2.16 MB for the activity scan, which reads table rows into the connection's page cache. Unfiltered offset 100,000 fell from 7.6 MB (sorted form) to 79 KB. |
| Cancellation at a third of the run, 5 runs each | All `context canceled`; cancel-to-return median 0 s (below timer resolution), max 119 µs. |

**Contact grouping ceilings.** `search_contacts` first reads, without folding, at most E+1 of the device's person entries: their count, the bytes of each chosen name, the bytes of all four name fields and the longest field. The grouped, folded, sorted page runs in the same statement only when all four are within the ceilings; otherwise its column is NULL and nothing is grouped or folded (`resource_limit`).

| Variant (device) | Entries | Chosen-name / all-field bytes | Current SQL: elapsed, SQLite heap above base |
| --- | --- | --- | --- |
| `short` (address-book names, 18 B) | 100,000 | 1.8 MB / 7.2 MB | 1.2–1.6 s, 14.9–17.0 MB |
| `ascii` (four 1 KiB `x` names) | 20,000 | 20.5 MB / 81.9 MB | 2.7–7.5 s, 83.5–99.7 MB |
| `expand` (four 1 KiB U+0390 names; folding triples them) | 20,000 | 20.5 MB / 81.9 MB | 4.6–9.5 s, 83.5–99.7 MB |
| `edge_e` (four 40 B U+0390 names) | 100,000 | 4.0 MB / 16.0 MB | 1.5–2.1 s, 23.6 MB |
| `edge_s` (four 1 KiB U+0390 names) | 4,096 | 4,194,304 / 16,777,216 | 0.9–1.5 s, 17.1–20.5 MB |
| `edge_s_ascii` (four 1 KiB `x` names) | 4,096 | 4,194,304 / 16,777,216 | 0.4–1.1 s, 17.1–20.5 MB |

Each variant measured with empty and `0001` queries at offsets 0 and 100,000 (the larger offset keeps every person in the ORDER BY sorter). A two-point fit from `short` and `ascii` gives about 63 bytes per entry plus 4.8 bytes per chosen-name byte; it predicted 25.5 MB for `edge_e` and 20.4 MB for `edge_s`, measured 23.6 and 20.5 MB. Fold expansion did not change the high-water (`expand` equals `ascii`).

| Check, proposed ceilings E = 100,000 entries, S = 4 MiB chosen-name bytes, A = 16 MiB all-field bytes, field 1 KiB | Result |
| --- | --- |
| Equivalence and refusal, 14 cases × 5 ceiling sets (unbounded, E = 1,000, S = 1 MiB, field = 100, A = 2 MiB) | 21 identical to the current SQL, 49 `resource_limit`; every refusal exactly when a ceiling is exceeded, with zero `mcp_fold` calls; no run over a ceiling. |
| At the proposed ceilings | `short`, `edge_e`, `edge_s`, `edge_s_ascii` run: max 2.33 s, max SQLite heap 23.6 MB above base. `ascii` and `expand` refused in 377–550 ms, 146 KB, no folds. |
| Cancellation at a third of the run, 5 runs each (`short`, `edge_e`, `edge_s`) | All `context canceled`; median 5.2–10.8 ms, max 36.8 ms. |

Interpretation and limits: both ceilings run entirely in the first step, so the earlier cancellation result applies; the chat ceiling makes walk work independent of the chat count, and the contact precheck bounds grouping memory and fold work before either starts. The ceilings are proposals, not approved values: the 32 MiB tool heap share they are sized against, C = maxOffset + maxListSize and the conservative refusal of a short page on a store larger than C (including a contact whose chats all lie beyond the newest 100,200) are owner decisions. Not covered: ingest's own SQLite heap demand alongside a tool read (so whether 256 MiB minus the share is sufficient is not shown), the Store admission gate and device paths, real-store integration, other native targets. Single host, five-run samples; timings are not bounds. T001 remains BLOCKED.

## Owner Decisions on U1 (2026-11-18)

The owner approved, in this session: a 32 MiB SQLite heap share for one tool read; keeping the 100,000 offset cap (which sets the 100,200-chat walk ceiling); conservative `resource_limit` for short pages on stores over that ceiling; capacity-neutral tool admission; and one extra ordinary waiter place while a tool read holds a slot (ordinary capacity stays 16 active + 64 waiting = 80; no 17th active slot or fifth reader). Later the same day the owner also approved the WAL guard (tool admission only while the -wal file is at most 64 MiB, cancellation above 128 MiB), the free-space admission condition (at least 640 MiB free) and the platform plan (Linux and macOS recorded as not verified now; the cancellation, ceiling, admission and WAL-guard tests become implementation tests run in CI on Linux, Windows and macOS). These are decisions on the U1 prerequisite, not an owner-judge review verdict.

## U1 Experiment 2: Coordinated Admission Model (2026-11-18)

Scratch module `D:\tmp\kilo\u1-spike\admission` (`gate.go`, `baseline.go`, `gate_test.go`; standard library only). `Gate` models a coordinated store gate under one mutex: ordinary operations (message writes, store reads, guarded device calls; nested device calls reuse their lease and never re-enter, `internal/store/device.go:280`) keep the current limits; a tool read is admitted only when no ordinary operation waits and a slot and a reader are free, never queues, and is cancelled when an ordinary operation must wait for a slot or when a fourth concurrent ordinary read needs its reader; its slot and reader stay accounted until it actually releases; the ordinary waiter cap is 64 plus one while a tool holds a slot. `Baseline` is the acquire path of `internal/store/admit.go` at `ade34d2`.

| Command | Exit | Result |
| --- | --- | --- |
| `CGO_ENABLED=0 go test -count=1 -v -timeout 2m ./...` (in the module, native Windows/amd64, Go 1.27.4), three iterations while fixing the harness | 1, 1, 0 | First run: schedule 1 failed because the test's waiter released at once (a slot really was free), and the invariant "waiting ≤ 64 + tool" fired when a tool released with 65 waiters (transient: the freed slot admits one). Second: a "waiter while a slot is free" check fired between a release's broadcast and the waiter's wake-up (transient). Both checks were replaced: the cap is asserted where a waiter registers, and missed wake-ups are detected as admission-wait timeouts, which must be zero. Final run: seven schedule tests and one stress test with three subtests, zero failures. |
| `wsl -d Ubuntu ... CGO_ENABLED=1 go test -race -count=3 -timeout 5m ./...` (WSL2, Go 1.27.3) | 0 | `ok`, no race reports. WSL2, not native Linux. |

| Schedule (final native run) | Result |
| --- | --- |
| 1. Ordinary operation waiting when a tool requests entry | Tool refused (`tool busy`) with no queue entry; the freed slot goes to the waiter, never to the tool. |
| 2. Ordinary arrivals after tool admission | 15 admitted at once with no cancellation; the 16th waits and cancels the tool at once; during a held 20 ms drain the slot stays accounted, a replacement tool is refused; the arrival is admitted when the tool releases. |
| 3. Earlier counterexample (tool + 15 active + 64 waiting, then an ingest write) | The write becomes waiter 65 and is admitted; the 81st ordinary operation is refused with 80 ordinary operations in flight. The baseline gate without tools refuses the same 81st operation. |
| 4. Repeated tool readmission against a full gate with a waiting ordinary operation | 10,000 of 10,000 attempts refused (tools can starve under load). |
| 5. Reader contention | Writes and three ordinary reads do not cancel the tool; a fourth ordinary read does; tool admission is refused while four ordinary reads run. |
| 6. Close while a tool has not returned | Close cancels it, refuses both classes at once and returns only after the tool's actual release. |
| 7. Control failure: tool held past the admission wait (shortened to 100 ms) | The waiting ordinary operation fails with `ErrBusy`. The guarantee rests on the measured release latency of a cancelled first-step read (≤ 37 ms in the ceiling runs), not on the gate. |
| Stress, 3 s each: 120, 24 and 12 workers (one in three ordinary operations a read) plus a tool loop whose reads return 0–3 ms after cancellation | No invariant violation, zero admission-wait timeouts, zero unexpected errors. 120 workers: 31,896 admitted, 106,604 `ErrBusy`, every one with 80 ordinary operations in flight; tools 0 admitted (starved). 24 workers: 33,605 admitted, 0 busy; tools 344 admitted, 342 cancelled. 12 workers: 17,660 admitted, 0 busy; tools 1,065 admitted, 684 cancelled. |

Result: in the model, a tool read never lowers ordinary capacity or overtakes waiting ordinary work; its only effect is to delay an ordinary operation by the tool's release latency. This is a model of the gate, not the store's code: the real gate change, its integration with `enter`/`quiesce`/device leases and a real-store schedule test are implementation work.

## U1 Experiment 4: Integrated Store Run (2026-11-18)

Scratch test `D:\tmp\kilo\u1-spike\zz_u1_integrated_test.go`, copied into `internal/store` only for each run and deleted afterwards (`git status` showed no change under `internal/` after every run). It opens a real store (`openTest`), seeds a synthetic device and the `edge_e` contact set (100,000 entries, four 40-byte U+0390 names; worst heap at the approved ceilings), runs the bounded `search_contacts` form through `Store.read` with the store's own `mcp_fold`, and ingests through `StoreMessages` (1,000 messages of 15 KiB, about 15 MiB of fields, or 60 of 256 KiB). Native Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`. Command each time: `U1_SPIKE=1 CGO_ENABLED=0 go test ./internal/store -run '^TestU1Integrated$' -count=1 -v -timeout 20m`.

| Run | Exit | Result |
| --- | --- | --- |
| 1, single concurrent phase | 0 | Ingest without errors, but the -wal file reached **756,802,832 bytes** while back-to-back tool reads ran. |
| 2, three phases | 1 | The first warm-up tool read failed with `context deadline exceeded`: it exceeded the store's 10 s operation limit (later runs took 3.1–4.5 s; the cause of the slow run was not identified). Cancellation returned the error as designed; no ingest was running. |
| 3, three phases, -wal size guard | 0 | See the final run; same pattern (peak 159,954,912 bytes guarded). |
| 4, byte-budget guard (cancel after 32 MiB of message fields committed during a read) | 0 | Ineffective: peak 606,641,192 bytes with 3 reads cancelled. Back-to-back reads chain their snapshots, so the WAL is never free of readers long enough to reset. |
| 5, final | 0 | Below. |

| Final run phase (20 s each, from a truncated WAL) | Batches / errors | Tool reads | Peak -wal | SQLite heap above use |
| --- | --- | --- | --- | --- |
| Ingest only (continuous, both batch shapes) | 22 / 0 | none | 41,438,992 | 0.59 MB |
| Back-to-back tool reads, unguarded | 15 / 0 | 5 completed | **602,751,912** | 24.3 MB |
| Tool reads admitted only while -wal ≤ 64 MiB, cancelled when it passes 128 MiB | 17 / 0 | 2 completed, 3 cancelled, 4,694 refused | 160,548,192 | 23.9 MB |
| Same guard, light ingest (one message every 500 ms) | 40 / 0, max 17 ms | 6 completed, 0 cancelled, 0 refused | 2,830,472 | 23.8 MB |

Alone: a tool read at the ceilings 3.1–3.7 s and 23.8 MB SQLite heap above use; a 15 MiB batch 0.9–1.1 s and 0.1 MB; a 60 × 256 KiB batch 0.65–0.79 s and 0.58 MB; 4.4 MB in use before. After every phase, four further batches with no tool brought the -wal file back to at most 64 MiB (`journal_size_limit`) by the second batch.

Interpretation and limits: SQLite heap is not the binding constraint (24.3 MB together, against 256 MiB). WAL retention is: tool reads that follow each other keep a snapshot open continuously, and the WAL then grows with all ingest; no ingest error occurred within 20 s, but on a real disk that growth ends at the disk guard, which refuses writes. A -wal size admission guard (≤ 64 MiB, the existing `walLimitBytes`) with cancellation at 128 MiB bounded the peak to 2 × 64 MiB plus one batch here. The store has no public WAL hook in the pinned driver (it exposes commit, rollback and pre-update hooks), so the guard reads the file size; a file of at most 64 MiB implies a WAL reset since the last growth beyond it. Not shown: behaviour on a nearly full disk (a proposed free-space condition is in research.md R6), guarded device operations under load, the real coordinated gate, other native targets. Single host, single runs; timings are not bounds. T001 remains BLOCKED.

## U1 Design-Review Follow-up: 5 s Timer and WAL Recovery (2026-11-18)

After the design review of `68ce167` (CONCERNS), the owner accepted a 5 s tool-store timer, a recovery TRUNCATE checkpoint and the low-disk refusal. Scratch test `D:\tmp\kilo\u1-spike\zz_u1_timer_test.go`, copied into `internal/store` only for the run and deleted afterwards (`git status` clean under `internal/`). Real store (`openTest`), synthetic device with the `edge_e` contact set (100,000 entries, four 40-byte U+0390 names), 150,000 chats with two messages in every tenth, written through the writer. Native Windows/amd64, Go 1.27.4.

| Command | Exit | Result |
| --- | --- | --- |
| `U1_SPIKE=1 CGO_ENABLED=0 go test ./internal/store -run '^TestU1Timer$' -count=1 -v -timeout 30m` | 0 | `PASS` in 100.07 s; output in `timer.out`. |

| Read through `Store.read` with a 5 s cancellation request, 15 runs each | Timeouts | Min / median / p90 / max |
| --- | --- | --- |
| `search_contacts` at the ceilings, offset 0 | 0 | 1.645 / 1.835 / 2.351 / 2.669 s |
| `search_contacts` at the ceilings, offset 100,000 | 0 | 1.854 / 2.079 / 2.375 / 2.590 s |
| `list_chats` activity, no-match walk of C = 100,200 | 0 | 130 / 141 / 167 / 173 ms |
| `list_chats` name, no-match walk | 0 | 93 / 120 / 149 / 171 ms |
| `list_chats` unfiltered, offset 100,000 | 0 | 107 / 120 / 181 / 192 ms |
| `get_contact_chats` walk | 0 | 234 / 247 / 315 / 357 ms |

| WAL recovery without writes | Result |
| --- | --- |
| Three rounds: an ordinary read snapshot held while six 15 MiB batches commit, then released, then no writes for 200 ms | -wal 209,642,112 / 226,183,912 / 241,015,912 bytes after release, unchanged without writes (the size guard would refuse every tool read: the defect the review found). `PRAGMA wal_checkpoint(TRUNCATE)` on the writer: busy 0, took 698 / 959 / 784 ms, -wal 0 bytes afterwards. |
| TRUNCATE issued while an ordinary read stays open for 300 ms | Waited in the busy handler, then succeeded: 550 ms, -wal 0 bytes. |

Interpretation and limits: the 5 s request covers the measured reads with margin on this host. Earlier real-store runs at the contact ceilings took 3.1–4.5 s and one cold run over 10 s, so slower hosts can still return `timeout`; the CI tests assert mechanisms, not durations. The recovery checkpoint costs about 1 s of writer time for a WAL near 2 × 64 MiB plus batches, and runs at most once per 30 s. Not shown: the gate-driven trigger itself (implementation, T004/T005), a near-full disk, other native targets.

### Remediation Validation

| Date | Command / check | Result |
| --- | --- | --- |
| 2026-11-17 | PowerShell checklist assertions over `git grep -h -E '^- \[[ xX]\]' -- specs/006-mcp-stdio-tools/tasks.md` | Exit 0: 42 sequential unchecked tasks, US1 8, US2 6, parallel 14, expected story boundaries and a file reference on every task. |
| 2026-11-17 | `git diff --check` | Exit 0: documentation patch whitespace-clean. |
| 2026-11-17 | `git ls-files --eol -- specs/006-mcp-stdio-tools` | Exit 0: all ten tracked feature files show i/lf and w/lf, no CRLF. |
| 2026-11-17 | Internal read-only remediation consistency pass and focused logger follow-up | Added explicit outer Enabled admission before ordinary severity filtering for real WARN sources at error level; corrected Handler ordering/current TextHandler terminology. No runtime tests, owner review or isolation proof. U1 remains OPEN/BLOCKED. |

## Owner-Accepted Amendment to the Isolation Policy (2026-11-18)

The design review of `87c3a11` returned CONCERNS. The owner accepted it without a further review, with the amendment below. It is recorded here, not in `contracts/store-and-lifecycle.md`, so that the reviewed commit stays current for the review gate; **it is binding for T004 and T005 and overrides sections 4 and 5 of that contract where they differ.** The code review checks the implementation against both. Fold it into the contract at the next design change that needs a review anyway.

**M1: the recovery checkpoint is tool-class work.**

1. A pending or running recovery checkpoint occupies the tool place in the gate, exactly like a tool read: while it is pending or running, tool reads are refused with `busy`, and a tool read and a checkpoint never hold the tool place together.
2. It starts only under the tool admission conditions: no ordinary operation waiting and an active slot free. It never waits for a slot. If those conditions do not hold, it is not started; the next refused tool call tries again, subject to the 30 s rate limit.
3. While it holds its slot, the ordinary waiter cap is 65, as for a tool read, so ordinary capacity stays 80.
4. Because it never coexists with a tool snapshot, its busy-handler wait is only for ordinary reads, which are short.
5. It does not count as a reader (it uses the writer connection). Ordinary writes queue behind it on the single writer connection for its duration (measured 0.55–0.96 s). Unlike a tool read, it is not cancelled by waiting ordinary work, because SQLite's checkpoint does not stop on interrupt in a useful state; the rate limit bounds how often this happens.
6. T004 adds two schedules: (a) a checkpoint pending or running while a tool call arrives: refused with `busy`, no tool snapshot while the checkpoint holds the place; (b) checkpoint holding a slot with 15 ordinary active and 64 waiting: the next ordinary operation becomes waiter 65, and only the 81st ordinary operation gets `ErrBusy`.

**Low findings, also binding for T005:**

- If reading the `-wal` file size fails, the tool read is refused with `busy` and an ordinary `store.limit` event; it is not admitted on an unknown WAL size. A missing `-wal` file counts as size 0.
- The free-space check (`diskGuard.check`, which may call the OS) and the `-wal` size checks run outside the gate's mutex. The gate decides on values read before it takes the lock; a check that has become stale between reading and deciding at most admits one tool read that the 128 MiB cancellation then bounds.
- The checkpoint does not use `Store.write`, which opens a `BEGIN IMMEDIATE` transaction in which `wal_checkpoint` cannot run. It runs `PRAGMA wal_checkpoint(TRUNCATE)` directly on a writer connection under its own gate entry, and does not consult the disk guard: it frees disk space, so it must run even while the guard refuses writes.
- Stale text superseded by `tasks.md` Current state, the contract's "Isolation Policy" and this amendment: `spike.md` "Resume Here" lines 12-13 and the "1 s store timer" mention near line 130; `research.md` R6 lines that say "Keep T001 blocked" or call the ceilings "not approved"; this file's "No review has run for 006" under the reviews table.

## Owner Reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-18 | 68ce167 | CONCERNS | Amend and review again: 5 s store timer, recovery TRUNCATE checkpoint, low-disk refusal accepted; all low findings fixed in the design |
| design | 2026-11-18 | 87c3a11 | CONCERNS | Accepted without another review; M1 and the low findings fixed by the owner-accepted amendment in this file (binding for T004/T005) |
| design | 2026-11-19 | 889ff65 | PASS | |
| design | 2026-11-19 | 42827be | CONCERNS | |

The review of `42827be` confirmed the revised dependency order is acyclic and the integrated obligations are unchanged. It found that the current native CI test output does not surface the named diagnostic cases, skips or child exit statuses required to close the foundation. It also noted that the design needs to preserve the configured ordinary log level after config loads without creating a second stderr pump. This was a read-only design review; no runtime or native test evidence was produced.

The [design amendment](tasks.md#diagnostic-command-tests-and-native-evidence-t045t046) assigns the focused native CI step to T046 and the shared ordinary threshold to T016/T046. T045 also covers healthy stderr at finalization and retains the failed-delivery exit precedence. These are planned changes, not test results or a new verdict.

A read-only pre-review of the draft atop `5b14118` also flagged loss of actionable refusal text, an ambiguous forced exit 1, and existing command tests dependent on ordinary stderr. The amendment now requires bounded actionable refusal records, test-only observation of the forced callback and timely acknowledgment after cleanup, and migration of existing assertions to observable owner readiness and cleanup without losing offline, empty-stdout or refusal checks. This pre-review was not a gate verdict; no runtime evidence was produced.

Design reviews are recorded above. The required design review follows task generation; the required code review follows implementation and evidence. A planning conformance table is not either review.
