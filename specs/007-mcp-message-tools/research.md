# Research: MCP Message Tools

Planning findings are from the pinned source and the archived 002/006 design. They identify the design to verify, not measured performance or an owner approval.

## R1. Read boundary and statement cancellation

**Decision:** Add message reads to the store-owned tool reader, using one admitted read-only snapshot per call. Retain the 006 connection, single tool-credit, preferred ordinary-store work, WAL fence and fail-closed drain behavior. All nontrivial SQLite work must execute during a cancellable first step; close each statement before proceeding. Release the reader before transport encoding/writing, but not before the snapshot and connection actually return.

**Rationale:** The existing tool boundary keeps SDK queues and slow consumers from monopolizing the store; cancelling a context is not proof that the pinned driver has released a snapshot. The old ordinary `ListMessages` expands context and fails the whole call at its limits; repeatedly calling it could multiply work and mix snapshots.

**Alternatives considered:** Ordinary store methods from the handler, independent retry snapshots, and middleware-only admission. All are rejected because they bypass the existing boundary, increase shared effort, or weaken consistency.

**Sources:** [006 store/lifecycle contract](../../.specify/archive/006-mcp-stdio-tools/contracts/store-and-lifecycle.md), [existing message queries](../../internal/store/queries.go), [SDK connection guard](../../internal/mcp/connection.go).

## R2. Bounded proof of search results

**Decision:** Use bounded index-ordered keyset batches before any residual filter or folded text evaluation. Apply chronological bounds in the seek, merge at most two alias streams when needed, deduplicate by `(chat_jid, id)`, and count matches after all filters. Keep at most 200 selected keys and verify a full requested page or end-of-stream before requesting context. Exercise batched, first-step folded matching against the fixtures in [feasibility.md](feasibility.md), evaluating only admitted candidates after length and byte checks; the batch returns keys and match flags rather than text. Compare against individual bounded projections if the batch plan cannot prove the ordering and budget boundary. The former 100,200-visit, 64 MiB original and 256 MiB folded limits are not production defaults. Choose fixed ceilings from the measurements and bind them in this design before implementation. Independently bound candidate visits, original and folded bytes examined, per-field expansion and transient allocation. Preserve the existing 5-second store cancellation trigger.

**Rationale:** SQL `LIMIT` applied after a folded text predicate does not cap the work of reaching the first match. Conversely, an arbitrary low work cap can reject routine sparse queries and all-zero short pages. The 400,000-message test scale (100,000 for the timed fixtures on macOS; see R5) and the oversized-field mix therefore define required compatibility fixtures, not proof that unlimited history is searchable. Narrow date/chat predicates still reduce the work. Reaching an approved ceiling without proving the page remains the specified search limit error, never an empty/short page.

