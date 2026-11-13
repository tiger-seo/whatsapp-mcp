# Tasks: Message Ingest

**Input**: Design documents in `specs/005-message-ingest/`: spec.md, plan.md, research.md (R1–R15), data-model.md,
contracts/ingest.md and quickstart.md.

**Prerequisites**: A passing owner-judge **design** review of the committed feature directory (the hook refuses
`/speckit-implement` without it), with the owner's answers to the plan's "Open questions for the design review"
(T004). Constitution 3.0.0. `docs/specification.md` must be present locally (T003).

**Tests**: Required (constitution VI and X, spec FR-014, SC-001 to SC-006). Write each test before the code it covers
and observe it fail for the right reason. Unit tests use the fake `ingestClient` and a temporary store; simulator
tests use `internal/wasim`. Use channels and barriers for timing, not sleeps. Automated tests never touch a real
account.

**Organization**: Setup, then a foundational phase that adds the store helpers, the session and owner wiring, the
ingest skeleton and the terminal-failure path, ending with the quiet-stop pass-rate rerun under `SynchronousAck`. Then
US1 to US4 in spec priority order, then polish and evidence.

**Decisions already made (do not reopen without the user)**:
- the dependency is `github.com/tiger-seo/whatsmeow` at `bedcf01` (`9209108` plus the two phone-request gates) with
  its approved limits; no other pin, fork commit or `replace` (research R1, plan contract item 8);
- a device storage fault keeps feature 003's behaviour; only an ingest failure stops the command (backlog
  `005-device-fault-command-wait.md`);
- E003-RX-01 is retired; the four library maps are accepted (R2);
- a storage failure stops ingest and the connected command; the operator restarts (spec clarifications, R9);
- receive overload is reported and follows the existing no-reconnect rule (R10);
- edits are not stored (R5); merges are not undone on a mapping change (R8, spec clarification 2026-11-13);
- history `pair` has not stored when it stops is reported and lost; a bounded wait is in the backlog (spec
  clarification 2026-11-13);
- a startup LID pass stopped by its budget is continued by the running session (spec clarification 2026-11-13).

No commit carries attribution lines.

## Current state

Resume point, 2026-11-13. Branch `005-message-ingest`. Nothing implemented yet. **Next: `/speckit-implement`, starting
at Phase 2 (T005).** The MVP is Phase 2 plus US1 (T005–T033).

- **Design**: passed at `a040210` after four owner-judge rounds (BLOCK, CONCERNS, PASS, PASS; table in
  `verification.md`). Its one remaining low: T004 names the plan heading "Open questions", which is now "Questions for
  the design review"; left unchanged so the PASS stays current.
- **Setup done**: T001 (fork pinned at `bedcf01`), T002 (race runs), T003 (spec sections), T004 (owner answers: limits
  accepted, unsafe stops during history accepted with T047's rate coming back, 16 MiB / 10 s history downloads kept).
- **Decided, do not reopen**: see "Decisions already made" above, the spec's clarifications of 2026-11-10 and
  2026-11-13, and plan.md's Fork Acceptance Contract (items 1–9).
- **Owner checkpoints during implementation**: T025 (pass rate with `SynchronousAck`) gates every story; T047 and
  T048 measurements return to the owner before the code review; T056 needs permission for each real-account step.
- **Environment**: the gates are in `AGENTS.md` "Commands". A `wsl --cd /mnt/...` call from Git Bash needs
  `MSYS_NO_PATHCONV=1`, or WSL exits 127 with `ERROR_PATH_NOT_FOUND`. gcc is installed in WSL Ubuntu for the race runs
  (T053). The fork's source is `D:\projects-mou\whatsmeow-receive-bounds`, branch `receive-bounds`; any fork change
  is a pin change and needs the owner.

This section is ignored by the review-gate hook.

## Format: `[ID] [P?] [Story] Description`

- Every task has a checkbox, a sequential ID and repository-relative file paths.
- `[P]` means different files and no dependency on an unfinished task.
- `[US1]` to `[US4]` mark story tasks. Setup, foundational and polish tasks have no label.
- Constraints quoted from data-model.md and contracts/ingest.md are binding.
- Test function names start with the prefix the quickstart runs: `TestIngest` (unit tests of `internal/wa/ingest*`),
  `TestSimulatorIngest` (simulator), `TestOwnerTerminal`, `TestQuietOrder`, `TestShellIngest` (commands), and the store
  helpers' own names (`TestValidateMessage`, `TestMessageFieldBytes`, `TestPhoneForLID`, `TestMappedLIDsWithData`,
  `TestContactNames`).

## Phase 1: Setup

**Purpose**: Confirm the gates and decisions. No production code.

