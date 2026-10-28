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
storage level. The WhatsApp client, ingest logic and deciding when a LID is merged are out of scope (003, 004), and so are the MCP
tools themselves (005, 006): this feature provides the operations they call.

## Settled in this spec

- **Two schema histories in one file.** whatsmeow keeps its own schema version for its device store tables. The
  message tables get their own, separate version history, so that a whatsmeow upgrade and one of our migrations
  do not share version records. Compatibility of both is checked before either is changed; supported device
  upgrades run before message migrations. The store is usable only after both finish. A committed earlier step
  remains committed if a later step fails; startup as a whole is not one transaction.
- **A database newer than the binary is refused.** If either schema is newer than the bundled implementation
  supports, opening fails during read-only preflight, naming the schema and both versions. The main file and
  existing sidecars are not changed, and no sidecars are created or removed. There is no downgrade path
  (Principle IX). Migration rollback instead preserves logical schema, data and version at that step's boundary;
  it does not promise byte-for-byte unchanged SQLite files.
- **Chat names have a quality.** "Never gets worse" (S4) needs a rule. A name has one of three qualities, from
  highest to lowest:
  - a *saved name*: a contact's full or first name from the account's own address book;
  - a *real name* chosen by someone else: a group subject, a contact's push or business name, a history-sync
    display name;
  - a *fallback* derived from the JID: a phone number, `Group <id>`, the JID's user part.

  The account owner's own label therefore outranks the name a sender picked for themselves, so a sender cannot
  make a chat look like someone else's by choosing a misleading push name. Every offered name carries the time it
  was seen. A stored name is replaced only by a non-empty name of a higher quality, or of the same quality seen at
  the same time or later, so a renamed group or contact is picked up. At equal quality and time the byte-wise
  greater name wins, so the result never depends on the order of arrival. Empty never replaces anything.
- **Re-storing a message replaces it.** Writing a message whose `(chat_jid, id)` already exists replaces its stored
  fields with the new ones (history sync and live delivery carry the same data, and a later copy may carry fresher
  media URLs). An empty media field in the new copy never overwrites a stored non-empty one, so an incomplete copy
  cannot make media undownloadable. The chat's `last_message_time` still only moves forward. This is the one case
  where the order of arrival matters: when two copies differ, the one written last is kept.
- **A contact is found under either identity.** Every query that takes a contact accepts a phone number, a phone
  JID or a LID (M6). The store resolves it through the device store's LID-to-phone map, and then matches messages
  and chats stored under the phone number, under the LID, or under both. A message stored under a LID before its
  phone number was known is therefore still found by the phone number once the map has it.
- **The `@lid` merge drops duplicates instead of skipping the chat.** In the old bridge (§3.5) a chat was skipped
  when both chats held the same message ID. Here the phone-number chat's copy is kept, the LID chat's copy is
  dropped, and the merge completes.
- **The merge rewrites the sender everywhere.** Merging a LID into its phone number also rewrites every message
  sent under that LID, in every chat (groups included), to the phone JID and number. It does this even when no
  `@lid` chat exists, so a person who only ever wrote in groups under a LID is merged too.
- **Context is chronological.** Messages before and after a message (for `get_message_context` and
  `include_context`) are both returned oldest first. The old bridge returned "before" newest first; the tools
  (006) decide how to present them.
- **Last interaction** with a contact is the newest message that is either in the direct chat with that contact
  (sent by either side) or sent by that contact in any group, under either of the contact's identities.
- **Text search ignores case in every script**, not only ASCII, so Cyrillic, Greek and accented Latin match
  regardless of case. Both sides are normalised to one Unicode form (NFC) first, so a composed `é` matches `e` plus
  a combining accent. Accents still count: `ete` does not match `été`. Wildcard characters typed by the caller
  (`%`, `_`) are matched literally.
- **The long-running commands open the store.** `stdio`, `serve` and `pair` open (and if needed create and migrate)
  the database after taking the lock and validating storage permissions and paths, so a broken, unsafe or too-new
  database is reported at startup before features 003–006 use it. `status` and `version` do not open it in this feature.
- **Private storage is not encryption.** Directory access and sensitive SQLite artifacts are checked on every open,
  including Windows ACLs. The operator owns disk/backup encryption and protection against a compromised host or
  the same unrestricted OS identity.
- **Store limits are defensive, not tool defaults.** Direct store callers are subject to bounded inputs, batches,
  results, context expansion, concurrency and waits. Their values are the approved storage acceptance profile
  below, fixed in code; performance targets are not resource ceilings.

## Clarifications

### Session 2026-10-28

- Q: Do you approve the proposed storage acceptance profile as fixed constants in code? → A: Yes, approved as
  proposed (fixed constants, not settings); recorded in the storage acceptance profile.
- Q: Should the spec get its own Security section, as constitution Principle X requires? → A: Yes, with
  FR-040–FR-050 and the storage acceptance profile moved into it, plus assets and trust boundaries, each
  finding taken up or deferred to a named feature, and abuse cases.
- Q: Should case-insensitive search also treat different Unicode spellings of the same letter as equal? → A:
  Ignore case and normalise both sides to NFC; accents still count (`ete` does not match `été`).
- Q: Where should the operator guide (backup/restore, retention, decommissioning) be written? → A: A new tracked
  `OPERATIONS.md` at the repository root with a "Storage" section; later features extend it and the README links to it.
- Q: How should the 200 ms query target be checked? → A: A Go benchmark over a seeded 100k-message database on the
  maintainer's machine, with results and machine recorded in `verification.md`; not a CI gate.
- Q: When two different real names reach the store for the same chat, which one is kept, independent of arrival
  order? → A: Each name carries the time it was seen; at the same quality the newer name wins, and at equal time
  the byte-wise greater name wins. The LID merge compares quality first, then time.
- Q: When an already stored message arrives again and the copies differ, which fields are replaced? → A: All fields
  come from the new copy, except that an empty media metadata field never overwrites a stored non-empty one.
- Q: For a person known under both a LID and a phone number, which JID and name does a contact search return? → A:
  The phone-number JID and number, with the phone-number entry's name (full, first, push, business, as ordered in
  the design review session below), falling back to the LID entry's name.

### Session 2026-10-28 (design review)

- Q: The contact queries accept only a phone number, and group messages sent under a LID are never rewritten once
  the phone number is known. How should 002 meet M6? → A: Add LID support. Every query that takes a contact
  accepts a phone number, a phone JID or a LID and resolves it through the device store's map; the LID merge
  rewrites the sender across all chats.
