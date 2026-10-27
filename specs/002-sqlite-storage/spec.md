# Feature Specification: Storage: one SQLite file and migrations

**Feature Branch**: `002-sqlite-storage`

**Created**: 2026-10-27

**Status**: Draft

**Input**: User description: "Create the single SQLite database on modernc.org/sqlite. It holds both the whatsmeow
device store and our message tables, with versioned embedded migrations and the store operations ingest and tools
need. See docs/specification.md S1-S6, D18, §3.2 (as the reference for what is stored), docs/spikes.md S1 (dialect
name and DSN), and docs/roadmap.md feature 002."

**Covers** (`docs/specification.md` §8): S1, S2, S3, S4, S6, D2, D16, D18, the store half of P2 (the `@lid` chat
merge as an operation) and the store part of Q3. S5 only as far as the store keeps the media metadata; generated
filenames belong to feature 008. It fixes §6 #2, #3, #6, #7, #8, #10, #11, #14 and the missing indexes of #17 at the
storage level. The WhatsApp client, ingest logic and LID resolution are out of scope (003, 004), and so are the MCP
tools themselves (005, 006): this feature provides the operations they call.

## Settled in this spec

- **Two schema histories in one file.** whatsmeow keeps its own schema version for its device store tables. The
  message tables get their own, separate version history, so that a whatsmeow upgrade and one of our migrations
  never interfere. Both are brought up to date when the database is opened.
- **A database newer than the binary is refused.** If the message schema is at a version the binary does not know
  (an older binary started after a newer one), opening fails with a message naming both versions, and the file is
  not changed. There is no downgrade path (Principle IX).