- [X] T001 Pin the owner-approved fork in `go.mod` and `go.sum` and run the full gates. Done during planning,
  2026-11-13: first `9209108` (commit `88a74e5`), then `d83e19e` (`replace go.mau.fi/whatsmeow =>
  github.com/tiger-seo/whatsmeow v0.0.0-20261113120547-d83e19ef5083`) after the first design review, then `bedcf01`
  (`v0.0.0-20261113124132-bedcf0185245`) after the second; evidence in
  `specs/005-message-ingest/verification.md` "Pinned Fork Gates", "Fork Re-request Gate" and "Fork Placeholder Gate".
- [X] T002 Run the race detector on the fork and on `./internal/wa ./cmd/...` in WSL2, and fix the race found in
  `cmd/whatsapp-mcp/store_test.go`. Done 2026-11-13; evidence in `verification.md` "Race Detector (WSL2)".
- [X] T003 Confirm `docs/specification.md` exists locally and contains §3.4, §3.5, §8.5 (S3–S5), §8.6 (P2, P3) and §8.7
  (M7). If it is missing, stop and ask the user for it; do not infer behaviour from code. Record the check in
  `specs/005-message-ingest/verification.md`.
- [X] T004 **OWNER DECISION.** Ask the owner the three "Open questions for the design review" in
  `specs/005-message-ingest/plan.md`: (1) accept or change the proposed limits in `data-model.md`; (2) accept that a
  stop during a history download can leave the marker unsafe (which means offline recovery, unlinking on the phone
  and pairing again), to be measured in T047, including a `pair`-shaped stop; (3) accept the 16 MiB / 10 s
  media-client limits for history downloads or change them. Record the answers and the date
  in `verification.md` "Owner Decisions" and update `data-model.md` if a number changes. Only the owner ticks this
  task. The `pair` history gap, LID remapping, the budget handover, the placeholder gate and the history check in T056
  were decided on 2026-11-13 and are in the spec, the plan and this file.

---

## Phase 2: Foundational — store helpers, wiring, skeleton and terminal failure

**Purpose**: Everything every story needs: the read-only store helpers, the session's success-status handler with
`SynchronousAck`, the ingest component's states, counters, lock, `Stop` and `Wait`, the owner's retirement hooks and
terminal signal, and the command's exit on a terminal failure. Depends on T003 and T004.

**⚠️ CRITICAL**: No story task may start before T025 (pass-rate rerun) is recorded.

### Tests (write first, observe failing)

- [ ] T005 [P] Add to `internal/store/validate_test.go` cases for `ValidateMessage` and `MessageFieldBytes`: for each
  field of `store.Message`, a value that `StoreMessages` rejects is rejected by `ValidateMessage` with the same field
  name, and one that it accepts is accepted (table shared with the existing `StoreMessages` validation cases);
  `MessageFieldBytes` equals the charge `StoreMessages` makes, so a batch of messages summing to exactly 16 MiB is
  accepted and one byte more is `ErrResourceLimit`. Error text never contains the field value.
- [ ] T006 [P] Write `internal/store/lids_test.go`: `PhoneForLID` returns the mapped phone JID, `ok == false` for no
  mapping, a `*ValidationError` for a JID not on `@lid`, the caller's context error on cancel, `ErrClosed` after
  `Close`; `MappedLIDsWithData(ctx, afterLID, limit)` returns "up to limit (1..200) mappings whose LID is greater than
  afterLID, in key order, and that have a chat or a message sender stored under the LID", rejects limit 0 and 201,
  skips mappings with no data, and pages through 1,000 mappings without repeats or gaps while new mappings are
  inserted between pages.
- [ ] T007 [P] Add to `internal/store/contacts_test.go` cases for `ContactNames`: saved is full name, else first name;
  real is push name, else business name; empty strings when absent; names over 1 KiB in the device table are
  returned as a `ErrResourceLimit`, not truncated silently; only the current device's rows are read.
- [ ] T008 [P] Add to `internal/store/device_lids_test.go` cases: after a successful `PutLIDMapping` or
  `PutManyLIDMappings` through the guarded device store, the registered mapping observer receives each **changed**
  LID exactly once with its previous phone JID ("" for a new row) and its new one; an upsert that leaves the row
  unchanged notifies nothing (the library repeats it for almost every LID-addressed message); a failed or refused write notifies nothing; the observer is called after the write returns and
  never while a store lock is held; a nil observer is allowed.
- [ ] T009 [P] Extend `internal/wa/quiet_test.go` `TestQuietAllowListPin`: when `go.mod` has a `replace` for
  `go.mau.fi/whatsmeow`, the version compared is the replacement's (research R15); add a table test of the parser on
  `go.mod` texts with and without a replace.