- Q: SC-003 promises the same result in any order, but a re-store replaces the message; abuse case 2 asks to block
  a swap between check and open, which the design cannot do. How should they read? → A: Reword both. SC-003 holds
  except for re-stores of the same `(chat_jid, id)` with different content, where the last copy written wins.
  Abuse case 2 relies on the private directory and refuses when the file opened is not the file checked.
- Q: Should the account's own address-book names outrank a sender's self-chosen push name? → A: Yes. Saved names
  (full, first) rank above other real names (push, business, group subject, history-sync display name), for chat
  names, sender names and contact search.
- Q: Anyone who messages the account can make `list_messages` with context exceed the result bound at default
  sizes. Who handles it? → A: Feature 006. It must fit the context into the store's bounds, by trimming or paging
  it, instead of failing; the store keeps its limits and errors as they are.

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
   is created, holds both schemas at their latest supported versions, and the directory, database, sidecars and
   sensitive temporary files meet FR-004 and FR-040 on that platform, including Windows ACLs.
2. **Given** an existing, up-to-date database, **When** a long-running command starts again, **Then** it opens the
   same file without changing the schema, and all stored chats and messages are still there.
3. **Given** a file at `<data dir>/whatsapp.db` that is not a database (for example truncated or random bytes),
   **When** a long-running command starts, **Then** it fails with a message that names the file and says it is not a
   usable database, and the file is left untouched.
4. **Given** a data directory with old `store/whatsapp.db` or `store/messages.db` files from the previous bridge,
   **When** a command starts, **Then** they are ignored (D16).
5. **Given** an existing directory, database or sidecar with unsafe ownership/access or a substituted path,
   **When** a command starts, **Then** startup fails before database writes, names the unsafe artifact safely, and
   does not silently repair permissions, truncate a target or replace the database.

---

### User Story 2 - The schema evolves safely (Priority: P1)

A developer adds a schema change in a later feature as a new numbered migration that ships inside the binary. An
operator upgrades the binary, and the database is brought up to date on the next start without any manual step.
If a migration fails, that step is rolled back and startup stops. Earlier successful steps may remain committed;
the operator is told which steps completed and how to follow the recovery procedure.

**Why this priority**: S2 and Principle III. Without a migration history, every schema change after the first
release would be a manual upgrade or a data loss.

**Independent Test**: Open a database at each earlier version (built by applying only the first migrations) and check
that opening it applies the rest; inject failures in both schema histories and check their transaction boundaries;
mark either schema with a future version and check read-only refusal, including the main file and sidecars.

**Acceptance Scenarios**:

1. **Given** an empty database, **When** it is opened, **Then** every message-schema migration is applied in order,
   and the recorded version equals the latest one the binary knows.
2. **Given** a database at an earlier version, **When** it is opened, **Then** only the missing migrations are
   applied, in order, and existing data is kept.
3. **Given** a migration that fails part-way, **When** the database is opened, **Then** opening fails with a message
   naming the migration, and the schema and data are exactly as before that migration.
4. **Given** a database whose message or device schema is at a version newer than the binary supports, **When** it
   is opened, **Then** preflight fails naming the schema and both versions, neither schema is upgraded, and the main
   file and sidecars remain unchanged as defined in FR-010.
5. **Given** the device store's own schema is behind the whatsmeow version in the binary, **When** the database is
   opened, **Then** the device store schema is upgraded as well.
6. **Given** a supported device upgrade that succeeds followed by a failing message migration, **When** opening
   fails, **Then** the device upgrade and any earlier committed message migrations remain, the failing step leaves
   no partial schema/data/version, no usable store is returned, and an offline retry follows the documented rules.
7. **Given** a failing device upgrade, **When** opening fails, **Then** no message migration runs, the failed device
   step meets its documented atomicity guarantee, and neither ingest nor WhatsApp connection starts.

---

### User Story 3 - Ingest stores chats and messages correctly in any order (Priority: P1)

Feature 004 hands the store live messages and history-sync batches, which arrive in no particular order and may
repeat. Whatever the order, the stored chats show their real latest activity and the best name known, and each
message is stored once.

**Why this priority**: §6 #10 (time going backwards) and §6 #9's effect on names break chat lists and the "last
message" shown to the user. S3, S4, D18.

**Independent Test**: Store a fixed set of chats and messages in several different orders, including duplicates, and
check that the resulting chats and messages are identical every time. Re-stores of one `(chat_jid, id)` with
different content are checked separately: the copy written last is kept.

**Acceptance Scenarios**:

1. **Given** a message `(chat C, id X)` already stored, **When** the same `(C, X)` is stored again with different
   content, **Then** there is still exactly one message `(C, X)`, and it carries the new content. **Given** a stored
   media message with a URL and direct path, **When** a copy without them is stored, **Then** the stored URL and
   direct path are kept and the other fields come from the new copy.
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
   stored. **Given** a real name seen at 10:00, **When** a different real name seen at 11:00 is offered, **Then** the
   new name is stored; **When** one seen at 09:00 is offered instead, **Then** the stored name stays.
   **Given** a stored saved name "Mum" (from the address book), **When** a push name "Bank Support" seen later is
   offered, **Then** the name stays "Mum".
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
4. **Given** a contact given as a phone number, a phone JID or a LID, **When** the direct chat with that contact is
   requested, **Then** only the chat whose phone number equals the contact's number exactly is returned (never a
   partial match), else the contact's `@lid` chat if it has not been merged yet, or "not found".
5. **Given** a contact who has a direct chat and has written in two groups, one of them under a LID that the device
   store maps to the contact's phone number, **When** the contact's chats are requested by phone number or by LID,
   **Then** those three chats are returned once each, newest activity first, each with its real latest message.
6. **Given** seeded messages, **When** messages are listed with any combination of after, before, chat, sender
   (a phone number, phone JID or LID) and search text, with a page size and offset, **Then** exactly the matching
   messages are returned, newest first; the sender matches messages stored under either of the contact's
   identities, and the search text matches either the message text or the caption.
7. **Given** a listing with context requested, **When** N before and M after are asked for, **Then** each hit comes
   with up to N earlier and M later messages from the same chat.
8. **Given** a contact given as a phone number, a phone JID or a LID, **When** the last interaction is requested,
   **Then** the newest message in the direct chat with them (from either side) or sent by them in a group, under
   either identity, is returned, or nothing.
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

At startup, and whenever a new LID-to-phone mapping arrives, feature 004 asks the store to merge that LID into its
phone number. Afterwards there is one chat per person, holding all their messages, and every message they sent in
any chat carries their phone JID.

