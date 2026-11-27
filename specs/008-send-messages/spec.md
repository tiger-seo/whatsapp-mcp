# Feature Specification: Sending Text, Files and Voice Notes

**Feature Branch**: `008-send-messages`

**Created**: 2026-11-27

**Status**: Draft

**Input**: [Roadmap item 008](../../docs/roadmap.md#008-sending-text-files-and-voice-notes): Send text, files and audio from local paths inside an allow-list. `.ogg` goes out as a voice note with a computed duration, and other audio is converted with ffmpeg.

**Source requirements**: [Project specification](../../docs/specification.md) M1, P1, P6, M4–M6, M10, Q3, Q4, D9; preserved behaviour from §3.8 and §4.4 (`send_message`, `send_file`, `send_audio_message`); defect avoided from §6 #1; the connection limits and the 2026-11-27 send authorization decision in §8.11. Threat model: [security review](../../docs/security-review.md) F01, F03, F05, F06, F09–F12. The consolidated baseline in `.specify/memory/` governs the existing local session, admission and store.

## Clarifications

### Session 2026-11-27

- Q: Which server-enforced rule decides whether the assistant may send to a recipient (security review F01)? → A: A recipient allow-list, `send_allowed_recipients`. It is empty by default, so sending is off until the owner lists recipients, and the single entry `*` is an explicit opt-in to any recipient. Message content is not restricted. Recorded in §8.11 of the [project specification](../../docs/specification.md#811-feature-security-decisions).
- Q: Should `send_file` accept a caption, although the earlier tool did not expose one (§4.4)? → A: Yes. WhatsApp supports captions on images, videos and documents, and the earlier bridge could send them. It is an optional `caption`, refused only for voice notes, which cannot show one.
- Q: What rate limit applies beyond one send at a time? → A: A fixed limit of 10 sends per minute and 200 per process run, with no setting. A call over the limit is refused with a retry hint. Recorded in the same §8.11 entry. (The 200 cap was changed to a rolling 24 hours below.)
- Q: Should the 200-send cap count per process run, or per rolling 24 hours? → A: Per rolling 24 hours, kept in memory, so that a long-running `serve` daemon keeps working. A restart resets the count; the cap is a safety brake, not a quota.
- Q: How long may one send take before it is stopped? → A: 2 minutes from the start of the call to WhatsApp's acceptance, including file reading, conversion and upload. Conversion has its own 60-second limit inside those 2 minutes.
- Q: Should a send to a phone number first check that the number is on WhatsApp? → A: Yes, for a phone recipient with no stored direct chat. Known chats, LIDs and groups skip the check. An unregistered number is refused before any file is read or anything is uploaded. The lookup counts towards the 2-minute deadline but not towards the rate limit. (The lookups' own limit and cache were added below.)
- Q: Should registration lookups have their own limit, given that an assistant looping sends to an unregistered permitted number (or any number with `*`) would otherwise fire unlimited contact-discovery queries? → A: Yes. Lookups are limited to 10 per rolling minute and 100 per rolling 24 hours, separately from sends. Each result (registered or not) is cached for 24 hours, so repeating a number costs no new lookup. Raised by the 2026-11-27 design review; recorded in §8.11.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Send a text message (Priority: P1)

The owner asks the local assistant to send a short text to a contact or group. The assistant calls `send_message` with the recipient and the text. The message arrives in that chat on WhatsApp as an ordinary text message from the owner's account, and the assistant gets back the sent message's identity so it can refer to it later.

**Why this priority**: Sending text is the most common account action and the base that file and audio sending build on. It delivers value on its own.

**Independent Test**: With the fake account boundary, call `send_message` for a permitted recipient and check that exactly one text message is handed to the account for the normalized address, that the result carries its chat and message ID, and that the sent message appears in the local message history.

**Acceptance Scenarios**:

1. **Given** a connected session and a permitted recipient given as a phone number, **When** the assistant sends "hello", **Then** one text message goes to that contact's phone address and the result reports the chat address, message ID and send time.
2. **Given** a recipient given as a LID with a known phone mapping, **When** the assistant sends a text, **Then** it goes to the mapped phone address, not to a second chat.
3. **Given** a recipient that the send policy does not permit, **When** the assistant sends a text, **Then** the call fails with a policy error and nothing is sent or stored.
4. **Given** the session is disconnected, **When** the assistant sends a text, **Then** the call fails with a "not connected" error, nothing is queued and no reconnect is attempted.
5. **Given** the default configuration (no `send_allowed_recipients`), **When** the assistant sends a text to anyone, **Then** the call fails with an error that names `send_allowed_recipients`, and nothing is sent.
6. **Given** 10 sends in the last minute, **When** the assistant sends another, **Then** it is refused with a hint saying when sending is possible again, and nothing is sent.

---

### User Story 2 - Send a file from the outbox (Priority: P2)

The owner places a photo, video or document in the outbox directory and asks the assistant to send it. The assistant calls `send_file` with the recipient and the file's absolute path. The file arrives as an image, video or document according to its extension, exactly as in the earlier implementation.

**Why this priority**: Sending files was broken in the earlier implementation (§6 #1) and is the main reason for this feature's path rules. It depends on the recipient and policy handling from User Story 1.

**Independent Test**: With a temporary outbox and the fake account boundary, send one file of each kind and check the uploaded type, MIME type and document title. Try paths outside the outbox, relative paths and link tricks, and check that each one is refused before any file content is read or uploaded.

**Acceptance Scenarios**:

1. **Given** `photo.jpg` in the outbox, **When** the assistant sends it with the caption "Holiday", **Then** it is uploaded and sent as an image with that caption.
2. **Given** `report.pdf` in the outbox, **When** the assistant sends it, **Then** it is sent as a document titled `report.pdf`.
3. **Given** a path outside every allowed directory, or a link inside the outbox that points outside it, **When** the assistant sends it, **Then** the call fails with an error that names the `send_allowed_dirs` setting, and nothing is uploaded or sent.
4. **Given** a relative path, **When** the assistant sends it, **Then** the call fails and says an absolute path is required.

---

### User Story 3 - Send a voice note (Priority: P3)

The owner asks the assistant to send a recorded or generated audio clip as a voice note. The assistant calls `send_audio_message` with the recipient and the path of an audio file in the outbox. An Ogg Opus file goes out directly as a voice note with its real duration. Other audio, such as `.mp3` or `.wav`, is first converted with ffmpeg and then sent as a voice note.

**Why this priority**: Voice notes are part of the preserved behaviour (P6), but they depend on file sending and on an optional external converter.

**Independent Test**: With reference Ogg Opus files of known length, check the computed duration and voice-note flag. With a stand-in converter, check conversion, its limits and cleanup. With no converter configured or found, check the explicit "ffmpeg missing" error.

**Acceptance Scenarios**:

1. **Given** a 12-second Ogg Opus file in the outbox, **When** the assistant sends it, **Then** it is sent as a voice note with a 12-second duration.
2. **Given** an `.mp3` file in the outbox and ffmpeg available, **When** the assistant sends it with `send_audio_message`, **Then** it is converted, sent as a voice note, and no converted file remains afterwards.
3. **Given** an `.mp3` file and no ffmpeg, **When** the assistant sends it with `send_audio_message`, **Then** the call fails with an error saying ffmpeg was not found and naming the `ffmpeg` setting, and nothing is sent.
4. **Given** a file named `.ogg` that is not Ogg Opus, **When** it is sent with `send_file`, **Then** the call fails and nothing is sent.

---

### Edge Cases

- **Broadcast and channel addresses**: Status (`status@broadcast`), broadcast lists, newsletters and server addresses are refused as recipients. Sending to the account's own address (a note to self) is allowed if the policy permits it.
- **Number not on WhatsApp**: A phone number with no stored chat that WhatsApp reports as unregistered is refused, and nothing is sent. If the lookup itself fails, the send fails too, rather than going ahead unchecked.
- **Unresolvable LID**: A LID without a known phone mapping is sent to the LID address as given. It is never guessed or matched to a similar number.
- **Lost connection during a send**: If the connection drops or the deadline passes after the message was handed to WhatsApp, the result says the outcome is unknown and that the message may have arrived. The call is not retried automatically, and nothing is stored locally.
- **File changes after validation**: The checks apply to the file actually opened. A file swapped or linked after the path check is refused or sent as it was when opened, never a different file. A file that grows after it is opened is refused.
- **Hard links**: A regular file with more than one hard link is refused, because a hard link can expose a file from outside the outbox under an allowed name.
- **Not a regular file**: Directories, devices, pipes, sockets and Windows alternate data streams are refused. So are UNC and device paths, and allow-list paths that use reparse points or junctions to leave the allowed directory.
- **Low disk space**: A file or audio send when the data directory's volume lacks the space in FR-011 is refused before any working file is written; text sends write no working files and are unaffected.
- **Slow or stuck source**: Everything that touches the file (resolving, opening, checking and copying it into the private working directory) happens before the send is admitted to the account connection, in work the call can abandon, so neither a stop nor shutdown waits on a slow or stuck drive. Until such abandoned work actually ends, further sends are refused as busy, so stuck reads cannot pile up. Leftovers are removed at the next start.
- **Empty or oversized files**: An empty file is refused. A file larger than the send size limit is refused before it is uploaded, and the limit also applies to the bytes actually read.
- **Caption on a voice note**: `send_file` with an `.ogg` file and a non-empty caption is refused, so that the caption is not silently dropped.
- **Extension case**: Extensions are matched case-insensitively (`PHOTO.JPG` is an image).
- **Ogg Opus without a granule position**: The duration is estimated from the size as in §3.8 and clamped to 1–300 seconds.
- **Conversion stalls or grows too large**: The conversion is stopped at its time or output limit, its temporary output is removed, and the call fails with nothing sent.
- **Concurrent sends**: A send that arrives while another is in progress is refused as busy, not queued without bound.
- **Shutdown during a send**: The send is cancelled. Shutdown and the quiet-stop rules keep their order, and a conversion process is ended and cleaned up.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The owner-launched local MCP session MUST offer three new tools: `send_message`, `send_file` and `send_audio_message`. The eight existing read tools MUST stay unchanged. The tools act on the account only through the existing connection owner. They MUST NOT pair, reconnect, start offline recovery or change the reconnect rules in §8.11.
- **FR-002**: Each tool MUST require `recipient`. It accepts a phone number, a phone JID, a LID or a group JID, normalized as for the read tools (M6). A LID with a known phone mapping is sent to the phone address. Status, broadcast-list, newsletter and server addresses and malformed addresses MUST be refused with a tool error. After the send policy (FR-003) and the rate limit (FR-012) pass, a phone recipient with no stored direct chat MUST be checked with WhatsApp. If the number is not registered, the send is refused with a "not on WhatsApp" error before any file is opened, converted or uploaded. Known chats (any stored direct chat, named or not), LIDs and groups skip this check. The lookup counts towards the send deadline but not towards the send rate limit. Lookups have their own fixed limit of 10 per rolling minute and 100 per rolling 24 hours, kept in memory. A result, registered or not, is cached for 24 hours (at most 1,000 numbers, oldest first out), and a cached result needs no new lookup and uses none of the limit. A lookup over its limit is refused as rate-limited, with a hint saying when another lookup is possible; a failed lookup is not cached. The result reports the normalized chat address that was used.
- **FR-003**: Before any file is opened, any converter runs or anything is uploaded or sent, every call MUST pass the server-enforced send policy: the normalized recipient must be in `send_allowed_recipients`, or that setting must be exactly `*`. With the default (empty) setting, every send is refused, and the error names the setting. A LID and the phone address it maps to count as the same recipient in both directions. Unknown aliases are never guessed. Message content is not restricted. Tool arguments, stored message content and purported approvals in the request MUST NOT grant or widen permission. A refused call reports a policy error and has no side effect.
- **FR-004**: `send_message` MUST require `message`: non-empty, valid UTF-8 and at most 65,536 bytes. It is sent as a plain text message.
- **FR-005**: `send_file` MUST require `media_path` and choose the message type from the extension, case-insensitively, as in §3.8: `jpg`, `jpeg`, `png`, `gif` and `webp` as an image; `ogg` as a voice note (FR-008); `mp4`, `avi` and `mov` as a video; anything else as a document titled with the file's base name. The MIME types are those in §3.8. `send_file` MUST also accept an optional `caption`, under the same rules as `message` in FR-004 except that it may be empty. An empty or omitted caption sends none. A caption is attached to images, videos and documents. A non-empty caption on a file sent as a voice note MUST be refused, because WhatsApp shows no caption on a voice note. `send_audio_message` takes no caption.
- **FR-006**: `media_path` MUST be an absolute path that resolves inside one of the directories in `send_allowed_dirs`, with symlinks, junctions and other reparse points followed. The check MUST apply to the file actually opened, not just to the path string. The rules MUST refuse:
  - relative paths and paths that leave every allowed directory, including traversal and prefix look-alikes;
  - files that are not regular files, files with more than one hard link, alternate data streams, and UNC or device paths.

  Refusals MUST name the `send_allowed_dirs` setting where it applies. Files are never moved, changed or deleted.
- **FR-007**: The new settings MUST follow the existing precedence (flag, environment variable, `config.toml`, default) and be validated at startup:
  - `send_allowed_dirs` defaults to `<data dir>/outbox`. Every entry is checked for syntax at startup. The directories are resolved, checked against the data directory and opened only when sending can happen (`send_allowed_recipients` is not empty); only then is the default outbox created as a private directory if it is missing. An entry that is relative, does not exist, is the data directory, contains it or lies inside it (except the default outbox) MUST then fail startup with a message that names the setting. That comparison MUST be by directory identity, not path text, so that a case-insensitive or aliased spelling cannot reach the data directory.
  - `ffmpeg` (`--ffmpeg`) sets an absolute path to the converter. A configured path that does not exist or is not an executable file MUST fail startup.
  - `send_allowed_recipients` is a list of phone numbers, phone JIDs, LIDs and group JIDs, empty by default. Each entry is normalized at startup. A malformed entry, or a status, broadcast, newsletter or server address, MUST fail startup with a message that names the setting. `*` is accepted only as the sole entry.
- **FR-008**: An `.ogg` file MUST be Ogg Opus: it starts with the `OggS` signature and carries an Opus header. Otherwise the send fails. The duration MUST come from the last granule position, the Opus pre-skip and the 48 kHz rate. When no granule position is found, it is estimated as bytes / 2000, and the result is clamped to 1–300 seconds (§3.8). It is sent as a voice note with that duration and a synthetic waveform. The analysis MUST be bounded in the bytes and pages it reads, and malformed input MUST return an error, never a crash.
- **FR-009**: `send_audio_message` MUST require `media_path`, under the same path rules as FR-006:
  - An Ogg Opus file is sent directly as a voice note under FR-008.
  - Any other file with a supported audio extension (`mp3`, `wav`, `m4a`, `aac`, `flac`, `ogg`, `opus`, `amr`) is converted to Ogg Opus (libopus, 32 kbit/s, 48 kHz, voip, VBR, 60 ms frames) and then sent under FR-008.
  - Other extensions are refused.

  The converter is the configured `ffmpeg` path, or else `ffmpeg` found on `PATH` (never in the current directory). If neither exists, the call fails with an error that says ffmpeg is missing and names the setting.
- **FR-010**: The conversion MUST be contained:
  - The converter is started directly with an argument list, never through a shell.
  - It reads only the content of the already-validated file, so it cannot be redirected by the file's name or reopen a swapped path.
  - It is limited to the expected input format and to local input.
  - It runs one at a time, in a private temporary directory, with a 60-second time limit (inside the send deadline in FR-012) and an output-size limit equal to the send size limit.
  - Its output is removed on every exit path, including failure, cancellation and shutdown. A stalled or cancelled converter is stopped.
- **FR-011**: A file MUST be at most 100 MiB. The limit applies both before reading and to the bytes actually read or produced. Empty files are refused. Before any working file is written, free space on the data directory's volume, measured fresh, MUST cover the store's resume threshold (512 MiB) plus the most working space the send can use: twice the file size for a file send, and the file size plus twice the 100 MiB limit for a conversion. `send_audio_message` always counts the conversion case, because whether a `.ogg` needs converting is known only after its bytes are read. Twice the file size includes 1 MiB for encryption padding and integrity data. A free space that cannot be measured also refuses. Otherwise the send is refused with a "not enough disk space" error and no side effect, so that a send can never push the store below its write reserve.
- **FR-012**: Sending MUST require a connected session. When disconnected, the call fails with a "not connected" error, and nothing is queued, reconnected or retried. Sends run one at a time; a call that arrives while another send is in progress is refused as busy. Each send MUST finish within 2 minutes, from the start of the call to WhatsApp's acceptance, including file reading, conversion and upload. The local record (FR-014) then has at most 5 seconds, and the reply MUST still reach the client when the acceptance comes just before the 2-minute mark. A send stopped by this deadline fails, or reports an unknown outcome under FR-013 if the message was already handed to WhatsApp. Sends MUST also stay within a fixed limit of 10 per rolling minute and 200 per rolling 24 hours; there is no setting. The count is kept in memory and starts again from zero when the process restarts. A call over either limit is refused before any side effect, with a hint saying when another send is possible. Every send handed to WhatsApp counts towards the limit, whether it succeeded, failed or has an unknown outcome. Refused calls do not count.
- **FR-013**: A successful send MUST return structured content and a text rendering with the same meaning: the normalized `chat_jid`, the message `id`, the RFC 3339 `timestamp` and the `media_type` (empty for text). Each failure is a tool error that says what went wrong without echoing message text or file contents. A send whose outcome cannot be known (cancelled, timed out, disconnected or an unrecognised error after the message was handed to WhatsApp) MUST say that the message may have been delivered, and it MUST NOT be retried automatically. Only a refusal from WhatsApp, or an error known to happen before the hand-over, is reported as a failed send. If the client cancels the request after the message was handed to WhatsApp, the send and its local record still finish, and the call returns an error that says the message was sent (with its chat and ID) or may have been sent, never a plain success and never a closed session.
- **FR-014**: After WhatsApp accepts a send, the message MUST be recorded in local history as the account's own message, with its text, caption and media type. Media sent this way MUST also keep the metadata a later download needs. The record MUST NOT recreate a chat that a LID merge has moved away: it is written under the same rules as an incoming live message. If the local record fails, the result still reports the send as successful and says the local record is missing. A send that WhatsApp did not accept is not recorded.
- **FR-015**: Logs MUST NOT contain message text, captions, file contents or converter output, except message text at `debug` (M10). Each send attempt MUST log one `info` event with the tool, the recipient's address kind, the outcome and its reason. Paths and recipients appear only in redacted form. stdout stays protocol-only.
- **FR-016**: Every tool MUST have offline acceptance cases through the existing in-memory client/server test path, with the fake account boundary. They cover each argument, each refusal, the outcome-unknown case and the local record. Ogg analysis and path validation MUST have unit and fuzz tests. Conversion MUST be tested with a stand-in converter, covering stall, oversize output, cancellation and cleanup. Platform-specific path controls (symlinks and hard links on Unix; junctions, reparse points, alternate data streams and UNC paths on Windows) MUST be tested on each platform they claim to cover. No automated test uses a real account.
- **FR-017**: Real-account acceptance MUST cover sending text, an image, a document, an `.ogg` and an `.mp3` to one chat the owner names. Each step needs the owner's separate permission and follows the project's real-account procedure. The `.mp3` step needs ffmpeg.

### Key Entities *(include if feature involves data)*

- **Recipient**: The chat a send goes to: a contact (phone or LID address) or a group, normalized to one WhatsApp address.
- **Send policy**: The owner's recipient allow-list (`send_allowed_recipients`), which decides on the server whether a send to a recipient may happen (FR-003). It is empty, and so closed, by default.
- **Outbox (allowed directory)**: A local directory from which files may be sent. Nothing outside the allowed directories can be sent.
- **Outgoing message**: The text, image, video, document or voice note sent, with its chat address, message ID, time and media type; recorded in local history once WhatsApp accepts it.
- **Voice note**: Ogg Opus audio sent with the voice-note flag, a duration and a waveform.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the offline acceptance suite, 100% of the cases for the three tools give the expected message type, address and result, including every argument and refusal.
- **SC-002**: In every abuse case (paths outside the allow-list, links, hard links, special files, recipients the policy refuses, malformed audio, fake approvals in arguments or stored messages), zero files are read outside the allowed directories and zero uploads or sends happen.
- **SC-003**: For each reference Ogg Opus file of known length, the computed voice-note duration is within 1 second of the true length (up to the 300-second clamp).
- **SC-004**: After every conversion that fails, stalls, is cancelled or is interrupted by shutdown, no converter process and no temporary file remain within 5 seconds.
- **SC-005**: In the real-account run, all five sends (text, image, document, `.ogg`, `.mp3`) arrive in the named chat as the expected type. Both voice notes play on the phone with a duration within 1 second of the source.

## Assumptions

- **Scope and dependencies**: Feature 003 supplies the connection owner and feature 006 the local MCP session, admission and tool conventions. Feature 005 supplies identity mapping and the message store. `serve` gets these tools over HTTP in feature 010. Media download (009), webhooks (011) and MCP pairing tools (backlog) are out of scope. Reactions, replies, edits, deletion and other new message types are non-goals (§8.2).
- **Duration clamp**: The 1–300-second clamp from §3.8 is kept as preserved behaviour, so longer voice notes report 300 seconds.
- **Size limit**: The 100 MiB limit is a fixed safe default for this release, below WhatsApp's own limits. The plan may propose a different value for owner approval.
- **Local record of sent messages**: The earlier implementation did not store sent messages. Storing them is a deliberate change, so that the read tools (for example `get_last_interaction`) see what the assistant sent. A later history sync of the same message updates the same record.
- **ffmpeg**: It is optional and not shipped with the binary. Its own security is the operator's responsibility. This feature limits what it is given and how long it runs, but does not sandbox it at the operating-system level.

## Security

**Assets and boundaries**: The integrity of messages sent in the owner's name, local files that could be sent off the machine, the converter executable, and the account's standing with WhatsApp. Boundaries: the local MCP client or LLM to the send tools, which an attacker can reach through prompt injection in stored messages; the tool to the local filesystem; the tool to the external converter; and the process to WhatsApp.

| Finding | Treatment and remaining scope |
| --- | --- |
| F01 | The server enforces the owner's recipient allow-list (FR-003; §8.11, 2026-11-27) before any side effect. Sending is off by default, and neither arguments nor stored content can grant or widen permission. Message content to a permitted recipient is not restricted, so a prompt-injected client can still send unrelated stored data to a permitted recipient; `*` extends that risk to anyone, by the owner's explicit choice. Configurable reads, MCP pairing and transport parity stay with the 006 backlog items and feature 010. |
| F03 | The default outbox and converter working directories are created private. Sent media keeps the existing private-storage rules. Data-directory areas holding secrets, the database or downloads cannot be allow-listed. |
| F05 | Containment is checked on the opened file (FR-006). Links, junctions, hard links, special files, alternate data streams, UNC and device paths, traversal and prefix look-alikes are covered on each platform claimed. |
| F06 | The Ogg analysis is bounded, and its parser is fuzzed. The converter is a trusted absolute executable, started without a shell, fed only the validated content and limited in input format, time, output size and concurrency. Output is cleaned on every exit. OS-level sandboxing of ffmpeg is documented as a residual risk, not provided. |
| F09 | Sends run one at a time, at most 10 per minute and 200 per rolling 24 hours (FR-012); registration lookups at most 10 per minute and 100 per rolling 24 hours, with a 24-hour result cache (FR-002); working files only when free disk space covers the store's resume threshold plus the send's worst case (FR-011); a size limit on bytes actually read and produced, deadlines and cancellation. There are no automatic retries and no unbounded queue. |
| F10 | All arguments are validated for type, length, address domain and extension before use. A rejected request never reaches the upload or the send. |
| F11 | One redacted `info` event per send attempt with its outcome. No content, file contents or full paths at `info`. A durable audit trail stays with 012. |
| F12 | Sent content leaves the machine for WhatsApp and its recipients; this is the feature's purpose. The local record follows the existing message retention. |
| F02, F04, F07, F08 | No new listener, secret, webhook or remote access. They belong to features 010 and 011. |
| F13–F16 | Shutdown, recovery and dependency rules are preserved. ffmpeg is an optional external program, not a dependency. Release assurance belongs to 012. |

**Abuse cases to verify**: Using synthetic data and the fake account, test:

- a stored message that tells the assistant to send to another contact, and a request carrying a fake approval
- traversal, prefix look-alike and relative paths
- symlinks and junctions that leave the outbox, a link swapped between the check and the open, and hard links
- devices, pipes, alternate data streams and UNC paths
- empty, oversized and growing files
- malformed and truncated Ogg data, and a non-Opus `.ogg`
- an argument-like file name, and a stalled or endlessly growing converter
- cancellation and shutdown during a conversion or an upload
- a send while disconnected, and rapid repeated sends.

Each one shows that nothing outside the allowed directories was read, nothing was uploaded or sent, no temporary file remains, and no reconnect or recovery started.
