# Feature Specification: MCP Over Stdio: Chat and Contact Tools

**Feature Branch**: `006-mcp-stdio-tools`

**Created**: 2026-11-17

**Status**: Draft

**Input**: User description: "006 MCP over stdio: chat, contact and login tools" from `docs/roadmap.md`. Roadmap specify input: "Serve MCP over stdio with the official go-sdk. Provide the chat, contact, login-status and pairing-QR tools, with corrected schemas and structured output."

## Clarifications

### Session 2026-11-17

- Q: Does launching the local stdio service authorize all stored chat/contact reads or require restricted reads in 006? -> A: The owner approves access to all stored chats and contacts for now. Configurable access restrictions are a future research topic, tracked in `docs/backlog/006-configurable-read-access.md`, not a requirement to implement in 006. This does not authorize pairing or sending.
- Q: Must 006 include the login-status and pairing-QR tools? -> A: No. The owner deferred both `get_login_status` and `get_pairing_qr` to `docs/backlog/006-mcp-login-tools.md`. Feature 006 exposes five chat/contact tools only; terminal `pair` and offline `status` remain unchanged. No stdio pairing opt-in is added.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Browse Stored Conversations (Priority: P1)

As the account owner using a local assistant, I can discover, search and inspect my stored chats without running a separate daemon or confusing an absent conversation with a broken service.

**Why this priority**: Browsing conversations is the first useful read capability and establishes the common calling and result conventions.

**Independent Test**: Start a local client with synthetic conversations and a fake WhatsApp connection, discover the tools and browse multiple pages, including chats with no messages.

**Acceptance Scenarios**:

1. **Given** an unlocked, valid data directory, **When** the client starts `whatsapp-mcp stdio` and initializes MCP, **Then** it discovers exactly the five tools listed in FR-002 with their required inputs and real defaults; stdout contains protocol traffic only.
2. **Given** 35 stored chats, **When** `list_chats` is called without optional inputs, **Then** it returns the first 30 in most-recent-activity order; page 1 returns the remaining five with no duplicates for unchanged data.
3. **Given** matching chat names and JIDs, **When** a query and `sort_by: "name"` are supplied, **Then** only matching chats are returned in name order; equal sort values have a stable tie-breaker.
4. **Given** a chat with a known latest message, **When** it is listed or fetched with `include_last_message` true and then false, **Then** the latest-message detail appears only in the former, while the chat's identity and last-activity time remain correct.
5. **Given** an unknown `chat_jid`, **When** `get_chat` is called, **Then** it returns a clear not-found tool error; an empty search instead returns a successful empty list.
6. **Given** another process holds the data directory, **When** stdio starts, **Then** it fails with the existing lock-holder diagnostic without opening a second account owner or silently proxying.
7. **Given** the owner launched the local stdio service, **When** its client reads any stored chat or searches contacts through the exposed tools, **Then** no per-chat allow-list or per-read approval is required; this read permission does not grant pairing or sending permission.

---

### User Story 2 - Find a Contact and Their Conversations (Priority: P1)

As the account owner, I can find someone by their saved or advertised name or phone number and inspect their direct chat and group conversations without partial-number mistakes or repeated chats.

**Why this priority**: Reliable identity matching prevents showing the wrong person's conversation and fixes several historical lookup defects.

**Independent Test**: Seed contacts with distinct full, push, first and business names, mapped and unmapped LIDs, similar phone numbers, and repeated messages in shared groups. Call the three contact tools without any live lookup.

**Acceptance Scenarios**:

1. **Given** contacts whose full name, push name, first name, business name or phone number matches a query, **When** `search_contacts` is called, **Then** each matching contact appears once and groups do not appear as contacts.
2. **Given** a phone number with supported formatting and its equivalent phone JID and mapped LID, **When** each is supplied to `get_direct_chat_by_contact` or `get_contact_chats`, **Then** each resolves to the same person and equivalent stored results.
3. **Given** two contacts with overlapping phone digits, **When** the complete number of one is requested, **Then** only its exact direct chat matches; a substring cannot select the other contact.
4. **Given** a contact has many messages in a group and a newer last message from another participant, **When** `get_contact_chats` is called, **Then** that group appears once with its actual latest message, not an arbitrary message from the contact.
5. **Given** an unmapped LID with a stored direct chat, **When** that LID is requested, **Then** the existing LID identity can be matched without inventing a phone number or making a network request.
6. **Given** no matching contact chats, **When** `get_contact_chats` is called, **Then** it returns a successful empty list; a missing direct chat is a not-found tool error.

### Edge Cases

- Missing required arguments, wrong types, unknown fields, empty required strings, unsupported JID domains and invalid sort values fail clearly, without account or conversation mutations.
- Omitted defaults differ from explicitly invalid zero values: `limit: 0` is not omission; `include_last_message: false` must remain false.
- Limits of 1 and 200 succeed; 0, negative and 201 fail. Pages are zero-based nonnegative integers; multiplication overflow and offsets above the inherited 100,000 ceiling fail before a query.
- A query containing `+`, `&`, `#`, quotes, `%`, `_`, Unicode or terminal controls must not corrupt matching, execute commands, inject a query or execute terminal controls in rendered output.
- Missing last activity and missing latest message are represented as absence, not a made-up timestamp or message. Empty lists are arrays, never null.
- Mapped phone and LID chat rows may coexist before merging; preserve distinct actual chats while returning each chat identity only once.
- Tied latest-message timestamps must select a deterministic real message. Captions remain distinct from message text.
- Cancellation, end-of-input, slow output consumers and request floods cannot leave unbounded queued work or release the data-directory lock before account and storage work quiesces.
- Calls to deferred login-status or pairing-QR tools fail as unknown tools, without opening a pairing attempt or disclosing QR material.
- Store busy, resource-limit and closed errors are clear failures, not empty successes. A terminal device storage fault must not leave an apparently healthy waiting stdio process.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: `whatsapp-mcp stdio` MUST serve an embedded MCP session using the existing exclusive data-directory owner, configuration, private storage and connection lifecycle. It MUST fail clearly when locked; it MUST NOT introduce implicit proxying, an HTTP listener or a second account owner.
- **FR-002**: This feature MUST expose exactly `list_chats`, `get_chat`, `search_contacts`, `get_direct_chat_by_contact` and `get_contact_chats`, with the preserved purposes in specification section 4.4. Login-status and pairing-QR tools are deferred to the backlog; message, send and media tools remain later work.
- **FR-003**: Advertised schemas MUST use snake_case names, required-field declarations, actual default values and allowed ranges/enumerations. Handlers MUST independently apply omitted defaults and validate supplied values. Explicit false MUST NOT be replaced by a default. Unknown arguments MUST fail with an actionable invalid-input response rather than being silently ignored.
- **FR-004**: `list_chats` MUST accept optional `query`, `limit` (30), `page` (0), `include_last_message` (true), and `sort_by` (`last_active`, alternatively `name`). Query matches stored chat name or JID, case-insensitively with literal substring semantics. Last-active order is newest first with missing activity last; name order is ascending with unnamed chats last. Ties MUST use chat JID in ascending order.
- **FR-005**: `get_chat` MUST require `chat_jid` and accept `include_last_message` (true). It MUST retrieve that exact stored chat, including group or unresolved LID chats, or return a not-found tool error. Omitting latest-message detail MUST NOT suppress last-activity metadata.
- **FR-006**: `search_contacts` MUST require a nonempty `query`, accept `limit` (50) and `page` (0), and search full, push, first and business names and phone number using case-insensitive literal substrings. It MUST exclude groups, deduplicate contact identities and provide stable pagination for unchanged data. Contact results MUST include `jid`, `phone_number` when known and `name`.
- **FR-007**: `get_direct_chat_by_contact` MUST retain the required input name `sender_phone_number` while accepting a phone number, phone JID or LID. It MUST normalize the contact using known local mappings and match the exact normalized phone identity, or the exact unresolved LID identity when no phone mapping exists. It MUST NOT match a partial phone number or return a group. Absence MUST be a not-found tool error.
- **FR-008**: `get_contact_chats` MUST require `jid` (accepting the same three contact forms), accept `limit` (20) and `page` (0), and return direct chats and chats containing stored messages from that person. Each stored chat identity MUST appear once, newest activity first with a stable JID tie-breaker, and its latest-message detail MUST describe the actual latest message, regardless of sender.
- **FR-009**: Every list tool MUST accept limits from 1 through 200 and reject larger, zero or negative values. Pagination MUST reject negative pages, nonintegers, overflow and computed offsets above 100,000. Empty results MUST be successful empty lists. Tools MUST NOT silently truncate a requested successful page to evade an output-byte limit.
- **FR-010**: Chat results MUST provide `jid`, `name`, group identity and nullable last-activity time, and optional latest-message detail. Latest-message detail MUST keep message identity scoped by both `chat_jid` and `id`, and include sender identity, sender name, timestamp, own-message flag, text, caption and media type; it MUST expose no media keys or device credentials. All returned timestamps MUST be RFC 3339. These five tools introduce no timestamp filter inputs.
- **FR-011**: Every successful tool MUST return structured content and a text rendering of the same result without double encoding. Structured content is authoritative. No optional compact `format: "text"` mode is included.
- **FR-012**: Invalid arguments, absent singular objects and bounded-operation failures MUST be clear tool errors, not opaque internal errors or successful empty substitutes. Malformed MCP protocol requests MAY use the protocol's corresponding error response. Errors MUST identify the invalid field or unavailable capability without exposing raw storage errors, queries, credentials or unnecessary paths.
- **FR-013**: `get_login_status` MUST NOT be registered in 006. The existing offline CLI `status` remains available with unchanged semantics; adding live MCP status is deferred to `docs/backlog/006-mcp-login-tools.md`.
- **FR-014**: `get_pairing_qr` MUST NOT be registered in 006. Pairing remains through the existing terminal `pair` command; 006 MUST NOT introduce a stdio pairing opt-in, automatic pairing or a tool-controlled pairing window. Existing recovery and reconnect gates remain unchanged.
- **FR-015**: Chat/contact reads MUST use locally available data and mappings, preserve stored-name precedence, and require no WhatsApp contact or group-info network fetch. Disconnected operation MUST continue to serve stored reads whenever storage and the read policy permit it; startup account behavior remains governed by features 003-005.
- **FR-016**: Launching the local stdio service by the owner MUST authorize its client to read all stored chats and contacts through the exposed read tools, without per-chat restrictions or per-read confirmation in 006. This permission MUST NOT authorize pairing, recovery or sending, and tool inputs or retrieved content MUST NOT grant those permissions. Configurable read restrictions are deferred for research, not included in 006. The owner decision is recorded in `docs/specification.md` section 8.11.
- **FR-017**: MCP read pressure MUST NOT consume all shared store admission capacity or cause ingest to stop merely because tool requests flooded it. Tool work MUST have bounded input/output bytes, field sizes, concurrency, waiting work and duration, enforce limits on actual consumption, propagate cancellation and reject overload explicitly. Planning MUST state concrete ceilings and a testable admission policy before implementation; no unbounded default or inherited receive-resource exception is permitted.
- **FR-018**: End-of-input, client disconnection and normal stop signals MUST stop accepting tool work, cancel and drain active requests, and preserve the existing account-stop, network-fence, storage-barrier and lock-release ordering. A terminal device storage fault or terminal ingest failure MUST trigger normal failure shutdown and unsuccessful exit, not an indefinitely waiting stdio shell. Failure MUST NOT claim a quiet stop or clear an unsafe marker without the existing proof.
- **FR-019**: All five tools MUST have success and negative acceptance coverage through the official go-sdk in-memory transport, using a seeded temporary store and a fake WhatsApp owner, with no real account or account network access. Tool discovery MUST omit both deferred login tools, and attempts to call them MUST fail without account work. Stdio initialization, protocol-only stdout, lock failure, cancellation, EOF and terminal faults also require command-level coverage.