**Why this priority**: P2 keeps the startup merge (§3.5). The store provides the operation; deciding which chats to
merge is 004's job.

**Independent Test**: Seed a LID chat and a phone-number chat with overlapping and distinct messages, merge, and check
the result; run the merge again and check nothing changes.

**Acceptance Scenarios**:

1. **Given** a LID chat L and a phone-number chat P, **When** L is merged into P, **Then** all of L's messages belong
   to P, L no longer exists, P's last activity is the later of the two, and P's name is the better of the two by the
   name rule (higher quality, then newer name time, then the byte-wise greater name).
2. **Given** only L exists, **When** it is merged into P, **Then** P is created with L's name, time and messages.
3. **Given** L and P both hold message ID X, **When** L is merged, **Then** P keeps its own copy of X, L's copy is
   dropped, and the merge completes.
4. **Given** messages in L, and in two group chats, whose sender is the LID, **When** the LID is merged, **Then**
   their sender becomes the phone JID and their phone number is filled in, in every chat.
5. **Given** no chat L but group messages sent under the LID, **When** the LID is merged, **Then** those senders
   are rewritten and no chat is created or removed.
6. **Given** a merge that fails part-way, **Then** every affected chat and message is exactly as before it.
7. **Given** a merge already done, **When** it is requested again, **Then** nothing changes and no error is
   reported.

---

### User Story 7 - Storage remains private, bounded and recoverable (Priority: P1)

The operator can keep the linked-device credentials and history private, diagnose failures without exposing
secrets, and recover a consistent database without connecting a copied device store to WhatsApp.

**Why this priority**: the file contains credentials as well as conversations. Resource exhaustion, unsafe paths
and incomplete recovery procedures must be addressed before later features depend on this store.

**Independent Test**: Use synthetic device/message data, isolated temporary directories, injected failures and
separate OS test identities. No real WhatsApp account or network connection is needed.

**Acceptance Scenarios**:

1. **Given** newly created and preexisting storage, including WAL/SHM and temporary files, **When** an unrelated
   identity attempts access on Unix and Windows, **Then** it cannot read or modify sensitive artifacts. Link swaps,
   junctions/reparse points, hard links and non-regular files are rejected according to FR-040.
2. **Given** an input, batch, result/context request or concurrent workload at and beyond each approved bound,
   **When** the store is called directly, **Then** boundary values work and excess work fails predictably without
   partial writes, unbounded allocation, silent truncation or an unbounded queue. Cancellation releases resources
   within the approved allowance, including while waiting for a database lock.
3. **Given** SQL-like search text, an unsupported sort, malformed identifiers/media metadata or overflowing
   numeric arguments, **When** a store operation runs, **Then** values cannot change query structure, invalid
   arguments are rejected without mutation, and valid literal search text still matches literally.
4. **Given** disk-low, disk-full, read-only storage or a migration failure, **When** an operation fails, **Then**
   committed state remains usable for documented recovery, no keys/history are deleted to make space, and logs
   contain the required safe event fields but no secret material or message bodies at any log level.
5. **Given** a write workload stopped using the documented quiesced-backup procedure, **When** its protected backup
   is restored to an isolated directory and opened offline, **Then** integrity checks pass and both schemas and all
   writes committed before quiescence are preserved. The original and restored device stores never connect.
6. **Given** the operator guide, **When** the operator reviews retention and decommissioning, **Then** indefinite
   growth, encryption/backup responsibilities, remaining copies and secure-deletion limitations are explicit.

---

### Edge Cases

- **Database locked by a long write elsewhere in the process:** admitted work waits only within the configured
  busy/operation deadline and is cancellable. Normal-load concurrency meets SC-005; overload returns a distinct
  bounded busy/resource-limit result rather than retrying forever.
- **Process killed mid-write:** on the next start every committed write is there and no partial write is visible.
- **Disk full or read-only file:** the write fails with an error naming the file and the reason; nothing is half
  written.
- **Graceful shutdown:** the database is closed cleanly as part of shutdown, within the existing 10-second limit.
  Closing stops admitting new operations at once, gives running ones a short grace period, then cancels them, so
  one long operation cannot use up the whole limit. A call made during or after closing gets a distinct "closed"
  error.
- **Empty or missing message ID or chat JID, or a chat JID that is not a full JID:** the write is rejected with an
  error naming the field; nothing is stored.
- **Message without a time:** rejected, because ordering and last activity depend on it.
- **Two messages in one chat with the same time:** the order is still deterministic (ties broken by ID), so paging
  never skips or repeats a message.
- **Search text with `%`, `_`, quotes or a backslash:** matched literally.
- **Search text in another script or case** ("ПРИВІТ" vs "привіт", "ÉTÉ" vs "été"): matches.
- **Search text in another Unicode form** (composed `é` vs `e` plus a combining accent, in either the search text or
  the stored text): matches. **Search text without accents** (`ete` vs `été`): does not match.
- **Phone number given as `+49 151 1234567`, `49-151-1234567` or `491511234567@s.whatsapp.net`:** all normalise to
  `491511234567` for phone-number filters and lookups.
- **Page beyond the end:** an empty result for a valid request within the approved offset/work limits.
- **Page size of zero or negative, negative/overflowing offset or context counts:** rejected; the store also
  enforces FR-043 limits independently of the tools (005, 006).
- **Chat with no messages** (a history-sync conversation without stored messages): listed with no latest message;
  chats with no last activity time sort after all others by last activity.
- **Chats without a name** sort after named chats when sorted by name.
- **`after` later than `before`:** an empty result, not an error.
- **Sender name for my own messages:** not looked up; the tools render them (006).
- **Merging a chat into itself, or merging a chat that is not `@lid`:** rejected with an error.
- **Device store after logout:** whatsmeow removes the device; the message tables are kept, and searching contacts
  returns no contacts until a device is paired again.
- **Oversized history batch:** rejected atomically; the caller must submit bounded batches. Splitting policy belongs
  to ingest (004), not an unbounded internal buffer in this feature.
- **Ambiguous ID with too many matching chats or a result exceeding the byte budget:** return a distinct
  resource-limit error asking for a narrower lookup, not a silently incomplete list of matching chats.
- **Unsafe or unverifiable storage protection:** fail closed before opening for writes; an unsupported filesystem's
  permission behavior is not a reason to continue with weaker protection.
- **Cancellation near commit:** report success only for a confirmed commit; if the outcome cannot be established,
  return an explicit indeterminate outcome, not a claim of rollback. A retry uses the existing idempotent identity.

## Requirements *(mandatory)*

### Functional Requirements

