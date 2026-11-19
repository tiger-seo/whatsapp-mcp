# Contract: Bounded Process-Lifetime MCP Stdio

This is an application transport implementing the official SDK's public `Transport` and `Connection`, not a replacement JSON-RPC/MCP dispatcher. It exists because SDK v1.8.0 admits/queues work before receiving middleware and stock inherited-stream I/O is not uniformly interruptible.

## Resource Profile

Proposed fixed implementation constants, subject to the feature's design review. The selected values resolve the design; they are not measured compatibility guarantees or a grant to relax existing limits.

| Resource | Bound and outcome |
| --- | --- |
| Sessions/transports | One per command process; no restart or reconnect of the MCP transport. |
| Input OS work | One reader pump, one fixed current frame buffer, no read-ahead queue beyond that frame. |
| Output OS work | One writer pump and one current immutable frame; no output backlog. All writes are serialized, including direct overload errors. |
| Diagnostic OS work | One additional process-lifetime stderr pump; separate ordinary queue (16 records, 4 KiB each) and mandatory queue (16 records, 1 KiB each), plus one active record. Ordinary overflow drops/counts; mandatory overflow/failure latches unsuccessful shutdown. Producers never wait for OS output; no retry pump. |
| Mandatory reporting | 5 s from submission to complete OS-write acknowledgment, one shared earliest-deadline timer, no per-record goroutine. A fixed enum table of at most 128 count/last-time cells preserves failure categories without retaining arbitrary input. Any undelivered mandatory record or latch-capacity failure makes exit 1. |
| Input frame | 64 KiB including optional trailing CR and newline; stop at the ceiling before allocating beyond it. Reject JSON-RPC batches. |
| JSON shape | Maximum container depth 16 and 4,096 JSON tokens per input frame, checked before SDK decoding; no unbounded recursive shape walk. |
| Request IDs | Strings at most 128 encoded bytes or plain decimal integers in -(2^53-1) through 2^53-1, checked before SDK number decoding. Reject null, fractions, exponent notation and duplicate active IDs. Track decoded string/integer identity, not raw JSON spelling, so escaped-string aliases and -0/0 cannot bypass duplicate checks. Cancellation IDs obey the same rules. Four requests outstanding across all methods. |
| Tool credit | One tool admitted from pre-dispatch through actual response-write completion; no queued tools or second large result. |
| Notifications | Initialize once; cancel only known outstanding IDs, once each. Forward the first 1,024 notifications; attempted notification 1,025 is not forwarded and stops the session with a fixed diagnostic. Ignore unknown notifications without retaining/echoing them or consuming this forwarded-message budget. |
| Input partial-frame time | 10 s from first byte to complete newline. No timeout for a fully idle input before the first byte. |
| Input fields | Query 1,024 UTF-8 bytes; JID/contact 256 bytes; bounded initialization metadata inside the outer frame cap. |
| Tool cancellation | Request cancellation no later than 10 s after tool admission; earlier cancellation wins. This is not a claim of completed response or drain within 10 s. |
| Store cancellation | Request cancellation no later than 5 s after tool-read admission, or immediately when ordinary work must wait, a fourth ordinary read needs the reader or the -wal passes 128 MiB. Actual transaction/connection return remains accounted. The first-step read form makes the request effective; see the isolation policy in contracts/store-and-lifecycle.md. |
| Structured result | At most 2 MiB encoded JSON, precharged before marshaling. |
| Text result | At most 2 MiB ASCII-safe JSON representation of the same value. |
| Final output frame | At most 8 MiB including both representations, JSON-RPC envelope and newline; count actual serialized bytes. Fixed-size errors also obey the cap. |
| Output wait | Public Write returns failure no later than 5 s including pump handoff, capped by remaining tool cancellation time. Successful completion requires an actual full OS write; timeout does not claim the fixed OS pump stopped. On failure permanently stop the session. |
| Retained protocol state | Four outstanding entries, one current input frame, one current output frame, fixed notification counters/flags; no request/result history, subscriptions or server-originated calls. |

Preflight output must charge every fixed key/value, all variable strings, arrays, escaping and the duplicate text representation using overflow-safe arithmetic. Strings and list cardinality are already bounded before encoding. Audit SDK intermediate copies in the pinned source: the outer frame cap alone cannot prevent earlier marshal allocation. A conservative charge may reject an otherwise encodable value; it must not accept one that can exceed its budget. Do not marshal arbitrary internal objects, client metadata or driver errors as result content.

## Read and Admission

