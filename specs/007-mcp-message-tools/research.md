# Research: MCP Message Tools

Planning findings are from the pinned source and the archived 002/006 design. They identify the design to verify, not measured performance or an owner approval.

## R1. Read boundary and statement cancellation

**Decision:** Add message reads to the store-owned tool reader, using one admitted read-only snapshot per call. Retain the 006 connection, single tool-credit, preferred ordinary-store work, WAL fence and fail-closed drain behavior. All nontrivial SQLite work must execute during a cancellable first step; close each statement before proceeding. Release the reader before transport encoding/writing, but not before the snapshot and connection actually return.

**Rationale:** The existing tool boundary keeps SDK queues and slow consumers from monopolizing the store; cancelling a context is not proof that the pinned driver has released a snapshot. The old ordinary `ListMessages` expands context and fails the whole call at its limits; repeatedly calling it could multiply work and mix snapshots.

**Alternatives considered:** Ordinary store methods from the handler, independent retry snapshots, and middleware-only admission. All are rejected because they bypass the existing boundary, increase shared effort, or weaken consistency.

**Sources:** [006 store/lifecycle contract](../../.specify/archive/006-mcp-stdio-tools/contracts/store-and-lifecycle.md), [existing message queries](../../internal/store/queries.go), [SDK connection guard](../../internal/mcp/connection.go).

## R2. Bounded proof of search results

**Decision:** Enumerate batches of at most 64 ordered candidate keys from an existing applicable index before evaluating any residual filters or folded text. Apply chronological bounds in the seek, merge at most two alias streams when needed, deduplicate by `(chat_jid, id)`, and count matches after all filters. Keep at most 200 selected keys and verify a full requested page or end-of-stream before requesting context. Use 100,200 visited candidates, 64 MiB of cumulative original field length and a separate 256 MiB cumulative folded-byte ceiling for text predicates. Neither byte total is a live allocation allowance. Reuse the existing folded values after length checks; read one candidate's text at a time, with a verified per-field bound on folded expansion. Use the existing 5-second store cancellation trigger. The date/chat/sender seek strategy affects how often the ceiling is reached, not result semantics. See the [store contract](contracts/store-and-lifecycle.md) for the design gate on underlying work and candidate costs.

**Rationale:** SQL `LIMIT` applied after a folded text predicate does not cap the work of reaching the first match. Charging bounded candidates and original bytes bounds repeated folding independently of the size of the complete history. A 50-match fixture with two 256 KiB fields per match accounts for 25 MiB of raw searchable data before any unrelated candidates. Narrow date/chat predicates can avoid examining unrelated rows. Reaching a ceiling without proving the page is the specified search limit error, never an empty/short page.

**Alternatives considered:** Whole-history SQL folding, FTS, and silent short search results. The first cannot prove bounded work; FTS changes literal matching semantics and introduces persistent write and migration costs; the last contradicts the accepted clarification.

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