**Alternatives considered:** Whole-history SQL folding, FTS, silent short search results, and separate field reads per candidate. The first cannot prove bounded work; FTS changes literal matching semantics and introduces persistent write and migration costs; silent partial results contradict the accepted clarification. Per-candidate reads missed the deadline; the [bounded batch probe](feasibility.md#bounded-batch-probe-2026-11-23) measured the global SQLite shape under the deadlines, but not its integration.

**Sources:** [message queries](../../internal/store/queries.go), [existing indexes](../../internal/store/migrations/0001_init.sql), [store limits](../../internal/store/limits.go), [FR-012](spec.md#functional-requirements).

## R3. Selected messages, neighbours and rendering

**Decision:** Project public fields only; preflight both encodings and the combined frame before assembling a reply. Reserve fixed metadata and one first-anchor slot, retain a contiguous prefix of complete anchors, then add closest neighbours in a stable walk. Track reduced sides separately from real conversation edges. If the first anchor cannot fit in a context-enabled listing, shorten only its text/caption fields at UTF-8 boundaries with field-specific markers. Continue at `page = original_offset + returned_anchor_count`, `limit = 1` with other inputs unchanged, subject to the offset limit. Run the completed response through the normal encoder and final wire checks.

**Rationale:** Two 256 KiB fields can grow past 2 MiB after escaping, even for a single anchor. The existing encoder is all-or-error and cannot trim a reply that was expensive to build. Accounting before fetching or retaining more material avoids both over-sized intermediates and surfacing a raw store-limit error for the default fixture. A short response can be lawful only for a verified page; no truncation behavior is added to existing chat/contact tools.

**Alternatives considered:** Larger output caps, smaller published defaults, a new cursor input, and unlimited retry with smaller pages. Each either changes an inherited safety/product contract or adds unnecessary state and load. Keep the existing limits and encode a continuation using the existing inputs.

**Sources:** [feature contract](spec.md#functional-requirements), [encoder](../../internal/mcp/results.go), [encoder tests](../../internal/mcp/results_test.go), [transport accounting](../../internal/mcp/transport.go).

## R4. Identity and disclosure

**Decision:** Resolve local aliases in the same snapshot and keep exact chat JIDs distinct from contact filters. Exact context uses the composite key; ID-only uses a capped distinct chat probe with a 101st sentinel. Last interaction compares the newest qualifying direct and group candidates using the same comparator; own identity checks self-chat only. Apply the existing owner-launched local scope; add no transport, accounts, consent mechanism, or export of device/media secrets.

**Rationale:** Falling back from a specified chat or guessing an alias can return another conversation. A bounded indexed probe prevents an ambiguous ID from becoming an unbounded error payload. The canonical source already defines local stored reads and the continuation/error treatment in the feature spec.

**Alternatives considered:** Old ID-only guessing, substring identity matching, and contact/network fallbacks. They violate exact identity, privacy, or offline operation.

**Sources:** [project requirements and local policy](../../docs/specification.md), [archived storage contract](../../.specify/archive/002-sqlite-storage/spec.md), [feature security scope](spec.md#security).

## R5. Platform search size, refills and ending a read

**Decision (owner, 2026-11-27):** Guarantee the short-message search fixtures at 400,000 rows on Windows and Linux and 100,000 on macOS. Keep the 5-second store cancellation trigger, the 10-second request budget, the search-work ceilings, the schema and the query shapes. Three defects found while preparing the change are corrected with it: a first page of one match refilled one key at a time for the whole walk; a statement failing because the call had ended could surface as a storage failure; and end of input right after a reply could be reported as a transport failure.

**Rationale:** On hosted macOS runners the 400,000-row fixture passed some runs and timed out in others on one image, and none of the four standard runner types met a 4.0 s bar on three machines each. At 100,000 rows both arm64 runner types passed every full and named run, with the slowest reply 2.14 s; at 200,000 neither did. A first page of one match over 100,000 rows exceeded the time limit on Windows, because each key cost one statement. Starting at one key and doubling keeps the early stop for a match at the top and reaches the ordinary batch size within a few refills. `database/sql` closes a cancelled transaction's statements on its own goroutine, so the next statement can fail with a library error that hides the cancellation. The framed reader closed the connection on end of input while a fully written reply could still be waiting for its acknowledgment; letting a write in progress finish for at most 250 ms leaves stuck and failed writes as they were.

**Alternatives considered:** An ascending covering index (failed the bar under load; doubles the database for large messages; saves little when messages arrive newest-first), memory-mapped reads (no gain on a first request, more than 300 MB resident), another hosted runner (none qualified), recording macOS timing without a guarantee (leaves users no history size to rely on), raising the store deadline (an inherited safety limit), and a smaller size on every platform (weakens a guarantee that Windows and Linux meet).

**Sources:** [verification record](verification.md#smaller-macos-sizes-results-2026-11-26-utc), [message search](../../internal/store/tool_message_search.go), [store reads](../../internal/store/ops.go), [framed transport](../../internal/mcp/transport.go), [006 exit rules](../../.specify/archive/006-mcp-stdio-tools/contracts/stdio.md).