- [ ] T010 [P] Write `internal/wa/ingest_test.go` skeleton cases with the fake client (new file
  `internal/wa/ingest_fake_test.go`): states follow data-model "Ingest state transitions" exactly (`running` →
  `failed` on a message-store error; `running` or `failed` → `retiring` at the retire bit; `retiring` → `stopped` when
  joined; never back to `running`); `Stop` is idempotent and never blocks; `Wait` returns when the workers return
  and returns the context error at its deadline; `Failed` is called exactly once; counters start at zero and are
  monotonic; a context error caused by the generation's own retirement does not make ingest `failed`.
- [ ] T011 [P] Write `internal/wa/session_ingest_test.go`: the session registers its handler with
  `AddEventHandlerWithSuccessStatus` and sets `SynchronousAck = true`, `ManualHistorySyncDownload = true`,
  `DisableManualHistorySyncReceipt = false`, and leaves feature 004's settings unchanged; owner events return `true`
  and dispatch as before; after the retire bit a message event returns `false`, increments `DroppedMessages`, does
  not touch the store and does not increment the owner-event drop count; an `UndecryptableMessage` with
  `IsUnavailable` returns `true` and increments `unavailable`; `IngestFailed()` closes when ingest fails.
- [ ] T012 [P] Extend `internal/wa/quiet_order_test.go` with the ingest steps: `ingest.Stop()` happens at step 4,
  `ingest.Wait` waits at most 2 s at step 5, workers still running are joined after step 8 and before step 9, and no
  step of feature 004 moves (the existing assertions keep passing). `QuietStopResult.DroppedMessages` is reported and
  never makes `Passed` false.
