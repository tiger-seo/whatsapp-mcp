# Research: Message Ingest

**Status**: Phase 0 complete on 2026-11-13. Every unknown in the plan's Technical Context is settled below. The
appendix keeps the investigation of 2026-11-10 to 2026-11-12, which was made under the earlier reading of Principle V;
where it disagrees with a decision below, the decision wins.

Each entry gives the decision, the reason and the alternatives that were rejected. Numeric limits marked
**proposed** are application limits that this design sets. The owner accepts or changes them in the design review;
the dependency limits were approved separately (R1).

## R1. Dependency revision

**Decision**: Pin `go.mau.fi/whatsmeow` through `replace go.mau.fi/whatsmeow => github.com/tiger-seo/whatsmeow
v0.0.0-20261113124132-bedcf0185245`, which is branch `receive-bounds` at `bedcf01`, pushed to the owner's fork on
2026-11-13. It was first pinned at `9209108`. The first design review found that `SynchronousAck` sent a phone
re-request for any undecryptable message, and the owner approved `d83e19e`, which gates it (plan, contract item 8);
the second found the same for the `unavailable` placeholder another account causes with a view-once message, and the
owner approved `bedcf01` (contract item 9). The `require` line keeps upstream `f1b798561919`, the base the fork was reviewed against. The owner approved
this revision and its limits on 2026-11-13: 16 MiB per frame after expansion, 64 nesting levels, 65,536 nodes per
frame, 2,048 queued handler items, 4,096 retry counters and 128 MiB per history sync after decompression. None has
been compared with real traffic.

**Rationale**: The constitution requires an exact version verified against `go.sum`; a directory replacement is
neither. The fork has twelve commits over `f1b7985` (`git rev-list --count f1b7985..bedcf01`): the Fork
Acceptance Contract's `be96244`, `54a9501`, `8e61d2a`, `6cabab6`, `a654373`, `9209108`, `d83e19e` and `bedcf01`; three
defect fixes outside the gate, `4a01445`, `8f99711` and `01da9e4`; and `c05eb84` (the change proposed upstream as
tulir/whatsmeow#1337, identical to `ce09515`). The pin therefore also carries the
fix for backlog item `004-whatsmeow-lost-restart-request.md`.

**Alternatives**: Keep the local directory (not immutable, rejected by the owner). Wait for upstream (no receive
bounds upstream as of 2026-11-12). Extend E003-RX-01 (not needed once the bounds are enforced; the owner retires it,
R2).

## R2. E003-RX-01 and the accepted library growth

**Decision**: E003-RX-01 is retired by the commit that pins the fork; backlog item
`003-whatsmeow-receive-resource-bounds.md` closes with it. Four library maps keyed by account or group
(`tcTokenSenderTS`, `groupCache`, `userDevicesCache`, `sessionRecreateHistory`) never shrink; the owner accepted this
growth on 2026-11-13 because it scales with the number of distinct accounts and groups, not messages. A backlog item
tracks it.

**Rationale**: The exception deferred exactly the bounds the fork now enforces. Principle X allows no silent
renewal; retirement is the owner's recorded decision.

## R3. Where live ingest runs

**Decision**: Live messages are stored synchronously inside the session's event handler, on the library's single
handler goroutine. The handler is registered with `AddEventHandlerWithSuccessStatus` and returns `false` when a
message it must store was not stored. The client is configured with `SynchronousAck = true`.

**Rationale**: With `SynchronousAck`, the library acknowledges a message only after every handler returned success
(`message.go`, `receipt.go`, `notification.go`, `retry.go` in the pin). A message whose write failed is therefore not
acknowledged. The single handler goroutine gives one ordering for every live write. A slow store backs up the fork's
bounded queue (2,048 items), whose overflow closes the connection with a record, which is the fail-closed overload
behaviour the owner chose. No application queue is added in front of it, so there is nothing more to bound.

**What else moves inline**: `SynchronousAck` affects more than the message acknowledgement. The plan lists every
site ("What `SynchronousAck` changes in the pin"): retry receipts, the `unavailable` placeholder request and
`server_sync` app-state fetches also run on the handler goroutine. The design reviews found two phone requests among
them that another account could cause; `d83e19e` and `bedcf01` gate both, so with this configuration nothing is sent
to the user's phone and the handler never waits for such a send.

**Consequences**: The library has already decrypted the message and advanced the Signal session before the handler
runs. A message that is not acknowledged is not guaranteed to be delivered again: on redelivery the library may see
an old counter and drop it. A failed write is therefore reported as a possible gap, never as "will be retried".
`SynchronousAck` also moves the per-message acknowledgement and the retry receipt for an undecryptable message from a
goroutine into the handler, so feature 004's quiet-stop measurement is repeated with it (plan, verification).

**Handler latency**: the handler shares the ingest lock with history flushes and run-time merges (R8), so a long
hold stalls live delivery and can fill the fork's queue under ordinary traffic. Two rules keep each hold short:
history flushes are cut at **250 messages or 4 MiB** of field bytes (proposed; well inside the store's 1,000 and
16 MiB), and the lock is released between flushes, so a waiting live write runs before the next one. Run-time merges
are rare (one per newly mapped identity with stored data) and the startup pass runs before any connection. The design
is accepted only if a simulator run of an initial history sync with a concurrent live burst shows no
`receive_overload` and a bounded lock wait for the handler (tasks, "Handler latency under history").

**Later option**: the library's decrypted-event buffer (`EnableDecryptedEventBuffer`; the guarded store already wires
`dev.EventBuffer`) would redeliver the stored plaintext of an unacknowledged message instead of dropping it as an old
counter. It stores plaintext in the database and starts a cleanup goroutine, a privacy and stop-order trade-off, so it
is not used here; it is the candidate if failed writes ever need replay.