**Database file (S1, D2, D16)**

- **FR-001**: All persistent state MUST live in one database file, `<data dir>/whatsapp.db`, holding both the
  whatsmeow device store and the message tables. No other database file is created and PostgreSQL is not supported.
- **FR-002**: The database MUST be opened with write-ahead logging, foreign-key enforcement, a busy timeout of at
  least 5 seconds, and write transactions that take the write lock at their start, so that concurrent writers wait
  instead of failing.
- **FR-003**: The database driver MUST be pure Go and build for all five targets with CGO disabled.
- **FR-004**: Creation and every subsequent open MUST verify private ownership/access for the data directory,
  database, WAL/SHM, rollback journals and any sensitive SQLite temporary files. On Unix the intended process
  identity owns directories with mode `0700` and files with mode `0600`, with no additional ACL grants to unrelated
  identities; on macOS, where an extended ACL can grant access that the mode bits do not show, any extended ACL
  entry on these artifacts is refused. On Windows a verified DACL MUST restrict access to the intended user/service identity and explicitly
  documented required system/administrative principals; inherited broad grants MUST NOT expose the data. Go mode
  bits alone are not Windows access control. New artifacts MUST be protected from first creation; unsafe existing
  artifacts or unverifiable protection MUST fail startup without silent permission changes or deletion.
- **FR-005**: `stdio`, `serve` and `pair` MUST open the database after taking the data-directory lock and validating
  FR-004/FR-040, and MUST close it during graceful shutdown. A database that cannot be opened MUST fail
  startup with a message naming the file and a safe reason under FR-048. No store, ingest or WhatsApp connection
  becomes available until opening and both schema upgrades succeed.
- **FR-006**: Files of the previous bridge (`store/*.db`) and its PostgreSQL database MUST NOT be read.

**Migrations (S2)**

- **FR-007**: The message schema MUST be defined by numbered migrations embedded in the binary, applied in order at
  open, and recorded with a version number kept separate from the device store's own version.
- **FR-008**: Migration 1 MUST create the complete message schema in an empty database. There is no older schema to
  detect or adopt.
- **FR-009**: Each migration MUST be applied atomically together with its version record: on failure, the schema,
  the data and the recorded version are as before that migration, and opening fails naming the migration. This is
  logical rollback of that step, not a promise that SQLite's main file or journal bytes never changed. Previously
  committed steps remain committed as specified in FR-041.
- **FR-010**: Before enabling write-affecting pragmas, recovering/checkpointing WAL or upgrading either schema,
  existing storage MUST undergo read-only compatibility preflight for both schema versions, including committed
  state in an existing WAL. Unsupported newer versions, invalid/unrecognised version metadata, or corruption found
  by the preflight (a file that is not SQLite, a damaged header, unreadable schema or version tables) MUST stop
  opening without modifying the main file or existing sidecars, or creating/deleting sidecars. A full integrity
  check is not run on every start. Corruption elsewhere in the file fails the operation that reaches it, with a
  safe `corrupt` error, and is handled through the restore procedure (FR-047). Errors MUST
  identify the schema and supported/observed versions when readable, without sensitive contents. A genuinely
  uninitialised schema is version zero; missing version metadata for already-present tables is not a fresh schema.
- **FR-011**: The device store's schema MUST be created or upgraded by whatsmeow's own mechanism when the database is
  opened, only after both compatibility checks pass and before message migrations (FR-041). Its version support
  and failure guarantees MUST be verified for the pinned dependency rather than inferred from message migrations.

**Chats and messages (S3, S4, D18, §3.2)**

- **FR-012**: A chat MUST record its full JID, its name, the quality of that name (saved, real, fallback, or none
  while the chat has no name yet), the time that
  name was seen, and its last activity time. Whether it is a group or a direct chat follows from the JID.
- **FR-013**: A message MUST record its ID, chat JID, full sender JID (without the device part), normalised sender
  phone number (empty when the sender is an unresolved LID), time, whether it is from me, text, caption, media type,
  and the media metadata needed to download it later: URL, direct path, media key, file hash, encrypted file hash,
  file length, MIME type and original file name.
- **FR-014**: The store MUST derive the normalised sender phone number from the sender JID itself (digits only, for
  phone-number JIDs), so the two can never disagree.
- **FR-015**: A message MUST be identified by `(chat_jid, id)`. Storing a message whose `(chat_jid, id)` exists MUST
  replace its stored fields with the new copy's, except that an empty media metadata field (URL, direct path, media
  key, file hash, encrypted file hash, file length, MIME type, original file name) MUST NOT overwrite a stored
  non-empty one; storing never creates a second copy.
- **FR-016**: Storing a message MUST create its chat if it does not exist and move the chat's last activity time
  forward to the message's time if that is later, in the same atomic step.
- **FR-017**: The store MUST offer a chat update (name, name quality, the time the name was seen, last activity
  time) for chats known without a
  message, such as history-sync conversations. The last activity time MUST only ever move forward, and an absent time
  MUST NOT clear it.
- **FR-018**: A chat's name MUST be replaced only by a non-empty name that ranks higher by, in order: quality (saved
  above real, real above fallback), then the time the name was seen (a later or equal time ranks at least as high), then byte-wise
  comparison of the names (greater wins). The outcome MUST NOT depend on the order in which names arrive. A name
  offered without a time, or with a time outside the approved timestamp range, is rejected.
- **FR-019**: A batch of messages within FR-043 count/byte limits MUST be storable in one transaction. Invalid or
  oversized batches MUST be rejected as a whole without storing a prefix; callers split larger conversations.
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
  contact's full, first, push or business name from the device store (in that order, so the address book comes
  first), looked up under the sender JID and then under its mapped phone JID or LID, else the saved or real name of
  the direct chat with the sender, else empty.
- **FR-025**: Every list query MUST take a page size and an offset, MUST return results in a deterministic order with
  ties broken by a unique key, and MUST reject a non-positive page size, a negative offset, overflow or a request
  above FR-043 bounds. A direct store caller MUST NOT bypass those bounds.
- **FR-026**: List chats: filter by a text contained in the name or the JID; sort by last activity (newest first) or
  by name (ascending); optionally include each chat's latest message by time.
- **FR-027**: Get chat: by JID, with its latest message; "not found" if absent.
- **FR-028**: Search contacts: match the text against full name, push name, first name, business name and phone
  number of the device store's contacts, excluding groups. A contact known by LID is reported with its phone number
  where the device store maps it; a person known under both a LID and a phone number is reported once, matched if
  either entry matches. Each result has a phone number (possibly empty), a name and a JID. For a person known under
  both, the result carries the phone-number JID and phone number, and the phone-number entry's first non-empty name
  in the order full, first, push, business; if that entry has none, the LID entry's name in the same order. A LID
  without a mapped phone number is reported with its LID JID and an empty phone number.
