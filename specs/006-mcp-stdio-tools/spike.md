# U1 Spike: Store Isolation and Actual Query Completion

**Created**: 2026-11-17
**Status**: Not started. This is a research/run guide, not evidence of a supported solution or owner approval.
**Task**: T001 in [tasks.md](tasks.md), before formal implementation-readiness review and T002-T042.

## Resume Here

Start with experiment 1: cancellation during a genuinely expensive later `rows.Next` through the public `database/sql` API and the repository's exact SQLite pin. Do not start by implementing the MCP server or assuming that a context timeout proves SQL drainage.

All 42 feature tasks remain unchecked. This guide accompanies the design fixes after `/speckit.analyze`; inspect `git status`, recent commits and any remaining diff before working. Preserve existing changes, including unrelated libsignal backlog edits. No prerequisite experiment, implementation, new dependency or owner design review has run.

The working design is explicitly BLOCKED on U1. A review can provide feedback now, but cannot replace the missing supported isolation/completion mechanism. Resolve the prerequisite first, update the design, then commit under the authorized workflow and obtain owner-judge design review. No commit, push or account action is authorized merely by this guide.

## Question to Answer

Can the five MCP chat/contact read operations execute without a flood of tool requests causing terminal ingest failure through shared admission, connections, SQLite memory/work or WAL resources?

The spike must identify both a mechanism and its assumptions. A passing load test alone is not an enforceable guarantee. Do not aim to prove availability under arbitrary OS/disk failure; distinguish those failures from contention introduced by tool reads.

## Sources and Boundaries

Read `.specify/memory/constitution.md`, [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md) R6 and [contracts/store-and-lifecycle.md](contracts/store-and-lifecycle.md), especially its blocking prerequisite. Use [contracts/tools.md](contracts/tools.md) for exact query semantics and [verification.md](verification.md) for evidence/status.

- Confirm the branch and actual `go.mod` before running anything. The recorded pin is `modernc.org/sqlite v1.61.0`; do not silently use another version. Always `CGO_ENABLED=0`.
- Use synthetic local fixtures and scratch artifacts under `D:\tmp\kilo\u1-spike` or another explicitly approved temporary location. Verify the parent before creating files/directories. No real WhatsApp session, account, contact data or existing production data directory.
- Do not change production code, repository dependencies, the driver/WhatsApp pin, global store budgets or architecture. Scratch test programs/fixtures are allowed; no MCP implementation is required for this research.
- Preserve existing aggregate 16-active/64-waiting/four-reader limits, 32 MiB result-field budget and SQLite/disk controls. A separate admission class does not implicitly authorize a seventeenth lease or fifth reader.
- Ingest ErrBusy stays terminal. Do not retry it, hide it or redefine failed writes as success to make the experiment pass.
- Account for both message writes and guarded device operations. All-data read authorization does not authorize pairing, recovery, sending or access to a real account.
- Use bounded fixture sizes, experiment timeouts and scratch disk usage. A subprocess watchdog may contain a stuck synthetic experiment, but killing it is not evidence that application cancellation/drain works or a proposed production solution.
- Separate measurements on the actual driver/store from models, mocks and source inspection. Do not claim a scratch model proves integration with every store/device path.

## Experiment 1: Actual Cancellation and Resource Return

This is the first experiment because its result determines whether cancelling a tool read on ordinary store activity is a viable part of the design.

| Case | Observation required |
| --- | --- |
| Expensive initial QueryContext | Whether and when the public call returns after cancellation. |
| Expensive later rows.Next | Whether cancellation still stops work after the initial query has returned; cover database/sql behavior, not only a direct driver call. |
| Contact grouping/sorting | Whether cancellation stops the actual scan/sort rather than just the caller's wait. |
| Folding/source fields | Whether work outside interruptible SQLite steps has a bounded size and return path. |
| Connection acquisition and transaction/rows close | When resources actually become reusable, including cancellation/close races. |

Record monotonic elapsed times for operation start, cancellation request, actual SQL/iteration return, rows/transaction close, connection/lease return and a subsequent successful write. Record returned errors and any work still active. Demonstrate that the intended expensive phase was reached; a query that finishes before cancellation does not test that phase.

The current 1 s store and 10 s tool values are cancellation-request proposals, not assumed completed-work bounds. Do not implement an outer `select` timeout and call it successful cancellation while the query goroutine continues. Retain real accounting until return and do not admit a replacement snapshot during delayed drainage.

Static research raised a possible difference between cancellation during initial query execution and later Next in the pinned driver. Treat that as a hypothesis to test, not an already reproduced defect. Inspect supported APIs/source paths; no unsafe/private-handle access or driver patch is authorized.

## Experiment 2: Admission and Ordinary-Work Priority

Establish a baseline without tool reads, then replay the same synthetic workload with a single tool-read path. Use deterministic barriers/held stages, not just random concurrent loops.