**Alternatives**: An application queue and worker (an extra bound, an extra drain and no better guarantee).
Asynchronous acknowledgement (the server is told "delivered" before the write, so a failed write is silent loss).

## R4. History acquisition

**Decision**: Keep `ManualHistorySyncDownload = true`. The handler recognises a history notification (a protocol
message from the account's own device, `Info.IsFromMe`), puts the notification into a per-generation history queue of
**8** entries (proposed), and returns. One history worker per generation takes entries in order and calls
`DownloadHistorySync(ctx, notif, true)` with synchronous storage, so the library's device-store side effects (NCT
salt, LID mappings, push names, message secrets) finish inside the call and leave no detached goroutine. The worker
then converts each conversation through `ParseWebMessage` and stores it in batches (R6, R3). The library's default
history receipt stays on.

The initial history after linking arrives on `pair`'s connection. `pair` keeps its lifetime; what it has not stored
when it stops is reported and lost (owner decision 2026-11-13, spec clarification). A bounded wait in `pair` is in
the backlog (`005-pair-initial-history-wait.md`).

At retirement, every notification still in the queue, and every history notification that arrives after the retire
bit, is reported as `history_incomplete` (reason `stopped`); the library sent its receipt when it arrived.

A full queue, a download error, `ErrHistorySyncTooLarge` and a parse error each report an incomplete history (chunk
order and sync type, never content) and do not close the connection. A message-store failure during history is a
storage failure (R9).

**Rationale**: Downloading and storing history can take tens of seconds per chunk and many chunks follow a pairing.
Doing it in the handler would hold the receive queue and turn a normal initial sync into an overload. The
notification and its inline payload come only from the account's own devices, which Principle V does not treat as
attackers; the queue length is a sanity bound, not a security limit. What other accounts wrote inside the history is
limited by the fork (128 MiB expanded), the media HTTP client (16 MiB, 10 s per response) and the store's per-field
and per-batch limits.

**Alternatives**: Automatic history download (detached storage goroutine, failures only logged; Principle V does not
allow a silent rejection). `DisableManualHistorySyncReceipt` (WhatsApp would keep resending, without a guarantee that
the next attempt succeeds; the receipt does not claim local persistence and is no worse than today).

## R5. One extraction path for live and history

**Decision**: History messages are turned into `*events.Message` with the client's `ParseWebMessage(chatJID, msg)`
and then go through the same extraction as live messages. Extraction keeps: text from `Conversation` or
`ExtendedTextMessage.Text`; image, video, audio and document with caption (image, video, document only), URL, direct
path, media key, both hashes, length, MIME type and the document file name. Events marked `IsEdit`, and every other
content type, are not stored. So is any message whose raw content is a bare `ProtocolMessage`, and, in history,
any message for which `ParseWebMessage` returned an ID different from `webMsg.Key.ID`: for a bare `MESSAGE_EDIT` it
replaces the ID and the content with the edited ones without setting `IsEdit` (`client.go:1172-1175` in the pin),
which would otherwise overwrite the original message. View-once and ephemeral wrappers and `DocumentWithCaptionMessage` are unwrapped by the
library and are stored as their inner type, as the earlier implementation did. A placeholder resend is stored like a
live message. An `unavailable` placeholder (an `UndecryptableMessage` with `IsUnavailable`) has no content; it is
acknowledged, not fetched from the phone (contract item 9), counted `unavailable` and reported in `ingest_stopped`, so
a view-once message from another account is a known, counted absence.

