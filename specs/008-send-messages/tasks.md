---
description: "Implementation tasks for sending text, files and voice notes"
---

# Tasks: Sending Text, Files and Voice Notes

**Input**: the `specs/008-send-messages/` design artifacts (spec, plan, research R1–R12, data model, [tools contract](contracts/tools.md), [pipeline contract](contracts/send-pipeline.md), quickstart, [feasibility](feasibility.md)) and `.specify/memory/constitution.md`.

**Organization**:

- **Grouping**: Tasks are grouped by independently testable user story.
- **`[P]`**: marks work on separate files with no unmet task dependencies; coordinate edits to shared files.
- **Tests**: The spec requires them (FR-016, Constitution VI). Automated tests use the A5 fake and `internal/wasim` only (Constitution VIII). Only T038 touches a real account, and only with the owner's permission for each step.

## Phase 1: Setup and Feasibility

- [X] T001 Run the four feasibility items of the plan's gate as scratch work outside `/speckit-implement`:
  1. simulator send and stop;
  2. upload deadline and memory;
  3. file containment;
  4. converter.

  Record the results in `specs/008-send-messages/feasibility.md`, align research R1, R2, R4, R6, R9 and R11, the contracts and the plan, and mark the record Verified. Done at `5f20340` on 2026-11-27.
- [X] T002 Create `specs/008-send-messages/verification.md` with an "Owner reviews" table (`| Stage | Date | Commit | Verdict | Owner decision |`). Commit this task list. Run the `owner-judge` agent in **design** mode over the feature directory, including `feasibility.md`, and record its row. `BLOCK` returns to design. `CONCERNS` needs the owner's decision, and does not clear the feasibility gate. `.claude/hooks/owner-review-gate.sh` must accept `/speckit-implement` only with a current design `PASS`.

## Phase 2: Foundation

**Prerequisite**: T002 has a current design `PASS`. No production code before it. Every user story depends on this phase.

- [X] T003 [P] Add the three settings to `internal/config/setting.go` and `Config` in `internal/config/config.go`, with tests in `internal/config/send_settings_test.go`:
  - **`send_allowed_dirs`**: a list, separated by the OS path-list separator in a flag or environment variable, a TOML array in `config.toml`. Default `<data dir>/outbox`.
    - Syntax is always checked.
    - Configuration validation checks syntax only. Only the tool-serving commands (`stdio` here), after the data-directory lock, and only when `send_allowed_recipients` is not empty, do the rest: "Absolute, existing directories, resolved with links, compared with the data directory by identity (`os.SameFile` on the entry, its ancestors and the data directory's ancestors). Each must not be the data directory, an ancestor of it, or inside it, except the default outbox and its subdirectories. Each is opened as an `os.Root` and kept."
    - On Windows, an entry whose path contains a junction fails at startup, but only when sending is enabled (R4, R10).
  - **`send_allowed_recipients`**: a list, comma-separated in a flag or environment variable, a TOML array. Default empty. Validation: "Each entry is a phone number, phone JID, LID or group JID, normalized. Status, broadcast, newsletter, server and malformed addresses are refused. `*` is allowed only as the sole entry."
  - **`ffmpeg`**: a path, default unset. Validation: "Absolute path to an existing executable regular file."

  Cover precedence (flag > environment variable > file > default), each refusal naming its setting, and the debug dump. Also cover:
  - with sending off: no directory is resolved, opened or created, and a junction in the default outbox's ancestors does not stop startup;
  - with sending on: `stdio` creates the default outbox private, after the lock;
  - `status` with `send_allowed_recipients` set creates nothing (no outbox, no data directory), as it does today;
  - a case-alias spelling of the data directory, or of an ancestor, is refused by the identity check. Run it on the native macOS runner (case-insensitive APFS), and record that the other platforms do not exercise it.
