# Feature Specification: MCP Message Tools

**Feature Branch**: `007-mcp-message-tools`

**Created**: 2026-11-22

**Status**: Draft

**Input**: [Roadmap item 007](../../docs/roadmap.md#007-mcp-message-tools): Add `list_messages`, `get_message_context` and `get_last_interaction`, returning lists of message objects and honouring every documented parameter.

**Source requirements**: [Project specification](../../docs/specification.md) S3, S6, P1, M4–M7, M10, Q3, D9, D18 and the local read-access decision in §8.11; preserved behaviour from §3.11 and §4.4; defects avoided from §6 #6, #13 and #14. The archived 002 and 006 specifications govern existing search and local read limits.

## Clarifications

### Session 2026-11-22

- Q: What should message search return if it hits its work limit before it can verify the requested page? → A: Return a limit error with no results or continuation and suggest narrower filters.

### Session 2026-11-23

- Q: How should ID-only lookup report more than 100 matching chats? → A: Under the owner-approved S6 amendment in the [project specification](../../docs/specification.md#85-storage), list all matching chats for 2–100; at 101 or more, return a bounded error asking for `chat_jid` with no chat list or selected message.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Find messages in stored history (Priority: P1)

As an account owner, I want my local assistant to find matching messages and captions so that I can retrieve information without reading whole conversations.

**Why this priority**: Search is the main way to find a message when its exact identity is unknown.

**Independent Test**: Seed conversations with matching captions, text, aliases, dates and tied timestamps; search from a disconnected session and compare results with the known matches.

**Acceptance Scenarios**:

1. **Given** seeded text and caption matches in several chats, **When** I combine date, chat, sender and search filters, **Then** only matching messages are returned in newest-first order, and captions are separate from text.
2. **Given** unchanged results with tied timestamps, **When** I request successive pages, **Then** order is stable and results do not repeat or disappear between pages.
3. **Given** matching messages with neighbours in the same chat, **When** I request context with unequal counts or zero on one side, **Then** each side honours its own count, in chronological order, even when neighbours do not match the search.
4. **Given** a seeded page of 50 maximum-size matches and five maximum-size neighbours on each side of each match, **When** I enable context and leave page size and context counts at defaults, **Then** I get a successful, nonempty result within the existing limits, see which context was reduced, and can continue at the first unreturned match without skipping it. Repeat with content whose escaped representation is unusually large.
5. **Given** a valid disconnected local session, **When** I supply a malformed argument, an above-cap size or an unsupported page offset, **Then** I get a tool error that names the problem, not an empty successful result.
6. **Given** a search that can verify some matches but not the requested page within its work limit, **When** I run it with or without context, **Then** I get a limit error suggesting narrower filters, with no matches or continuation returned.

### User Story 2 - Read around a specific message (Priority: P2)

As an account owner, I want to open a message and see what came before and after it so that I can understand the exchange.

**Why this priority**: Context prevents isolated quotes from being mistaken for the whole exchange.

**Independent Test**: Seed duplicate message IDs in two chats and surrounding messages on both sides; verify exact-chat, unique-ID, ambiguous-ID and missing-message requests.

**Acceptance Scenarios**:

1. **Given** a message ID that occurs in multiple chats, **When** I supply the chat address too, **Then** I see only that chat's message and closest requested neighbours, in chronological order.
2. **Given** the same duplicate ID, **When** I omit the chat address, **Then** I get a distinct error naming the matching chat addresses within the supported bound and guidance to retry with `chat_jid`; no message is guessed. A unique ID alone succeeds, and a missing ID or nonmatching pair returns a not-found error.
3. **Given** a message near the start or end of its chat, **When** I omit the context counts, **Then** I get up to six neighbours on each side, with an empty list on a side with no neighbours. Explicit zero requests no neighbours on that side.

### User Story 3 - Find my latest interaction with a contact (Priority: P3)

As an account owner, I want to find the most recent stored interaction with a contact without remembering which chat it happened in.

**Why this priority**: This answers a common question in one lookup, including when the latest interaction was an outgoing direct message.

**Independent Test**: Seed a contact's direct exchanges and group messages under phone and linked identities, alongside newer unrelated activity.

**Acceptance Scenarios**:

1. **Given** a contact with a known alias, **When** I look up that contact by phone, phone JID or LID, **Then** each spelling returns the same single newest qualifying message, from a direct conversation in either direction or a group message sent by the contact.
2. **Given** newer messages sent by the account to other contacts, **When** I look up the account's own identity, **Then** only its self-chat qualifies. A valid contact with no qualifying history returns a not-found error.

### Edge Cases

- No search matches or a page beyond the last match returns an empty list, not a not-found error. `after` and `before` are exclusive; equal or reversed valid bounds match nothing.
- A query containing spaces, punctuation or wildcard characters is matched literally. A caption may be the only match. An unmapped LID is not guessed to be a phone number.
- A duplicate ID in more than 100 chats returns a bounded error explaining that too many chats matched and asking for `chat_jid`; it never picks a chat or presents a partial set as complete.
- Reaching the start/end of a chat or explicitly requesting zero neighbours is not budget trimming. A sparse chat is not padded with unrelated messages.
- A nonempty context listing at `page: 100000`, `limit: 1` cannot be advanced by `page + 1`. If the base page is complete, keep it marked complete and advise narrowing the search to browse further, without claiming that another match exists.
- Malformed arguments, out-of-range counts, unsafe page offsets and unavailable storage give distinct, actionable errors; none is reported as an empty successful search.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The existing owner-launched local MCP session MUST offer exactly three new read-only tools: `list_messages`, `get_message_context` and `get_last_interaction`. They MUST use only locally stored data and identity mappings, remain available when disconnected if local reads are permitted, and add no account, network or persistent-data side effects.
- **FR-002**: Successful calls MUST return structured results containing lists of message objects and a text rendering with the same meaning. Each object MUST include `id`, `chat_jid`, `chat_name`, `sender` (full WhatsApp sender address), `sender_name`, `timestamp`, `is_from_me`, `content`, `caption` and `media_type`. Unknown names or absent content, captions or media types are empty strings. Use existing local name precedence and never fetch a name over the network. Use RFC 3339 for timestamps in inputs and outputs. Do not double-encode the structured result or add a `format` option.
- **FR-003**: Published snake_case arguments, types, ranges and actual defaults MUST agree with observed behaviour. Reject null, duplicate or unknown fields, invalid types and malformed or out-of-range values with a tool error identifying the problem without echoing private input. Retain existing pagination and field limits: maximum offset 100,000, query at most 1,024 UTF-8 bytes, chat address and message ID each at most 256 UTF-8 bytes; accepted timestamps span 2000 inclusive to 2100 exclusive in UTC.
- **FR-004**: `list_messages` MUST combine optional `after`, `before`, `chat_jid`, `sender_phone_number` and `query` filters with AND. Match the exact chat address, the normalized sender identity, and a case-insensitive Unicode literal substring of `content` or `caption`, treating canonically equivalent text as equal and preserving accents and the original returned text. An omitted or empty query imposes no text filter. `sender_phone_number` matches the author, not the whole direct conversation; accept phone numbers, phone JIDs and LIDs under the existing contact identity rules, resolving known aliases without guessing.
- **FR-005**: `list_messages` MUST use `limit` (default 50, range 1–200) and `page` (default 0, range 0–100,000, with `page × limit` at most 100,000). Sort by descending timestamp, then descending chat address and message ID. Paging counts only base matches, not neighbours. No matches MUST return an empty `messages` list.
- **FR-006**: `list_messages` MUST accept `include_context` (default false), `context_before` and `context_after` (each default 5, range 0–20). Validate supplied counts even when context is off, but do not retrieve or return neighbours in that case. When enabled, attach `before` and `after` lists of the closest same-chat neighbours to each match; neither list includes the match itself. Present each list oldest first, using message ID ascending on timestamp ties; determine sides with that same ordering. Filters select matches, not their neighbours. Explicit zero means no neighbours on that side.
- **FR-007**: `get_message_context` MUST accept required `message_id`, optional `chat_jid`, and `before` and `after` counts (each default 6, range 0–20). A supplied chat selects only the exact pair, with no fallback; without a chat, only a unique match may succeed. Missing matches are not-found errors. For 2–100 matches, return an ambiguous-ID tool error listing all matching chat addresses in ascending order and asking for `chat_jid`. Apply the bounded ambiguity behaviour in Edge Cases above 100. Return the selected message as a one-item `messages` list and its `before` and `after` lists using FR-006 neighbour rules.
- **FR-008**: `get_last_interaction` MUST require `jid`, accepting the same contact identity forms as FR-004. Return a one-item `messages` list containing the newest stored message qualifying under either identity: a message in the contact's direct chat from either side or a group message authored by the contact. For the account's own identity, only its self-chat qualifies. Use FR-005 tie-breaking. Return a specific not-found error when no interaction qualifies. If a work limit prevents verifying the outcome, return an interaction-specific limit error with no message, not a not-found or reply-size error. Explain that the newest interaction could not be verified; do not suggest filters this tool does not accept.
- **FR-009**: The tools MUST respect the existing 2,000 context rows and 32 MiB result-data ceilings per call, as well as the tighter local reply limits of 2 MiB per representation and 8 MiB for the whole reply. Once `list_messages` has verified a nonempty base page under FR-012, a default-size call with context enabled MUST return a nonempty result even if a full reply with maximum-size messages would exceed those ceilings. This guarantee covers reply-size limits, not cancellation, storage errors or unfinished searches. Prefer a contiguous newest-first prefix of matches over keeping extra neighbours. If fewer neighbours fit, keep the closest ones on each side, keep their order, and explicitly identify each reduced side. Do not label genuinely missing neighbours as reduced.
- **FR-010**: Whenever the budget shortens the base page of a listing with context, the result MUST say it was shortened and provide explicit next `page` and `limit` values that resume at the first unreturned match with the same filters and context options. A continuation may use `limit: 1` and the absolute match offset as `page`; for such a one-match page, the next ordinary page is `page + 1`. If the next offset is outside the supported range, state that the caller must narrow the search instead. No shortened page with matches may return zero anchors. If even the first valid match cannot fit in full in a listing with context enabled (including a one-match continuation), return that anchor with explicitly marked, shortened text/caption as necessary, preserving its identity and other fields; matching still uses the full stored values. Never silently shorten text or caption or change stored data. A listing without context MUST return a complete page or an actionable size error. Exact lookup and last-interaction calls MUST return the complete selected message or an actionable size error; exact context MAY shorten only neighbours with the same visibly reduced-side semantics.
- **FR-011**: Each new tool MUST have an offline acceptance case through the existing in-memory client/server test path, including schema defaults and every argument, the duplicate-ID and no-result cases, and the maximum-size default-context page. Existing chat/contact tool behaviour and all five supported build targets MUST remain intact. No acceptance step requires a real WhatsApp account.
- **FR-012**: If `list_messages` reaches a search-work limit before verifying the base page's membership, order or end, it MUST return a limit error suggesting narrower filters, with no matches or continuation. This applies whether or not context is requested. The successful shortening rule in FR-009–FR-010 applies to a verified base page whose reply would exceed a result budget, not to an unfinished search.

### Key Entities *(include if feature involves data)*

- **Message**: One stored item identified by chat address and message ID, with sender, timestamp, text, optional caption and media type.
- **Message context**: The same-chat messages immediately before and after a selected message, with reduced-side indicators when a reply cannot include them all.
- **Message page**: Ordered base matches for a search, plus optional context and a next request when the reply budget shortens the page.
- **Contact identity**: A phone number or WhatsApp address, including a LID (WhatsApp's privacy-preserving identifier); known aliases refer to the same contact.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the agreed offline acceptance fixture, 100% of supported filter, paging, context and identity cases return the expected messages in the expected order; each published argument has at least one case demonstrating its effect or validation.
- **SC-002**: In all duplicate-ID fixtures, the caller either gets the requested chat's message or an actionable ambiguity error; zero calls return another chat's message as the selected result.
- **SC-003**: On a seeded 50-match maximum-size page, a default-size context request returns at least one match within the existing 10-second request budget, with any omissions disclosed. On unchanged data, continuing to the end of the fixture recovers all 50 match identities exactly once, including in the escape-heavy variant.
- **SC-004**: A reviewer can complete all three user journeys from the local assistant on the offline fixture, with zero WhatsApp connections or writes to conversation or account records. Structured and readable outputs agree on matches, order and completeness.
- **SC-005**: On 400,000 seeded short messages, a default search with 12 matches spread across history and a separate default search with no match both complete with exact results within the existing 10-second request budget. On a separate history with 128 newer maximum-size nonmatching messages followed by an older match, a default search finds that match. These fixtures do not guarantee a complete search for an arbitrarily large history.

## Assumptions

- **Scope and dependencies**: Feature 006 supplies the existing local session and admission boundary; feature 002 supplies stored messages and query semantics. Acceptance uses seeded data and the fake account boundary, not live ingest. Sending (008), media download (009), HTTP/proxy access (010), webhooks (011), pairing and ingest changes are out of scope.
- **Omitted versus explicit zero**: Missing counts use the established defaults; zero neighbour counts request none. A zero page size is invalid, not an alternate spelling for the default.
- **Browsing under change**: Paging has no cross-request snapshot guarantee; repeatability and no-skip continuation apply to an unchanged matching history. The marked preview is a last-resort browse result, not a guarantee that a full, exceptionally expensive message can fit in a single reply.

## Security

**Assets and boundaries**: Message text, captions, identities, names, account credentials and shared read/write capacity. Boundaries are the owner-launched local client to the reader, stored sender-authored data to the assistant, message queries to the shared store, and diagnostics to the local operator.

| Finding | Treatment and remaining scope |
| --- | --- |
| F01 | Apply 006's local read-only boundary before disclosure. Stored data, request fields and purported client approvals cannot grant more authority. Retrieved data can mislead a client; this feature neither closes F01 nor adds restricted-read profiles or account actions. Send authorization belongs to 008, remote-policy parity to 010, and configurable reads and MCP pairing remain in the named 006 backlog items. |
| F02, F04–F08 | Do not disclose credentials, pairing material or media decryption metadata. No extra listener, secret handling, file access, media processing or webhook capabilities. Those controls belong to 008 (sending), 009 (downloads), 010 (remote access), 011 (webhooks), and the deferred 006 login-tools work. |
| F03, F12–F14 | Preserve private storage, locking, fail-closed recovery and shutdown. Reads do not initiate repair, write response files or persist new archives. Use the local client only if its downstream retention is acceptable to the owner. Deployment, backup/retention and incident guidance remain 012. |
| F09–F10 | Enforce inherited input, output, cancellation, waiting-work and store-read limits, including coexisting ingest and a stalled client. Budget context, filtering, alias expansion and repeated lookups within one call before work can grow, and count actual reply bytes in both renderings. A cap cannot authorize unbounded scanning or retries. No receive-resource exception is adopted. |
| F11 | Preserve inherited redacted failure reporting and protocol-only stdout. Log neither query text nor returned payloads; safely render display controls and attribute messages to their chats. Durable audit and retention remain 012. |
| F15–F16 | Preserve dependency integrity and supported-platform checks. Record synthetic verification, limits and untested platforms separately. Release assurance belongs to 012; no compliance claim is made. |

**Abuse cases to verify**: Use synthetic data to test malformed and oversized requests, unusual search literals, excessive ID collisions, maximum-size escaped messages, repeated searches under ingest pressure, cancellation and a stalled output reader. Stored instructions, display controls and fake approvals remain inert; credentials and payloads do not enter diagnostics. Failures do not write conversation data, create files, send messages, connect, pair, or trigger recovery.