- **Chat names have a quality.** "Never gets worse" (S4) needs a rule: a name is either a *real name* (a group
  subject, a contact's full, push, first or business name, a history-sync display name) or a *fallback* derived from
  the JID (a phone number, `Group <id>`, the JID's user part). A stored name is replaced only by a non-empty name of
  the same or a higher quality. Same quality replaces, so a renamed group or contact is picked up; empty never
  replaces anything.
- **Re-storing a message replaces it.** Writing a message whose `(chat_jid, id)` already exists replaces its stored
  fields with the new ones (history sync and live delivery carry the same data, and a later copy may carry fresher
  media URLs). The chat's `last_message_time` still only moves forward.
- **The `@lid` merge drops duplicates instead of skipping the chat.** In the old bridge (§3.5) a chat was skipped
  when both chats held the same message ID. Here the phone-number chat's copy is kept, the LID chat's copy is
  dropped, and the merge completes.
- **Context is chronological.** Messages before and after a message (for `get_message_context` and
  `include_context`) are both returned oldest first. The old bridge returned "before" newest first; the tools
  (006) decide how to present them.
- **Last interaction** with a contact is the newest message that is either in the direct chat with that contact
  (sent by either side) or sent by that contact in any group.
- **Text search ignores case in every script**, not only ASCII, so Cyrillic, Greek and accented Latin match
  regardless of case. Wildcard characters typed by the caller (`%`, `_`) are matched literally.
- **The long-running commands open the store.** `stdio`, `serve` and `pair` open (and if needed create and migrate)
  the database right after taking the lock, so a broken or too-new database is reported at startup and the file
  exists before features 003–006 use it. `status` and `version` do not open it in this feature.

## User Scenarios & Testing *(mandatory)*

The "users" of this feature are the operator, who sees one database file in the data directory, and the developers
of features 003–008, who call the store operations.

### User Story 1 - One database file, created on first start (Priority: P1)

An operator starts `whatsapp-mcp serve` (or `stdio`, or `pair`) for the first time on an empty data directory. A
single database file appears in the data directory. It will hold both the linked device's keys and the message
history. On every later start the same file is used, from any working directory.

**Why this priority**: S1 and Principle III require one file for all state, and every later feature stores into it.
It also removes the old split between two files (§6 #3).

**Independent Test**: Start a long-running command on an empty data directory, stop it, and check that exactly one
database file (plus its journal files) exists and contains both the device store tables and the message tables.
Start again and check that nothing is recreated.

**Acceptance Scenarios**:

1. **Given** an empty data directory, **When** `stdio`, `serve` or `pair` starts, **Then** `<data dir>/whatsapp.db`
   is created, holds the device store schema and the message schema at their latest versions, and is readable and
   writable only by the current user where the OS supports that.
2. **Given** an existing, up-to-date database, **When** a long-running command starts again, **Then** it opens the
   same file without changing the schema, and all stored chats and messages are still there.
3. **Given** a file at `<data dir>/whatsapp.db` that is not a database (for example truncated or random bytes),
   **When** a long-running command starts, **Then** it fails with a message that names the file and says it is not a
   usable database, and the file is left untouched.
4. **Given** a data directory with old `store/whatsapp.db` or `store/messages.db` files from the previous bridge,
   **When** a command starts, **Then** they are ignored (D16).

---

### User Story 2 - The schema evolves safely (Priority: P1)

A developer adds a schema change in a later feature as a new numbered migration that ships inside the binary. An
operator upgrades the binary, and the database is brought up to date on the next start without any manual step.
If something goes wrong, the database is left as it was.

**Why this priority**: S2 and Principle III. Without a migration history, every schema change after the first
release would be a manual upgrade or a data loss.

**Independent Test**: Open a database at each earlier version (built by applying only the first migrations) and check
that opening it applies the rest; inject a failing migration and check the database is unchanged; open a database
marked with a future version and check the refusal.

**Acceptance Scenarios**:

1. **Given** an empty database, **When** it is opened, **Then** every message-schema migration is applied in order,
   and the recorded version equals the latest one the binary knows.
2. **Given** a database at an earlier version, **When** it is opened, **Then** only the missing migrations are
   applied, in order, and existing data is kept.
3. **Given** a migration that fails part-way, **When** the database is opened, **Then** opening fails with a message
   naming the migration, and the schema and data are exactly as before that migration.
4. **Given** a database whose message schema is at a version newer than the binary knows, **When** it is opened,
   **Then** opening fails with a message naming the database's version and the binary's latest version, and the
   file is not modified.
5. **Given** the device store's own schema is behind the whatsmeow version in the binary, **When** the database is
   opened, **Then** the device store schema is upgraded as well.

---

### User Story 3 - Ingest stores chats and messages correctly in any order (Priority: P1)

Feature 004 hands the store live messages and history-sync batches, which arrive in no particular order and may
repeat. Whatever the order, the stored chats show their real latest activity and the best name known, and each
message is stored once.

**Why this priority**: §6 #10 (time going backwards) and §6 #9's effect on names break chat lists and the "last
message" shown to the user. S3, S4, D18.

**Independent Test**: Store a fixed set of chats and messages in several different orders, including duplicates, and
check that the resulting chats and messages are identical every time.

**Acceptance Scenarios**:

1. **Given** a message `(chat C, id X)` already stored, **When** the same `(C, X)` is stored again with different
   content, **Then** there is still exactly one message `(C, X)`, and it carries the new content.
2. **Given** the same message ID `X` in two different chats, **When** both are stored, **Then** both are kept as
   separate messages.
3. **Given** chat C with a latest message at 12:00, **When** a history message from 09:00 is stored in C, **Then**
   C's last activity time stays 12:00.
4. **Given** chat C with last activity 09:00, **When** a message from 12:00 is stored, **Then** C's last activity
   time becomes 12:00.
5. **Given** a message for a chat that is not stored yet, **When** it is stored, **Then** the chat is created with
   that message's time as its last activity, and the message is stored, as one atomic step.
6. **Given** a chat whose stored name is the real name "Alice", **When** an update offers the fallback name
   "491511234567", **Then** the name stays "Alice".
7. **Given** a chat whose stored name is a fallback, **When** an update offers a real name, **Then** the real name is
   stored. **Given** a real name, **When** a different real name is offered, **Then** the new real name is stored.
8. **Given** an image, video or document message with a caption, **When** it is stored, **Then** the caption is kept
   in its own field, separate from the message text, and the media metadata needed to download it later is kept.
9. **Given** a message from `491511234567:12@s.whatsapp.net` (a device of that user), **When** it is stored, **Then**
   the sender is kept as the user's full JID `491511234567@s.whatsapp.net`, and its normalised phone number as
   `491511234567`. **Given** a sender that is an unresolved LID, **Then** the full LID JID is kept and the phone
   number is empty.

---

### User Story 4 - The tools find chats, contacts and messages (Priority: P1)

Features 005 and 006 answer the 14 MCP tools by calling store queries, and only wire them up. Every query a tool
needs exists, is correct for the cases where the old bridge was wrong, and is fast on a large history.

**Why this priority**: the roadmap's "done when" for this feature, and §6 #2, #3, #6, #7, #8.

**Independent Test**: Seed a database with known chats, contacts and messages in a temporary directory and check every
query's result against the expected rows.

**Acceptance Scenarios**:

1. **Given** seeded chats, **When** chats are listed with a search text, a page size and an offset, sorted by last
   activity or by name, **Then** exactly the chats whose name or JID contains the text are returned, in that order,
   each optionally with its real latest message.
2. **Given** a chat JID, **When** the chat is requested, **Then** it is returned with its latest message, or a
   distinct "not found" result if no such chat exists.
3. **Given** contacts in the device store, **When** contacts are searched, **Then** the text matches full name, push
   name, first name, business name or phone number; groups are excluded; and a contact known under both a LID and a
   phone number appears once.
4. **Given** a normalised phone number, **When** the direct chat with that contact is requested, **Then** only the
   chat whose phone number equals it exactly is returned (never a partial match), or "not found".
5. **Given** a contact who has a direct chat and has written in two groups, **When** the contact's chats are
   requested, **Then** those three chats are returned once each, newest activity first, each with its real latest
   message.
6. **Given** seeded messages, **When** messages are listed with any combination of after, before, chat, sender
   phone number and search text, with a page size and offset, **Then** exactly the matching messages are returned,
   newest first; the search text matches either the message text or the caption.
7. **Given** a listing with context requested, **When** N before and M after are asked for, **Then** each hit comes
   with up to N earlier and M later messages from the same chat.
8. **Given** a contact, **When** the last interaction is requested, **Then** the newest message in the direct chat
   with them (from either side) or sent by them in a group is returned, or nothing.
9. **Given** every message returned by any query, **Then** it carries its ID, chat JID, chat name, sender JID, sender
   phone number, sender name, time, whether it is from me, text, caption and media type.

---

### User Story 5 - A single message is found by chat and ID (Priority: P2)

Tools that act on one message (context, media download) identify it by `(chat_jid, id)`. When a caller gives only
the ID and it exists in several chats, it gets an error that lists those chats, so it can ask again precisely.

**Why this priority**: S6 and §6 #14. Message IDs are unique only within a chat.

**Independent Test**: Store the same ID in two chats and one other ID in one chat, then look each up by
`(chat_jid, id)` and by ID alone.

**Acceptance Scenarios**:

1. **Given** message X in chat C, **When** it is looked up by `(C, X)`, **Then** that message is returned.
2. **Given** no message `(C, X)`, **When** it is looked up, **Then** the result is a distinct "not found" naming the
   chat and the ID.
3. **Given** ID X in exactly one chat, **When** it is looked up by ID alone, **Then** that message is returned.
4. **Given** ID X in chats C1 and C2, **When** it is looked up by ID alone, **Then** the result is a distinct
   "ambiguous ID" error that lists C1 and C2.
5. **Given** a media message, **When** its media metadata is requested by `(chat_jid, id)`, **Then** everything
   needed to download it later is returned, or "not a media message" for a message without media.

---

### User Story 6 - Merge a LID chat into the phone-number chat (Priority: P2)

At startup, feature 004 finds `@lid` chats whose phone number is now known and asks the store to merge each one into
the phone-number chat. Afterwards there is one chat per person, holding all their messages.

**Why this priority**: P2 keeps the startup merge (§3.5). The store provides the operation; deciding which chats to
merge is 004's job.

**Independent Test**: Seed a LID chat and a phone-number chat with overlapping and distinct messages, merge, and check
the result; run the merge again and check nothing changes.

**Acceptance Scenarios**:

1. **Given** a LID chat L and a phone-number chat P, **When** L is merged into P, **Then** all of L's messages belong
   to P, L no longer exists, P's last activity is the later of the two, and P's name is the better of the two by the
   name-quality rule.
2. **Given** only L exists, **When** it is merged into P, **Then** P is created with L's name, time and messages.
3. **Given** L and P both hold message ID X, **When** L is merged, **Then** P keeps its own copy of X, L's copy is
   dropped, and the merge completes.
4. **Given** messages in L whose sender is the LID, **When** L is merged, **Then** their sender becomes the phone
   JID and their phone number is filled in.
5. **Given** a merge that fails part-way, **Then** both chats are exactly as before it.
6. **Given** a merge already done, **When** it is requested again, **Then** nothing changes and no error is
   reported.

---

### Edge Cases

- **Database locked by a long write elsewhere in the process:** writers and readers wait (up to a few seconds) rather
  than failing at once; ingest and tool queries running together never fail with "database is locked" under normal
  load.
- **Process killed mid-write:** on the next start every committed write is there and no partial write is visible.
- **Disk full or read-only file:** the write fails with an error naming the file and the reason; nothing is half
  written.
- **Graceful shutdown:** the database is closed cleanly as part of shutdown, within the existing 10-second limit.
- **Empty or missing message ID or chat JID, or a chat JID that is not a full JID:** the write is rejected with an
  error naming the field; nothing is stored.
- **Message without a time:** rejected, because ordering and last activity depend on it.
- **Two messages in one chat with the same time:** the order is still deterministic (ties broken by ID), so paging
  never skips or repeats a message.
- **Search text with `%`, `_`, quotes or a backslash:** matched literally.
- **Search text in another script or case** ("ПРИВІТ" vs "привіт", "ÉTÉ" vs "été"): matches.
- **Phone number given as `+49 151 1234567`, `49-151-1234567` or `491511234567@s.whatsapp.net`:** all normalise to
  `491511234567` for phone-number filters and lookups.
- **Page beyond the end:** an empty result, not an error.
- **Page size of zero or negative, negative offset:** rejected; caps and defaults are applied by the tools (005, 006).
- **Chat with no messages** (a history-sync conversation without stored messages): listed with no latest message;
  chats with no last activity time sort after all others by last activity.
- **Chats without a name** sort after named chats when sorted by name.
- **`after` later than `before`:** an empty result, not an error.
- **Sender name for my own messages:** not looked up; the tools render them (006).
- **Merging a chat into itself, or merging a chat that is not `@lid`:** rejected with an error.
- **Device store after logout:** whatsmeow removes the device; the message tables are kept, and searching contacts
  returns no contacts until a device is paired again.

## Requirements *(mandatory)*

### Functional Requirements

**Database file (S1, D2, D16)**

- **FR-001**: All persistent state MUST live in one database file, `<data dir>/whatsapp.db`, holding both the
  whatsmeow device store and the message tables. No other database file is created and PostgreSQL is not supported.
- **FR-002**: The database MUST be opened with write-ahead logging, foreign-key enforcement, a busy timeout of at
  least 5 seconds, and write transactions that take the write lock at their start, so that concurrent writers wait
  instead of failing.
- **FR-003**: The database driver MUST be pure Go and build for all five targets with CGO disabled.
- **FR-004**: When the file is created, it MUST be readable and writable only by the current user where the OS
  supports that, because it holds the device's private keys.
- **FR-005**: `stdio`, `serve` and `pair` MUST open the database right after taking the data-directory lock and MUST
  close it during graceful shutdown. A database that cannot be opened MUST fail startup with a message naming the
  file and the reason.
- **FR-006**: Files of the previous bridge (`store/*.db`) and its PostgreSQL database MUST NOT be read.

**Migrations (S2)**

- **FR-007**: The message schema MUST be defined by numbered migrations embedded in the binary, applied in order at
  open, and recorded with a version number kept separate from the device store's own version.
- **FR-008**: Migration 1 MUST create the complete message schema in an empty database. There is no older schema to
  detect or adopt.
- **FR-009**: Each migration MUST be applied atomically together with its version record: on failure, the schema,
  the data and the recorded version are as before that migration, and opening fails naming the migration.
- **FR-010**: Opening a database whose message schema version is newer than the binary's latest MUST fail without
  modifying the file, naming both versions.
- **FR-011**: The device store's schema MUST be created or upgraded by whatsmeow's own mechanism when the database is
  opened.

**Chats and messages (S3, S4, D18, §3.2)**

- **FR-012**: A chat MUST record its full JID, its name, the quality of that name (real or fallback), and its last
  activity time. Whether it is a group or a direct chat follows from the JID.
- **FR-013**: A message MUST record its ID, chat JID, full sender JID (without the device part), normalised sender
  phone number (empty when the sender is an unresolved LID), time, whether it is from me, text, caption, media type,
  and the media metadata needed to download it later: URL, direct path, media key, file hash, encrypted file hash,
  file length, MIME type and original file name.
- **FR-014**: The store MUST derive the normalised sender phone number from the sender JID itself (digits only, for
  phone-number JIDs), so the two can never disagree.
- **FR-015**: A message MUST be identified by `(chat_jid, id)`. Storing a message whose `(chat_jid, id)` exists MUST
  replace its stored fields; storing never creates a second copy.
- **FR-016**: Storing a message MUST create its chat if it does not exist and move the chat's last activity time
  forward to the message's time if that is later, in the same atomic step.
- **FR-017**: The store MUST offer a chat update (name, name quality, last activity time) for chats known without a
  message, such as history-sync conversations. The last activity time MUST only ever move forward, and an absent time
  MUST NOT clear it.
- **FR-018**: A chat's name MUST be replaced only by a non-empty name of the same or a higher quality. Real names rank
  above fallbacks.
- **FR-019**: A batch of messages (a history-sync conversation) MUST be storable in one transaction.
- **FR-020**: The schema MUST support fast lookup by chat and time, by sender phone number, and by time.

**Single-message lookup (S6)**

- **FR-021**: Looking up a message by `(chat_jid, id)` MUST return it or a distinct "not found" error that names the
  chat and the ID.
- **FR-022**: Looking up a message by ID alone MUST return it when exactly one chat holds that ID, "not found" when
  none does, and a distinct "ambiguous ID" error listing every matching chat JID when more than one does.
- **FR-023**: The store MUST return a message's media metadata by `(chat_jid, id)`, or a distinct "not a media
  message" error.

**Queries for the tools (§4.4, M5, M6)**

- **FR-024**: Every message returned by a query MUST include ID, chat JID, chat name, sender JID, sender phone number,
  sender name, time, from-me flag, text, caption and media type. The sender name of a message not from me is the
  contact's full, push, first or business name from the device store (in that order), else the real name of the
  direct chat with the sender, else empty.
- **FR-025**: Every list query MUST take a page size and an offset, MUST return results in a deterministic order with
  ties broken by a unique key, and MUST reject a non-positive page size or a negative offset.
- **FR-026**: List chats: filter by a text contained in the name or the JID; sort by last activity (newest first) or
  by name (ascending); optionally include each chat's latest message by time.
- **FR-027**: Get chat: by JID, with its latest message; "not found" if absent.
- **FR-028**: Search contacts: match the text against full name, push name, first name, business name and phone
  number of the device store's contacts, excluding groups. A contact known by LID is reported with its phone number
  where the device store maps it; a person known under both a LID and a phone number is reported once. Each result
  has a phone number (possibly empty), a name and a JID.
- **FR-029**: Direct chat by contact: the direct chat whose phone number equals the given normalised number exactly,
  or "not found".
- **FR-030**: Contact chats: the direct chat with the contact plus every group in which the contact sent a message,
  each once, newest activity first, each with its real latest message.
- **FR-031**: List messages: filter by strictly-after time, strictly-before time, chat JID, sender phone number and
  search text, all optional and combined; newest first. The search text matches the text or the caption.
- **FR-032**: Message context: given a message, up to N earlier and M later messages from the same chat, both oldest
  first. The same expansion MUST be available for every hit of a message listing.
- **FR-033**: Last interaction: the newest message in the direct chat with a contact (from either side) or sent by
  that contact in any group, or none.
- **FR-034**: Text matching in every query MUST ignore case across scripts (not only ASCII) and MUST treat wildcard
  and escape characters in the caller's text literally.
- **FR-035**: The store MUST provide one phone-number normalisation used by every query and by storing: it accepts
  digits with an optional `+`, spaces, dashes, dots and parentheses, or a phone-number JID, and yields the digits.
  Resolving a LID to a phone number is the caller's job.

**LID chat merge (P2, §3.5)**

- **FR-036**: The store MUST offer an operation that merges a `@lid` chat into a given phone-number chat atomically:
  create or update the phone-number chat with the better name and the later last activity time, move the messages,
  keep the phone-number chat's copy when both hold the same message ID, rewrite senders equal to the LID to the phone
  JID with the phone number filled in, and delete the LID chat.
- **FR-037**: Merging MUST be idempotent: merging a LID chat that no longer exists does nothing and reports no
  error. Merging a chat into itself, or a source chat that is not `@lid`, MUST be rejected.

**Layout and tests (A4, Q3)**

- **FR-038**: The store MUST live in `internal/store` in the single module, behind operations that the ingest, MCP and
  media features call; no other package issues database queries.
- **FR-039**: Automated tests MUST cover, against a database in a temporary directory: the migrations (fresh,
  incremental, failing and too-new), upserts, the last activity time never going back, name quality, the LID merge,
  every query in FR-021–FR-034, and opening a database that holds both schemas.

### Key Entities

- **Database file**: `<data dir>/whatsapp.db`, the single store for device keys and message history, with its
  write-ahead journal files next to it.
- **Message schema version**: the number of the last message-schema migration applied; separate from the device
  store's version.
- **Migration**: a numbered, embedded schema change, applied once and in order.
- **Chat**: a conversation identified by its full JID (direct or group), with a name, the quality of that name, and the
  time of its newest stored message.
- **Message**: identified by chat JID and ID; carries the sender (full JID and phone number), time, from-me flag, text,
  caption, media type and media metadata. Belongs to exactly one chat.
- **Media metadata**: what is needed to download a message's media later: URL, direct path, key, hashes, length, MIME
  type and original file name.
- **Contact**: a person as known to the device store (names and JID, with the LID-to-phone map). Read by the store,
  written only by whatsmeow.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After a first start on an empty data directory, the data directory holds exactly 1 database file
  (plus its journal files), containing both the device store and the message tables; a restart changes no schema and
  keeps 100% of stored chats and messages.
- **SC-002**: The store's automated tests (FR-039) pass on Linux, Windows and macOS in CI, using only a temporary
  directory and no network.
- **SC-003**: Storing the same set of chats and messages in at least 20 different random orders, with duplicates,
  produces identical chats (names and last activity times) and identical messages every time.
- **SC-004**: On a database of 100,000 messages across 1,000 chats, every query in FR-021–FR-033 returns within 200 ms
  on a typical developer laptop.
- **SC-005**: 16 concurrent writers making 100 writes each, while queries run, complete with 0 "database is locked"
  errors.
- **SC-006**: Opening a database marked with a newer message-schema version fails every time, and the file is
  byte-for-byte unchanged afterwards; a failing migration leaves the schema version and data unchanged.
- **SC-007**: After the process is killed during a stream of writes, the next start opens the database without error
  and every write that had committed is present.

## Assumptions

- **Settled decisions used as given:** one SQLite file on a pure-Go driver, no PostgreSQL (S1, D2); whatsmeow's
  device store opens on that driver with the dialect name `"sqlite"` and the connection options found in
  `docs/spikes.md` S1; captions in their own field (D18); no import of earlier data (D16).
- **One device at a time:** the process links one WhatsApp account (§8.2), so contact queries read the device store
  of the current device only.
- **Who decides what is stored:** which WhatsApp messages are worth storing (text or a recognised media type), how
  names are resolved and how LIDs map to phone numbers are decided by ingest (004). The store enforces only identity,
  ordering, name quality and integrity.
- **Defaults and caps belong to the tools:** default page sizes, the 200 cap (D9), page numbers and RFC 3339
  rendering are applied by 005 and 006; the store takes an explicit page size and offset and returns times as
  time values.
- **Generated media filenames** (S5) are built by feature 008 from the stored ID and media type; the store keeps only
  the original file name of documents.
- **Messages are kept indefinitely.** There is no deletion other than the LID merge, and no message retention setting
  (Principle IX).
- **Sent messages:** whether messages sent through the tools are stored is decided by 007; the store treats them like
  any other message.
- **Out of scope:** the WhatsApp client and pairing (003), ingest and LID resolution (004), the MCP tools (005, 006),
  media download and its cache (008), backups and database maintenance commands.
