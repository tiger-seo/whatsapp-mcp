# U1 Spike: Store Isolation and Actual Query Completion

**Created**: 2026-11-17
**Status**: Spike complete (2026-11-18); its policy is in contracts/store-and-lifecycle.md ("Isolation Policy"), with the owner's prerequisite decisions recorded in verification.md. T001 closes with the owner-judge design review.

**2026-11-19:** The WAL cancellation-only design was blocked in review. The peaks and time-to-release observations below remain historical. The proposed transaction fence and separate free-space proof are in [the current contract](contracts/store-and-lifecycle.md#5-wal-guard); do not infer either from this spike.
**Task**: T001 in [tasks.md](tasks.md), before formal implementation-readiness review and T002-T042.

## Resume Here

The spike is complete and its policy is in contracts/store-and-lifecycle.md ("Isolation Policy"). The first design review (`68ce167`, CONCERNS) led to the amendments in "Design Review Amendments" below. Next: the renewed owner-judge design review of the amended commit (tasks.md Current State). Do not start implementing the MCP server or assume that a context timeout proves SQL drainage.

All 42 feature tasks remain unchecked. This guide accompanies the design fixes after `/speckit.analyze`; inspect `git status`, recent commits and any remaining diff before working. Preserve existing changes, including unrelated libsignal backlog edits. Preliminary cancellation runs have completed; no implementation, new dependency or owner design review has run.

The working design is explicitly BLOCKED on U1. A review can provide feedback now, but cannot replace the missing supported isolation/completion mechanism. Resolve the prerequisite first, update the design, then commit under the authorized workflow and obtain owner-judge design review. No commit, push or account action is authorized merely by this guide.

## Progress: 2026-11-17

- Native Windows/amd64, Go 1.27.4, `CGO_ENABLED=0`, pinned `modernc.org/sqlite v1.61.0`; synthetic scratch harness and WAL database under `D:\tmp\kilo\u1-spike`, no real account data.
- The aggregate was confirmed entered by a registered scalar callback. An initial `QueryContext` returned `context canceled` 0.542 ms after cancellation. A later `rows.Next` returned 2.476 s after cancellation with `Next=true` and `rows.Err()=context canceled`; uncancelled comparison was 1.723 s. Rows/transaction cleanup and a subsequent write were observed after the operation returned.
- The callback ran once per aggregate row and affects the timing; these single samples are not production query timings or upper bounds. The source indicates that the driver's initial-query interrupt watcher does not remain installed for later `Next`. See research R6 and verification.md for exact commands, source references, timestamps, error and resource-return observations.
- Candidate direction, not an approved solution: indexed, bounded batches with explicit candidate/byte ceilings and bounded aggregation, while retaining one snapshot and real accounting. Establish semantics, query plans, shared SQLite/temp/WAL limits and actual completion before relying on it.
- Still open: realistic grouping/sorting and folding, connection acquisition and close races, worst-case and proposed query plans for all five operations, deterministic ordinary/device admission schedules, resource/WAL bounds, integrated pressure, and other native targets. The 1 s/10 s timers remain requests to cancel, not verified completion deadlines. T001 and implementation readiness remain BLOCKED.

### Follow-up: Query Plans and WAL

- The existing synthetic `TestQueryPlans` passed all 24 subtests on Windows/amd64. Its assertions cover representative plans for all five read operations: ordered index scans for chat listing/contact chats, indexed point lookups for get/direct chat, and temporary B-trees for contact grouping and name ordering. It does not prove bounded candidates, worst-case behavior or proposed 006 query plans. See verification.md for command and results.
- Static query analysis: filtered chat listing and contact-chat membership can scan many candidates even when `LIMIT` is small. Contact search groups aliases and sorts before paging; a bounded implementation must preserve grouping, all four name fields, phone-name preference and global ordering. Get/direct chat are primarily indexed lookups but still need pre-materialization source-length and latest-message budgets. See research R6 for file references and candidate approaches.
- A separate synthetic WAL experiment with the default 1,000-page autocheckpoint held a read transaction open while 4,000 rows were inserted. Passive checkpoint reported 1,465 WAL frames and zero checkpointed; the WAL was 6,035,832 bytes. After the reader rolled back, a truncate checkpoint returned the WAL to zero bytes. This demonstrates snapshot retention in that fixture, not an upper bound or a production-store integration test.
- Next: prototype and measure bounded candidate keysets including mixed-direction/null ordering and contact alias grouping; demonstrate cancellation/close and WAL release during actual later steps. Model and test admission atomically across ordinary, device and tool paths. Establish realistic worst-case work, memory and disk ceilings rather than choosing them from the sample measurements.

### Follow-up: Activity Keyset and Admission Analysis

- A scratch-only activity-order prototype walked 235 synthetic chats with timestamp ties and NULLs, using batch sizes 1, 7, 50 and 200. Every traversal matched a full SQL ordering, without duplicates or omissions. The test does not yet validate paged/filter semantics, source-byte budgets, snapshot concurrency, actual VM work, or production queries.
- On this driver and fixture, `EXPLAIN QUERY PLAN` for an initial NULL continuation without an explicit `last_message_time IS NULL` showed only the expression prefix of `chats_last_activity`, not a `jid` range. Adding the logically redundant explicit null predicate produced `(<expr>=? AND last_message_time=? AND jid>?)`. Merely naming an index does not establish a candidate-work bound. See verification.md for commands, plans and limitations.
- Source analysis suggests a candidate single-lock admission design: register ordinary arrivals before deciding grants, prioritize waiting ordinary/device work, fail tool admission without queueing, and count any tool within the existing 16 active and four reader totals through actual return. Device nested work must reuse its existing lease. This is a proposal, not code or test evidence: admission alone cannot prevent an already-running tool from consuming shared SQLite memory or retaining WAL after cancellation. Continuous ordinary traffic may starve tools.
- Next: test pagination and budget-exhaustion/no-partial-result cases for bounded candidate scans, including rare matches and high offsets; prototype contact grouping and verify actual plans and work; test atomic gate/close/device schedules. U1 and T001 remain BLOCKED.

### Focused 006 Follow-up: List Chats Paging

- A scratch-only `list_chats` activity-order model used the existing 235-chat synthetic fixture. Nine successful cases matched a full SQL baseline for JID substring filtering, offsets and page limits, including rare/no matches, NULLs and ties. Three budget-exhaustion cases returned an error with no partial rows. See verification.md for commands and individual counts.
- The model intentionally covers only JID filtering; actual 006 also needs folded name/JID matching, source-size checks, optional latest messages and the real store. Candidate counting occurs after SQLite's `rows.Next` call; the call that discovers exhaustion can already step one more row, and an SQL batch can do work before returning it. Thus the result establishes page behavior for these cases, **not** an SQLite work or actual-completion bound.
- Continue only with controls necessary for 006: bounded execution of its five reads, shared ingest/device admission and real completion. Contact grouping and name sorting remain necessary because they are exposed by those five tools; unrelated store operations are outside this spike. T001 remains BLOCKED.

### Focused 006 Follow-up: Candidate Budget Boundary

- The synthetic activity-list model now caps each SQL `LIMIT` by the remaining candidate allowance and stops before another `rows.Next` when that allowance is exhausted. Thirteen cases passed on Windows/amd64: nine baseline-equivalent pages and four expected errors with no partial result, including the exact-budget boundary. The full-page case can succeed at exactly its budget.
- A partial page at exactly the candidate limit returns `resource_limit` even if the last inspected candidate happens to be the last row: detecting end-of-input would require a further probe. This is a conservative trade-off in the scratch model, not an approved feature rule or compatibility claim.
- This closes only the model's extra-returned-candidate issue. An indexed SQL step can still do more work than one row, and the experiment does not bound SQLite VM instructions, field bytes, memory, WAL, cancellation or competing writes. Real 006 query semantics and the other four tools remain unverified. See verification.md; T001 remains BLOCKED.

### T001 Decision Point: Cancellation, Writes and Isolation

- A Windows/amd64 synthetic two-connection case held a later `Next` inside a deliberately blocked scalar callback after a real read snapshot was established. After cancellation, the step was still active at 100 ms; 1,000 synthetic writes succeeded while it was held, but a passive checkpoint showed 336 WAL frames and zero checkpointed, 1,384,352 bytes retained. After releasing the callback and waiting for actual `Next`/transaction return, a truncate checkpoint reduced WAL to zero. This is an intentionally noncooperative test, not a claim that production folding blocks for that duration, that ingest failed, or that the observed size is an upper bound. Full command and caveats: verification.md.
- The pinned driver's public APIs examined do not supply a per-connection later-`Next` interrupt/progress budget or tool-only heap quota. Its `sqlite.Limit` controls specific SQLite limits; `DBStatus` measures but does not reserve resources. The process-wide hard heap limit, WAL and disk remain shared with ingest. A candidate-count cap and priority gate do not alone provide the completion and shared-resource controls required by the contract.
- A conditional admission counterexample also rules out an unconditional no-interference claim: with a tool still holding one of 16 slots after cancellation, 15 ordinary active operations and 64 ordinary waiters, a further ingest write hits the existing immediate waiter-cap error; without the tool the same arrival can instead be waiter 64. This is a source-derived schedule, not an executed test, and FR-017 does not explicitly require universal equivalence to a no-tool run. It demonstrates why the exact accepted interference model matters; it does not prove all possible compliant designs impossible.
- **Conclusion:** A concrete supported isolation/completion mechanism has not been established within the current pin, architecture and budgets; a cancellation-only or candidate-only design cannot clear T001. No design PASS, numeric bound, revised requirement or implementation is implied. The documented stop rule requires an explicit owner prerequisite decision if the missing controls cannot be justified; options and trade-offs are in research.md. Other native platforms and integrated real Store tests are not verified.

### Candidate Mechanism: All Work in the First Step

- Each page query is wrapped as one aggregate row (`json_group_array(... ORDER BY <page order>)` over the current query with its `LIMIT`/`OFFSET`), so SQLite does all its work inside the first `step`, where the pinned driver's interrupt watcher runs. The later `Next` that cannot be interrupted then only returns a row that is already computed. Public API only; no driver change.
- Scratch prototype `firststep.go` (verification.md, 2026-11-17), 300,000 chats and 100,000 contacts: 23 of 23 pages identical to the current SQL. Cancellation of the wrapped chat query returned faster than the timer could resolve, while the current form's later `Next` ran on for up to 280 ms. Contacts (the sort already runs in the first step) returned in at most 9 ms. Wrapping adds no measurable SQLite heap.
- Supported per-connection size controls: `sqlite.Limit(SQLITE_LIMIT_LENGTH)` on the tool reader makes an oversized page fail in the first step, and does not affect other connections. `octet_length` checks a 64 MiB value without loading it, so fields can be length-guarded before folding.
- Part 2 (`firststep2.go`, `rollback.go`): ContactChats keys 14 of 14 and latest messages 44 of 44 identical to the current SQL. Cancelling the wrapped key walk released the connection within 125 µs, against up to 508 ms streamed. Under concurrent writes, a TRUNCATE checkpoint issued at cancel emptied the WAL in 3–5 ms wrapped, against 441–682 ms streamed. An `octet_length` guard lets GetChat report a 64 MiB latest message that the current query fails to load.
- Two rules for the eventual tool reader: (1) open and close a pooled connection per read, as `Store.read` does. `database/sql` rolls a cancelled transaction back asynchronously and only `Conn.Close` waits for it; a connection held across reads kept the old transaction open in 33–72% of cancelled runs. (2) Check the context after a successful read: streamed forms returned complete results after their deadline in up to 10% of runs; wrapped forms never did.
- What this does not settle: ContactChats and filtered ListChats still walk every chat, so their work grows with the chat count; the memory of the contact grouping grows with the contact count (about 10 MB above baseline at 100,000 entries), so it needs a count ceiling or a precheck rather than a candidate walk. The other three tools, latest messages, concurrent ingest, WAL retention under the wrapped form and other native targets are untested. The admission interference model and the 100,000 offset cap still need owner decisions.

### Candidate Ceilings: Chat Walks and Contact Grouping (2026-11-18)

Scratch `ceiling.go`, real `mcp_fold` (exact copy of `internal/store/fold.go`), store read pragmas; evidence in verification.md.

- **Chat walk ceiling C = 100,200** (maxOffset + maxListSize), for `list_chats` and `get_contact_chats`: the wrapped query walks at most C chats in index order, then filters and pages. A full page is exact; a short page is exact only if the table holds at most C chats, otherwise `resource_limit`. Unfiltered pages never hit it. At C on 300,303 chats: ≤ 468 ms, ≤ 2.2 MB SQLite heap (page cache), cancellation ≤ 119 µs; 50 pages identical to the current SQL, 42 correct refusals, no false refusal when the table fits.
- **Query shape matters:** the outer page query must order by an aliased walk column (`... IS NULL AS k0`, then `ORDER BY k0, ...`). Written with the raw expression, SQLite adds a sorter of offset+limit rows (7.6 MB at offset 100,000 with short names, roughly 100 MB with 1 KiB names). The plan must be asserted in a test.
- **Contact ceilings** for `search_contacts`, checked in the same statement before any grouping or folding: at most E = 100,000 person entries, S = 4 MiB of chosen-name bytes, A = 16 MiB across all four name fields, and no field over 1 KiB. SQLite heap is about 63 B per entry plus 4.8 B per chosen-name byte, so entry count alone is not enough: 20,000 entries with 1 KiB names used 100 MB and up to 9.5 s. At the ceilings the worst measured case is 23.6 MB and 2.3 s; refusals take ≤ 550 ms with no folds; cancellation ≤ 37 ms.
- `get_chat` and `get_direct_chat_by_contact` need no walk ceiling: point lookups plus the `octet_length` guard. Latest messages for a page are ≤ 200 point lookups.

**Owner decisions (all four approved 2026-11-18):** (1) a SQLite heap share for one tool read; the ceilings are sized for 32 MiB of the 256 MiB process limit. (2) Keep the 100,000 offset cap, which now also sets C. (3) Accept conservative `resource_limit` for short pages on stores over C chats, including a contact whose chats all lie beyond the newest 100,200. (4) The admission interference model (counterexample above). Still not verified: ingest's own SQLite heap alongside a tool read, the Store gate and device paths, real-store integration, other native targets. T001 remains BLOCKED.

### Candidate Admission and WAL Guard (2026-11-18)

The owner approved the four decisions above and one extra ordinary waiter place. Evidence in verification.md (experiments 2 and 4).

- **Admission (scratch model, 8 schedule tests plus stress, native and `-race` on WSL2):** a tool read never lowers ordinary capacity or overtakes waiting work. The earlier counterexample's ingest write is admitted as waiter 65, and an ordinary `ErrBusy` needs 80 ordinary operations in flight, as without tools. Tools starve under saturation. If a tool read fails to return within the 5 s admission wait, the waiting ordinary operation gets `ErrBusy`, so the first-step cancellation latency is what this rests on.
- **Real store, SQLite heap:** not the constraint. A tool read at the ceilings plus continuous maximal ingest peaked at 24.3 MB.
- **Real store, WAL: new finding.** Back-to-back tool reads keep a snapshot open all the time, so the WAL grows with all ingest: 603–757 MB in 20 s, against 41 MB without tools. A per-read byte budget does not help, because the snapshots chain. Guard: admit a tool read only while the -wal file is ≤ 64 MiB (`walLimitBytes`), and cancel it once the file passes 128 MiB. Measured peak 160.5 MB under continuous maximal ingest, no ingest errors, back to 64 MiB straight afterwards. Under light ingest, all tool reads completed and the WAL peaked at 2.8 MB.
- **Proposed, untested:** also admit tool reads only while free disk is at least 640 MiB (`diskResume` + 2 × `walLimitBytes`), so the extra WAL cannot by itself push free space below the 256 MiB reserve.
- One real-store tool read at the ceilings exceeded the 10 s operation limit once; other runs took 3.1–4.5 s. Cancellation reported it correctly, but the worst case is closer to the 10 s timer than the scratch runs suggested.

**Owner-approved 2026-11-18:** the WAL guard and its thresholds (transient WAL up to about 2 × 64 MiB plus one batch), the free-space condition, and the platform plan (Linux and macOS recorded as not verified now; the cancellation, ceiling, admission and WAL tests become implementation tests that CI runs on all three OSes). The design now carries this policy; T001 closes with the design review.

### Design Review Amendments (2026-11-18)

The owner-judge review of `68ce167` returned CONCERNS. The owner accepted the recommended fixes:

- **5 s store timer** instead of 1 s. Re-measured on the real store (scratch `zz_u1_timer_test.go`): 0 of 90 reads timed out; contact search at the ceilings 1.6–2.7 s, chat walks ≤ 357 ms.
- **Recovery checkpoint.** Confirmed defect: after a held snapshot and no further writes, the -wal stayed at 210–241 MB, so the size guard would refuse every tool read. A TRUNCATE checkpoint took 0.70–0.96 s and left 0 bytes; the gate now starts one, rate-limited, when it refuses on size and no tool read is active.
- **Low disk** below 640 MiB refuses tool reads with `unavailable` ("low disk space"), also while disconnected.
- Clarified: every gated reader-pool user counts as a reader; the 128 MiB check runs after each gated write; a failed free-space measurement does not refuse; one SQL definition per read; the command observes `tool_store_not_quiescent`; T040 also updates README.md and AGENTS.md.

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

The preliminary synthetic run and source inspection show a difference between cancellation during initial query execution and a later Next for the pinned driver. Do not generalize the observed duration into a bound; extend the experiment to realistic query stages and inspect supported APIs/source paths. No unsafe/private-handle access or driver patch is authorized.

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
