# Contract: Message Ingest

Internal contract between the session adapter (`internal/wa/session.go`), a new ingest component
(`internal/wa/ingest.go` and helpers), the connection owner (`internal/wa/owner.go`), the store (`internal/store`)
and the connected commands (`cmd/whatsapp-mcp`). It adds to
[feature 004's quiet-stop contract](../../004-shutdown-drain/contracts/quiet-stop.md) and changes only its event-count
rule (below). Decisions and their reasons are in [research.md](../research.md); state and bounds in
[data-model.md](../data-model.md).

## Client configuration

Set in `OpenSession`, in addition to feature 004's settings, which stay unchanged:

```go
client.SynchronousAck = true            // acknowledge a message only after every handler succeeded
client.ManualHistorySyncDownload = true // unchanged; the history worker downloads
// DisableManualHistorySyncReceipt stays false
s.handlerID = client.AddEventHandlerWithSuccessStatus(s.handleEvent) // returns bool
```

`EnableAutoReconnect`, `InitialAutoReconnect`, `DisableLoginAutoReconnect`, `AutomaticMessageRerequestFromPhone`
(false) and the fenced HTTP clients are unchanged. Everything `SynchronousAck` moves onto the handler is listed in
plan.md, "What `SynchronousAck` changes in the pin"; with the pin `bedcf01`, an undecryptable message from another
account causes a retry receipt and an acknowledgement, an `unavailable` placeholder causes an acknowledgement, and
neither sends a message to the phone.

## Ingest component

```go
type IngestConfig struct {
    Store   *store.Store
    Client  ingestClient             // narrow: ParseWebMessage, DownloadHistorySync, GetGroupInfo, own JID
    Log     *slog.Logger
    Lib     context.Context          // derived from gen.lib: every library call the workers make
    Live    func(store.Message)      // after a live write committed; nil in this feature
    Failed  func()                   // called once when ingest becomes terminal
}

func newIngest(cfg IngestConfig) *ingest

// HandleMessage stores one live message, or queues a history notification. It returns false when a message that
// should have been stored was not (the library then sends no acknowledgement).
func (in *ingest) HandleMessage(evt *events.Message) bool

// NoteMapping reports a LID whose mapping the device store just changed: a new row, or a different phone. An
// unchanged upsert is not reported. oldPhoneJID is "" for a new row. Never blocks.
func (in *ingest) NoteMapping(lidJID, oldPhoneJID, newPhoneJID string)

// Stop tells the workers to stop at their next checkpoint and refuses new work. Idempotent; never blocks.
func (in *ingest) Stop()

// Wait waits for the workers to return, up to ctx.
func (in *ingest) Wait(ctx context.Context) error

func (in *ingest) Counters() IngestCounters
```

`ingestClient` is an interface so tests drive the component with a fake, without a network (Principle VI).

### HandleMessage

1. Retiring or failed: count `droppedMessages`, return `false`.
2. A history notification from the account's own device: enqueue it (queue of 8). Full: report
   `history_incomplete` (`queue_full`). Return `true`, because the notification itself carries no message to store.
3. Not eligible (data-model "eligible": chat server, no text or recognised media, an edit, a bare protocol
   message): count `ignored`, return `true`.
4. Normalise chat and sender (2 s per lookup), build the `store.Message`, and check it with `store.ValidateMessage`.
   Invalid: count `rejected`, return `true`.
5. Under the ingest lock: look up again any chat or sender that is still an unmapped LID (one 2 s deadline for the
   message; a LID not resolved in time stays a LID), then `StoreMessage`. On error: become terminal (below) and return
   `false`.
6. Offer the chat name (R12): cached comparison first (a miss reads `ChatName` once), at most one `UpdateChat` when the offer is better; a group
   without a usable name goes to the lookup set. A failed name write is terminal like a failed message write
   (`ingest_failed` with operation `name`), but the message itself is already stored, so return `true`.
   *Late contact names*: the contact table is read once per direct chat when the chat is first named, but the device's
   contact sync can store saved names after that (about a second after connecting is too early). A direct chat whose first
   read found no saved name is queued for a re-read 60 s after that read; after every live message and every history flush
   (outside the ingest lock, on the calling goroutine, no new goroutine) up to 8 due chats are re-read, and a saved name
   (else a real one) better than the known name is offered through the same `ExistingOnly` update. A chat still without a
   saved name is queued again at 5 min and at 30 min after the previous read, then never again in the generation. A chat no
   longer in the name cache, or already at the saved quality, is dropped. A failed name write is terminal as above.
7. Call `Live` if set. Return `true`.

### History worker

For each queued notification: `DownloadHistorySync(lib, notif, true)`; then for each conversation, in order:
normalise the chat; offer the conversation name; for each message `ParseWebMessage`, skip it as not eligible when
the parsed ID differs from `webMsg.Key.ID`, then steps 3–4 above; add to the batch; flush at 250 messages or 4 MiB (`MessageFieldBytes`). A flush is one `StoreMessages` and one `UpdateChats`
under the ingest lock, after the same re-lookup of the flush's distinct unmapped LIDs under one 2 s deadline, and
the lock is released
between flushes so a waiting live write goes first. After each flush, outside the lock, every distinct chat of the
batch is named as a live message would name it (step 6: stored name read once, contact table once and re-read later as
described there, best better name, a group without a name gets `Group <id>` and a lookup request), and the offers of the batch go to the store in
one `UpdateChats`; a failure is terminal with operation `name`. At `Stop`, every notification still queued is reported as
`history_incomplete` (`stopped`). Checkpoints (where `Stop` is observed): before each download, between conversations, before
each flush. A store error is terminal and reports the chunk and the messages committed from it. History never calls
`Live`.

### Group-name and reconcile workers

The group-name worker takes one group at a time: `GetGroupInfo(lib with 10 s deadline)`, then `UpdateChat` with the
subject. A failure is cached for 10 minutes. The reconcile worker takes one hint at a time: `PhoneForLID`; visible:
`MergeLID` under the ingest lock; not yet visible: retry up to 5 times, 1 s apart, observing `Stop` between
attempts. A failed merge logs `lid_merge_failed`. When the startup pass left a cursor,
the worker also pages on from it with `MappedLIDsWithData` (200 per page), one merge at a time, between hints, until
the table is done. A failed merge is logged and left
for the next startup; it is not terminal (both sides stay intact). Checkpoint: between items.

### Terminal failure

Once: set `failed`, call `Stop`, log `ingest_failed`, call `Failed`. Later message events are dropped as in step 1.
A context error caused by this generation's own retirement is not a failure.

## Session adapter

- Creates the ingest component in `OpenSession`, with `Lib` derived from the library context, and wires the device
  LID store wrapper's mapping changes to `NoteMapping`.
- A history notification event that arrives after the retire bit is reported as `history_incomplete` (`stopped`) as
  well as counted in `droppedMessages`.
- `*events.UndecryptableMessage` with `IsUnavailable` is counted `unavailable` and returns `true` (the library then
  acknowledges the placeholder and, with `AutomaticMessageRerequestFromPhone` false, sends nothing to the phone).
  Other undecryptable messages return `true` and keep the library's retry receipt.
- `handleEvent` returns `bool`. Owner events keep their mapping to `Dispatch` and return `true`. `*events.Message`
  goes to `HandleMessage`, and its result is returned. While the retire bit is set, a message event is counted in
  `droppedMessages` and returns `false`; other events keep feature 004's counting.
- On `*events.Disconnected` it reads `client.LatestReceiveQueueOverload()`; a record logs `receive_overload` once and
  dispatches `SessionReceiveOverload` before `SessionDisconnected`.
- `IngestFailed() <-chan struct{}` is closed by the `Failed` callback.

## Owner

- Retirement step 2 also stops ingest admission (through the retire bit). Step 4 calls `ingest.Stop()`. Step 5 waits
  `ingest.Wait` for up to 2 s beside the repair pass. Whatever is still running is joined after step 8, before step 9,
  together with the handler callbacks. No step moves.
- `Health()` gains `ReceiveOverload string` (the fork's reason, empty when none) and `IngestFailed bool`.
- `Terminal() <-chan struct{}` is closed on an ingest failure. A device storage fault keeps feature 003's behaviour
  (backlog `005-device-fault-command-wait.md`).
- `QuietStopResult` gains `DroppedMessages uint64`. It is reported, and does not fail the check.

### Revised event-count rule

Feature 004's rule counts owner-relevant events dropped after the retire bit. A message event dropped after the retire
bit is **not** counted there: it holds no session state and this process did not start it. It is counted in
`DroppedMessages`, left unacknowledged, and reported as a possible gap. Every other part of the rule stands.

## Connected commands (`serve`, `stdio`)

- Before the owner opens a generation: run the startup LID reconciliation (R8) under the remaining startup budget and
  log `lid_reconcile`. Its failure is reported and never stops the command. A pass stopped by the budget passes its
  cursor to the first generation's reconcile worker.
- Wait on the first of the lifecycle's stop and `owner.Terminal()`. A terminal signal requests the normal stop. After
  a clean stop the exit code is 1 and stderr carries
  `whatsapp-mcp: message storage failed; messages may be missing. Fix the cause and start again.`
  stdout stays empty in `stdio`.
- `pair` ingests through the same session code while its generations are connected; this is where WhatsApp sends
  the initial history. It does not wait longer than it does today: a history notification still queued or arriving
  after its retire bit is reported as `history_incomplete`, because the library has already sent its receipt. The
  owner accepted this gap on 2026-11-13 (spec clarification); a bounded wait is in the backlog. `pair` also waits on
  `owner.Terminal()`: an ingest failure prints the storage message after the link outcome, and `pair` exits 1.
- `status` reports nothing new offline: overload and ingest failure exist only in a running process and its log.

## Invariants

- N1: A message is acknowledged only if its write committed, or it was not eligible or was rejected as invalid.
- N2: No message, history batch or merge is written after its generation's ingest component was joined.
- N3: Every message write, history batch and merge of a generation is serialised by its ingest lock.
- N4: No message record has a time, sender, chat or ID that WhatsApp did not supply (the own JID for own messages
  excepted).
- N5: No application-owned queue, set, cache or worker of this feature exceeds its bound in
  [data-model.md](../data-model.md).
- N6: Every loss or doubt is reported (`ingest_failed`, `history_incomplete`, `receive_overload`,
  `lid_merge_failed`, `DroppedMessages`, the `unavailable` count in `ingest_stopped`); none is reported as success.
- N7: Feature 004's invariants I1–I9 hold unchanged.
