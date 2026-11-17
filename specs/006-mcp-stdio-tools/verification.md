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
| Supported store-isolation/query-resource/completion prerequisite | BLOCKED; one preliminary later-Next experiment run, remaining cases and mechanism not established. |
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