### Key Entities *(include if feature involves data)*

- **Chat**: A stored direct, group or unresolved-LID conversation, identified by its full JID, with a display name, last activity and optional real latest message.
- **Contact**: A locally known person's names and phone/JID/LID aliases. Unknown phone identity remains unknown; a mapping does not authorize any action.
- **Chat Page**: A bounded, stably ordered set of matching chats or contacts, with effective limit/page and count of returned items; no total-match count is promised.
- **Tool Result**: Authoritative structured chat/contact data with a faithful safe text rendering; no QR or device credentials are included.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An owner can discover all five capabilities and complete chat browsing and contact lookup in one local client session without a separate service; every scripted primary journey succeeds against synthetic stored data.
- **SC-002**: In a seeded set of 235 chats, paging at default and maximum accepted sizes returns every matching chat exactly once for unchanged data; every invalid size/page boundary is rejected with an actionable explanation.
- **SC-003**: Every seeded full-name, push-name, first-name, business-name and phone-number search case finds the expected person; every mapped identity alias returns equivalent contact results, and partial-number direct lookups select zero incorrect people.
- **SC-004**: In every duplicate-participation and tied-timestamp fixture, each chat is returned once and any latest-message detail belongs to a real latest message. All timestamps are valid standardized date-time strings and all empty collections are represented consistently.
- **SC-005**: Discovery lists zero login/pairing tools; every attempted call to either deferred tool fails, discloses zero QR material and initiates zero account actions.
- **SC-006**: Request-flood, cancellation and slow-consumer tests stay within the documented work and byte ceilings, trigger zero admission-pressure ingest stops, and leave zero tool work running after completed shutdown.
- **SC-007**: Captured tool output and diagnostics at every supported log level contain zero credentials or QR secrets, and the protocol channel contains zero nonprotocol output. Every rejected abuse case leaves conversations and account state unchanged.

