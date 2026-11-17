# Contract: Bounded Process-Lifetime MCP Stdio

This is an application transport implementing the official SDK's public `Transport` and `Connection`, not a replacement JSON-RPC/MCP dispatcher. It exists because SDK v1.8.0 admits/queues work before receiving middleware and stock inherited-stream I/O is not uniformly interruptible.

## Resource Profile

Proposed fixed implementation constants, subject to the feature's design review. The selected values resolve the design; they are not measured compatibility guarantees or a grant to relax existing limits.

| Resource | Bound and outcome |
| --- | --- |
| Sessions/transports | One per command process; no restart or reconnect of the MCP transport. |
| Input OS work | One reader pump, one fixed current frame buffer, no read-ahead queue beyond that frame. |
| Output OS work | One writer pump and one current immutable frame; no output backlog. All writes are serialized, including direct overload errors. |
| Diagnostic OS work | One additional process-lifetime stderr pump, 16 queued records of at most 4 KiB each plus one active record. Producers never wait for OS output. Drop/count on full queue, disable/count after sink failure; no retry pump. |
| Input frame | 64 KiB including optional trailing CR and newline; stop at the ceiling before allocating beyond it. Reject JSON-RPC batches. |
| JSON shape | Maximum container depth 16 and 4,096 JSON tokens per input frame, checked before SDK decoding; no unbounded recursive shape walk. |
| Request IDs | Strings at most 128 encoded bytes or plain decimal integers in -(2^53-1) through 2^53-1, checked before SDK number decoding. Reject null, fractions, exponent notation and duplicate active IDs. Track decoded string/integer identity, not raw JSON spelling, so escaped-string aliases and -0/0 cannot bypass duplicate checks. Cancellation IDs obey the same rules. Four requests outstanding across all methods. |
| Tool credit | One tool admitted from pre-dispatch through actual response-write completion; no queued tools or second large result. |
| Notifications | Initialize once; cancel only known outstanding IDs, once each. Forward the first 1,024 notifications; attempted notification 1,025 is not forwarded and stops the session with a fixed diagnostic. Ignore unknown notifications without retaining/echoing them or consuming this forwarded-message budget. |
| Input partial-frame time | 10 s from first byte to complete newline. No timeout for a fully idle input before the first byte. |
| Input fields | Query 1,024 UTF-8 bytes; JID/contact 256 bytes; bounded initialization metadata inside the outer frame cap. |
| Tool work | 10 s from tool admission through output completion; earlier cancellation/deadline wins. |
| Store snapshot | 1 s from immediate gate entry through connection/transaction closure, one at a time; actual work remains accounted if cancellation takes longer. |
| Structured result | At most 2 MiB encoded JSON, precharged before marshaling. |
| Text result | At most 2 MiB ASCII-safe JSON representation of the same value. |
| Final output frame | At most 8 MiB including both representations, JSON-RPC envelope and newline; count actual serialized bytes. Fixed-size errors also obey the cap. |
| Output completion | 5 s including pump handoff and actual write; capped by the tool's remaining deadline. On failure permanently stop the session. |
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

Before entering runShell or emitting startup diagnostics, install stdio's nonblocking diagnostic sink and, on Unix, SIGPIPE handling. Keep the signal policy active until process exit if any pump may still write. Treat stdout EPIPE/short-write as a terminal transport error, not a successful response or an immediate account kill. Stderr EPIPE disables the diagnostic sink but does not itself fail MCP or block cancellation.

All stderr paths, including human messages and existing lifecycle/logger writes, must use the bounded sink. Records larger than 4 KiB are safely truncated with a marker; queue saturation drops records and increments a fixed counter. Do not recursively log drops/failures, retry indefinitely or wait for diagnostic flushing during cleanup. New structured diagnostic fields must be bounded and secret-free before logger serialization. Diagnostic delivery cannot be guaranteed to a permanently blocked/closed sink; this is best-effort operational logging, not durable audit. The third fixed OS pump obeys the same no-owner/no-store/no-callback and no-replacement lifetime rules as the protocol pumps.

## Completion and Exit

| Event | Required action / exit |
| --- | --- |
| Normal EOF between frames, normal client disconnect or first stop signal | Stop admission, cancel/drain requests, stop/join owner then close store/release lock. Exit 0 only if existing cleanup/quiet-stop rules and latched fault checks succeed. |
| Invalid/truncated/over-limit frame, response failure, assembly/write timeout or notification ceiling | Fixed secret-free diagnostic on stderr, permanent session stop and normal cleanup; exit 1. Do not attempt an unbounded final reply. |
| Invalid tool argument/not found/busy/result too large | Bounded tool error while session is writable; does not by itself disconnect or mutate account state. |
| Device/ingest terminal fault | Stop and drain; exit 1 regardless of an EOF race. Preserve existing marker policy and diagnostics. |
| Cleanup deadline exceeded | Report failure, retain ownership until actual account/store quiescence; no premature lock release. Existing explicit forced-exit/second-signal behavior remains unchanged. |

The existing owner may already begin retirement when the shared lifecycle context is cancelled. MCP integration must not delay network blocking or move the fence/barrier/library-context order. It must ensure tool work and account work are both quiescent before the store is closed and the lock released.
