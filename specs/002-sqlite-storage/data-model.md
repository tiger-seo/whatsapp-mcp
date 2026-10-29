# Data model: Storage: one SQLite file and migrations

Phase 1 of the [plan](plan.md). This model is what migration 1 creates and what the store operations
([contracts/store-api.md](contracts/store-api.md)) enforce. Decisions referenced as R1–R14 are in
[research.md](research.md).

## The file

`<data dir>/whatsapp.db`, plus `whatsapp.db-wal` and `whatsapp.db-shm` while open. It holds two independent schema
histories:

| History | Version record | Owner | Tables |
| --- | --- | --- | --- |
| Device store | `whatsmeow_version (version, compat)` | whatsmeow `sqlstore` (R3) | `whatsmeow_*` |
| Message store | `mcp_version (version)` | this feature's migrations (R5) | `mcp_version`, `chats`, `messages` |

The store **reads** these whatsmeow tables and never writes them: `whatsmeow_device` (the current device's JID),
`whatsmeow_contacts` and `whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL)`. The LID map is not per device,
and it holds bare user parts: `lid` without `@lid`, `pn` without `@s.whatsapp.net`. The store adds the suffixes
when it compares them with stored JIDs.

All message-store tables are `STRICT`, so SQLite rejects a value of the wrong type instead of storing it.

## `mcp_version`

| Column | Type | Rules |
| --- | --- | --- |
| `version` | INTEGER NOT NULL | exactly one row; the number of the last applied migration |

The preflight refuses zero rows, more than one row, a version above the latest bundled migration, or `chats`/
`messages` present without this table (R4).

## `chats`

| Column | Type | Rules |
| --- | --- | --- |
| `jid` | TEXT PRIMARY KEY | full JID; server `s.whatsapp.net`, `g.us` or `lid`; ≤ 256 B |
| `phone` | TEXT NOT NULL DEFAULT '' | digits of the user part when the server is `s.whatsapp.net`, otherwise `''`; set by the store from `jid` |
| `name` | TEXT NOT NULL DEFAULT '' | ≤ 1 KiB; original text |
| `name_fold` | TEXT NOT NULL DEFAULT '' | `fold(name)` (R7) |
| `name_quality` | INTEGER NOT NULL DEFAULT 0 | 0 = none, 1 = fallback, 2 = real (push, business, group subject, history-sync display name), 3 = saved (address-book full or first name) |
| `name_time` | INTEGER | Unix ms when the name was seen; `NULL` while `name_quality = 0` |
| `last_message_time` | INTEGER | Unix ms; `NULL` = no activity known |

Indexes: `chats_last_activity ((last_message_time IS NULL), last_message_time DESC, jid)`,
`chats_name ((name_quality = 0), name_fold, jid)`, `chats_phone (phone)`. The first two are on the exact `ORDER BY`
expressions of the two chat sorts, so listing chats needs no sort step.

No index is partial. SQLite uses a partial index for `col = ?` only when it can prove from the query that the bound
value satisfies the index's condition, which it cannot for a parameter, so a partial index would leave those
lookups scanning the table.

Whether a chat is a group follows from the JID (`g.us`); it is not stored.

### Name rule (FR-018)

An offered name `(name, quality, time)` replaces the stored one when the name is non-empty and
`(quality, time, name)` is greater than `(name_quality, name_time, name)` in row-value order. Quality is compared
first, then time, then the names byte-wise; a stored `NULL` time (no name yet) compares lowest. The result is a
total order, so it never depends on which offer
arrives first (SC-003). Empty names are rejected before the comparison. An offer without a time, or with one
outside [2000-01-01, 2100-01-01), is a validation error.

### Last activity rule (FR-016, FR-017)

`last_message_time = max(last_message_time, offered)`; `NULL` offered never changes it.

## `messages`

| Column | Type | Rules |
| --- | --- | --- |
| `chat_jid` | TEXT NOT NULL, FK → `chats(jid)` | part of the key |
| `id` | TEXT NOT NULL | 1–256 B, valid UTF-8, no control characters; opaque |
| `sender_jid` | TEXT NOT NULL | full JID without the device part; server `s.whatsapp.net` or `lid`; ≤ 256 B |
| `sender_phone` | TEXT NOT NULL DEFAULT '' | digits of `sender_jid`'s user part for `s.whatsapp.net`, else `''`; derived by the store (FR-014) |
| `time` | INTEGER NOT NULL | Unix ms in [2000-01-01, 2100-01-01) |
| `from_me` | INTEGER NOT NULL | 0 or 1 |
| `text` | TEXT NOT NULL DEFAULT '' | ≤ 256 KiB |
| `text_fold` | TEXT NOT NULL DEFAULT '' | `fold(text)` |
| `caption` | TEXT NOT NULL DEFAULT '' | ≤ 256 KiB |
| `caption_fold` | TEXT NOT NULL DEFAULT '' | `fold(caption)` |
| `media_type` | TEXT NOT NULL DEFAULT '' | `''`, `image`, `video`, `audio` or `document` (CHECK) |
| `media_url` | TEXT | ≤ 4 KiB; `NULL` = absent |
| `media_direct_path` | TEXT | ≤ 4 KiB |
| `media_key` | BLOB | exactly 32 B when present |
| `file_sha256` | BLOB | exactly 32 B when present |
| `file_enc_sha256` | BLOB | exactly 32 B when present |
| `file_length` | INTEGER | 0 to 4 GiB when present |
| `mime_type` | TEXT | ≤ 255 B |
| `file_name` | TEXT | ≤ 1 KiB; the original document file name |

Key: `PRIMARY KEY (chat_jid, id)`. Foreign key `chat_jid REFERENCES chats(jid)`, with no cascade: the merge moves
messages explicitly.

Indexes (S3, FR-020):

- `messages_chat_time (chat_jid, time DESC, id DESC)`: chat history, context, latest message per chat
- `messages_sender_time (sender_phone, time DESC, chat_jid DESC, id DESC)`: sender filters, contact chats and last
  interaction by phone number, in the listing order, so a single-identity sender query needs no sort
- `messages_sender_jid (sender_jid, time DESC, chat_jid DESC, id DESC)`: the same three by LID, and the LID merge's
  sender rewrite
- `messages_time (time DESC, chat_jid DESC, id DESC)`: listing across chats, `after`/`before`, in exactly the
  listing's order, so no sort step is needed

- `messages_id (id)`: a lookup by message ID alone (FR-022), which the `(chat_jid, id)` key cannot serve
- `messages_chat_sender_phone (chat_jid, sender_phone, time DESC, id DESC)` and
  `messages_chat_sender_jid (chat_jid, sender_jid, time DESC, id DESC)`: a listing filtered by both a chat and a
  sender, which would otherwise read every row of the chat (owner's decision, sixth design review; they add two
  index writes per stored message). They also serve the per-chat `EXISTS` probes of `ContactChats`

A sender under both identities is never matched with one `OR`: SQLite plans `sender_phone = ? OR sender_jid = ?` as a
`MULTI-INDEX OR`, which collects the row IDs of every message of the contact and sorts them, and with a chat filter
as a search on the chat alone. Every sender query is instead a `UNION ALL` of a phone branch and a LID branch, each on
its own index, with `ORDER BY`, `LIMIT` and `OFFSET` on the compound (owner's decision, 2026-10-30). Its plan is
`MERGE (UNION ALL)` over `SEARCH messages USING COVERING INDEX messages_sender_time (sender_phone=?)` and
`... messages_sender_jid (sender_jid=?)`, or the two chat-and-sender indexes with a chat filter. A time filter adds
`AND time<?` or `time>?` to each search, and search text makes the index non-covering (`USING INDEX`), since the
folded text is read from the row. A contact with one identity runs only its branch. A plan with
`MULTI-INDEX OR`, or with a `SEARCH` on `messages_chat_time (chat_jid=?)` for a chat-and-sender listing, is rejected.

A query plan test (`EXPLAIN QUERY PLAN`) holds the expected plan of every query in FR-021–FR-033 and fails on any
other. Every lookup by key, chat, sender or contact is a `SEARCH` on one of these indexes. A text search, and a
listing with no chat or sender filter, walk `messages_time` newest first (`SCAN messages USING INDEX messages_time`)
and stop once the page is full. Context queries bind the hit's position as a row value, `(time, id) < (?, ?)` or
`> (?, ?)`, so their plan is `SEARCH messages USING INDEX messages_chat_time (chat_jid=? AND (time,id)<(?,?))`; a
plan with only `(chat_jid=?)` walks the chat from one end and is rejected. Joins to `whatsmeow_lid_map` always put
the map's key on its own side (`m.lid = <expression on the contact>`, `m.pn = ...`), so the plan is a `SEARCH` on
the map's key; a `SCAN m` is rejected. A temporary b-tree is expected only in the places this table lists, and never holds full
message rows:

| Query | Expected sort | Why |
| --- | --- | --- |
| `SearchContacts` | `USE TEMP B-TREE FOR GROUP BY` and for its `ORDER BY` | one row per person, built from both of the person's entries before the text is matched; holds contact rows, bounded by the device store's contact count |

No message query has a temporary b-tree: every listing, including a sender listing under both identities, walks
its indexes in the listing order. `ContactChats` walks `SCAN chats USING COVERING INDEX chats_last_activity` with
two correlated `SEARCH`es per chat, on `messages_chat_sender_phone (chat_jid=? AND sender_phone=?)` and
`messages_chat_sender_jid (chat_jid=? AND sender_jid=?)`, and has none either.

The order of every list is deterministic: messages by `(time, chat_jid, id)`, newest first; chats by `(last_message_time, jid)`
or `(name_fold, jid)`. Rows without a name or time come last.

### Re-store rule (FR-015)

On `(chat_jid, id)` conflict every column takes the new value, except the eight media metadata columns. Each of
those keeps its stored value when the new one is `NULL`; an empty string or empty blob counts as `NULL`. The chat's
last activity follows the max rule above.

### Validation (FR-042)

Validation happens before any SQL runs. The store rejects:

- empty or malformed identifiers and servers outside the allow-lists;
- invalid UTF-8 and control characters in identifiers;
- any field over its limit;
- a media type outside the list;
- media keys or hashes not exactly 32 B;
- a negative file length or one over 4 GiB;
- a time outside the range.

Text, caption and names may contain any valid UTF-8, control characters included. They are stored unchanged and
escaped only when they appear in diagnostics.

## Derived values

| Value | Definition |
| --- | --- |
| Sender name (FR-024) | For `from_me = 0`: the device-store contact for `sender_jid`, then for its counterpart through `whatsmeow_lid_map` (user part looked up, suffix added) (`full_name`, then `first_name`, `push_name`, `business_name`), else the saved or real name (`name_quality >= 2`) of the chat whose `jid = sender_jid`, else `''`. For `from_me = 1`: `''` (the tools render "Me"). |
| Chat name on a message | `chats.name` of `chat_jid` |
| Latest message of a chat | the first row of `messages_chat_time` for that chat |
| Contact identity (FR-028) | the device's `whatsmeow_contacts` rows, joined with `whatsmeow_lid_map (lid, pn)`. A LID row with a mapped phone and the phone row are one person, keyed by the phone JID. The name comes from the phone row, else from the LID row, each in the order full, first, push, business. Groups (`g.us`) are excluded. |
| Resolved contact (FR-035) | the input as a phone number, phone JID or LID JID, plus its counterpart from `whatsmeow_lid_map` (`pn = <digits>` gives `lid`, `lid = <user part>` gives `pn`): `{phone, phoneJID, lidJID}`, where `phoneJID = phone || '@s.whatsapp.net'` and `lidJID = lid || '@lid'`; any may be empty but not all. Message matches use `sender_phone = phone OR sender_jid = lidJID`. The direct chats are `jid IN (phoneJID, lidJID)`: both exist only before a merge. `DirectChat` returns the phone chat, else the `@lid` chat. |
| Current device | the single row of `whatsmeow_device`. If there is none (never paired, or logged out), contact queries return no rows and sender names fall back to chat names. Its own number and LID are the user parts of `jid` and `lid` without a device part; `lid` can be `NULL` or `''`, and an empty own value never equals anything, so an unmapped contact's empty LID is never taken for the account. |

## State transitions

```text
missing ──create (private file)──▶ empty ──device upgrade──▶ device@16 ──migration 1..N──▶ ready
   any ──preflight refusal──▶ refused (no file changed)
   device@k ──device step fails──▶ device@k (step rolled back), no message migration, startup fails
   messages@k ──migration k+1 fails──▶ messages@k, startup fails, retry resumes from k
ready ──Close──▶ closed (WAL checkpointed by SQLite on last close)
```

A LID moves through the merge as follows, in one transaction: every message with `sender_jid = L`, in any chat,
gets `sender_jid = P` and the phone filled in; then, if chat `L` exists, `P` takes every message and `L` is gone.
`L`'s name is offered to `P` by the name rule only when its quality is saved or real; a fallback is dropped.
Repeating the merge once nothing is left under `L` does nothing.