- [ ] T013 [P] Write `internal/wa/owner_terminal_test.go`: `Terminal()` closes once on an ingest failure and not on
  a device storage fault (feature 003's behaviour is unchanged); `Health()` reports `IngestFailed` and `ReceiveOverload` (empty when none).
- [ ] T014 [P] Write `cmd/whatsapp-mcp/ingest_cmd_test.go` with the fake owner seam: for `serve` and `stdio`, a terminal
  signal runs the normal stop and exits 1 with exactly `whatsapp-mcp: message storage failed; messages may be
  missing. Fix the cause and start again.` on stderr; stdout stays empty in `stdio`; a lifecycle stop without a
  terminal signal behaves as before; for `pair`, an ingest failure after the link prints the link outcome, then the
  storage message, and exits 1.
- [ ] T015 Run the tests of T005–T014 and record that each fails for the expected reason (missing symbol or
  assertion), in `specs/005-message-ingest/verification.md` "Foundational tests".

### Implementation

- [ ] T016 [P] Export `ValidateMessage` and `MessageFieldBytes` in `internal/store/validate.go` (and
  `internal/store/messages.go` if the charge lives there), with `StoreMessages` calling the same code, so there is one
  definition of valid and of size. Make T005 pass.
- [ ] T017 [P] Add `PhoneForLID`, `MappedLIDsWithData` and the `LIDMapping` type in `internal/store/lids.go` as ordinary
  read operations (admission, 10 s limit, typed errors), reading `whatsmeow_lid_map`, which has no device column (one account per data directory).
  Keyset paging on `lid`; existence checks through the existing `messages_sender_jid` index and the chats primary
  key; no schema change. Make T006 pass.
- [ ] T018 [P] Add `ContactNames` in `internal/store/contacts.go`, reusing the contact-name expressions already in
  `contacts.go` and `queries.go`. Make T007 pass.
- [ ] T019 [P] Add a mapping observer to the guarded device LID store in `internal/store/device_lids.go` (set through
  the device barrier or `LoadDevice`, one per generation), called after a successful write for each row that was
  inserted or whose phone changed, with the previous phone read in the same transaction. Make T008 pass.
- [ ] T020 [P] Change `TestQuietAllowListPin` in `internal/wa/quiet_test.go` to use the `replace` target. Make T009
  pass.
- [ ] T021 Create `internal/wa/ingest.go`: `IngestConfig`, `newIngest`, the `ingestClient` interface (`ParseWebMessage`,
  `DownloadHistorySync`, `GetGroupInfo`, own JID), the state machine, the per-generation ingest lock, counters
  (`stored`, `ignored`, `rejected`, `unavailable`, `droppedMessages`, `historyIncomplete`, `lookupsDropped`, `hintsDropped`,
  `merged`, `mergeFailed`, each `uint64`), `Stop`, `Wait`, `Counters`, and the terminal-failure routine of
  contracts/ingest.md (once: set `failed`, `Stop`, log `ingest_failed`, call `Failed`). `HandleMessage` returns `true`
  for everything at this stage except the retiring and failed cases. Make T010 pass.
- [ ] T022 Wire ingest into `internal/wa/session.go`: `SynchronousAck = true`, `AddEventHandlerWithSuccessStatus`,
  `handleEvent` returning `bool`, message events to `HandleMessage`, the retire-bit drop counted in
  `DroppedMessages`, the mapping observer to `NoteMapping`, `IngestFailed()`, and `Lib` derived from the library
  context, never the work context. Make T011 pass.
- [ ] T023 Wire ingest into `internal/wa/owner.go`: `ingest.Stop()` at step 4, a bounded 2 s `Wait` at step 5, the join
  after step 8 with the handler callbacks, `Terminal()`, `Health().IngestFailed`, `Health().ReceiveOverload`,
  `QuietStopResult.DroppedMessages` in the stop log with "messages may be missing" when non-zero. Then wire
  `cmd/whatsapp-mcp/commands.go` (`serve`, `stdio`, `pair`) to wait on `owner.Terminal()` beside the lifecycle and exit 1 with
  the message of T014. Update `specs/004-shutdown-drain/contracts/quiet-stop.md`'s event-count rule only by pointing to
  contracts/ingest.md "Revised event-count rule". Make T012–T014 pass.
- [ ] T024 Adapt feature 004's `TestSimulatorStopDuringQueuedAppStateFetches` in `internal/wa/appstate_stop_test.go`
  to `SynchronousAck`: `server_sync` fetches now run inline on the handler (plan, "What `SynchronousAck` changes in the
  pin"), so four fetches can no longer be queued in goroutines. Keep the test's purpose (a stop while app-state work is
  in flight and more is pending refuses only reads after the barrier and passes the check) with the new shape: one
  fetch in progress on the handler and at least four `server_sync` notifications waiting in the handler queue. Run it
  with `WA_APPSTATE_STOP_RUNS=20` and record the result in `verification.md` before T025. Do not weaken its refused-read,
  write or quiet assertions.
- [ ] T025 Rerun the quiet-stop pass rate with `SynchronousAck` on: `WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run
  'TestQuietStopPassRate' -count=1 -v -timeout 60m` (quickstart §5a). Record per kind and failure categories in
  `verification.md` "Pass rate with SynchronousAck" next to feature 004's recorded rates. A lower rate for any kind
  goes to the owner before any story starts.

**Checkpoint**: the session acknowledges only after handlers succeed, ingest can fail terminally and stop the command,
and retirement still passes feature 004's order and rate.

---

## Phase 3: User Story 1 — Keep Live Messages (Priority: P1) 🎯 MVP

**Goal**: Supported live messages, received or sent, are stored with chat, ID, sender, time, direction, text, separate
caption and media metadata; a failed write is not acknowledged and stops the command; receive overload is reported.

**Independent Test**: Deliver synthetic live events to a connected test session and inspect stored records, without a
real account.

### Tests for User Story 1 (write first, observe failing)

- [ ] T026 [P] [US1] Write `internal/wa/ingest_extract_test.go`: text from `Conversation`, else
  `ExtendedTextMessage.Text`; image, video and document with caption in `Caption` and never in `Text`; audio without a
  caption; URL, direct path, media key, file SHA-256, encrypted SHA-256, length, MIME type and the document file name
  copied; view-once and ephemeral wrappers and `DocumentWithCaptionMessage` stored as their inner type; an event with
  `IsEdit`, a bare `ProtocolMessage` (including `MESSAGE_EDIT` and `REVOKE`), a sticker, a reaction, a location, a
  poll, an empty message, a status broadcast and a newsletter message are ignored and counted `ignored`; an
  eligible message with a zero time, a time outside "2000-01-01 to 2100-01-01 UTC", an empty or over-256-byte ID, a
  sender absent or not a user or LID JID, a 32-byte field of another length or text over "256 KiB" is `rejected`,
  never repaired, and nothing is written.
- [ ] T027 [P] [US1] Write `internal/wa/ingest_live_test.go` with the fake client and a temporary store: an own message's
  sender is the account's phone JID; a group message's sender is `Info.Sender` with the device part dropped; a direct
  message's sender is the chat's user; a duplicate `(chat, ID)` leaves one row and keeps non-empty media metadata as
  feature 002 does; the same ID in two chats gives two rows; `HandleMessage` returns `true` for stored, ignored and
  rejected messages and `false` after a store error; after that error every later message returns `false` and is
  counted; the `ingest_failed` warning carries the operation `live`, the error class and the escaped chat JID and
  message ID, and no text or caption; `ErrIndeterminate` is reported as "may or may not be stored"; `Live` is called
  once per committed live message with the stored record and never for an ignored or rejected one.
- [ ] T028 [P] [US1] Write `internal/wa/wasim_ingest_test.go` on the simulator: a text and a captioned image sent to the
  account are stored and acknowledged; with the store made to fail, the message is not acknowledged (assert on the
  simulator's received acks, extending `internal/wasim` to record them if needed), the owner's `Terminal()` closes and
  the stop passes its quiet check; a stanza from another account that cannot be decrypted produces one retry receipt
  and one acknowledgement and **no** message to the account's own phone, and an `unavailable` placeholder of type
  `view_once` produces one acknowledgement, **no** message to the phone and an `unavailable` count of 1 (assert on
  every node the simulator receives); a full handler queue (the fork's 2,048 items, driven by a blocked handler) logs
  one `receive_overload` with reason `item_limit`, sets `Health().ReceiveOverload`, and no reconnect follows.
- [ ] T029 [US1] Run T026–T028 and record the expected failures in `verification.md`.

### Implementation for User Story 1

- [ ] T030 [US1] Create `internal/wa/ingest_extract.go`: eligibility and field extraction from `*events.Message` into
  `store.Message` per data-model "Message", then `store.ValidateMessage`. Make T026 pass.
- [ ] T031 [US1] Implement live `HandleMessage` steps 1, 3, 4, 5 and 7 of contracts/ingest.md in `internal/wa/ingest.go`
  (no LID normalisation and no name offer yet: chat and sender are stored as supplied, device part dropped). The write
  takes the ingest lock. Make T027 pass.
- [ ] T032 [US1] Read `client.LatestReceiveQueueOverload()` on `*events.Disconnected` in `internal/wa/session.go`, log
  `receive_overload` once with the fork's reason and "messages may be missing", dispatch `SessionReceiveOverload`
  before `SessionDisconnected`, and record the reason for `Health().ReceiveOverload` in `internal/wa/owner.go`. Make
  T028 pass.

**Checkpoint**: live text and media are stored and acknowledged only after the write; failure and overload are
reported.

---

## Phase 4: User Story 2 — Recover Available History (Priority: P1)

**Goal**: WhatsApp-supplied history is stored in bounded batches beside live messages, without moving last activity
backward and without claiming a partial chunk as complete.

**Independent Test**: Feed synthetic history conversations and live messages in varied orders and compare the
resulting records.

### Tests for User Story 2 (write first, observe failing)

- [ ] T033 [P] [US2] Write `internal/wa/ingest_history_test.go` with the fake client: a history notification from the
  account's own device is queued and `HandleMessage` returns `true`; one from another sender is ignored; the queue
  holds "8 notifications" and the ninth is reported `history_incomplete` (`queue_full`) without a disconnect; the
  worker calls `DownloadHistorySync` with synchronous storage and the library context; each history message goes
  through `ParseWebMessage` and the same extraction as live, so a group participant, an own message and a direct
  message get the senders of US1; a history entry whose parsed ID differs from `webMsg.Key.ID` (a bare
  `MESSAGE_EDIT`) is ignored and the original message is unchanged, in both arrival orders; 600 valid messages are written as flushes of "250 messages", 250 and 100, and messages
  summing to more than "4 MiB of field bytes" of `MessageFieldBytes` are cut before that; the ingest lock is released
  between flushes, and a live write waiting on it runs before the next flush; a message without a timestamp is
  rejected and its neighbours are stored; a conversation's display name or name is offered once per batch as
  `NameReal`; history never calls `Live`.
- [ ] T034 [P] [US2] Write failure cases in `internal/wa/ingest_history_test.go`: `ErrHistorySyncTooLarge`, a download
  error and a parse error each report `history_incomplete` with the reason (`too_large`, `download`, `parse`), the
  chunk order and sync type, and keep the connection and ingest `running`; a store error on the second batch keeps
  the first batch, reports `ingest_failed` with operation `history` and the messages committed from the chunk, makes
  ingest `failed` and claims nothing complete; a live message newer than the history keeps the chat's last activity
  for every arrival order (SC-003).
- [ ] T035 [P] [US2] Write retirement cases in `internal/wa/ingest_history_test.go` and `internal/wa/quiet_order_test.go`:
  `Stop` is observed before each download, between conversations and before each flush; a worker blocked inside
  `DownloadHistorySync` at step 5 is not cancelled by the work context, is joined after step 8, and a device write it
  attempts after the barrier is refused and counted, so the check fails with `storage_refused` (the honest outcome,
  R11); no message-store write happens after the worker was joined; notifications still queued at `Stop`, and a
  history notification that arrives after the retire bit, are each reported `history_incomplete` (`stopped`).
- [ ] T036 [US2] Run T033–T035 and record the expected failures in `verification.md`.

### Implementation for User Story 2

- [ ] T037 [US2] Create `internal/wa/ingest_history.go`: the history queue ("8 notifications"), the single worker, the
  download with synchronous storage under `Lib`, conversion through `ParseWebMessage` and `ingest_extract.go`, flushes
  cut at "250 messages or 4 MiB of field bytes" with one `StoreMessages` and one `UpdateChats` per flush under the
  ingest lock, released between flushes, the checkpoints, and the `history_incomplete` and `ingest_failed` reports. Add history step 2 to `HandleMessage`. Make
  T033–T035 pass.

**Checkpoint**: history and live messages coexist in any order; every incomplete chunk is reported.

---

## Phase 5: User Story 3 — Unify Contact Identities (Priority: P1)

**Goal**: Mapped LIDs are stored under the phone identity, unmapped ones stay LIDs, and LID data is merged at startup
and when a mapping appears during the run.

**Independent Test**: Seed synthetic LID and phone identities and overlapping messages; run startup reconciliation
and ingest events with and without mappings.

### Tests for User Story 3 (write first, observe failing)

- [ ] T038 [P] [US3] Write `internal/wa/ingest_lid_test.go` normalisation cases: a mapped chat LID and a mapped sender LID
  are replaced by the phone JID and the sender phone is filled; an unmapped LID is kept and its phone is empty; a
  lookup that exceeds "2 s", returns an error or is cancelled keeps the LID and is not treated as "no mapping"
  elsewhere; normalisation applies to live and history alike.
- [ ] T039 [P] [US3] Write run-time merge cases in `internal/wa/ingest_lid_test.go`: a `NoteMapping` for a LID with stored
  messages merges them through `MergeLID` without a restart; a mapping not yet visible is retried "5 attempts 1 s
  apart" and merged once it commits, else dropped and counted `hintsDropped`, and `Stop` during those retries ends the
  worker within one attempt; a failed merge logs `lid_merge_failed`; a flush with 250 distinct unmapped LIDs and a slow
  store re-looks them up under one 2 s deadline, writes those not resolved in time as LIDs, and holds the ingest lock
  no longer than that deadline plus the write; the hint set holds "1,024 LIDs" and a
  1,025th distinct hint is dropped and counted; a message normalised before the mapping existed and written while the
  merge is pending ends under the phone JID (ingest lock ordering, invariant N3); a message normalised before the
  mapping existed, whose mapping is written **and merged** before the message takes the lock, is still written under
  the phone JID (the re-lookup under the lock), for live and for a history flush; an unchanged mapping upsert produces
  no hint and no merge; a failed merge leaves both sides
  intact, is counted `mergeFailed` and does not make ingest `failed`; a mapping that moves a LID to another phone
  logs `lid_reassigned` with the escaped LID and the old and new phone JIDs from `NoteMapping`, and undoes nothing; given a startup cursor, the worker pages on from it with
  `MappedLIDsWithData` (200 per page) between hints until the table is done, and stops at a checkpoint between
  merges.
- [ ] T040 [P] [US3] Write `cmd/whatsapp-mcp/reconcile_test.go` for the startup pass: before the owner opens a
  generation, mapped LID chats and group-only LID senders are merged through `MappedLIDsWithData` pages of 200; the
  phone chat's duplicate wins and names and latest times follow feature 002 (SC-004); a second startup merges nothing;
  an unmapped LID and a merge that fails are left intact and reported in `lid_reconcile`; an exhausted startup budget
  stops the pass with `budget_exhausted`, the command still starts, and the first generation's reconcile worker
  receives the cursor and merges the rest without a restart.
- [ ] T041 [US3] Run T038–T040 and record the expected failures in `verification.md`.

### Implementation for User Story 3

- [ ] T042 [US3] Create `internal/wa/ingest_lid.go`: normalisation through `store.PhoneForLID` with a 2 s deadline, used
  by `HandleMessage` and the history worker before validation, and the re-lookup of still-unmapped LIDs under the
  ingest lock before each write or flush; the hint set ("1,024 LIDs", deduplicated), the
  reconcile worker with its retries, checkpoints and the startup cursor continuation, `MergeLID` under the ingest lock, and the `lid_reassigned` log.
  Implement `NoteMapping`. Make T038 and T039 pass.
- [ ] T043 [US3] Add the startup reconciliation to `cmd/whatsapp-mcp/commands.go` for `serve` and `stdio`, after the run
  guard and before `attachOwner`, under the remaining startup budget, logging `lid_reconcile` (merged, failed,
  skipped, completed or `budget_exhausted`), and pass a `budget_exhausted` cursor to the owner for the first
  generation's reconcile worker. A failure never stops the command. Make T040 pass.

**Checkpoint**: a person's direct and group messages land under one identity whenever WhatsApp supplies the mapping.

---

## Phase 6: User Story 4 — Recognize Chats Without Repeated Lookups (Priority: P2)

**Goal**: Chats get the best available name, stored names come first, and a group is looked up at most once while its
name is available.

**Independent Test**: Supply stored names, synthetic contact and history names, and a counted group-information
provider while ingesting repeated messages.

### Tests for User Story 4 (write first, observe failing)

- [ ] T044 [P] [US4] Write `internal/wa/ingest_names_test.go`: a stored non-empty name is read once into the "4,096
  chats, least recently used" cache and a worse offer is never written; direct chats get saved (full or first), then
  real (push or business), then the message's push name (never from an own message, whose push name is the
  owner's), then the JID user part as `NameFallback`, each offered only
  when better or newer at the same quality; a group with a history name never triggers a lookup; a group without one
  is stored with `Group <id>` and looked up once by the counted fake for 100 messages (SC-005); a failed lookup is not
  repeated for "10 min" while it is in the failure cache of "1,024 groups" (the oldest entry goes first); the lookup set holds "64 distinct groups" and the 65th is dropped and counted; each lookup
  has a "10 s" deadline and runs under `Lib`; a failed name write is terminal (`ingest_failed` with operation
  `name`) but the message stays stored and
  `HandleMessage` returns `true`; 10,000 distinct groups never grow the cache, the set or the worker count past their
  bounds.
- [ ] T045 [US4] Run T044 and record the expected failures in `verification.md`.

### Implementation for User Story 4

- [ ] T046 [US4] Create `internal/wa/ingest_names.go`: the name cache, the direct-chat order through
  `store.ContactNames`, observation times per research R12, the group lookup set and single worker with the failure
  cache (its own map of "1,024 groups"), and step 6 of `HandleMessage`. Make T044 pass.

**Checkpoint**: names are stable and group lookups are bounded.

---

## Phase 7: Polish and Cross-Cutting Concerns

**Purpose**: Abuse and privacy coverage, documentation, measurements, gates, platforms and evidence.

- [ ] T047 Measure stops with ingest busy (plan open question 2, research R11): add `TestQuietStopWithIngestWorkers`
  to `internal/wa/quiet_measure_test.go` under `WA_MEASURE=1`. Run 50 stops while the simulator delivers history
  chunks, 50 while a group lookup and a run-time merge are each in progress, and 50 while a live message is inside
  the handler (two slow LID lookups and a slow store write), and 50 `pair`-shaped stops at the follow-up confirmation
  while the first history notification is being downloaded. Record per case the pass rate, the
  failure categories and the `stop_bound_exceeded` count in `verification.md` "Stops with ingest busy". The owner
  reviews the numbers before the code review.
- [ ] T048 Measure handler latency under history (research R3): add `TestIngestHandlerLatency` to
  `internal/wa/quiet_measure_test.go` under `WA_MEASURE=1`. On the simulator, deliver an initial history of at least
  20 chunks while other accounts send a burst of 2,000 live messages, half of them LID-addressed with a phone
  `SenderAlt` (so the library repeats mapping upserts), with 100 `view_once` placeholders mixed in, 20 runs. Record the handler's ingest-lock wait
  (p50, p99, max), the number of `receive_overload` events and the messages stored, in `verification.md` "Handler
  latency under history". Target: no `receive_overload`. A miss goes to the owner with the numbers before the code
  review; do not raise the fork's queue or change a limit on your own.
- [ ] T049 [P] Write `internal/wa/ingest_abuse_test.go` (spec abuse cases): oversized text, caption, names and media
  fields; malformed JIDs, timestamps and control characters; a message whose text asks to send, pair or call a
  webhook; concurrent live, history, lookup and merge pressure. Assert nothing invalid is written, no partial batch
  exists, no send or pairing call reaches the fake client, and no bound in data-model "Per-generation ingest state"
  is exceeded.
- [ ] T050 [P] Extend `internal/wa/security_test.go` (or the store's `logsafety_test.go` pattern): with a debug-level
  and an info-level logger, text and captions appear only at debug; media keys, hashes, URLs and direct paths appear
  at no level; names never appear above debug; JIDs and IDs in warnings are escaped; nothing is written to stdout.
- [ ] T051 [P] Update `OPERATIONS.md` with a "What is stored" section (message text and captions, media metadata
  including keys that decrypt the media, LID-to-phone links, indefinite growth, deletion in WhatsApp does not delete
  here, backups hold the same data), the new events (`ingest_failed`, `history_incomplete`, `receive_overload`,
  `lid_reconcile`, `lid_reassigned`, `lid_merge_failed`, `ingest_stopped` with its `unavailable` count, which is why a
  view-once or other placeholder message is missing), the exit-1 storage message and what the operator does, and the
  split between implemented and verified.
- [ ] T052 [P] Check doc comments in `internal/wa/ingest*.go`, `session.go`, `owner.go`, `internal/store/lids.go`,
  `validate.go`, `contacts.go` and `device_lids.go`: no FR-, SC-, US, D, F or research IDs (constitution); no stale
  text about asynchronous acknowledgements.
- [ ] T053 Run the full gates of `AGENTS.md` "Commands" and record each exit code read directly: `gofmt -l .`, `go vet
  ./...`, `go mod verify`, `CGO_ENABLED=0 go test ./... -count=1 -timeout 30m`, the five cross-builds, `go run
  golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`, and `go vet`, `go test` and the build of
  `./internal/wa/testdata/cmd/session-check`. Confirm `go.mod` and `go.sum` still pin `bedcf01` and are unchanged since
  the commit that pinned it (`git log -1 --format=%h -- go.mod`, then `git diff <that> -- go.mod go.sum`). Keep `TEMP` and `TMP` on `D:\tmp`.
- [ ] T054 Run the race detector again in WSL2 on `./internal/wa ./cmd/...` (quickstart §6, `MSYS_NO_PATHCONV=1`) and
  the whole suite under WSL2 with `CGO_ENABLED=0` as in `AGENTS.md`; record both as WSL2. Push the branch for CI only
  if the user asks; anything not run is "not verified".
- [ ] T055 Check LF endings with `git ls-files --eol`; no tracked file shows `w/crlf`.
- [ ] T056 **Permitted real-account journey, only with the owner's explicit permission for each step** (quickstart §7),
  after reviewing the roadmap's shutdown follow-up: a new directory under `D:\tmp`, the binary from a named commit,
  `pair`, recording its `history_incomplete` counts by reason, the history it stored and its `unavailable` count; then
  `serve`, recording its `unavailable` count and any
  history it receives; one text and one captioned image from another phone, and a live message from a contact who is
  addressed by LID (roadmap: "pair, let history sync complete, and check chats, messages and captions"); stop; inspect
  the store; the owner
  unlinks the device and the directory is deleted. Record redacted results; a step not performed is "not verified".
  Do not act on a real account without that permission.
- [ ] T057 Roll up `specs/005-message-ingest/verification.md`: the pin and its limits unchanged, the race and pass-rate
  results, the history unsafe-stop rate, platforms covered, accepted risks (library maps, no replay guarantee,
  history after `pair` with T056's measured counts, no undo on a LID remap, `ErrBusy` stopping ingest,
  `unavailable` placeholders not fetched), and every "not verified" item. Update `AGENTS.md` "Current state" and this file's "Current
  state". Commit the work and the evidence and run the owner-judge agent in **code** mode; add its row to the "Owner
  reviews" table and commit it.

---

## Dependencies and Execution Order

- **Setup** (T001–T004): T001 and T002 are done. T003 and T004 come first; **T004 is an owner gate**.
- **Foundational** (T005–T025): tests T005–T014 in parallel, then T015; implementation T016–T020 in parallel, then T021,
  T022, T023 and T024 in order; **T025 (pass rate) gates every story**.
- **US1** (T026–T032) depends on Phase 2. It is the MVP.
- **US2** (T033–T037) depends on US1's extraction (T030) and live path (T031).
- **US3** (T038–T043) depends on Phase 2 (T017, T019, T021) and on US1's write path (T031); T042 also normalises in the
  history worker, so it follows T037 when US2 is done first, or adds that call when US2 lands.
- **US4** (T044–T046) depends on Phase 2 (T018) and US1 (T031); it uses history names from US2 when present.
- **Polish** (T047–T057) depends on all stories. T047 and T048 need US2 to US4. T056 needs the owner's permission.
  T057 is last.
- Within a story: tests, then the failing-observation task, then implementation.

### Parallel opportunities

- Phase 2 tests: T005–T014 together (distinct files); T016–T020 together.
- US1 tests: T026, T027 and T028 together.
- US2 tests: T033, T034 and T035 touch one file and the order test: write T033 first, then T034 and T035.
- US3 tests: T038 and T039 (one file, write in sequence) in parallel with T040.
- After Phase 2 and US1, US3 and US4 can proceed in parallel with US2 (different files), except where noted above.
- Polish: T049, T050, T051 and T052 together.

### Parallel example (Phase 2 tests)

```text
Task: "Write internal/store/validate_test.go (T005)"
Task: "Write internal/store/lids_test.go (T006)"
Task: "Write internal/wa/ingest_test.go and ingest_fake_test.go (T010)"
Task: "Write cmd/whatsapp-mcp/ingest_cmd_test.go (T014)"
```

## Implementation Strategy

1. **MVP = Phase 1 + Phase 2 + US1.** Live messages are stored and acknowledged only after the write; failure and
   overload are reported and stop or disconnect as designed.
2. Add US2 for history and US3 for identities (both P1), then US4 for names.
3. Finish with the stop and handler-latency measurements, abuse and privacy tests, documentation, gates, WSL2 runs, the
   permitted real-account journey and the code review.
4. If a measured pass rate falls below feature 004's, or a bound is reached in normal simulator traffic, stop and ask
   the owner. Do not widen a limit, the allow-list or the quiet check on your own.

## Notes

- Total tasks: 57 (Setup 4, Foundational 21, US1 7, US2 5, US3 6, US4 3, Polish 11).
- Keep each commit to one logical change; Conventional Commits that explain why; no attribution lines.
- Tick tasks `[X]` as they are done and record results with commands, dates, counts and exit codes in
  `verification.md`.
- No task changes `go.mod` or `go.sum`, adds a setting, adds a reconnect path, adds a migration or reopens decisions
  D1–D18.
