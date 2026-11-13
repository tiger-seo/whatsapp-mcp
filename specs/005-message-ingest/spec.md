# Feature Specification: Message Ingest

**Feature Branch**: `005-message-ingest`

**Created**: 2026-11-10

**Status**: Draft

**Input**: Feature 005 in `docs/roadmap.md`: store live messages and history sync, normalize LIDs, merge LID chats at startup, and resolve names without fetching group information for every message.

**Sources and scope**: `docs/specification.md` P2, P3, M7, S3-S5, D18, M10, §3.4-§3.5 and §6 defects 6, 9, 10 and 11; `specs/002-sqlite-storage/spec.md` governs existing storage and merge semantics; `specs/004-shutdown-drain/` governs session retirement. Feature 011 owns webhook delivery, feature 009 owns media download and cache naming, and features 006-007 own MCP read tools. No sending, new message types, new reconnect policy or old-deployment import is included.

## Clarifications

### Session 2026-11-10

- Q: What should happen when a live message or history batch cannot be saved because storage is full or temporarily unavailable? → A: Stop ingest, report the failure and incomplete record, and require operator intervention; previously committed messages remain.
- Q: When a phone-number mapping becomes known during a running session, when should earlier messages stored under that person's LID be merged? → A: Merge when the new mapping is detected and also reconcile at startup.
- Q: After ingest stops because a message could not be saved, how should the operator resume receiving messages? → A: Stop the connected command safely; the operator fixes the problem and restarts under existing session-safety rules.
- Q: What should happen if the receive queue is full? → A: Stop the affected connection and explicitly report the overload and a possible gap; do not assume replay or bypass the existing reconnect and quiet-stop rules. Numeric limits require separate approval after measurement.

### Session 2026-11-13

- Q: WhatsApp sends the initial history to the `pair` command's connection, which stops soon after it confirms the link, and the library has already acknowledged each notification it received. What happens to history that `pair` has not stored when it stops? → A: Accept the gap: `pair` keeps its current lifetime, ingests what arrives while it runs, and reports what it could not store as incomplete history. It is not recovered. A bounded wait in `pair` is deferred to the backlog.
- Q: When WhatsApp later maps a LID to a different phone number, what happens to data already merged under the earlier phone? → A: It stays where it was merged; merges are not undone. Later messages follow the new mapping, and the change is logged.
- Q: If the startup reconciliation runs out of its startup time budget, what happens to the mappings it has not reached? → A: The command starts, and the running session's merge worker continues through the remaining mappings in the background, within its existing limits; nothing waits for a restart.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Keep Live Messages (Priority: P1)

An account owner receives or sends supported messages while the service is connected and can rely on their content and identity being available for later use.

**Why this priority**: Live messages are the basic ongoing record.

**Independent Test**: Deliver synthetic live events to a connected test session and inspect stored records, without a real account.

**Acceptance Scenarios**:

1. **Given** a valid text message, **When** it is received or sent by the account, **Then** its chat, message ID, sender, time, direction and text are stored.
2. **Given** an image, video, audio or document message, **When** it is ingested, **Then** its type and available download metadata are stored; image, video and document captions are kept separately from text.
3. **Given** the same chat and message ID delivered twice, **When** both copies are stored, **Then** one message remains and the existing store's replacement and non-empty media preservation rules apply.
4. **Given** a message without supported text or recognized media, **When** it arrives, **Then** it is ignored without creating a message.

### User Story 2 - Recover Available History (Priority: P1)

After linking a device, the owner wants the history supplied by WhatsApp to be retained alongside new messages, without old history making a chat appear less recent.

**Why this priority**: History makes a fresh installation useful and must coexist with live delivery.

**Independent Test**: Feed synthetic history conversations and live messages in varied orders and compare the resulting records.

**Acceptance Scenarios**:

1. **Given** a history sync containing supported messages, **When** it is processed, **Then** each valid message retains its own time, sender, direction, text, caption and available media metadata under the correct chat.
2. **Given** a newer live message already stored, **When** older history is processed, **Then** the chat's last activity remains the newer time.
3. **Given** history larger than the store's batch limits, **When** it is processed, **Then** it is divided into bounded batches; a failed batch is reported and no partial batch is claimed as saved.
4. **Given** a history conversation with no usable message timestamp, **When** it is processed, **Then** no fabricated message time is stored; invalid messages are reported or skipped without preventing independent valid messages from being processed.

### User Story 3 - Unify Contact Identities (Priority: P1)

An owner sees a person's direct chat and group messages consistently associated with their phone identity when WhatsApp supplies a mapping, while unresolved identities remain usable.

**Why this priority**: Split identities hide messages and misidentify senders.

**Independent Test**: Seed synthetic LID and phone identities and overlapping messages; run startup reconciliation and ingest events with and without mappings.

**Acceptance Scenarios**:

1. **Given** a mapped LID on a chat or sender, **When** a message is ingested, **Then** the mapped phone JID is used and the sender's phone number is retained.
2. **Given** an unmapped LID, **When** its message is ingested, **Then** the LID is retained and its sender phone number is empty, rather than guessed.
3. **Given** a mapped LID chat and a phone chat with overlapping IDs, **When** startup reconciliation runs, **Then** the store's atomic merge retains the phone chat's duplicate, moves other messages, applies the existing name and latest-time rules, and rewrites that sender in all chats; repeating it changes nothing.
4. **Given** an unmapped LID or a failed mapping or merge, **When** reconciliation runs, **Then** existing data is not deleted; the outcome is reported safely and a later startup can retry.
5. **Given** messages stored under a LID, **When** its phone mapping is detected during a running session, **Then** the same atomic merge is attempted without waiting for restart.

### User Story 4 - Recognize Chats Without Repeated Lookups (Priority: P2)

An owner sees useful chat names without each group message triggering another group-information request.

**Why this priority**: Stable names improve recognition and reduce unnecessary network work.

**Independent Test**: Supply stored names, synthetic contact and history names, and a counted group-information provider while ingesting repeated messages.

**Acceptance Scenarios**:

1. **Given** a stored non-empty chat name, **When** a message arrives, **Then** it is consulted first and a worse fallback never replaces it.
2. **Given** a group lacking a stored name, **When** multiple messages need its name, **Then** available history names are used before a group-information lookup and successful lookup results are cached rather than fetched per message.
3. **Given** unavailable contact or group information, **When** a name is needed, **Then** the existing fallback and name-quality rules apply and the message is not discarded solely for lack of a name.

### Edge Cases