- **FR-029**: Direct chat by contact: the contact is resolved by FR-035. The result is the direct chat whose phone
  number equals the contact's number exactly, else the contact's `@lid` chat if it still exists, or "not found".
- **FR-030**: Contact chats: the contact is resolved by FR-035. The result is the direct chat with the contact plus
  every group in which the contact sent a message under either identity, each once, newest activity first, each
  with its real latest message.
- **FR-031**: List messages: filter by strictly-after time, strictly-before time, chat JID, sender and search text,
  all optional and combined; newest first. The sender is a contact resolved by FR-035 and matches messages sent
  under either identity. The search text matches the text or the caption.
- **FR-032**: Message context: given a message, up to N earlier and M later messages from the same chat, both oldest
  first. The same expansion MUST be available for every hit of a message listing, subject to per-side, aggregate
  row and result-byte limits in FR-043; checked arithmetic MUST precede expansion/allocation.
- **FR-033**: Last interaction: the contact is resolved by FR-035. The result is the newest message in the direct
  chat with the contact (from either side) or sent by the contact in any group under either identity, or none.
- **FR-034**: Text matching in every query MUST ignore case across scripts (not only ASCII), MUST compare the search
  text and the stored text after normalising both to Unicode NFC, MUST NOT ignore accents or other diacritics, and
  MUST treat wildcard and escape characters in the caller's text literally. Stored text is returned as originally
  received; normalisation applies only to matching.
- **FR-035**: The store MUST provide one phone-number normalisation used by every query and by storing: it accepts
  digits with an optional `+`, spaces, dashes, dots and parentheses, or a phone-number JID, and yields the digits.
  Every query that takes a contact MUST also accept a LID JID, and MUST resolve the contact to its identities: the
  phone JID and number, the LID, or both, using the current device's LID-to-phone map in both directions. A
  contact with no mapping has the one identity it was given. What ingest stores as a sender, and which LIDs it
  merges, remain ingest's decision (004).

**LID chat merge (P2, §3.5)**

- **FR-036**: The store MUST offer an operation that merges a LID into a given phone number atomically. It rewrites
  every message whose sender is the LID, in every chat, to the phone JID with the phone number filled in. If the
  `@lid` chat exists, it also creates or updates the phone-number chat with the better name under FR-018 and the
  later last activity time, moves the messages, keeps the phone-number chat's copy when both hold the same message
  ID, and deletes the LID chat.
- **FR-037**: Merging MUST be idempotent: merging a LID with no chat and no messages left under it does nothing and
  reports no error. Merging a JID into itself, a source that is not `@lid`, or a target that is not a phone JID
  MUST be rejected.

**Layout and tests (A4, Q3)**

- **FR-038**: The store MUST live in `internal/store` in the single module, behind operations that the ingest, MCP and
  media features call; no other package issues database queries. The one exception is whatsmeow's device store,
  which queries the same file through the container the store exposes to the WhatsApp client (003).
- **FR-039**: Automated tests MUST cover, against a database in a temporary directory: the migrations (fresh,
  incremental, failing and too-new), upserts, the last activity time never going back, name quality, the LID merge,
  every query in FR-021–FR-034, and opening a database that holds both schemas. Tests MUST also cover FR-040–FR-050:
  Unix permissions and Windows ACLs (new/existing files, sidecars and sensitive temporary files), separate-identity
  access, path substitutions, combined schema failures, every approved resource boundary, cancellation/contention,
  disk-low/full failures, parameterized/literal queries, validation fuzz cases, secret-free diagnostics at all log
  levels, and protected offline backup/restore. Fixtures MUST be synthetic and tests MUST NOT connect to WhatsApp.
  Cross-identity/platform checks needing special privileges MUST have a documented isolated test procedure and
  recorded evidence before acceptance; an unavailable CI capability MUST NOT be reported as a passed test.

**Storage security and recovery**: FR-040–FR-050 are listed in the Security section below.

### Key Entities

- **Database file**: `<data dir>/whatsapp.db`, the single store for device keys and message history, with its
  write-ahead journal files next to it.
- **Message schema version**: the number of the last message-schema migration applied; separate from the device
  store's version.
- **Migration**: a numbered, embedded schema change, applied once and in order.
- **Chat**: a conversation identified by its full JID (direct or group), with a name, the quality of that name (saved, real or fallback), the time
  the name was seen, and the time of its newest stored message.
- **Message**: identified by chat JID and ID; carries the sender (full JID and phone number), time, from-me flag, text,
  caption, media type and media metadata. Belongs to exactly one chat.
- **Media metadata**: what is needed to download a message's media later: URL, direct path, key, hashes, length, MIME
  type and original file name.
- **Contact**: a person as known to the device store (names and JID, with the LID-to-phone map). Read by the store,
  written only by whatsmeow.

## Security *(constitution Principle X)*

The threat model is the [security review](../../docs/security-review.md) (F01–F16). This section takes up the
storage portions of F03, F05, F09–F13 and F16 as requirements and defers every other finding to a named later
feature. These are requirements, not claims of implemented controls or confirmed vulnerabilities. D1–D18 remain
unchanged.

### Assets and trust boundaries

**Assets**: the linked device's keys and session (whatsmeow device store); message history (text, captions,
senders, times); contacts and the LID-to-phone mapping; media keys, hashes and download URLs; the database's
sidecars, temporary files and backups, which carry the same data.

**Trust boundaries**:

- **WhatsApp content → store.** Messages, names and media metadata originate from anyone who can message the
  account. Ingest (004) passes them in; the store treats them as untrusted data (FR-042, FR-046).
- **Store callers → store.** Ingest and the MCP tools (005–008) call the store directly. Tool arguments come from
  an LLM that can be misled, so the store enforces its own limits independently of the tools (FR-043, FR-044).
- **Filesystem and other OS identities.** Other local users, link substitutions and copied files must not reach
  the database or its sidecars (FR-004, FR-040).
- **Logs and errors → operator and log collectors.** Diagnostics must not become a second copy of secrets or
  conversations (FR-048).
- **Backups and decommissioned copies.** These leave the process's control; the operator owns their protection
  (FR-047, FR-049, FR-050).

### Findings taken up or deferred