**Rationale**: One path keeps the sender rules identical: `ParseWebMessage` sets the sender from `Key.FromMe`
(own JID), `Key.Participant` or `Participant` (group), else the chat. An edit stored under its own stanza ID would
create a second message with the edited text, which is a new behaviour (Principle IX, §8.2).

**Alternatives**: A separate history extractor (two sender rules to keep in step, the earlier implementation's defect
6).

## R6. Validation and batches

**Decision**: Each candidate message is checked with a new exported `store.ValidateMessage` (the exact rules
`StoreMessages` applies) before it is added to a batch. An invalid message is skipped and counted, never repaired: no
fabricated time, sender or ID. History batches are cut at 1,000 messages or at 16 MiB of field bytes, measured by a
new exported `store.MessageFieldBytes`, whichever comes first; history uses the smaller cut of R3 (250 messages
or 4 MiB). Each batch is one `StoreMessages` call. Chat names from
the history conversation go in one `UpdateChats` call per batch, at most 1,000 updates.

**Rationale**: `StoreMessages` is all-or-nothing; one invalid message would reject 999 valid ones. Exporting the
store's own checks keeps one definition of valid and of size.

## R7. Identity normalisation

**Decision**: Before each write, a chat or sender JID on `@lid` is looked up in the device's LID map with a 2 s
deadline, through a new read-only `Store.PhoneForLID` that reads `whatsmeow_lid_map` directly. A found mapping
replaces the JID and fills the sender phone; not found, an error or the deadline keeps the LID and an empty phone.
Device parts are dropped by the store. The account's own messages use the account's phone JID.

**Rationale**: Reading through the message store keeps ingest off the device barrier, so a lookup at retirement is
neither refused nor counted. The read changes nothing. The library writes the mapping it learns from a message before
it dispatches the event, so a mapped sender is found on its first message.

## R8. LID reconciliation

**Decision**:

- **At startup**, in `serve` and `stdio` after the run guard and before the owner opens a generation, page through
  `whatsmeow_lid_map` in key order, 200 rows per page, with a new `Store.MappedLIDsWithData(ctx, afterLID, limit)`
  that returns only mappings with a chat or a sender stored under the LID. Call `MergeLID` for each. Count merged,
  failed and skipped. The pass stops when the startup budget (10 minutes, shared with the store's open) runs out,
  reports how far it got (`budget_exhausted`) and hands its cursor, the last LID it reached, to the first session's
  reconcile worker, which continues paging from there between hints (owner decision 2026-11-13).
- **During a run**, the device LID store wrapper reports each LID whose mapping it **changed** (a new row, or a
  different phone; an unchanged upsert reports nothing), with the previous phone if there was one, to a per-generation
  hint set
  of **1,024** LIDs (proposed). The set is deduplicated; a full set drops the hint and counts it (the next startup
  covers it). A reconcile worker takes hints, re-reads the mapping with `PhoneForLID` and, when it is visible, merges.
  A hint whose mapping is not yet visible (the write was inside an outer transaction that has not committed) is
  retried up to **5** times, 1 s apart, observing `Stop` between attempts, and then dropped and counted.
- **Ordering**: every message write, history batch and merge takes one per-generation ingest lock. A message whose
  chat or sender is still an unmapped LID after normalisation is looked up again **under the lock**, just before the
  write (for history, once per distinct LID in the flush, at most 250, all under **one** 2 s deadline; a LID not
  re-looked-up in time is written as a LID and merged at the next startup). So a mapping committed and merged between the
  first lookup and the write is seen, and the message is written under the phone JID, never under the LID after its
  merge.

A failed merge leaves both sides intact; it is retried at the next startup. A mapping that later points a LID at a
different phone is not undone: data merged earlier stays where it went, later writes follow the new mapping, and the
reassignment is logged with both JIDs escaped. This matches the earlier implementation, which also merged without
provenance; the owner accepted it on 2026-11-13 (spec clarification).

**Rationale**: Enumerating the mapping table, not only `@lid` chats, covers senders that appear only in groups. The
table grows with contacts, and each page costs 200 index probes. Startup is the guarantee; the run-time merge is a
prompt attempt, as the spec asks.

**Alternatives**: A durable queue of hints (needs its own disk bound and overload rule; startup already covers a lost
hint). Scanning `messages.sender_jid` for `@lid` (walks the whole index when few senders are LIDs).

## R9. Storage failure and the connected command

**Decision**: A message-store error on a live write, a history batch or a merge-triggered write (`ErrDiskLow`,
`ErrResourceLimit`, `ErrBusy`, `ErrIndeterminate`, `*StorageError`, `ErrClosed` or a context error that is not the
generation's own stop) makes ingest **terminal** for the generation:

1. the handler returns `false` (no acknowledgement) and every later message event is dropped unacknowledged and
   counted;
2. the history and reconcile workers stop at their next checkpoint;
3. one `ingest_failed` warning names the operation, the error class, the chat JID and message ID (escaped) or the
   history chunk, and the number of messages dropped since. `ErrIndeterminate` is reported as "may or may not be
   stored";
4. the session signals the owner, and the owner exposes a terminal signal that `serve` and `stdio` wait on beside the
   lifecycle; the command then stops through the normal stop sequence and exits 1 with `whatsapp-mcp: message
   storage failed; messages may be missing. Fix the cause and start again.` on stderr.

The quiet-stop check is unchanged: an ingest failure is not a device-store fault, so a clean stop can still clear the
marker. A device storage fault does not use this signal: making it stop `serve` and `stdio` would change feature
003 behaviour that the spec does not cover, so it is in the backlog (`005-device-fault-command-wait.md`). `pair`
also watches the signal: an ingest failure while it runs prints the same storage message after its link outcome
and makes it exit 1.

**Rationale**: This is the clarified behaviour (stop, report, operator fixes, restart under existing rules). Exit 1
lets a supervisor see the failure.

**Later load**: `ErrBusy` is terminal too, as the clarification says for temporarily unavailable storage. Features
006–007 add MCP reads to the same store, and a read burst could then stop ingest. The backlog item
`005-store-busy-stops-ingest.md` makes this a planning input for feature 006.

## R10. Receive overload

**Decision**: On a `Disconnected` event the session reads `LatestReceiveQueueOverload()` from the client. A record
for this client (one client per generation, so any record is this generation's) logs one `receive_overload` warning
with its reason (`item_limit`, `frame_size_limit`, `decode_limit`) and "messages may be missing", sets
`Health().ReceiveOverload`, and the owner treats the disconnect as it treats any remote disconnect today: no
reconnect. `status` and `serve` show the disconnected state; the operator restarts.

**Rationale**: The fork already closed the connection. Existing feature 003/004 rules deny an ordinary reconnect, so
nothing new is needed to avoid bypassing them, and the record must not be silent.

## R11. Retirement

**Decision**: Ingest follows feature 004's order and adds no new step. In detail:

- The retire bit (step 2) makes the handler drop each later message event, return `false` and count it in a new
  `droppedMessages` counter. The quiet check does not fail on it: the event carries no session state and was not
  started by this process. The stop log reports the count with "messages may be missing". This is the revision of the
  event-count rule that the quiet-stop contract asked feature 005 to make.
- At step 4 the history, group-name and reconcile workers are told to stop. They stop at checkpoints: between
  conversations, between batches, between lookups, between merges. Their library calls run under a context derived
  from `gen.lib`, never `gen.work`, so step 4 cannot cancel a device-store write half-way.
- At step 5 the owner waits up to **2 s** (proposed) for the workers beside the repair pass. A worker still inside a
  library call is left to the fence and the barrier, and is joined after step 8 with the handler callbacks. A device
  write it attempts after that is refused and counted, and the check fails with `storage_refused`: a stop during a
  history download can leave the marker unsafe. That is the honest result and the measured rate is recorded.
- Message-store writes are not behind the device barrier. An admitted write finishes or fails under the store's own
  10 s limit before the worker or handler returns; nothing writes after its goroutine was joined.

**Rationale**: Steps 6 to 9 keep their order and meaning. The only new waiting is bounded and sits inside the
existing stop bound of 10 s. Because steps 6 and 8 can already take 5 s and 10 s in the worst case, the extra wait is
measured with every worker busy, not assumed harmless (tasks, the stop measurement with active workers).

## R12. Chat names

**Decision**:

- **Stored name first**: a per-generation name cache of **4,096** chats (proposed, least recently used) holds the best
  stored quality and name. A miss reads the chat once.
- **Direct chats**: from the device contact table, read through the message store: full name or first name
  (`NameSaved`), then push name or business name (`NameReal`), then the message's push name (`NameReal`, only for a
  message not from the account itself, whose push name is the owner's), then the
  JID user part (`NameFallback`). An offer is written only when it is better than, or newer at the same quality
  than, the cached one; feature 002's rule decides in the store.
- **Groups**: the history conversation's display name or name (`NameReal`); otherwise the cached stored name;
  otherwise the group JID is put into a lookup set of **64** groups (proposed). One group-name worker calls
  `GetGroupInfo` with a **10 s** deadline (proposed) and writes the subject (`NameReal`). A failed lookup is cached
  for **10 minutes** (proposed) in its own map of at most **1,024** groups (proposed; the oldest entry goes first), so
  a group that keeps failing is asked at most once per 10 minutes while its entry is kept. A full lookup set drops the
  request and counts it. The message is stored meanwhile with `Group <id>` (`NameFallback`), never discarded.
- **Observation time**: a name from a message or a history conversation takes that message's time; a name from the
  contact table or a group lookup takes the time it was read.

**Rationale**: One lookup per group per success (SC-005). Other accounts can create groups and send from them, so
the lookup set, the single worker and the failure cache bound the network work they can cause.

## R13. Logging and privacy

**Decision**: Text and captions appear only at debug. Media keys, hashes, URLs and direct paths never appear.
Warnings carry JIDs and message IDs escaped with the store's existing escaping, error classes and counts; names never
appear above debug. `OPERATIONS.md` gains a "What is stored" section: message text and captions, media metadata
including keys that decrypt the media, LID-to-phone links, indefinite growth, and that deleting a chat in WhatsApp
does not delete it here; backups hold the same data.

## R14. Live delivery to later features

**Decision**: `IngestConfig.Live`, a callback called after a live message's write committed, with the stored
`store.Message` (normalised identity, text, caption, media). It is `nil` in this feature. History never calls it. It
runs on the handler goroutine, so feature 011 must keep it non-blocking and bounded.

**Rationale**: FR-012 asks for the hand-off point only. A callback adds no queue, goroutine or option to this
feature.

## R15. Test version check

**Decision**: `TestQuietAllowListPin` reads only the `require` line, which is now the upstream base, not the code that
runs. It is changed to compare against the `replace` target when one exists. The allow-list is empty, so nothing fails
today, but the check would pass a stale entry.

## Appendix: investigation 2026-11-10 to 2026-11-12

Kept as the record of what was investigated. Superseded where R1 to R15 decide otherwise.

### Scope Decision 2026-11-12

The owner decided that the WhatsApp service and the account's own linked devices are not attackers (constitution 3.0.0, Principle V). The sections below were written against the earlier reading, in which every byte on the socket was hostile. They remain as a record of what was investigated, but the following are no longer requirements for this feature: a resource ceiling for decoded objects, bounds on detached library work and on retained IQ responses, cumulative app-state paging limits, protobuf structure preflight for history and app state, and the handling of terminal frames behind a pre-decode limit. All of them concern data that only the service or own devices can author.

What another account can drive in the library is the handler queue, the size of one frame, the per-message retry counters and, because history contains messages written by other accounts, the expanded size of a history sync. The rebuilt fork branch bounds those four; see the Fork Acceptance Contract in [plan.md](plan.md#fork-acceptance-contract). The multi-child decryption problem described below was a regression of the earlier prototype and does not exist on the rebuilt branch.

Findings below that concern the application itself (history acquisition design, identity and storage, lifecycle and failure, names and privacy) are unaffected.

### Receive Resource Gate

**Decision**: Pursue enforced bounds, not an assumed exception. The owner subsequently authorized development in the local fork and its local replacement for feature 005; this does not authorize a real-account operation or an exception. On 2026-11-10 the owner selected fail-closed connection shutdown on overload with a warning that history may have a gap. Numeric compatibility limits still require measurement and separate approval.

**Evidence**: The pinned `go.mau.fi/whatsmeow` commit `f1b798561919` decompresses inbound frames with unbounded `io.ReadAll` before application callbacks and starts an overflow goroutine per node when its handler queue is full. The project's earlier synthetic evidence in `docs/backlog/003-whatsmeow-receive-resource-bounds.md` recorded 65,770 compressed bytes expanding to 32 MiB and 2,048 overflow nodes producing 2,048 goroutines; this is not evidence of an ordinary-contact exploit. The feature 004 exception explicitly excludes 005 (`specs/004-shutdown-drain/verification.md`).

Public upstream head inspected was [`c88388f983b4bc43dfd0bcc2b46969fcde82d61f`](https://github.com/tulir/whatsmeow/commit/c88388f983b4bc43dfd0bcc2b46969fcde82d61f); its [comparison with the pin](https://github.com/tulir/whatsmeow/compare/f1b798561919a9106f284e39078b39a57982625e...c88388f983b4bc43dfd0bcc2b46969fcde82d61f) shows one commit changing `user.go`, not these receive paths. [Issue 602](https://github.com/tulir/whatsmeow/issues/602) reports queue-full resource symptoms; [PR 1236](https://github.com/tulir/whatsmeow/pull/1236) was closed unmerged and did not bound live overflow. Search cannot rule out unpublished fixes.

**Candidate, not yet verified**: A dependency-level change would enforce limits before and during decompression and node parsing, account for queued and active bytes/counts and stop the generation on overload without spawning unlimited overflow workers or blocking response routing. Illustrative starting points for measurement only are 16 MiB expanded frames, 64 nesting levels, 65,536 nodes, and a 2,048-node/64 MiB retained-work budget. None is a verified compatibility limit or an RSS guarantee. Proof must include exact/over-limit and malformed frames, decoder allocation accounting, saturated IQ response routing, slow handlers, cancellation, and quiet-stop/reconnect interleavings. The local replacement is not an immutable pin and any future pin change requires reassessment.

**Alternatives considered**: Application callbacks occur too late to limit upstream expansion; blocking them can accumulate upstream overflow. Extending E003-RX-01 would require a separately scoped owner decision, named owner, resources, paths, controls, expiry and evidence; it is not approved. Monitoring alone is not a bound.

**Scope for continued fork work**: The acceptance contract is in [plan.md](plan.md#fork-acceptance-contract). Work targets paths reachable with manual history download and library auto-reconnect disabled, including any shared or detached work that can affect resource or shutdown safety. Optional automatic-history and reconnect behavior is not a separate hardening goal for 005. The existing fork prototype and its tests are evidence to investigate, not an approved release or proof that this contract is met; see the dated entries in [verification.md](verification.md).

**Configured-path investigation**: The direct receive queue releases its reservation when the handler returns, but reachable handlers spawn detached acknowledgements and retry work, app-state key-share and missing-key work, and notification handlers. Some retain decoded data or use `context.WithoutCancel`. A bounded direct queue therefore does not itself bound or join this work. Inventory and account for reachable descendants before spawning, including their retained data and completion; saturation must fail closed without blocking response routing. A synchronous-ack setting alone does not cover the unconditional spawn sites. Automatic history download is not fed in manual mode, although manual-history receipts remain enabled by default. The local fork now runs message-triggered contact-name updates and manual-mode protocol receipts in the direct handler, and performs socket writes without a detached per-send worker. These changes affect timing and do not establish complete resource or retirement safety; failed-send socket cleanup can still outlive its caller.

**Queue accounting**: The local fork now charges admitted direct-queue work for unpacked frame length plus the decoder's logical work metric. This better distinguishes compact structure-heavy frames from similarly sized simple frames, but the weighted units are not allocated bytes, and responses routed before queue admission, detached descendants, temporary allocations and other clients remain outside this budget. Limits and real-traffic compatibility remain provisional.

**Response retention**: Matching IQ and acknowledgement responses bypass the direct queue to preserve progress, then enter buffered per-request channels. A request count or queue budget alone does not bound already-delivered responses retained by delayed consumers. Source review identified timeout paths that leave waiters registered until response or disconnect, including periodic keepalive; cancellation/delivery races require identity-safe cleanup. Establish process-wide and generation-owned request and response lifetime accounting without blocking the frame consumer; timeout cleanup alone is not an aggregate memory bound.

The fork now performs identity-safe cancellation and cleans up synchronous IQ, retry and keepalive waiters on terminal timeout or cancellation. This still does not determine the lifetime of a delivered response: in-library consumers parse responses after `sendIQ` returns, and some public APIs and errors can retain raw nodes. A proposed process-wide logical admission budget would require ownership through all reachable consumers, nonblocking response routing and separate pre-decode accounting; it must explicitly exclude or redesign arbitrary caller-retained results. No such budget or numerical limit is established, and a release convention alone cannot force Go callers to discard pointers.

Generation-specific IQ matching alone is also insufficient: a delayed old-generation stream error can clear client-wide waiters and change login state, and `retryFrame` is shared with message sends. An attempted narrow binding was left unchanged because those effects and lock/callback ordering require coordinated lifecycle analysis. The configured application disables the optional CAT refresh callback; the fork's callback-under-lock behavior is not by itself evidence of an application-reachable deadlock.

The configured-path inventory includes connection and pairing, keepalive, prekey uploads, encryption and app-state notifications, key shares, missing-key requests, retry processing, privacy fetches and owner repair. Future manual history adds media-connection and download work. A possible response-lease design would charge request admission before registration, transfer the charge on response delivery, and retain it through the last reachable consumer, while failing the originating generation closed without blocking frame routing. Child-node aliases, node-bearing errors, nested requests, old generations, producer goroutines and pre-decode allocations need explicit accounting. This is a research candidate, not an implemented design or an approved limit; the simpler operation-owner alternative below also has unresolved bounds.

An operation-owner cap alone is insufficient: a single app-state fetch can process an unbounded server-supplied sequence of `has_more` pages while accumulating derived events for later dispatch. Missing-key throttle entries and reachable caches can also outlive an operation, as can errors holding raw response nodes. Any owner-based envelope needs explicit cumulative per-operation and persistent-state bounds plus evidence of ownership transfer and routing progress; no such envelope is yet demonstrated.

Pre-decode admission also has a control-frame trade-off. Closing on a saturated process-wide decoder limit before classifying the frame can hide a `stream:error` such as logout or replacement; waiting in a bounded decode lane avoids that particular rejection but can hold frame buffers and delay IQ and transport control traffic. A candidate lane must bound waiting generations and buffers, keep decoded response routing live, and define a conservative unknown-terminal outcome when an authenticated frame cannot be classified. It cannot promise to observe a later terminal frame behind unread data. The owner's existing fail-closed overload choice does not by itself settle persistence or reconnect behavior for an unknown terminal outcome.

For missing app-state keys, cancelling send-lock admission now releases only that invocation's matching throttle reservation. This covers a proven pre-send point, not every failure before the key-request frame: a guarded LID lookup can fail after admission but before writing, and its reservation remains. A concurrent request filtered by a reservation that is later released is not automatically retriggered. The production missing-key path still uses `context.WithoutCancel`; changing it to synchronous cancellation therefore requires a reviewed liveness policy and lifecycle tests, not just the lock fix.

The fork's multi-encrypted-child failure handling also differs from the pinned upstream: it withholds the immediate retry and acknowledgement on a decryption failure and suppresses final handling after an old-counter or already-processed child. A synthetic real-Signal test confirmed that, in manual mode, a malformed first child prevents a valid later key-share child from being processed and makes no acknowledgement, NACK or retry attempt; a single-child control processed that key share. Earlier children may already have sent peer or history receipts, so withholding the final acknowledgement is not atomic. This is a demonstrated synthetic liveness risk, not a demonstrated real-account loss or proof of replay. A generation-owned failure signal, broader encrypted multi-child tests and protocol evidence are needed before selecting retry/acknowledgement behavior; neither silently withholding both nor restoring an immediate acknowledgement is proven safe for mixed history and key-share stanzas.

### History Acquisition

**Finding**: `internal/wa/session.go` sets `ManualHistorySyncDownload = true`. The pinned automatic downloader and `DownloadHistorySync` expand zlib data with unbounded reads and materialize a protobuf; later 16 MiB store batches do not bound that work. The pinned manual mode exposes history notifications through message events, but upstream receive processing has already occurred. Protocol receipts may still be sent; replay or successful persistence cannot be inferred from a receipt.

**Candidate**: Research a bounded application-owned notification, download, decompression and parsing path without `DownloadHistorySync`, with limits on actual compressed and expanded bytes, structure, fields, time, concurrent work and disk. The client `Download` itself reads the body, so a verified bounded transport is needed. Reproduce or explicitly account for the upstream history processing's device-store side effects, receipts and server-side deletion; no equivalent implementation has yet been demonstrated. The candidate cannot solve the baseline frame/queue gate.

**Configured-path recheck**: The current session receives manual notifications but does not download history. Its fenced HTTP client limits each response to 16 MiB and 10 seconds, while the fork prototype permits 32 MiB compressed history, 128 MiB expanded bytes and one process-wide history operation; those per-stage limits do not establish a whole-operation time or peak-memory bound. The default manual receipt is sent during notification handling, before any future application persistence; its failure is logged rather than propagated. `DownloadHistorySync` performs non-atomic device-store side effects and does not delete media; asynchronous storage can outlive the call. A future design must decide receipt timing, bound admission before starting work, and address partial device-store writes, uncertain outcomes and the existing HTTP limit without assuming replay. These are findings and design candidates, not approval to change acknowledgement semantics.

**Alternative**: Enable automatic history. Rejected as a complete bounded solution because the upstream expansion precedes store limits and its detached storage work complicates retirement.

### Identity And Storage

**Existing contract**: `StoreMessages` validates a whole batch and commits atomically; maximum 1,000 messages and 16 MiB aggregate field bytes. Upserts use `(chat_jid, id)` and retain existing non-empty media metadata when a replacement omits it. `MergeLID` atomically rewrites senders across chats, preserves the phone-side duplicate and is idempotent, but does not itself detect mappings or prevent later LID writes. Name updates use the feature 002 quality/time/tie-break rule.

**Candidate requiring design and tests**: Enumerate all mapped identities in bounded keyset pages at startup, not merely LID chat rows, to cover senders appearing only in groups. Detect committed mapping changes from all guarded mapping paths using a bounded, durable reconciliation signal; avoid treating a nested transaction's return as a durable commit. Serialize normalization with message writes and mapping-triggered merges so an in-flight old LID cannot be written after its merge. Re-read mappings inside the merge transaction and prove startup pagination does not miss concurrent inserts. A durable queue itself needs a disk bound and overload policy.

**Unresolved**: Mapping reassignment can change or displace an existing association, and existing merges discard provenance; automatic reversal cannot be assumed. The current single-transaction merge can exceed operation or disk budgets on large histories. Both need explicit behavior and evidence before implementation.

### Lifecycle And Failure

**Finding**: `internal/wa/session.go` does not forward message/history events, and its dropped-event accounting does not count them. The device storage-fault signal does not cover application message writes. `serve` and `stdio` currently wait for lifecycle cancellation even after the owner retires a generation for a device fault.

**Candidate**: Add generation-scoped, bounded admission and drain accounting for all ingest, mapping, name lookups and event delivery. Signal terminal storage failure to a coordinator outside the callback, stop ingest and the connected command, and report an incomplete record; preserve prior commits and classify indeterminate commit outcomes honestly. Late callbacks and unjoined application work must fail the quiet-stop check. Preserve work cancellation, disconnect, network fence, storage barrier and library-context order; no storage close or lock release while admitted writes remain.

### Names And Privacy

**Decision**: Preserve stored-name-first and the feature 002 quality ordering; bound cache size, lookup time, concurrency and lifetime in the eventual design. History group names precede network lookup. Avoid per-message group requests and avoid holding owner locks across network or storage work.

**Open evidence**: Explicitly assess ephemeral, view-once, edit and placeholder-resend provenance before deciding whether an event is eligible for persistent ingest or live-event delivery; the pin unwraps some event variants, and the current specification does not settle their treatment. Any product decision must follow the source specification and constitution rather than be assumed here.

### Verification Boundary

The plan requires synthetic adversarial cases for receive expansion, history bytes and structure, actual memory/work accounting, identity races, batch failures, private logging, post-retirement callbacks and shutdown ordering. Record native platform evidence separately. Real-account pairing, connecting and receiving each need separate permission; review the roadmap shutdown follow-up before any such test. Existing historical test or soak results are not fresh proof of this design.