- History and live deliveries can overlap, arrive out of order or contain identical IDs in different chats; identity is the pair of chat and message ID.
- Invalid identifiers, absent timestamps, malformed fields and unsupported message types do not create invalid message records; independent valid records remain eligible for processing.
- A group-history participant, an own message, and a direct-message sender must each be attributed to the actual sender when supplied; unresolved identity is not invented.
- Mapping lookup timeout or failure retains the original identity; a failure is not mistaken for proof that a mapping does not exist.
- A LID whose mapping later points to a different phone number keeps the data already merged under the earlier phone; later messages follow the new mapping and the change is logged.
- History that arrives while `pair` is stopping, or after it stops, is reported as incomplete and is not recovered.
- An individual store operation can fail due to cancellation, limits or low disk; stop ingest and the connected command safely and report the incomplete record, while retaining committed earlier batches. The operator fixes the problem and restarts under existing session-safety rules.
- Receive overload closes the affected connection and is reported with a warning that local history may have a gap; retry or replay is not guaranteed.
- Shutdown during ingest cannot allow writes after retirement or falsely establish a quiet stop; pending work is accounted for under feature 004's existing rules.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST ingest supported live messages and history supplied by WhatsApp, including account-originated messages, without requiring a separate history request (P3, §3.4).
- **FR-002**: Each stored message MUST retain its chat identity, message ID, full sender JID without a device component, normalized phone number when known, timestamp, direction, text, separate caption, media type and available download metadata, including URL, direct path, key, hashes, length, MIME type and original filename (S3, P3, D18).
- **FR-003**: Text includes ordinary and extended text; supported media are image, video, audio and document. Image, video and document captions MUST be stored separately from text. A message with neither supported text nor recognized media MUST NOT be stored (§3.4, D18).
- **FR-004**: Live and history duplicates MUST follow feature 002's composite identity, last-written replacement and preservation of non-empty media metadata; IDs shared by different chats MUST remain distinct.
- **FR-005**: Chat creation and last activity MUST follow the existing atomic store rules; processing older messages MUST NOT move last activity backward (S4).
- **FR-006**: The system MUST resolve chat and sender LIDs to phone identities when the mapping is known; on no mapping or lookup failure it MUST retain the LID and MUST NOT invent a phone number. Mapping lookups MUST have a two-second maximum wait (§3.5, P2).
- **FR-007**: At startup, before live ingest can race the reconciliation, the system MUST reconcile mapped LID chats using feature 002's atomic, idempotent merge, including sender rewrites across chats, phone-copy precedence on duplicate IDs and its name-quality rules. It MUST also attempt that merge when a new mapping is detected during a running session. Unmapped or failed items remain intact and are reported; repeat startup retries them. Mappings the startup pass did not reach within its time budget are continued by the running session (P2).
- **FR-008**: The system MUST consult a stored name first, use available history names for groups, and cache group-information results rather than fetch them for each message. It MUST apply feature 002's saved, real and fallback name ranking, observation time and tie-breaking rules (M7, S4).
- **FR-009**: When no better name is available, group names fall back to `Group <id>`; direct names use the available saved contact name, then self-chosen contact name, sender name or JID user part, consistent with feature 002's quality ordering. Failed name lookup MUST NOT by itself prevent storing a valid message (§3.4).
- **FR-010**: History processing MUST submit batches within the existing store limits of 1,000 messages and 16 MiB of aggregate field bytes, and MUST NOT buffer unbounded application-owned work. On a storage failure affecting a live message or history batch, ingest MUST stop, report the failure and incomplete record, and stop the connected command safely. The operator fixes the problem and restarts under existing session-safety rules; previously committed messages remain intact and MUST NOT be represented as a completely successful sync.
- **FR-011**: A valid message's sender MUST reflect its supplied sender or history participant, or the account identity for an own message. An absent or invalid required sender, chat, ID or time MUST NOT be replaced with a fabricated value merely to make a record valid.
- **FR-012**: Stored live messages MUST be made available to the later event-delivery feature with their normalized identity, text, caption and media information; history MUST NOT be emitted as a live event. Webhook delivery itself is outside this feature.
- **FR-013**: Ingest MUST participate in the existing generation and shutdown safety rules: stop admitting work at retirement, account for admitted work, and preserve the network-block, storage-barrier and quiet-check ordering. An incomplete or uncertain stop MUST NOT clear the unsafe marker.
- **FR-014**: Automated synthetic-event tests MUST cover live and history, captions and media metadata, out-of-order and duplicate delivery, group and own senders, mapping failures, merge collisions, name caching, bounded batches, failure and shutdown interleavings. Real-account acceptance requires separate permission for each pairing, connection and receiving step; review the roadmap's shutdown follow-up before that test.

### Key Entities