1. The single input pump reads bounded chunks. A session-owned timer starts when the first byte of a new frame is observed. At most one completed frame waits for `Read`; it cannot launch work.
2. Validate frame size, JSON shape, envelope, ID and essential-notification fields before SDK dispatch. Use the SDK's public JSON-RPC codec only after that validation.
3. For requests, reserve an outstanding-ID credit. `tools/call` additionally reserves the sole tool credit. For valid over-capacity requests, emit one bounded overload reply synchronously via the shared writer, then continue only after completion; do not add a reply queue or SDK entry.
4. Forward admitted requests to SDK dispatch. A receiving middleware may record tool/outcome timings, but it is not the admission boundary.
5. Forward initialization once and validated per-ID cancellation with the notification ceiling. Malformed essential notifications fail the session; unknown notifications are ignored, as they have no reply. No client responses are expected because the server initiates no RPC; unsolicited responses fail validation. Explicitly disable tool listChanged/subscription defaults and reject `subscriptions/listen` before SDK dispatch; no subscription state may be retained.
6. Retain credits until the response's actual write completes, even if SDK internal bookkeeping has already removed its ID. SDK parameter errors, unknown-tool errors and cancellation before handler entry retire the same transport-owned credit through their response; do not rely on a handler defer. If response construction/output fails or cancellation produces no response, stop the session and retain bounded accounting until actual SDK/handler drainage and buffer disposal are established. A live session must never lose capacity permanently from a rejected/cancelled call.

Prevalidate supported method parameter shapes before the SDK's decoder and sanitize every outbound SDK-generated protocol error to fixed text/data before marshaling. Preserve the request ID and protocol error code, but do not retain/echo malformed params or SDK error text containing them. Apply this at the server connection boundary for in-memory acceptance too, not only the OS framing wrapper. Test malformed params containing synthetic secrets and adjacent safe/unsafe numeric-ID boundaries.

Use a common guarded SDK connection above the production byte-framing connection or above the server end of `NewInMemoryTransports`. The in-memory path must enforce the same method admission/error-sanitization policy without OS pump assumptions. Configure SDK diagnostics to discard raw SDK messages/errors or map them to fixed safe categories; never enable LoggingTransport or a default logger that can bypass payload redaction. Application outcome logs remain bounded and secret-free.

Partial input EOF with non-whitespace bytes is a malformed/truncated frame failure. EOF between frames is normal client disconnection. Repeated initialization is rejected; the client cannot replace retained initialization state indefinitely.

## Write, Backpressure and Pump Lifetime

`Connection.Read` and concurrent-safe `Write` must select on a transport-owned done signal for every wait, including writer acquisition and completion. SDK response contexts may suppress cancellation, so they are insufficient. `Write` receives only bounded, preflighted successful results or bounded fixed errors; check actual final encoding again. Partial writes loop only within the same current frame/deadline.

Stop closes the connection-owned signal exactly once, rejects further reads/writes and cancels the SDK session by making its reader terminate. Do this before SDK session Close/Wait, because waiting for active handlers alone does not cancel them. Track and join actual handlers/store calls; a cancelled context is not proof they ended.

Do not synchronously close or join arbitrary inherited stdin/stdout handles as a prerequisite for connection stop. On Windows or other non-pollable descriptors an OS call may remain blocked. At most the two original I/O pumps may remain, holding only their fixed bounded buffers/channels; neither may access owner/store, callbacks, mutable result objects or diagnostics. Never launch replacement pumps. Once the command has safely drained requests, owner and storage and released ownership, normal process termination ends those pumps. This contract does not promise reusable in-process transports or zero pump goroutines before process exit.

Native process tests must prove command exit with blocked stdin, blocked stdout, slow reader and closed reader. Cooperative injected streams must release test pumps at test completion; tests must not accumulate process-lifetime pumps in the test runner. Use subprocesses for deliberately noncooperative streams.

Before entering runShell or emitting startup diagnostics, install stdio's nonblocking diagnostic sink, mandatory-reporting failure latch and, on Unix, SIGPIPE handling. Keep the signal policy active until process exit if any pump may still write. Treat stdout EPIPE/short-write as a terminal transport error, not a successful response or an immediate account kill. Stderr failure must never block cancellation; if a mandatory record is pending, or is subsequently submitted, it latches unsuccessful normal shutdown. Failure with only ordinary logs pending may disable those logs without terminating MCP.

All stderr paths, including human messages and existing lifecycle/logger writes, use the bounded sink but not the same loss policy. Ordinary successful-read/info/debug records may be safely truncated at 4 KiB and dropped/count on ordinary queue saturation. Mandatory records are bounded structured records of at most 1 KiB: denied/failed tool events, WARN/ERROR diagnostics including history_incomplete/receive overload, terminal faults and human refusal/failure messages. Unknown names/categories map to fixed enum entries; no credentials, bodies or client values are retained. Never truncate away the mandatory category/time/outcome identity. An unencodable/oversized mandatory record is a reporting failure, not an ordinary dropped message.

