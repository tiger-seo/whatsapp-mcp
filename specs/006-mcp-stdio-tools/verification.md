# Verification: MCP Chat and Contact Tools

## Current State

2026-11-17: Analysis remediation is documented for the five-tool design; all 42 tasks remain unchecked (setup 3, foundation 15, US1 8, US2 6, polish/evidence 10). **Implementation/design-PASS readiness is BLOCKED** by supported store isolation and actual query-completion controls. T001 scratch research has begun with a later-Next cancellation counterexample below; all other prerequisite cases and the isolation mechanism remain open. T002-T042 cannot start before the prerequisite and owner design review clear. No runtime implementation, dependency change, account action or owner verdict has run. Both login tools and configurable read restrictions remain deferred. Earlier artifact checks do not resolve the blocker.

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

## Runtime Gates

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
| U1 - ingest isolation not enforced | Withdraw opportunistic shared-gate proof and PASS; contracts/R6/T001 require a supported admission/resource/control argument, exact ceilings and adversarial/native evidence before design PASS or implementation. No implicit extra lease/reader, pin change or ErrBusy retry. | **OPEN, blocking prerequisite**. Static inspection identifies relevant driver limitations, not a verified solution. Stop for an explicit owner prerequisite decision if existing architecture cannot meet FR-017. |
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

### Remediation Validation

| Date | Command / check | Result |
| --- | --- | --- |
| 2026-11-17 | PowerShell checklist assertions over `git grep -h -E '^- \[[ xX]\]' -- specs/006-mcp-stdio-tools/tasks.md` | Exit 0: 42 sequential unchecked tasks, US1 8, US2 6, parallel 14, expected story boundaries and a file reference on every task. |
| 2026-11-17 | `git diff --check` | Exit 0: documentation patch whitespace-clean. |
| 2026-11-17 | `git ls-files --eol -- specs/006-mcp-stdio-tools` | Exit 0: all ten tracked feature files show i/lf and w/lf, no CRLF. |
| 2026-11-17 | Internal read-only remediation consistency pass and focused logger follow-up | Added explicit outer Enabled admission before ordinary severity filtering for real WARN sources at error level; corrected Handler ordering/current TextHandler terminology. No runtime tests, owner review or isolation proof. U1 remains OPEN/BLOCKED. |

## Owner Reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |

No review has run for 006. The required design review follows task generation; the required code review follows implementation and evidence. A planning conformance table is not either review.