- **Message**: A chat-scoped ID, sender identity, time, direction, text, separate caption and optional media metadata.
- **Chat**: A direct or group conversation, its best known name and latest message time.
- **Identity mapping**: An association between a privacy-preserving LID and a phone-number identity, which may be unavailable.
- **History sync**: WhatsApp-supplied conversations and past messages, distinct from live delivery.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In synthetic acceptance cases, 100% of valid supported live and history messages are represented with the expected chat, sender, time and direction, subject to explicitly reported storage failures.
- **SC-002**: In the acceptance corpus, 100% of image, video and document captions and supplied media metadata survive ingest separately from message text.
- **SC-003**: For all tested arrival orders of distinct messages, chat last activity equals the newest stored message time; duplicate copies follow the stated replacement rule.
- **SC-004**: All tested mapped LID merges preserve nonduplicate messages and the phone-side duplicate, and a second merge produces no further change.
- **SC-005**: In a repeated-message test for a group with a successfully resolved name, one initial lookup is sufficient and no lookup is made per subsequent message while that name remains available.
- **SC-006**: The synthetic security and shutdown cases show no invalid partial batch, unauthorized account action, secret disclosure or falsely reusable session.

## Assumptions

- WhatsApp determines the history it supplies; this feature does not promise complete account history or automatic replay after a failed batch.
- The initial history after linking is delivered while `pair` runs; what `pair` has not stored when it stops is lost and reported (clarification 2026-11-13).
- Existing feature 002 storage limits, name precedence and merge decisions apply; the implementation plan must specify bounded ingest admission, cache lifetime, failure reporting and recovery without weakening those limits.
- Existing feature 004 connection and quiet-stop restrictions remain in force. The receive-resource exception E003-RX-01 was retired on 2026-11-13, when the owner-approved fork that enforces those bounds was pinned (plan, Fork Acceptance Contract).
- Acceptance against a real account is not authorized by this specification and remains unverified until separately permitted and recorded.

## Security

**Assets and boundaries**: Messages, captions, names, phone/LID associations and media decryption metadata cross from untrusted WhatsApp senders into private local storage and later consumers. Stored content is data, not instructions. The session and its credentials remain protected by the existing private-storage and shutdown controls.

- **F03, F05, F13**: Preserve feature 002's private storage, safe paths, atomic writes and recovery guarantees. Media is not downloaded or opened here; download path containment is feature 009.
- **F09**: Enforce actual application-owned field, byte, batch, concurrency, wait and disk limits, including for history, mapping and cached names; reject excess safely, without unlimited queues or silent success. Planning must identify measurable limits and failure behavior before implementation. Device-store limits remain mandatory. E003-RX-01 is limited to its explicitly approved upstream receive paths and does not automatically cover 005.
- **F10**: Validate untrusted identities, timestamps and field sizes before storage; preserve valid content as data and safely encode untrusted names in diagnostics. A rejected record causes no partial message write.
- **F11**: Message bodies and captions MUST NOT appear above debug level; credentials, media keys and QR material MUST NOT appear at any log level. Failure diagnostics MUST avoid unnecessary personal data and safely encode control characters. Broader audit policy is deferred to feature 012.
- **F12**: Extend operator-facing privacy and retention guidance to identify stored message content, media metadata and LID-phone linkage, indefinite growth and the limits of deletion and backup protection; downstream export belongs to features 006-007 and 011.
- **F01**: Ingest never interprets stored text as instructions or authorizes a send, pairing or other account action. Read/send authorization policy is a separate owner decision for the affected tool features.
- **F02, F04, F06-F08, F14-F16**: Remote transport and secret lifecycle belong to feature 010; media processing to 008-009; webhook destination and delivery to 011; incident response, release provenance and deployment assurance to 012. Existing controls and dependency checks remain in force.

**Abuse cases**: Synthetic tests cover oversized text, captions, names, metadata and history; malformed JIDs, timestamps and control characters; cache and concurrent-event pressure; mapping and storage failures; disk exhaustion; malicious message instructions; and callbacks racing retirement. Rejected inputs must not write a partial message or trigger a send, pairing, webhook or other external side effect. Platform-specific claims require platform-specific evidence.