## Assumptions

- Features 002-005 provide storage, contact normalization, the fakeable owner, message ingest and quiet-stop safety; this feature does not change the WhatsApp pin, storage budgets or reconnect permissions.
- Target users are account owners launching a trusted local client under their OS identity. By the owner's decision, that launch authorizes access to all stored chats and contacts in 006; it does not authorize pairing or sending. This is not a multi-user or remote-access service. A prompt-injected client could read unrelated stored chats under this policy; future configurable restrictions need research and a separate owner decision.
- Current list defaults are retained: chats 30, contact chats 20 and contact search 50. Contact search gains explicit pagination so its result cap is discoverable and useful.
- Optional latest-message inclusion defaults to true, preserving the previously returned information while making explicit false effective. Missing timestamps/latest messages are nullable or absent, not synthetic zero dates.
- The roadmap's optional compact text format is omitted. A faithful text rendering remains mandatory alongside structured content.
- Outcome verification uses synthetic data only. No real-account action is authorized by specification creation or automated acceptance.
- Concrete new transport/resource ceilings are planning outputs requiring review before implementation, not silently approved numeric policy. The approved store ceilings and the 200-item cap remain binding now.

## Scope and References

This feature covers `docs/roadmap.md` 006; `docs/specification.md` P1 (the five chat/contact tools, as amended by the owner's 2026-11-17 deferral), M4-M7, S6 (identity of embedded latest messages), D1 (embedded default and lock refusal), D9, D15 and section 4.4. P4's MCP login/QR portion is deferred, not a 006 acceptance requirement; terminal pairing and CLI status remain unchanged. It fixes the exposed chat/contact/schema defects in section 6 items 2, 3, 6, 7, 8 and 13 using the existing storage behavior. A5, M10, Q3 and constitution Principles I-X remain binding. The official go-sdk and in-memory verification are project mandates; implementation organization belongs in the plan.

Included: local stdio protocol service, the five chat/contact tools, truthful schemas/results, exact contact normalization, bounded reads, and the stdio terminal device-fault behavior identified in `docs/backlog/005-device-fault-command-wait.md`. Planning must reassess `docs/backlog/005-store-busy-stops-ingest.md`; this spec does not change busy writes into retryable writes.

Excluded: `get_login_status`, `get_pairing_qr` and stdio pairing-window controls (`docs/backlog/006-mcp-login-tools.md`); message search/context/last-interaction tools (007); sends and send-approval policy (008); downloads and media cache (009); HTTP, bearer tokens, `serve` listener and `stdio --connect` (010); webhooks (011); release-wide runbooks/assurance (012). The `serve` shell's terminal-fault backlog remains outside this stdio change unless separately scoped. No phone-code pairing, same-process re-pair, new reconnect path, automatic account recovery, multiple accounts, migration/import or REST is introduced.

## Security

### Assets and Trust Boundaries

Assets are stored conversations, names, phone/LID linkage, latest-message previews, device credentials, shared store availability and exclusive account ownership. Boundaries are local MCP client to tool invocation; stored WhatsApp-authored content to assistant output; tool work to the shared ingest/store resources; and diagnostics to protocol stdout/stderr. Device credentials and any pairing material must remain outside the tools' disclosure boundary. A trusted client can still be prompt-injected, and returned names or messages remain untrusted data.

### Security Requirements

- **SEC-001**: The owner-approved all-stored-data read policy in FR-016 MUST be enforced before data disclosure. Permission to read all stored chats/contacts MUST NOT be treated as permission to pair, recover or send. Tool arguments, stored messages and client-supplied approval claims MUST NOT grant additional authority. No restricted-read profile is approved or implemented in 006; the residual prompt-injection disclosure risk remains explicit.
- **SEC-002**: The MCP service MUST expose no QR retrieval or pairing-window control in 006. Tool output, logs, errors and verification evidence MUST contain no pairing material or device credentials. Existing terminal pairing safeguards remain unchanged.
- **SEC-003**: Treat names, captions, text and contact identities as data, never instructions. Clearly identify their chat/contact provenance. Safely encode controls in text/diagnostics without changing authoritative stored values. Latest-message output MUST exclude decryption metadata and device-store secrets. Logs MUST NOT include successful tool payloads or QR results.
- **SEC-004**: Validate input types, lengths, supported identity domains, literal queries, sort choices and pagination before expensive work. Reject malformed/deep/oversized protocol input and bound serialization as well as query results. Slow consumers and repeated calls MUST NOT grow memory or waiting work without a ceiling. Bounds MUST cover input, processing and emitted bytes, not only list counts.
- **SEC-005**: Preserve private-storage protections on Windows and Unix and the inherited safe shutdown/recovery gates. Tool errors MUST NOT trigger repair, deletion, marker clearing, new account generations, file writes containing results or automatic account recovery.
- **SEC-006**: Record tool name, time, outcome and a bounded secret-free error category for denied/failed requests and terminal faults; successful sensitive-read events may record the tool and outcome but not query text, names, phone numbers or bodies. Preserve stderr and existing debug-only body restrictions; this feature adds no body logging. Durable audit retention remains release work.
- **SEC-007**: Adding the mandated SDK MUST retain exact dependency versions, module integrity verification, reachable-vulnerability checks and CGO-free builds on all five supported targets. No pin change to whatsmeow is authorized.

### Finding Disposition

| Finding | Treatment and remaining scope |
| --- | --- |
| F01 - authorization/consent | Owner decision 2026-11-17 authorizes all stored chat/contact reads for the owner-launched local stdio client, without per-read prompts or chat restrictions (FR-016). This does not eliminate prompt-injection disclosure risk or close F01 as a whole. Configurable read restrictions are research in `docs/backlog/006-configurable-read-access.md`. MCP pairing and its authorization policy are deferred to `docs/backlog/006-mcp-login-tools.md`. Send policy remains an owner decision for 008; transport parity belongs to 010. |
| F02 - remote confidentiality | No network MCP listener/proxy enabled; 010 owns HTTP/proxy protection. |
| F03 - private storage | Inherit existing storage controls under SEC-005. At-rest/backup encryption and downstream-client custody remain operator responsibilities; 012 owns deployment guidance. |
| F04 - secret lifecycle | SEC-002 excludes credential/QR disclosure here. MCP QR lifecycle is deferred with the login tools; HTTP/HMAC credential lifecycle belongs to 010/011. |
| F05/F06 - paths and media | No outbound filesystem/media processing enabled; inherited data-directory checks remain. Sending/conversion belongs to 008, download/cache to 009. |
| F07/F08 - webhooks | No webhook enabled; destination, replay and delivery controls belong to 011. |
| F09 - resource exhaustion | FR-009/017 and SEC-004 require bounded stdio, query and output work and protection of ingest admission. Concrete reviewed bounds gate implementation. No receive exception is adopted or renewed. Later message context, media and HTTP bounds belong to 007-010. |
| F10 - validation | FR-003 through FR-012 and SEC-003/004 cover schema/handler parity, safe normalization, filtering, pagination and rendering. |
| F11 - logs/audit | SEC-002/003/006 keep payloads/QR out of diagnostics and record bounded outcomes. Complete durable audit collection, retention and operator review belong to 012; no complete audit claim here. |
| F12 - retention/privacy | Reads deliberately disclose selected data and phone/LID linkage to the local assistant/provider. Owner must understand that recipient's retention before use. No new result/QR persistence is introduced. Full retention/decommissioning inventory belongs to 012. |
| F13 - recovery/backups | Preserve existing storage and drain controls; calls cannot initiate restoration. Backup/restore rehearsal and operator guidance remain 012. |
| F14 - incident response | No automatic unlink, credential deletion or account recovery; preserve fail-closed diagnostics. Full response runbook belongs to 012, credential-specific rotation to 010/011. |
| F15 - supply chain | SEC-007 applies to dependencies now. Signing, provenance and release identity belong to 012. |
| F16 - assurance/ownership | Local single-owner stdio profile only; record synthetic evidence and platform limitations. Deployment/control ownership and regulated assurance remain owner decisions for 012. No compliance claim. |

### Abuse Cases and Verification

- Submit oversized/deep JSON, wrong types, unknown fields, SQL-like query text, malformed identities and overflow pagination; verify bounded clear failures with no account or conversation changes.
- Flood chat/contact reads while ingest writes, cancel queued/active calls and stall the output reader; verify measured ceilings, protected ingest progress and no leaked work after shutdown.
- Seed names and previews with terminal controls, fake approval instructions and synthetic secrets; verify safe text rendering, provenance, no account actions and no payload leakage through logs/errors.
- Attempt to invoke deferred login/QR tools and submit fake approval claims; verify unknown-tool failure, no credential/QR disclosure and no pairing or recovery side effect.
- Trigger device-storage/ingest terminal faults and disconnect/EOF during calls; verify unsuccessful fault exit, safe drain, no premature store close/lock release and no invalid marker clear.
- Capture stdout/stderr for initialization, tool success/failure, startup lock refusal and shutdown; verify protocol-only stdout and redacted diagnostics at every level.

The later `verification.md` must record commands, dates, counts, exit codes, measured bounds and platform limitations. Unrun controls and real-account checks remain not verified; no real account is needed for this feature's automated acceptance.
