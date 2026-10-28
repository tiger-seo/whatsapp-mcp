# Contract: `internal/store` API

The operations features 003–008 call. This is the interface, not the implementation. Names are Go identifiers;
"page" means `Page{Limit, Offset int}`. Limits are the approved storage acceptance profile in
[spec.md](../spec.md#storage-acceptance-profile-approved-2026-10-28), defined once as constants in
`internal/store/limits.go`.

## Lifecycle

```go
// Open checks, creates if missing, preflights, upgrades and opens <dataDir>/whatsapp.db. The caller holds the
// data-directory lock. ctx bounds the whole startup (the caller passes the 10-minute startup budget).
func Open(ctx context.Context, dataDir string, logger *slog.Logger) (*Store, error)

// Close stops admitting operations, waits up to 2 s for running ones, cancels whatever is still running, waits
// up to 1 s more for them to release, then closes both pools. It is safe to call twice. ctx can shorten every
// wait. Any call made during or after Close returns ErrClosed.
func (s *Store) Close(ctx context.Context) error

// Device returns whatsmeow's device-store container, sharing the store's file and writer connection.
// Feature 003 builds the WhatsApp client from it. It is the only handle on the database outside this package.
func (s *Store) Device() *sqlstore.Container
```

`Open` order: path-form check → artifact checks, recording each file's identity (R6) → create or header check →
read-only preflight (R4) → register `mcp_fold` → writer pool → artifact re-check, same identities → reader pool →
device upgrade → message migrations → ready. On any failure, everything opened so far is closed before it
returns.

## Writes

```go
type Message struct {
    ChatJID   string    // full JID: s.whatsapp.net, g.us or lid
    ID        string
    SenderJID string    // s.whatsapp.net or lid; a device part (":12") is stripped
    Time      time.Time
    FromMe    bool
    Text      string
    Caption   string
    MediaType string    // "", "image", "video", "audio", "document"
    Media     MediaMeta // zero value = no media metadata
}

type MediaMeta struct {
    URL, DirectPath        string
    MediaKey               []byte // 32 B when present
    FileSHA256, FileEncSHA256 []byte // 32 B when present
    FileLength             *int64 // nil = absent
    MIMEType, FileName     string
}

// NameNone (0), NameFallback (1), NameReal (2: push, business, group subject, history-sync display name),
// NameSaved (3: address-book full or first name). Ingest chooses the quality of what it offers.
type NameQuality int

type ChatUpdate struct {
    JID          string
    Name         string      // "" = no name offered
    NameQuality  NameQuality // required when Name != ""
    NameTime     time.Time   // required when Name != ""
    LastActivity time.Time   // zero = not offered
}

// StoreMessages stores 1..1,000 messages (≤ 16 MiB of field bytes) in one transaction. Chats are created as
// needed and their last activity moves forward. All or nothing.
func (s *Store) StoreMessages(ctx context.Context, msgs []Message) error

// UpdateChats applies 1..1,000 chat updates in one transaction (name rule and last-activity rule).
func (s *Store) UpdateChats(ctx context.Context, updates []ChatUpdate) error

// MergeLID merges everything stored under lidJID into phoneJID in one transaction: the sender of every message
// sent as lidJID, in any chat, and the chat lidJID if it exists. Nothing left under lidJID is a no-op.
func (s *Store) MergeLID(ctx context.Context, lidJID, phoneJID string) error
```

Single-item helpers (`StoreMessage`, `UpdateChat`) wrap the batch forms.

## Reads

```go
type MessageView struct {
    ID, ChatJID, ChatName                string
    SenderJID, SenderPhone, SenderName   string
    Time                                 time.Time
    FromMe                               bool
    Text, Caption, MediaType             string
}

type ChatView struct {
    JID, Name    string
    IsGroup      bool
    LastActivity *time.Time   // nil = none
    Latest       *MessageView // nil when not requested or the chat has no messages
}

type Contact struct{ JID, Phone, Name string }

type ChatSort int // ByLastActivity (newest first), ByName (ascending, unnamed last)

// A contact argument is a phone number (any NormalizePhone form), a phone JID or a LID JID. The store resolves it
// through the current device's LID map and matches either identity.
func (s *Store) ListChats(ctx context.Context, query string, sort ChatSort, withLatest bool, p Page) ([]ChatView, error)
func (s *Store) GetChat(ctx context.Context, jid string) (ChatView, error)          // with Latest
func (s *Store) DirectChat(ctx context.Context, contact string) (ChatView, error)   // exact phone, else the @lid chat
// ContactChats and LastInteraction cover both direct chats (phone and @lid) while both exist, before a merge.
func (s *Store) ContactChats(ctx context.Context, contact string, p Page) ([]ChatView, error)
func (s *Store) SearchContacts(ctx context.Context, query string, p Page) ([]Contact, error)

type MessageFilter struct {
    After, Before time.Time // zero = unset; both strict
    ChatJID       string
    Sender        string    // a contact; "" = unset
    Query         string    // matches text or caption
}

type Context struct{ Before, After int } // 0..20 each

type Hit struct {
    Message       MessageView
    Before, After []MessageView // both oldest first
}

func (s *Store) ListMessages(ctx context.Context, f MessageFilter, c Context, p Page) ([]Hit, error) // newest first
func (s *Store) MessageContext(ctx context.Context, chatJID, id string, c Context) (Hit, error)
func (s *Store) GetMessage(ctx context.Context, chatJID, id string) (MessageView, error)
func (s *Store) GetMessageByID(ctx context.Context, id string) (MessageView, error)
func (s *Store) MediaInfo(ctx context.Context, chatJID, id string) (MessageView, MediaMeta, error)
func (s *Store) LastInteraction(ctx context.Context, contact string) (*MessageView, error) // nil = none
```

`Page.Limit` must be 1–200, and `Page.Offset` 0–100,000. Context rows across all hits must not exceed 2,000, and
result bytes 32 MiB.

## Phone normalisation

```go
// NormalizePhone returns the digits of a phone number written with an optional +, spaces, dashes, dots and
// parentheses, or of an @s.whatsapp.net JID. Anything else is ErrInvalid. Exported so 004–006 use the same rule.
func NormalizePhone(s string) (string, error)
```

## Errors

Every error matches exactly one of these with `errors.Is` or `errors.As`. Error text never contains message
text, captions, keys, SQL or raw driver messages.

| Error | When | Carries |
| --- | --- | --- |
| `*ValidationError` (`ErrInvalid`) | a field fails validation; bad page/context/sort | field name, reason |
| `*NotFoundError` (`ErrNotFound`) | no chat or message | chat JID, message ID |
| `*AmbiguousIDError` | the ID exists in more than one chat | matching chat JIDs (≤ 100; more is `ErrResourceLimit`) |
| `ErrNotMedia` | `MediaInfo` on a message without media | — |
| `ErrResourceLimit` | a batch, result, context or byte budget is exceeded, or SQLite's 256 MiB heap limit is reached | the limit's name (`memory` for the heap) |
| `ErrBusy` | 64 operations already waiting, or the 5 s admission wait expired (including the wait for one of the 2 deep sorted listing slots) | — |
| `ErrClosed` | the call was made during or after `Close` | — |
| `ErrDiskLow` | free space below the reserve | free bytes, reserve |
| `ErrIndeterminate` | the commit outcome cannot be established | — |
| `*SchemaError` (`ErrSchemaTooNew`, `ErrSchemaInvalid`) | preflight refusal | schema (`messages`/`device`), observed, supported |
| `*MigrationError` | a migration or device-upgrade step failed | schema, failed step, completed steps |
| `*StorageError` (`ErrUnsafeStorage`, `ErrNotDatabase`, `ErrIO`) | unsafe artifact or path form, not a database, I/O or disk-full | artifact (escaped, bounded), `private` fix text, code |

Context cancellation returns the caller's `ctx.Err()` (wrapped), except during commit (`ErrIndeterminate`).
`MergeLID` rejects `lidJID == phoneJID`, a source that is not `@lid` and a target that is not
`@s.whatsapp.net` with `*ValidationError`. A contact argument that is none of the three accepted forms is a
`*ValidationError` naming the field.

## Concurrency

All methods are safe for concurrent use. At most 16 run at once and 64 wait (R9). Each runs under the earlier of
the caller's deadline and 10 s, and is cancelled by `Close` after its grace period.
