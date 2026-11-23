# Public Contract: Local MCP Message Tools

The feature adds three read-only tools to existing stdio discovery. No separate CLI flags, credentials, `format`, remote transport or opt-in. The owner-selected local client may use the same stored data boundary as chat/contact reads. Use strict argument objects with no unknown, duplicate or null fields or type coercion. Apply schema defaults in the handler too. Reuse the existing contact parser (phone in supported formatting, phone JID or LID; supported device suffix rules), exact chat-address validation and input error conventions. Bounds are in UTF-8 bytes; string data is preserved when validated, except for identity normalization and explicitly marked previews.

| Tool | Required | Optional and defaults |
| --- | --- | --- |
| `list_messages` | None | `after`, `before`: exclusive RFC 3339 instants, absent means unbounded on that side; `chat_jid`, `sender_phone_number`: omit for no filter; `query`: empty (max 1,024 bytes); `limit`: 50 (1–200); `page`: 0 (0–100,000, with checked product at most 100,000); `include_context`: false; `context_before`, `context_after`: 5 each (0–20). |
| `get_message_context` | `message_id`: valid nonempty ID | `chat_jid`: optional exact address; `before`, `after`: 6 each (0–20). |
| `get_last_interaction` | `jid`: valid contact identity | None. |

IDs and exact chat addresses have a 256-byte ceiling. Input instants must be within `[2000-01-01T00:00:00Z, 2100-01-01T00:00:00Z)`; compare instants, not string encodings. Equal/reversed valid bounds succeed with no matches. Combine all enabled filters with AND. Query matches literal, case-folded and canonically normalized substrings in text or caption; preserve diacritics and original text. Filter by normalized sender author, not direct-chat membership. Validate context counts when context is disabled but do not acquire any neighbours.

## Success

Each success has structured content and a control-safe text rendering with the same semantics. Arrays are never `null`. See [Message fields](../data-model.md#stored-and-projected-types).

| Tool | Structured content |
| --- | --- |
| `list_messages` without context | `{messages: Message[]}`. Newest first; an empty array means no matches in that verified page. Complete page or reply-size error. |
| `list_messages` with context | `{messages: ContextHit[], page_reduced: bool, next_page?: {page: int, limit: int}, narrow_search?: true}`. Each hit is a message with its own `before`, `after`, reduction flags and optional `preview_fields`. |
| `get_message_context` | `{messages: [Message], before: Message[], after: Message[], before_reduced: bool, after_reduced: bool}`. Exact pair only if supplied; otherwise a unique matching ID is required. |
| `get_last_interaction` | `{messages: [Message]}`. Select the newest message from either local direct alias (from either side) or a group message authored by the contact under either alias; self-identity only considers self-chats. |

Only reply-size reductions use `page_reduced: true`. Supply `next_page` for the first unreturned match within the original verified page, or `narrow_search: true` when its offset exceeds 100,000, never both. Reuse the original filters and context options; replace only `page` and `limit`. A fully returned one-match continuation resumes via ordinary `page + 1`; stop at the first empty verified page. At `page: 100000` and `limit: 1`, a complete nonempty context listing instead returns `page_reduced: false`, `narrow_search: true`, and no `next_page`. Both renderings advise narrowing the search if the caller wants to browse further, without claiming that another match exists. An empty page does not carry this advice. There is no cross-request consistency promise during writes. Optional fields are absent when unused.

For context, the nearest same-chat neighbours may be outside the search filter. Context rows are ordered ascending by timestamp and ID and exclude the anchor. Reply shaping keeps full anchors ahead of context, and never previews neighbour messages, exact targets or last interactions. Mark every omitted requested side and every previewed field in both representations. No marker is needed for genuinely absent neighbours.

## Errors

Use MCP tool errors, not successful empty lists or internal exceptions, for invalid input, not found, ambiguity and limits. Map to bounded diagnostics using fixed categories; do not echo rejected values, text, captions, private file paths, credentials or SQLite details. Continue to use the existing guarded transport policy for malformed frames, cancellation, unavailability, timeouts and terminal faults.

| Condition | Public behaviour |
| --- | --- |
| Invalid input | Name the argument or incompatible range; do not echo its value. |
| Local store unavailable | Return `unavailable` in both renderings, with no result data or continuation. A closed store directs the caller to check the local session; a low-disk refusal gives a safe disk-space explanation. Do not suggest changing search filters or triggering recovery. |
| Missing exact/unique message or no last interaction | Identify what was not found without returning a different match. An explicit chat never falls back. |
| ID in 2–100 chats | Distinct `ambiguous_id` error; bounded structured `matching_chats` strings in ascending order, plus the same safe list in readable text. Ask for `chat_jid`. |
| ID in more than 100 chats | Limit error explaining there were too many matching chats; ask for `chat_jid`. Do not list a subset. |
| Listing work cap prevents page verification | `resource_limit` with a search-specific explanation and narrower-filter advice. No matches, page metadata or continuation. |
| Interaction work cap prevents winner or no-history verification | `resource_limit` explaining that the newest stored interaction could not be verified. No selected message, not-found claim, or advice to use unsupported filters. |
| Reply size cannot be recovered by the permitted shaping | `resource_limit` with a reply-specific explanation and an applicable remedy, such as a smaller list limit or narrower context where that can help. No misleading claim that an oversize exact anchor can fit with fewer neighbours. |