1. An ordinary operation is already waiting when a tool read requests entry.
2. An ordinary message/device operation arrives immediately after tool admission.
3. A write arrives during initial SQL, a later expensive Next or folding.
4. Tool reads repeatedly attempt immediate readmission after completion/cancellation.
5. Message ingest and guarded device/LID work operate concurrently with tool reads.

Observe actual active/waiting counts, connection occupancy, cancellation/return points, write latency/errors and close races. Compare against the baseline, including near-capacity schedules. Identify which policy rule prevents tools from taking protected capacity or overtaking pending ordinary work.

An immediate shared-gate TryAcquire plus one tool credit is the rejected insufficient proposal. A candidate reservation/priority policy must be atomic, cover every relevant normal/device path and stay within approved totals. Ordinary writes must never wait on a tool semaphore, response output or diagnostic delivery. If a proposed policy cannot satisfy these requirements, record the counterexample rather than adjusting the fixture to conceal it.

## Experiment 3: Query Work and Shared Resource Bounds

Investigate each of ListChats, GetChat, SearchContacts, DirectChat and ContactChats. Preserve their exact normalization, alias grouping, filtering, sorting, latest-message and pagination semantics.

- Inspect and retain `EXPLAIN QUERY PLAN` for each relevant path and worst-case fixture. A page LIMIT does not bound candidates processed before GROUP BY/sort.
- Include maximal allowed text/caption/name fields, oversized legacy device names, many aliases and repeated sender/group participation. Keep the fixture itself within explicit scratch budgets.
- Measure candidate rows/bytes processed, Go allocations/retained state, SQLite/process memory, temporary disk and WAL growth/recovery. Go heap measurements alone do not account for every SQLite/process allocation.
- Determine which source values are checked before Scan/folding/materialization. Do not allocate a giant value merely to discover afterward that it exceeded its budget.
- Where current queries have unbounded work, prototype verified indexed keyset batches and bounded aggregation/top-page selection if feasible. Select and justify concrete total candidate, batch and byte ceilings; exceedance must return resource_limit, never a partial successful page.
- Investigate only supported per-connection controls. Advisory cache_size/private cache is not a hard heap quota; hard_heap_limit is process-wide; temp_store=FILE merely moves pressure unless temporary disk is bounded too. sqlite.Limit is not automatically a per-connection memory or VM-work budget.
- Demonstrate actual snapshot/transaction return and WAL release after cancellation. A small output object is not proof of small SQL intermediate work.

## Experiment 4: Integrated Adversarial Verification

Combine the candidate mechanism with synthetic ingest/device work and the schedules above. Show how the mechanism protects each shared resource; use pressure measurements to validate that argument, not replace it.

Exercise cancellation, close and ordinary arrivals at each held stage. Include a noncooperative/delayed-return case to verify that accounting is retained, no replacement snapshot is created and failure is reported instead of claiming drainage. Record exact normal-OS/driver assumptions and which control failures require unsuccessful cleanup.

Begin on the available native host and record its OS/architecture. Other target/control combinations remain not verified until actually tested; cross-builds or WSL2 do not establish their native runtime behavior. A local result is not a blanket five-target PASS.

## Evidence to Keep

| Evidence | Destination |
| --- | --- |
| Scratch harness location, version/fixture setup, exact commands and direct exit codes | verification.md |
| Case counts, actual OS/architecture, cancellation-to-return timings and resource high-water/recovery measurements | verification.md |
| Supported API/source references, query plans, admission rules, concrete work/resource bounds and assumptions | research.md, then the affected design/contracts |
| Failing schedules, unsupported controls, unrun cases and remaining limitations | Both documents, explicitly open/not verified |

Keep fixtures synthetic and redact unnecessary local paths/data from durable evidence. Retain enough harness/fixture instructions to reproduce the result; if scratch files will be removed, record how to recreate them first. A zero-test filter, wrapper timeout or watchdog termination cannot be recorded as a passing cancellation experiment.

## Outcomes and Stop Rules

1. **Supported within current constraints:** Record the concrete mechanism, resource/completion bounds, assumptions and evidence; update all affected artifacts. U1 can proceed to formal review only when its required outputs are established, not merely because tests were green.
2. **Bounded query redesign needed:** Document/prototype the affected query strategy and verify semantics and bounds before declaring the prerequisite resolved. Do not turn scratch research into unreviewed production implementation.
3. **Unsupported under current pin/budgets/architecture:** Keep U1 BLOCKED and present technical alternatives/trade-offs for an explicit owner decision. Do not change a pin, add a process/database, increase budgets or weaken FR-017/terminal ingest failures implicitly.

After a supported result, update research.md, verification.md, plan.md, contracts and affected task descriptions; update tasks.md Current State with the exact next step. Under the authorized commit workflow, commit the ready design and obtain owner-judge design review of that exact commit before T002-T042. This guide neither grants a review verdict nor authorizes a commit or implementation now.