Route using a typed reporter/structured slog Handler with trusted level/event metadata, not substring searches in arbitrary rendered log bytes. Human failure/refusal output uses an explicit mandatory adapter; its bounded allowed metadata may include the existing numeric lock-holder PID. Application/WhatsApp-authored names or text cannot select the reporting category or supply latch keys. Existing incomplete-history WARN and receive-loss signals must be covered explicitly in tests, not assumed covered by testing a new synthetic event alone.

The outer `slog.Handler.Enabled` must first admit WARN/ERROR even when the configured ordinary log level is error; Handle then routes mandatory records before delegating ordinary output to its configured filter. A downstream writer behind the current level-filtering TextHandler is insufficient. Denied/failed request events use the typed mandatory reporter independently of logging severity. Do not enable unrelated info/debug output or debug message bodies to achieve this. Test actual incomplete-history/receive-loss sources at every supported log level, especially error, and preserve the ordinary filter unchanged for nonmandatory records.

Record mandatory submission in the fixed latch table before queueing; clear its pending state only after a complete OS write acknowledgment. The writer prioritizes the mandatory queue after its current write; it cannot preempt a blocked OS write, so one shared earliest-deadline timer watches all pending mandatory records. Full mandatory queue, an expired 5 s acknowledgment deadline, failed sink or exhausted fixed accounting immediately latches diagnostic_delivery_failed, closes tool admission and requests normal cleanup/exit 1. History-incomplete remains distinguishable from diagnostic-delivery failure. Do not report either as delivered, clear that operational failure on EOF, or claim durable audit. Exit failure is the observable fallback when no channel can deliver the warning; no silent successful continuation is allowed.

The reporter/Handler must only latch and send a nonblocking capacity-one wake-up; it must not log recursively, call owner methods or synchronously request cleanup while an owner/logging lock may be held. The command observes the latch outside those callbacks, stops admission and requests lifecycle cancellation; inspect it again after owner drain and before choosing exit status so EOF/cleanup races cannot hide the failure.

Do not recursively log drops/failures, retry indefinitely or wait for diagnostic flushing before cancellation. During cleanup, a pending mandatory record may use its original deadline but cannot prevent owner/store drain; if it remains unacknowledged at exit, latch failure rather than waiting indefinitely. The fixed failure table and timer are lifecycle state, not OS-pump callbacks; pumps hold only bytes/completion channels. Total OS pumps remain three. No marker policy is changed: reporting failure controls exit status, while only the owner may establish a quiet stop and clear its marker under existing rules.

## Completion and Exit

| Event | Required action / exit |
| --- | --- |
| Normal EOF between frames, normal client disconnect or first stop signal | Stop admission, cancel/drain requests, stop/join owner then close store/release lock. Exit 0 only if existing cleanup/quiet-stop rules and latched fault checks succeed. |
| Invalid/truncated/over-limit frame, response failure, assembly/write timeout or notification ceiling | Fixed secret-free diagnostic on stderr, permanent session stop and normal cleanup; exit 1. Do not attempt an unbounded final reply. |
| Invalid tool argument/not found/busy/result too large | Bounded tool error while session is writable; does not by itself disconnect or mutate account state. |
| Device/ingest terminal fault | Stop and drain; exit 1 regardless of an EOF race. Preserve existing marker policy and diagnostics. |
| Mandatory reporting unavailable/overflow/expired acknowledgment | Latch failure, stop admission, cancel/drain normally and exit 1; preserve original loss/fault category and never pretend the record was delivered. |
| Cleanup deadline exceeded | Report failure, retain ownership until actual account/store quiescence; no premature lock release. Existing explicit forced-exit/second-signal behavior remains unchanged. |

The existing owner may already begin retirement when the shared lifecycle context is cancelled. MCP integration must not delay network blocking or move the fence/barrier/library-context order. It must ensure tool work and account work are both quiescent before the store is closed and the lock released.

## Verification Stages

T045/T046 verify the diagnostic shutdown contract with the real command/lifecycle but without an MCP session: retain stdin-discard, install the single stderr pump and Unix signal policy before startup diagnostics, and prove the reporting failure wins normal EOF/signal exit after ordered cleanup. The diagnostic subprocess subset must pass on native Linux and native macOS before T015/T016 close, including blocked/closed stderr, SIGPIPE and first/forced-second signal handling. A Windows reporter helper, WSL2 or a cross-build does not establish either native result.

T024 reuses the same reporting path and revalidates failure and exit ordering with actual guarded SDK handler/session drain. T036-T038 still require the full integrated native matrix (including inherited stdin/stdout, stderr, and signals) and the three-pump bound. The foundation subset is not integrated acceptance.