| Finding | In this feature | Deferred to |
| --- | --- | --- |
| F01 Authorization/consent policy | — | 005–007, after the owner's decision is recorded in `docs/specification.md` |
| F02 Remote HTTP/proxy transport | — | 009 |
| F03 Sensitive storage protection | Database, sidecars, temporary files: FR-004, FR-040 | Media downloads 008; secret files 009/010 |
| F04 Secret lifecycle | — | 009, 010 |
| F05 Filesystem containment | Storage paths and objects: FR-040 | Outbox 007; download naming and cache 008 |
| F06 Media processing / ffmpeg | — | 007, 008 |
| F07 Webhook confidentiality/destination | — | 010 |
| F08 Webhook replay/delivery | — | 010 |
| F09 Resource bounds | Store inputs, results, work, waits, disk: FR-043–FR-045, storage acceptance profile | Tool requests 005/006; media 007/008; HTTP sessions 009; webhook queue 010 |
| F10 Input validation | Store inputs and queries: FR-042, FR-046 | Tool argument schemas 005/006; URLs 009/010 |
| F11 Security events in logs | Storage events: FR-048 | Pairing/QR 003; message-body logging 004; tool, send, HTTP and webhook events 005–010 |
| F12 Retention and privacy | Stored history disclosure: FR-049, FR-050 | Media retention 008; webhook recipients 010; overall data-flow inventory 011 |
| F13 Backup/migration recovery | FR-009–FR-011, FR-041, FR-047 | — |
| F14 Incident response | — | 011 (operator runbook) |
| F15 Supply chain | New pure-Go dependencies pass the existing CI module verification and govulncheck | Release provenance and signing 011 |
| F16 Boundary/control ownership | Operator responsibilities for storage: FR-049 | Deployment profiles and control matrix 011, with the owner's decision |

Traceability is limited to this feature: private artifacts/paths address F03/F05 (`AC-3`, `SC-28`); bounded work
and query/input rules address F09/F10 (`SC-5`, `SI-10`); safe events address the storage portion of F11 (`AU-12`);
recovery addresses F13 (`CP-9`, `CP-10`); retention/operator responsibilities address F12/F16 (`SI-12`, `CM-6`).
These mappings do not establish satisfaction of a NIST baseline or close the wider review findings.

