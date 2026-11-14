# Data Model: Message Ingest

No schema change. Messages and chats use feature 002's tables, rules and limits
([store data model](../002-sqlite-storage/data-model.md), [store API](../002-sqlite-storage/contracts/store-api.md)).
This document lists what ingest derives from WhatsApp events, the in-memory state it keeps per generation, and the
new read-only store helpers. Numbers marked *proposed* are settled in the design review ([research.md](research.md)).

## Stored entities (existing)

### Message (`store.Message`)

| Field | Source, live (`events.Message`) and history (`ParseWebMessage`) | Rule |
| --- | --- | --- |
| `ChatJID` | `Info.Chat`, normalised (R7) | phone, group or LID JID; no device part; never invented |
| `ID` | `Info.ID` | non-empty, at most 256 bytes; identity is `(ChatJID, ID)` |
| `SenderJID` | own message: the account's phone JID; group: `Info.Sender` (history: participant); direct: `Info.Sender`, normalised | device part dropped; an absent or invalid sender rejects the message |
| `Time` | `Info.Timestamp` | 2000-01-01 to 2100-01-01 UTC; zero or outside rejects the message |
| `FromMe` | `Info.IsFromMe` | |
| `Text` | `Conversation`, else `ExtendedTextMessage.Text` | at most 256 KiB, valid UTF-8 |
| `Caption` | image, video or document caption | at most 256 KiB; never copied into `Text` |
| `MediaType` | `image`, `video`, `audio`, `document`, or empty | audio has no caption |
| `Media` | URL, direct path, media key, file SHA-256, encrypted SHA-256, length, MIME type, document file name | store limits; 32-byte keys and hashes |

`SenderPhone` is derived by the store from a phone `SenderJID`; it stays empty for an unmapped LID.

A message is **eligible** when its chat is a user, group or LID JID, it has text or a recognised media type, it is
not an edit (`IsEdit`), its raw content is not a bare `ProtocolMessage`, and, in history, `ParseWebMessage` kept the
ID of `webMsg.Key.ID`. Every other message, including status broadcasts and newsletters, is ignored without a
write. An eligible message that fails `store.ValidateMessage` is **rejected**: skipped,
counted, never repaired.

### Chat (`store.ChatUpdate`)

| Field | Source | Rule |
| --- | --- | --- |
| `JID` | normalised chat JID | |
| `Name`, `NameQuality`, `NameTime` | R12 order: history name, stored name, contact table, push name, group lookup, fallback | feature 002 quality, time and tie-break rule; never downgraded |
| `LastActivity` | set by `StoreMessages` from message times | only moves forward (S4) |

### Identity mapping (`whatsmeow_lid_map`, device-owned, read only here)

| Field | Meaning |
| --- | --- |
| `lid` | user part of the `@lid` JID (key) |
| `pn` | user part of the phone JID |

Ingest never writes this table; the library does. A mapping may change (R8): merges are not undone.

## New store helpers (read only, no schema change)

```go
// ValidateMessage applies exactly the checks StoreMessages applies to one message. It never repeats field values.
func ValidateMessage(m Message) error

// MessageFieldBytes is the size StoreMessages charges against its 16 MiB batch limit for m.
func MessageFieldBytes(m Message) int

// PhoneForLID returns the phone JID mapped to lidJID in the LID map. The map has no device column; it is the one
// account's map, since a data directory holds one account. ok is false when there is no mapping. It reads only;
// ctx bounds it (ingest passes 2 s).
func (s *Store) PhoneForLID(ctx context.Context, lidJID string) (phoneJID string, ok bool, err error)

// MappedLIDsWithData returns up to limit (1..200) mappings whose LID is greater than afterLID, in key order, and
// that have a chat or a message sender stored under the LID.
func (s *Store) MappedLIDsWithData(ctx context.Context, afterLID string, limit int) ([]LIDMapping, error)

type LIDMapping struct{ LIDJID, PhoneJID string }

// ContactNames returns the device contact table's names for jid: saved (full, else first) and real (push, else
// business). Empty strings when absent.
func (s *Store) ContactNames(ctx context.Context, jid string) (saved, real string, err error)

// ChatName returns the stored name of a chat with its quality and observation time, so ingest can tell a better
// offer from a worse one without writing. ok is false when the chat is missing or has no name (quality none).
func (s *Store) ChatName(ctx context.Context, jid string) (name string, quality NameQuality, at time.Time, ok bool, err error)
```

All are ordinary store operations: admission, the 10 s limit, `ErrClosed` after `Close`, typed errors.

## Per-generation ingest state (in memory)

| State | Bound | On overflow |
| --- | --- | --- |
| History queue | 8 notifications (*proposed*) | the notification is reported as incomplete history; no disconnect |
| History worker | 1 goroutine; one decoded history at a time (≤ 128 MiB expanded, fork limit) | – |
| History flush | 250 messages or 4 MiB of field bytes (*proposed*), ingest lock released between flushes | the batch is cut early |
| Name cache | 4,096 chats, least recently used (*proposed*) | the oldest entry is forgotten; the next miss reads the store again |
| Group lookup set | 64 distinct groups (*proposed*); 1 worker; 10 s per lookup (*proposed*) | the request is dropped and counted; the fallback name stays |
| Failed-lookup cache | 1,024 groups (*proposed*), entries expire after 10 min (*proposed*) | the oldest entry is forgotten; that group may be asked again sooner |
| LID hint set | 1,024 LIDs (*proposed*); 1 worker; 5 attempts 1 s apart (*proposed*) | the hint is dropped and counted; startup reconciles it |
| Reconcile cursor | one LID: where an unfinished startup pass stopped; the worker pages on from it, 200 per page, between hints | – |
| Ingest lock | one per generation | – (serialises message writes, history batches and merges) |
| Counters | `stored`, `ignored`, `rejected`, `unavailable`, `droppedMessages`, `historyIncomplete`, `lookupsDropped`, `hintsDropped`, `merged`, `mergeFailed` | `uint64`, logged at stop and on terminal failure |
| Re-lookup under the ingest lock | one 2 s deadline per message write or history flush, at most 250 distinct LIDs | the remaining LIDs are written as LIDs; startup merges them |

## Ingest state transitions (one generation)

```text
running ──(message-store error)──────────────▶ failed  (terminal: no acks, workers stop, owner signalled)
running ──(retire bit, step 2)───────────────▶ retiring (no acks, drops counted, workers stop at checkpoints)
failed  ──(retire bit)───────────────────────▶ retiring
retiring ──(workers and callbacks joined)────▶ stopped
```

`failed` and `retiring` never return to `running`. A new generation starts in `running` with empty state.

## Reports

| Event (`event` key) | Level | Fields |
| --- | --- | --- |
| `ingest_failed` | warn | operation (`live`, `history`, `name`), error class, chat JID and message ID escaped, or history chunk order and sync type; dropped count. Terminal. |
| `history_incomplete` | warn | reason (`queue_full`, `download`, `too_large`, `parse`, `stopped`), chunk order, sync type. A store failure during history is `ingest_failed` with operation `history`, which also carries the messages committed from the chunk. |
| `lid_merge_failed` | warn | LID and phone JID escaped, error class. Not terminal; retried at the next startup. |
| `receive_overload` | warn | reason from the fork, "messages may be missing" |
| `lid_reconcile` | info | merged, failed, skipped, completed or `budget_exhausted` |
| `lid_reassigned` | info | LID, old and new phone JID, escaped |
| `ingest_stopped` | info | the counters above, including `unavailable` (placeholders not fetched) |
| message received | debug | text and caption appear only here |