- [X] T004 [P] Implement recipient normalization and the send policy in `internal/media/recipient.go` and `internal/media/policy.go`, with tests in `internal/media/recipient_test.go` and `internal/media/policy_test.go`.
  - **Recipient argument:** "1–256 UTF-8 bytes: a phone number (digits, optional leading `+`, spaces and dashes ignored), a phone JID, a LID or a group JID". Status, broadcast-list, newsletter, server and malformed addresses are refused. The account's own address (note to self) is allowed.
  - **Recipient kind:** `phone`, `lid`, `group` or `self`, for the audit event.
  - **Policy:** The policy matches the allow-list after normalization. A LID and its mapped phone count as the same recipient in both directions, and unknown aliases are never guessed. An empty list refuses with `not_permitted` naming `send_allowed_recipients`; `*` permits any recipient.
  - **Store helpers (all reads, with the guard's `write` flag false):**
    - add `LIDsForPhone` beside `PhoneForLID` in `internal/store/lids.go`;
    - add `ChatExists` in `internal/store/chats.go`, true for any stored chat, named or not (`ChatName`'s ok is false for an unnamed chat, so it cannot be used);
    - add `FreshFreeBytes` in `internal/store/disk.go`, which measures the data directory's volume now with the guard's own function, never the 30-second cached value, returns the measurement error instead of letting it pass, and exposes `diskResume` as a constant the send service uses.

    Tests in `internal/store/lids_test.go`, `internal/store/chats_test.go` and `internal/store/disk_test.go`.
- [X] T005 [P] Implement the rate window and the process-wide send slot in `internal/media/limits.go`, with tests in `internal/media/limits_test.go` using an injected clock:
  - "A send is allowed when fewer than 10 fall in the last minute and fewer than 200 in the last 24 hours", with at most 200 timestamps kept;
  - a send is recorded when it is handed to WhatsApp, whatever its outcome, and refused calls are not counted;
  - `retry_after_seconds` is computed from the oldest timestamp in the binding window;
  - the slot allows one send at a time per process and refuses a second with `busy`;
  - **Lookup window:** "fewer than 10 fall in the last minute and fewer than 100 in the last 24 hours", at most 100 timestamps, counted when a lookup is sent.
  - **Lookup cache:** "Per normalized phone number, the last lookup result (registered with its LID, or unregistered) and its time; used for 24 hours; at most 1,000 entries, oldest evicted first. Failed lookups are not cached." A cache hit uses none of the window. An over-limit lookup gives `rate_limited` that names the lookup limit.
- [X] T006 Replace the media client's fixed 10-second total in `internal/wa/http.go` with the request-dependent bound proven in feasibility item 2, and build the session's media client with it in `internal/wa/session.go`:
  - an upload `POST` must carry a context deadline and is bounded by it;
  - every other request is bounded by a 10-second context, released when its body closes;
  - the 16 MiB response limit, the https-only and redirect policy, and the fence wrapping are unchanged.

  Add tests in `internal/wa/http_test.go` for the existing download tests (unchanged), a throttled 12 MiB upload completing under a 2-minute deadline and stopped at a 3-second one, a stalled download stopped at 10 s, and an upload without a deadline refused.
- [X] T007 Extend `internal/wasim` with send support in `internal/wasim/send.go`, with tests in `internal/wasim/send_test.go`. Move the feasibility scratch handlers there. The simulator answers:
  - the device-list usync;
  - the prekey fetch, with a `Peer` signed prekey bundle;
  - the registration usync (`jid` = LID and `pn_jid` = phone for a registered number; `contact type="out"` otherwise);
  - the user-info usync with `<lid val=…>`;
  - group info for a test group with participants;
  - `POST /mms/...` uploads through `Server.Client()`, recording the bytes received;
  - acknowledgements for outgoing `message` nodes.

  A test can hold any step (`lookup`, `devices`, `prekeys`, `groupinfo`, `upload`, `ack`) and release it.
- [X] T008 Implement the owner's send port in `internal/wa/send.go`, the session side in `internal/wa/session_send.go`, and a fake port in `internal/wa/fake.go` (A5):
  - **Port operations:** a registration lookup; a streaming upload (`UploadReader`) whose source is the private working copy and whose ciphertext goes to a caller-provided private temporary file, never the user's file; a send; and the local record (T009).
  - **Admission:** only when connected, on the current generation, not retiring, owner not stopping; otherwise `ErrNotConnected`. Admitted calls are counted. Each call's context is derived from the generation's work context and the caller's deadline.
  - **Retirement:** In `internal/wa/owner.go`, `retireGeneration` waits, after `work_cancelled` and within the existing drain bound, for admitted send calls to return (trace step `sends_waited`) before `Retire` blocks storage. A call still running at the bound makes the stop unsafe.
  - **Never:** The port never connects, reconnects, pairs or starts recovery.
  - **Outcome:** "Only two kinds of error are a failure (`send_failed`): WhatsApp's own refusal (an error code in the acknowledgement), and errors from a fixed list known to happen before the node is written, such as recipient not on WhatsApp, a failed device or prekey fetch, or a failed encryption." Anything else once the library send call started is `outcome_unknown`; no automatic retry. Unit-test the mapping with each listed error, an unlisted error, and a wrapped context error.
- [X] T009 Implement the local record in `internal/wa/send_record.go`, reusing `extractContent` from `internal/wa/ingest_extract.go`. The data model's sent-message row:
  - `chat_jid` "The normalized recipient: phone JID, group JID, or the LID as given when no mapping is known";
  - `sender_jid` "The account's own phone JID, without a device part";
  - `time` the server timestamp;
  - `from_me` true;
  - `text`, `caption`, `media_type` and the upload's media metadata (URL, direct path, key, both hashes, length, MIME, document file name).

  Write it through ingest's serialized live-write path in `internal/wa/ingest.go`, within a 5-second bound (shortened to the remaining budget) that retirement waits for:
  - add `lockForLiveCtx(ctx)`, which selects on the lock and `ctx.Done()` and keeps `liveWaiting` balanced on both paths;
  - under it, `relookup` of a LID chat, then the store write.

  The relookup and `StoreMessage` take the record's context too, never ingest's library context. That context is detached from the request's cancellation and bounded by the budget, 5 s and the work context. A failed write, or a lock not obtained in time, returns "record missing" and logs a content-free warning; a write cancelled mid-commit may also log `store.io_error`, which is expected. Tests in `internal/wa/send_record_test.go`:
  - a LID merge interleaved with the record leaves the message in the phone chat and does not recreate the LID chat;
  - with a worker holding the lock, the record gives up when the remaining budget runs out;
  - with a worker holding the lock, the record gives up when retirement cancels the work context, and the stop stays within its drain bound;
  - `liveWaiting` is zero afterwards in both cases, so the worker is not starved;
  - with the store's single writer held, the record ends within its bound and reports "record missing".
- [X] T010 Add port tests: `internal/wa/send_owner_test.go` with a fake session and `internal/wa/wasim_send_test.go` on the simulator.
  - **Owner, with a fake session:** admission refusals in every phase, and the retirement order `work_cancelled` → send returned → `sends_waited` → `disconnect` → `fence_closed` → `barrier_retired`. A send that does not return within the drain bound marks the stop unsafe.
  - **Simulator — sends:** lookup registered and unregistered, a text send, an image `UploadReader` plus send with caption, a group send, and the local record of each.
  - **Simulator — stops:** a stop held at each step (`lookup`, `devices`, `prekeys`, `groupinfo`, `upload`, `ack`) returns before this generation's `barrier_retired` and passes the quiet-stop check with no library goroutines and no refusals. A held `ack` gives "outcome unknown". The group steps were not covered by feasibility. If library work remains after a stop there, stop and bring it to the owner as a quiet-stop allow-list decision before the tools ship; do not widen the allow-list in this task.
  - **Repetition:** Repeat the stop cases with `-count=20` in the verification run.
- [X] T011 Implement the send service pipeline in `internal/media/service.go` and the per-send working directory in `internal/media/workdir.go`, with tests in `internal/media/service_test.go` against the fake port:
  - steps 1–10 of the pipeline contract in order, each refusal stopping all later steps;
  - the budget: acceptance by 2:00 from the call's start, the record within the remaining time and at most 5 s;
  - the registration lookup only for a phone recipient without a stored chat (`ChatExists` false), not counted in the send window, using the lookup cache and window (T005);
  - the disk check (R12) before the working directory exists: `FreshFreeBytes` ≥ 512 MiB plus two times the file size plus 1 MiB (`send_file`), or the file size plus 200 MiB (`send_audio_message`, always, including an Ogg Opus that will not be converted), otherwise `insufficient_space`; text sends skip it;
  - the source stage (R3), before any port call and in one goroutine the handler can abandon: syntax check, resolve, confined open, handle checks, disk check, working directory, copy. The copy is limited to the opened size plus one byte (more refuses with `file_refused`) and checks the context between 1 MiB chunks. On cancellation the handler returns. The send slot stays taken until the goroutine returns, so later calls get `busy`, and the startup sweep removes a leftover directory;
  - a failed free-space measurement refuses with `insufficient_space`.
  - the error codes of the tools contract;
  - "A private directory under the data directory ... removed on every exit path", with a startup sweep of leftovers.

  The service holds the slot (T005) for the whole call, including the local record. Tests cover:
  - an injected low free-space value: refused, with no working directory, upload, send or store write;
  - a held `Lstat` (resolution), a held open and a held read, each during a stop: the handler returns within 1 s, the MCP stop step completes, no port call is in flight, and the owner's stop passes the quiet-stop check (injectable filesystem hooks);
  - while a stage is held, a second call gets `busy`, and after release the slot frees;
  - a file grown after the open is refused with no upload;
  - a stage that finishes after its handler was cancelled removes its working directory and then frees the slot;
  - `send_audio_message` with a small non-Opus `.ogg` is checked with the conversion worst case;
  - client cancellation: before the hand-over gives an ordinary refusal with no send; after an acknowledgement gives `sent_cancelled` with chat, ID and timestamp and a stored record; after the hand-over without an acknowledgement gives `outcome_unknown`;
  - the budget arithmetic.
- [X] T012 In `internal/mcp/connection.go`, give the three send tools a guard deadline of 2 minutes 15 seconds, inject one clock interface into the guard (admission, cancellation timers, response-write deadline) and into the send service, keep the current tool call's admission time as per-session guard state, and let the handler read it through a session-to-guard map in `internal/mcp/server.go`, never from client parameters (the service derives its deadlines from it), leaving every other tool at 10 seconds and keeping the single tool credit. In `internal/mcp/diagnostics.go`, add tool identities and categories for the new error codes, and one `info` audit event per send attempt with tool, recipient kind, result code, media type and duration, never the address, text, caption, path, file contents or converter output. Tests in `internal/mcp/connection_test.go` and `internal/mcp/diagnostics_test.go`:
  - a read tool is refused `busy` while a send is in flight;
  - the send's deadline and a read's 10 s;
  - with a fake clock, a fake send acknowledged at 1:59.9 after admission, whose record takes the full 5 s, still delivers its success reply, with no `notifications/cancelled` and no closed connection;
  - a client `notifications/cancelled` arriving between the acknowledgement and the reply: the reply is the `sent_cancelled` tool error, the connection stays open, and the record is stored;
  - the audit fields and their redaction.
- [X] T013 Add the tool registration scaffold `RegisterSendTools` in `internal/mcp/send_tools.go` and the result types in `internal/mcp/send_results.go`, following the tools contract:
  - **Annotations:** `readOnlyHint: false`, `destructiveHint: false`, `idempotentHint: false`, `openWorldHint: true`.
  - **Descriptions:** each says the call sends a WhatsApp message in the owner's name, and that sending is off until `send_allowed_recipients` is configured.
  - **Arguments:** strict validation (null, duplicate, unknown and wrongly typed fields refused without echoing input).
  - **Success shape:** `sent`, `chat_jid`, `id`, `timestamp` (RFC 3339), `media_type`, `voice_note`, `duration_seconds` (voice notes only) and `local_record` (`stored` or `missing`), with a one-sentence text rendering.
  - **Error codes:** a mapping table covering every code in the contract.

  The handlers receive a send-service interface, never the owner, client or store writer.
- [X] T014 Wire `stdio` in `cmd/whatsapp-mcp/commands.go`:
  - build the send service from the configuration, the store's read helpers, the owner's send port and the data directory;
  - create the default outbox with `private.MkdirAll`;
  - sweep leftover working directories at startup;
  - call `RegisterSendTools`.

  The owner is never passed to `internal/mcp`. Add a test in `cmd/whatsapp-mcp/send_wiring_test.go` that `tools/list` shows the three tools beside the eight read tools and that the outbox is created private.

## Phase 3: User Story 1 - Send a Text Message (Priority: P1) 🎯 MVP

**Goal**: The assistant sends a text to a permitted contact or group and gets back the sent message's identity. The message appears in local history.

**Independent test**: Through the in-memory MCP transport with the fake port, `send_message` to a permitted recipient produces exactly one text message for the normalized address, the result carries the chat, ID and time, and the store has the row. Every refusal before the hand-over produces no send.

### Tests

- [X] T015 [P] [US1] Add `internal/mcp/send_message_test.go` (in-memory transport, fake port, seeded store) with one case per outcome:
  - **Schema:** required `recipient` and `message`; `message` "1–65,536 bytes of valid UTF-8".
  - **Recipients:** each recipient form; a LID with a mapping sent to the phone address; refused address kinds.
  - **Refusals:** `not_permitted` under the default configuration naming `send_allowed_recipients`; `rate_limited` with `retry_after_seconds` after 10 sends in a minute; `busy`; `not_connected` with no reconnect; `not_on_whatsapp` and `lookup_failed` for a phone without a stored chat.
  - **Failures:** `send_failed`; `outcome_unknown` (counted in the rate window, not retried).
  - **Success:** the full result shape and `local_record` `stored` and `missing`; structured and text renderings agree.
  - **No side effects:** the fake records zero uploads and zero sends for every refusal before the hand-over.
- [X] T016 [P] [US1] Add text-path cases to `internal/media/service_test.go`: the lookup is skipped for a stored direct chat, a LID or a group; the lookup is not counted in the rate window; the send is counted at the hand-over; a plain text `Conversation` message is built; the record holds `text` and an empty `media_type`.

### Implementation

- [X] T017 [US1] Implement `send_message` in `internal/mcp/send_tools.go` and the text path in `internal/media/service.go`, building a `Conversation` message.
- [X] T018 [US1] Run the US1 and foundation tests (`go test ./internal/media ./internal/mcp ./internal/wa ./internal/config ./internal/store -run 'Send|Recipient|Policy|Limit|LIDsForPhone' -count=1`, then the full packages) and record the command, date, counts and exit code in `specs/008-send-messages/verification.md`.

## Phase 4: User Story 2 - Send a File from the Outbox (Priority: P2)

**Goal**: Images, videos and documents placed in an allowed directory are sent by extension, with an optional caption. Nothing outside the allowed directories is ever read or uploaded.

**Independent test**: With a temporary outbox and the fake port, each file kind is uploaded and sent with the expected type, MIME and title. Every path abuse case is refused before any byte is read or uploaded.

### Tests

- [X] T019 [P] [US2] Port the feasibility containment cases to `internal/media/path_test.go`, `internal/media/path_windows_test.go` and `internal/media/path_unix_test.go`.
  - **Refused, all platforms:** relative, traversal, prefix look-alike, outside, directory, empty, over 100 MiB (sparse file), missing, hard link to an outside file, symlink and symlinked directory escaping, and a directory swapped for a link after resolution.
  - **Accepted:** a link inside the outbox and a link into another allowed directory.
  - **Kept roots:** a root directory swapped for a link after startup does not redirect an open, because the kept `os.Root` handle is used. On Windows, record whether the user can still rename or delete an allowed directory while its root is kept open, and document the result in T037.
  - **Refused, Windows:** a junction to outside, a junction swap, an alternate data stream, UNC, `\\?\`, `\\.\` and forward-slash UNC, `CON`, `NUL.txt`. Informational: trailing dot and space, 8.3 names.
  - **Refused, Unix:** a FIFO, and a file swapped for a FIFO after resolution, without blocking; a symlink to `/dev/null`; a device node when run as root.
  - **Skips:** Report cases that need a privilege as skipped, never as passed.
  - **Fuzz:** Add `FuzzSyntacticPath`.
- [X] T020 [P] [US2] Add `internal/mcp/send_file_test.go`:
  - **Types:** every row of the contract's extension table, case-insensitive, with its MIME type; a document titled with the base name.
  - **Caption:** "0–65,536 bytes of valid UTF-8"; a non-empty caption on a `.ogg` refused with `invalid_input`; an omitted caption sends none.
  - **Refusals:** `file_refused` naming `send_allowed_dirs` for an outside path; `upload_failed`.
  - **Result:** `media_type`, and the local record with media metadata and caption.
  - **Size limit:** a file growing past 100 MiB after the open is stopped by the counting reader with no send.

### Implementation

- [X] T021 [US2] Implement path containment in `internal/media/path.go`, `internal/media/path_windows.go` and `internal/media/path_unix.go` (R4):
  - a syntactic check;
  - `filepath.EvalSymlinks`;
  - the choice of the allowed root, then `os.Root` `OpenFile` of the relative name, with `O_NONBLOCK` on Unix;
  - handle checks: "a regular file with exactly one link, and its size must be 1 byte to 100 MiB";
  - a counting reader that enforces 100 MiB on the bytes actually read.

  Return one open handle, used for every later read. Use the `os.Root` handles opened and kept at startup (T003), never one reopened by path.
- [X] T022 [US2] Implement the message type table and media message construction in `internal/media/types.go`: image, video (with the streaming sidecar from the upload) and document messages, built from the upload response. Upload from the plaintext working copy (T011) through the port, with the ciphertext in a temporary file in the per-send working directory (R3).
- [X] T023 [US2] Implement `send_file` with its optional `caption` (default `""`) in `internal/mcp/send_tools.go` and the file path in `internal/media/service.go`.
- [X] T024 [US2] Run the US2 tests natively on Windows and in WSL2, and in CI on Linux, Windows and macOS (T034). Record the command, date, per-platform counts, skips and exit codes in `specs/008-send-messages/verification.md`. A platform not run is "not verified".

## Phase 5: User Story 3 - Send a Voice Note (Priority: P3)

**Goal**: Ogg Opus audio is sent as a voice note with its real duration. Other audio is converted with ffmpeg inside contained limits.

**Independent test**: Reference Ogg Opus files give voice notes within 1 s of their length. A stand-in converter shows the limits, cancellation and cleanup, with no process or file left. A missing converter gives the explicit error.

### Tests

- [X] T025 [P] [US3] Add `internal/media/ogg_test.go` and `FuzzOggOpusDuration`, using small Ogg Opus reference files committed under `internal/media/testdata/` (generated once with ffmpeg; record the command in the test file's comment):
  - each reference duration within 1 s;
  - a file without a granule position estimated as bytes / 2000;
  - the clamp to 1–300 s;
  - a missing `OggS`, a missing `OpusHead`, Ogg Vorbis, and a truncated page or segment table each refused with an error, never a panic.
- [X] T026 [P] [US3] Add `internal/media/convert_test.go`, with a stand-in converter built from `internal/media/testdata/fakeffmpeg/` and a launcher stand-in that starts it as a child without passing a kill on:
  - the argument list and empty environment;
  - the 60-second limit, injectable for tests;
  - output over the limit;
  - cancellation and shutdown;
  - the launcher's child stopped with the tree (job object on Windows, process group on Unix);
  - no process and no working directory left within 5 s;
  - one conversion at a time;
  - `ffmpeg_missing` naming the `ffmpeg` setting when neither the setting nor `PATH` has it;
  - executable substitution: an `ffmpeg` in the current directory, or in a relative `PATH` entry, is never used.

  Add `TestRealFFmpeg`, guarded by `WA_FFMPEG_TESTS=1`:
  - each allowed input kind the local build can create, converted to Ogg Opus of the right duration;
  - refusal of the disguised playlist and concat files from feasibility item 4.
- [X] T027 [P] [US3] Add `internal/mcp/send_audio_test.go`:
  - **Extensions:** the allowed list (`ogg`, `opus`, `mp3`, `wav`, `m4a`, `aac`, `flac`, `amr`), and refusal of others.
  - **Direct send:** Ogg Opus sent without conversion.
  - **Conversion:** other input, including non-Opus `.ogg`, converted.
  - **Result:** `voice_note: true` and `duration_seconds`.
  - **Errors:** `invalid_audio`, `ffmpeg_missing`, `conversion_failed`.
  - **`send_file` with `.ogg`:** a voice note, or `invalid_audio` when the file is not Opus.

### Implementation

- [X] T028 [US3] Implement the bounded Ogg Opus parser in `internal/media/ogg.go` (R5).
- [X] T029 [US3] Implement the converter in `internal/media/convert.go`, `internal/media/proc_windows.go` and `internal/media/proc_unix.go` (R6):
  - **Lookup:** the configured absolute path, or `exec.LookPath("ffmpeg")`.
  - **Input:** a size-checked private copy named `input`.
  - **Invocation:** the exact R6 argument list, with `-protocol_whitelist file,pipe` and the demuxer from the extension.
  - **Output:** read from stdout through a counting writer, stopped at 100 MiB, written to `output.ogg` in the working directory.
  - **Environment:** empty (on Windows only `SystemRoot`).
  - **Process tree:** on Windows started suspended in a kill-on-close job object and then resumed (`golang.org/x/sys/windows`); on Unix its own process group, killed as a group.
  - **Limits:** 60 s, one conversion at a time, `WaitDelay` 2 s, the working directory removed on every exit path.
- [X] T030 [US3] Implement voice-note construction in `internal/media/types.go`: PTT, `audio/ogg; codecs=opus`, the duration, and the synthetic 64-byte waveform seeded by the duration. Implement `send_audio_message` in `internal/mcp/send_tools.go`, and route `send_file`'s `.ogg` through the same path.
- [X] T031 [US3] Run the US3 tests, plus `WA_FFMPEG_TESTS=1` where ffmpeg is installed. Record the ffmpeg versions, platforms, counts and exit codes in `specs/008-send-messages/verification.md`.

## Phase 6: Polish and Cross-Cutting Concerns

- [X] T032 [P] Add `internal/mcp/send_abuse_test.go`, using synthetic data, for every abuse case in the spec's Security section:
  - a stored message telling the assistant to send elsewhere, and fake approvals in arguments;
  - paths, links, special files, and empty, oversized and growing files;
  - malformed Ogg data, and an argument-like file name;
  - a stalled and a growing converter;
  - cancellation and shutdown during conversion or upload;
  - low free disk space, and a source file whose read is stuck;
  - a send while disconnected, and rapid repeated sends.

  Each case must show that nothing outside the allowed directories was read, nothing was uploaded, sent or stored, no temporary file remains, and no connect, reconnect or recovery started.
- [X] T033 [P] Add the native stdio test `cmd/whatsapp-mcp/send_stdio_test.go`:
  - stdout carries only the protocol;
  - stderr carries one redacted audit event per attempt and no recipient address, text, caption, path or file bytes, with message text only at `debug`;
  - a read tool is refused `busy` during a send and works afterwards;
  - shutdown during a send cancels it and stops in the safe order.
- [X] T034 Extend `.github/workflows/ci.yml` (actions pinned to full commit SHAs):
  - the path containment tests already run in the native matrix;
  - add a job that installs ffmpeg on Linux (`apt`), macOS (`brew`) and Windows (`choco`, which exercises the package-manager launcher case) and runs `WA_FFMPEG_TESTS=1 go test ./internal/media -run RealFFmpeg`.
- [X] T035 Run the [quickstart](quickstart.md) offline gates, with the simulator stop cases repeated `-count=20`. Record the command, date, platform, commit, counts and exit codes in `specs/008-send-messages/verification.md`.
- [X] T036 Run every gate in `AGENTS.md`'s Commands section on the final code:
  - `gofmt -l .`, `go vet ./...`, `go mod verify`;
  - `go test ./... -count=1 -timeout 30m`;
  - the five `CGO_ENABLED=0` builds;
  - govulncheck v1.9.0.

  Record each exit code read from the command itself, and the CI run link for native Linux, Windows and macOS. Cross-builds are not native evidence.
- [ ] T037 Document the delivered feature:
  - **`OPERATIONS.md`:** that a send path on a hung network drive leaves sending `busy` until restart (abandoned source stage), the three settings, the outbox, the recipient allow-list (off by default, `*`), the rate window, ffmpeg lookup and its residual OS-sandbox risk, junctions refused on Windows, and what stays unverified.
  - **`README.md`:** the send tools and the configuration example.
  - **`AGENTS.md` current state.**
  - **`docs/roadmap.md`:** a "Done" line for 008 once merged.
  - **Backlog:** the review note in `docs/backlog/005-placeholder-phone-request.md`, whose trigger (the F01 send decision) fired on 2026-11-27, and `docs/backlog/INDEX.md` in step.
- [ ] T038 Real-account acceptance, **only with the owner's separate permission for each step**, following [quickstart](quickstart.md) section 3 and the project's procedure:
  - a new empty data directory under `D:\tmp`, the binary built from a named commit, and the owner scanning the QR code;
  - `send_allowed_recipients` set to the one chat the owner names;
  - text, a `.jpg` with a caption, a `.pdf`, an `.ogg` and an `.mp3`;
  - checks on the phone, and of `list_messages`;
  - the time of a large file send (tens of MiB) through the owner's MCP client, to observe the client's own request timeout (the TypeScript MCP SDK's default is believed to be 60 s), documented in `OPERATIONS.md`;
  - cleanup by unlinking on the phone, then deleting the directory.

  Record redacted results in `specs/008-send-messages/verification.md`; a step not run is "not verified".
- [ ] T039 Commit the implementation and its `verification.md` evidence, then run the `owner-judge` agent in **code** mode and record its row. Resolve `BLOCK`, record the owner's decision for `CONCERNS`, and review again whenever covered work changes. Do this before creating or merging the pull request.

## Dependencies and Execution Order

- **Gates:** T001 (done) → T002, the owner design gate; this task list does not grant it. T002 → every later task.
- **Foundation:** T003, T004 and T005 in parallel. T006 and T007 in parallel. T008 after T006 and T007. T009 after T008. T010 after T007–T009. T011 after T004, T005 and T008–T009. T012 and T013 in parallel after T011's interface exists. T014 after T003 and T011–T013.
- **US1:** T015 and T016 in parallel after the foundation. T017 → T018. US1 is the MVP and the base for US2 and US3, which share the service and the registration file.
- **US2:** T019 and T020 in parallel. T021 → T022 → T023 → T024. Coordinate `internal/mcp/send_tools.go` and `internal/media/service.go` with US3.
- **US3:** T025, T026 and T027 in parallel. T028 and T029 in parallel. T030 after T022, T028 and T029. Then T031.
- **Polish:**
  - T032 and T033 after the three stories;
  - T034 any time after T026 exists;
  - T035 and T036 after all tests and implementation, and again on the final code;
  - T037 documents verified behaviour;
  - T038 needs the built binary and the owner's permissions;
  - T039 comes last, and again after any change it covered.

## Parallel Execution Examples

- **Foundation:** `T003` (config), `T004` (recipient and policy) and `T005` (limits) touch separate packages and files; then `T006` (`internal/wa/http.go`) beside `T007` (`internal/wasim/send.go`).
- **US1:** `T015` (`internal/mcp/send_message_test.go`) beside `T016` (`internal/media/service_test.go`).
- **US2:** `T019` (`internal/media/path*_test.go`) beside `T020` (`internal/mcp/send_file_test.go`).
- **US3:** `T025` (`ogg_test.go`), `T026` (`convert_test.go`) and `T027` (`send_audio_test.go`); then `T028` (`ogg.go`) beside `T029` (`convert.go`, `proc_*.go`).

## Implementation Strategy

1. Pass the design gate (T002) first.
2. Build the foundation: settings, policy, limits, the port with its retirement wait and the local record, the simulator, the service, the guard deadline and the wiring.
3. Deliver US1 (text) as the MVP and verify it on its own.
4. Add US2 (files and containment), then US3 (voice notes and the converter), each verified independently.
5. Run the cross-cutting abuse, native, CI and full gates.
6. Run the real-account acceptance with permission.
7. Ask for the owner's code review.

This task list authorizes no real-account action, dependency change, whatsmeow pin change or schema migration.