The data-directory privacy and lock safety of the [001 hardening](../../docs/backlog/001-binary-skeleton-security-hardening.md)
(`001-SEC-01`, `001-SEC-02`) are merged into `main` (#2) and provide `internal/private`; feature 002 creates its
sensitive files through it and still owns verification of its database, sidecars and temporary artifacts. A check
after config loading or lock acquisition does not retroactively protect earlier operations.

### Security requirements

- **FR-040**: Filesystem checks MUST protect the actual objects opened/created, not only a pathname checked earlier.
  The data-directory path and database/sidecar/temp entries MUST resist replacement by unrelated identities;
  unsupported symlinks, junctions/reparse points, hard-linked database/sidecar files, non-regular files and unsafe
  path aliases MUST be rejected before mutation. The platform contract MUST state supported local filesystems and
  handling of UNC/device paths and alternate data streams; unsupported forms fail closed. A path form is judged by
  where it resolves, so a mapped network drive or a `subst` drive that leads to a network path is rejected like a
  UNC path. SQLite-created artifacts MUST stay in protected locations. Rechecking names alone is not sufficient for
  a check/open race: the private data directory keeps unrelated identities from replacing entries, and after
  SQLite opens the database the store MUST confirm that the file at each path is the same file it checked (by file
  identity) and fail startup on a mismatch. Substitution by the same OS identity is outside this feature's threat
  model (see "Private storage is not encryption").
- **FR-041**: Startup MUST check both schema histories, upgrade the device schema, then apply message migrations,
  stopping at the first error and closing all opened resources. Each device-upgrade step and its version record
  MUST likewise have a verified atomic failure boundary; if the pinned upstream mechanism cannot provide it,
  resolve that incompatibility before implementation rather than promising unsupported rollback or hand-editing
  its schema. Earlier successful steps remain committed, no whole-startup rollback is promised, and the safe error
  MUST identify completed/failed steps. Retry MUST resume from committed versions without downgrade or automatic
  restore. A failed or indeterminate upgrade MUST never yield a partially usable store.
- **FR-042**: Store operations MUST validate types, required fields, syntax and approved byte/range limits before
  issuing mutations. Chat JIDs use `s.whatsapp.net`, `g.us` or `lid`; sender JIDs use `s.whatsapp.net` or `lid`, with
  device components normalised as in story 3. Empty/malformed identifiers and unsupported domains are rejected;
  phone numbers follow FR-035. Message IDs are opaque values, never paths or SQL. Media types are empty or the
  supported image/video/audio/document types; present keys/hashes, lengths, URLs and MIME metadata MUST meet the
  approved storage contract, including nonnegative, representable file lengths. Missing optional media metadata
  may be stored for later download validation; metadata is never fetched or executed by the store. Required message
  times and supplied filters MUST fit the documented timestamp range; absent chat activity remains allowed, and
  reversed filters retain the empty-result behavior above. Original message/name text is preserved, not executed
  or silently sanitised; diagnostics encode it safely under FR-048.
- **FR-043**: All direct store calls MUST enforce the approved storage acceptance profile: field/search byte lengths,
  batch count/aggregate bytes, list size/offset, per-side and total expanded context, aggregate result bytes
  (including ambiguous-ID results), active/waiting work, database connections and wait/operation deadlines. Base
  list sizes MUST NOT exceed 200; tools still own their existing defaults and page-to-offset conversion (D9).
  Context rows have a separate aggregate bound, not an implicit exemption. Arithmetic for offsets, sizes and
  expansion MUST be checked before SQL/allocation. Reject oversized requests before expensive work where possible;
  bound materialisation of existing/upstream data too, without silent truncation or partial successful results.
  Excess work returns distinct validation/resource-limit/busy errors. No unbounded queue or retry loop is allowed.
- **FR-044**: Every blocking operation MUST accept cancellation and use the earlier of its caller deadline and its
  configured operation budget, including admission, SQL, lock waits and iteration. The configured busy timeout
  remains at least 5 seconds (FR-002), but MUST NOT override an earlier deadline/cancellation. Transactions MUST
  release resources on cancellation/failure and report confirmed versus indeterminate commit outcomes honestly.
  Startup upgrades and maintenance have separately approved finite budgets rather than unlimited waits.
- **FR-045**: Disk-low admission and disk-full/write errors MUST preserve committed state and fail without automatic
  deletion of device keys, messages, journals or backups. The acceptance profile MUST define the free-space reserve,
  measurement interval and safe write-resumption rule. Already admitted/device-store writes may still encounter
  full storage and MUST fail safely; a free-space check is not a guarantee of available disk. Bounded WAL/checkpoint
  behavior and long-reader handling MUST be documented and tested. No finite total-history retention is implied.
- **FR-046**: All SQL values, including search patterns, identifiers used as values and pagination, MUST be bound
  parameters. Any dynamic SQL identifiers, sort columns or directions MUST come only from fixed allow-lists;
  unsupported sort values are errors. Literal wildcard escaping and Unicode matching (FR-034) remain required;
  injection-like but valid search strings MUST NOT be rejected merely to avoid safe parameterization.
- **FR-047**: Feature acceptance MUST include an operator guide: a "Storage" section in a tracked `OPERATIONS.md` at
  the repository root, which later features extend and the README (011) links to. It MUST give a procedure for a
  coherent, quiesced SQLite backup: stop the
  owner and all connections, verify successful checkpoint/clean closure, and protect the resulting backup like
  live credentials. Copying only the main file while writes/WAL are active is explicitly unsupported. If clean
  checkpoint/closure cannot be verified, preserve the coherent stopped file set for documented recovery rather
  than discarding sidecars. Restore into a protected, isolated directory, offline, and validate SQLite integrity,
  foreign keys, both schema versions and expected data before replacing live storage. Keep the original protected
  until validation succeeds. An online method may be documented only if actually supported and tested. The guide
  MUST warn that directory locks do not coordinate copies and that a restored device store MUST NOT connect beside
  its original. Live account checks/re-pairing require separate explicit permission; no new backup command is added.
- **FR-048**: Storage startup/shutdown, permission/path refusal, schema preflight/upgrade success/failure and storage
  resource/I/O failures MUST produce structured events with UTC time, operation/correlation, schema/version where
  relevant, outcome and a safe reason. Logs/errors MUST exclude device/media keys, tokens, QR data, sensitive SQL
  parameters and message/caption bodies at every level in this feature. Do not forward raw driver/upstream errors
  that can contain these values. Paths and untrusted diagnostic fields MUST be bounded and control-character-safe.
  Retain useful safe operation/version/error codes and stderr in stdio mode; no new audit service is required.
- **FR-049**: The operator guide MUST disclose indefinite message/metadata retention and resulting database,
  WAL and backup growth; data classes include device credentials, contacts/LID mappings, text/captions and media
  keys. The operator selects protected backup destinations, retention, recovery-point/recovery-time objectives,
  disk/backup encryption and restore rehearsals. Owner-only access is not encryption at rest, and this feature
  neither adds SQLite encryption nor establishes a NIST/FIPS assurance claim.
- **FR-050**: The operator guide MUST document offline decommissioning: stop the owner, inventory only this installation's database,
  sidecars, temporary artifacts and related caches/logs/backups, and explicitly identify responsibility for copies
  held elsewhere. Explain that unlinking files does not guarantee erasure on SSDs, snapshots or backups, and that
  encryption/key destruction depends on the operator's storage system. Do not delete unrelated files or add
  WhatsApp-message deletion/automatic retention as part of this feature.

### Storage acceptance profile (approved 2026-10-28)

Approved by the owner on 2026-10-28. The values are fixed constants in code, not configuration settings, and do
not change tool defaults. Every value is enforced on what is actually consumed and has a boundary test and an
excess/overflow test (SC-009). Zero or unset never means unlimited. Sizes are UTF-8 bytes; KiB/MiB/GiB are powers
of 1024.

| Parameter group | Approved values |
| --- | --- |
| Fields and metadata | JID ≤ 256 B; message ID ≤ 256 B; chat and contact names ≤ 1 KiB; text ≤ 256 KiB; caption ≤ 256 KiB; search text ≤ 1 KiB; URL ≤ 4 KiB; direct path ≤ 4 KiB; MIME type ≤ 255 B; original file name ≤ 1 KiB; media key, file hash and encrypted file hash exactly 32 B when present; file length 0 to 4 GiB; message times and time filters from 2000-01-01T00:00:00Z (inclusive) to 2100-01-01T00:00:00Z (exclusive). A value outside these is a validation error naming the field, with nothing stored |
| Batch and results | Batch ≤ 1,000 messages and ≤ 16 MiB aggregate field bytes; chat-update batch ≤ 1,000 updates (their size is bounded by the field limits); list size 1–200; offset 0–100,000; context ≤ 20 rows per side and ≤ 2,000 context rows per call in total; ≤ 32 MiB of result bytes per call; an ambiguous-ID error lists ≤ 100 chats. Existing or upstream data that would exceed a result bound returns a resource-limit error, never a truncated or partial result |
| Work and waits | 1 write connection and 4 read connections; ≤ 16 active and ≤ 64 waiting operations (beyond that: immediate busy error); admission wait ≤ 5 s; busy timeout 5 s; each query or write ≤ 10 s; resources released within 1 s of cancellation; startup schema upgrades ≤ 10 min in total; closing the store fits within the existing 10-second shutdown limit |
| Disk and WAL | Free-space reserve 256 MiB, checked before each write transaction from a measurement at most 30 s old; once refused, writes resume when ≥ 512 MiB is free. The write-ahead log is truncated to ≤ 64 MiB after checkpoints; a long reader is bounded by the 10 s query deadline. Nothing (keys, messages, journals, backups) is deleted automatically |
| Platform and recovery | Local filesystems only (NTFS on Windows; ext4 and other local POSIX filesystems; APFS). UNC/network paths (including mapped and `subst` drive letters that resolve to one), `\\?\` and `\\.\` device paths and alternate data streams are rejected. On Windows the access list is limited to the user, SYSTEM and Administrators (constitution Principle V). Upstream device-migration guarantees, backup/restore steps and the isolated cross-identity test procedure are verified and documented under FR-011, FR-041, FR-047 and FR-039 |

### Abuse cases

Each is a negative test with synthetic data. It shows the rejected action has no side effect: nothing is written,
created, repaired or deleted, and a sentinel file outside the data directory stays unchanged. A platform-specific
case is tested on each platform it covers; one that cannot be run there is recorded as not verified.

1. An unrelated OS identity tries to read or write the database, WAL/SHM or a temporary file (Unix and Windows):
   denied.
2. The database or a sidecar is a symlink, junction/reparse point, hard link or non-regular file: startup refuses
   before any write, and the link target is untouched. The database file is replaced between the check and
   SQLite's open (a test hook stands in for the attacker): the store detects that the opened path no longer holds
   the checked file, closes without migrating or storing anything, and fails startup. An unrelated identity cannot
   make that replacement at all, because it cannot create or rename entries in the private data directory (shown
   in the cross-identity evidence).
3. A preexisting data directory, database or sidecar is readable by others: startup refuses with the fix, and the
   permissions are not changed.
4. Search text contains SQL fragments, quotes, `%`, `_` or a backslash: it matches literally and returns only
   literal matches.
5. An unsupported sort value, a malformed JID or message ID, malformed media metadata, an oversized field, or an
   overflowing offset or context count is passed: validation error, nothing stored.
6. An oversized history batch is stored: rejected as a whole, with no prefix stored.
7. Calls exceed 16 active and 64 waiting operations: the excess gets a busy error at once; memory and goroutines
   stay bounded.
8. Context expansion or results exceed the row or byte limits: resource-limit error, not a truncated result.
9. Free space falls below the reserve, or the disk is full: writes are refused, and no key, message, journal or
   backup is deleted.
10. A database with a newer schema version is opened: refused, and the main file and sidecars are byte-for-byte
    unchanged, with none created or removed.
11. Success and failure paths run over seeded device keys, media keys, message bodies and names containing
    terminal control sequences: none of them appear in logs or errors at any level.
12. A restored backup is validated offline only: neither it nor the original device store connects to WhatsApp.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After a first start on an empty data directory, the data directory holds exactly 1 database file
  (plus its journal files), containing both the device store and the message tables; a restart changes no schema and
  keeps 100% of stored chats and messages.
- **SC-002**: The store's automated tests (FR-039) pass on Linux, Windows and macOS in CI, using only a temporary
  directory and no network.
- **SC-003**: Storing the same set of chats and messages in at least 20 different random orders, with duplicates
  of identical content, produces identical chats (names and last activity times) and identical messages every
  time. The one exception is a message re-stored under the same `(chat_jid, id)` with different content: there the
  copy written last is kept (FR-015), and a separate test checks that rule.
- **SC-004**: On a database of 100,000 messages across 1,000 chats, every query in FR-021–FR-033 returns within 200 ms.
  This is measured by a Go benchmark over a seeded database of that size, run on the maintainer's machine. The
  results and the machine (CPU, memory, disk, OS) are recorded in the feature's `verification.md`. It is not a
  CI gate.
- **SC-005**: 16 concurrent writers making 100 writes each, while queries run, complete with 0 "database is locked"
  errors.
- **SC-006**: Opening a database marked with a newer message-schema version fails every time, and the file is
  byte-for-byte unchanged afterwards, as do existing sidecars, with none created or removed. The same holds for a
  newer device schema and the other FR-010 preflight refusals. A failing migration instead leaves logical schema,
  version and data unchanged at that step's boundary; combined failures obey FR-041, not whole-startup rollback.
- **SC-007**: After the process is killed during a stream of writes, the next start opens the database without error
  and every write that had committed is present.
- **SC-008**: Unix and Windows evidence demonstrates that unrelated identities cannot read/write any sensitive
  artifact in FR-004. Every tested unsafe preexisting artifact/path substitution fails before mutation, including
  WAL/SHM and temporary-file cases; unsupported/unverified platform guarantees are not counted as passed.
- **SC-009**: For every approved profile parameter, tests cover its boundary and excess/overflow cases, record
  elapsed time, memory/result bytes, active/waiting work and disk/WAL growth as applicable, and verify the approved
  ceilings and cancellation allowance. SC-004/SC-005 remain normal-load targets, not substitutes for these bounds.
- **SC-010**: An offline restore of synthetic device/message data backed up after a quiesced write workload passes
  integrity/foreign-key checks, preserves both schema histories and every pre-quiescence committed write, and
  satisfies private-access rules. Interrupted upgrades and disk-full failures follow the documented recovery path.
- **SC-011**: Captured startup, success and failure diagnostics at every log level contain the required event fields
  and none of the seeded secrets/message bodies or executable terminal control sequences. The "Storage" section of
  `OPERATIONS.md` covers FR-047/FR-049/FR-050 and identifies the owner of encryption, backups, retention and recovery objectives.

## Assumptions

- **Settled decisions used as given:** one SQLite file on a pure-Go driver, no PostgreSQL (S1, D2); whatsmeow's
  device store opens on that driver with the dialect name `"sqlite"` and the connection options found in
  `docs/spikes.md` S1; captions in their own field (D18); no import of earlier data (D16).
- **One device at a time:** the process links one WhatsApp account (§8.2), so contact queries read the device store
  of the current device only.
- **Who decides what is stored:** which WhatsApp messages are worth storing (text or a recognised media type), how
  names are resolved, which quality a name has, and when a LID is merged are decided by ingest (004). The store
  only reads the device store's LID-to-phone map to answer queries (FR-035). The store enforces only identity,
  ordering, name quality, integrity and its own validated resource/input contract. Ingest must handle store
  rejection and bounded history batches without bypassing that contract.
- **Tool defaults versus store bounds:** default page sizes, page numbers and RFC 3339 rendering are applied by
  005 and 006; the store takes an explicit page size and offset and returns time values. Both layers enforce the
  200 base-list cap (D9); the store independently enforces FR-042–FR-045, including context and result-byte limits.
- **Context at tool defaults is 006's to fit.** Anyone who can message the account can send messages large enough
  that a `list_messages` page with context, at the tools' default sizes, exceeds the 2,000-row or 32 MiB bound.
  The store answers that with `ErrResourceLimit`, as it does for every bound. Feature 006 must not pass that
  failure on to the user at default sizes: it trims or pages the context so the call fits. This is recorded as a
  requirement for 006 in `docs/roadmap.md`.
- **Generated media filenames** (S5) are built by feature 008 from the stored ID and media type; the store keeps only
  the original file name of documents.
- **Messages are kept indefinitely.** There is no deletion other than the LID merge, and no message retention setting
  (Principle IX). This is not a promise of bounded total disk use: growth, encryption, backup retention and offline
  decommissioning responsibilities MUST be disclosed under FR-045/FR-049/FR-050.
- **Sent messages:** whether messages sent through the tools are stored is decided by 007; the store treats them like
  any other message.
- **Out of scope:** the WhatsApp client and pairing (003), ingest and the choice of LIDs to merge (004), the MCP tools (005, 006),
  media download and its cache (008), recipient/approval policy, HTTP/webhooks/converters, and backup/database
  maintenance commands. Storage-compatible backup/restore and decommissioning guidance and offline acceptance
  evidence are in scope; no new service, account action or maintenance command is implied.
