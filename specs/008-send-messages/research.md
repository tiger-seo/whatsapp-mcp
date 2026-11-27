# Research: Sending Text, Files and Voice Notes

These findings come from reading the code at `163b114` and the pinned whatsmeow fork (`tiger-seo/whatsmeow@bedcf01`). They describe the design to verify; nothing here is measured yet. Items marked **feasibility** need scratch evidence in `feasibility.md` before implementation (see the plan's gate).

## R1. Who performs a send

**Decision:** The connection owner gets a narrow send port. The MCP tools receive a send service built on that port, never the owner. The port lets the service:

- ask whether a phone number is registered,
- upload media,
- send a message,
- record the sent message locally.

Each call is admitted only while the owner is connected, its current generation is not retiring and the owner is not stopping. An admitted call runs under a context derived from that generation's work context. The owner counts admitted send calls, as it tracks the repair pass. Retirement cancels the work context, then waits for admitted send calls to return (bounded by the existing drain bound) before `Retire` blocks storage. Every network path of the call already runs through the generation's fence.

**Rationale:** The owner is the only component that knows whether the generation is usable, and the stop order (work cancel → `Disconnect` → fence → storage barrier → library context) is a safety invariant. Admitting sends like the repair pass keeps them inside that order. No reconnect, pairing or recovery path is reachable from the port.

**Alternatives considered:**

- Giving the tools the owner, or the session's `*whatsmeow.Client`. This breaks the rule that tools never hold the account owner, and it would let calls outlive a generation.
- A send queue inside the owner. Sends are synchronous and run one at a time (R7), so a queue adds unbounded state for no benefit.

**Sources:** [owner retirement](../../internal/wa/owner.go) (`retireGeneration`, `startRepair`), [session adapter](../../internal/wa/session.go), [A5 interfaces](../../internal/wa/client.go).

## R2. Upload transport and its time limit (**feasibility**)

**Decision:** Media upload uses the generation's fenced media HTTP client, as every library request does. That client currently has a fixed 10-second total timeout, shared with history and app-state downloads, so a large upload would fail. The plan gives uploads their own bound inside the same fenced transport: a request deadline equal to the remaining send deadline (at most 2 minutes). Downloads keep their 10-second total limit and 16 MiB response limit. The upload response stays under the existing response-size limit. The feasibility work confirms that the split leaves every download bound unchanged (existing download tests unchanged and passing) and that an upload of 100 MiB completes against a local test server.

**Rationale:** Raising the shared timeout would loosen an approved download bound. Making the deadline depend on the request (an upload `POST` against the remaining send budget) keeps one fenced client and changes nothing for downloads.

**Alternatives considered:**

- A second media client for uploads. The library has one media client, so this would need a library change, which is an owner decision.
- Raising the shared timeout to 2 minutes. This loosens the download bound.

**Sources:** [HTTP clients](../../internal/wa/http.go), [fence](../../internal/wa/fence.go); fork `upload.go` (`cli.mediaHTTP.Do`).

## R3. Upload memory

**Decision:** Use the library's streaming upload (`UploadReader`), which encrypts into a caller-supplied temporary file. That file is created in a private per-send working directory under the data directory and removed on every exit path. The plaintext is streamed from the validated open file through a counting reader that enforces the 100 MiB limit on the bytes actually read.

**Rationale:** The byte-slice `Upload` holds the plaintext and the ciphertext in memory, about 200 MiB at the size limit. Streaming keeps memory flat, and it applies the size limit to bytes as they are read.

**Alternatives considered:** `Upload` with a 100 MiB buffer. It is simpler, but its peak memory depends on the input size.

## R4. Path containment (**feasibility** on each platform)

**Decision:** At startup, each `send_allowed_dirs` entry is resolved to an absolute real path, symlinks included, and must be an existing directory. At call time:

- `media_path` must be absolute and pass a syntactic check that refuses UNC, device and `\\?\` paths, alternate data streams (`:` after the volume), NUL and empty components.
- The allowed root that contains the resolved path is chosen, and the file is opened with the standard library's confined open (`os.Root` on that root, relative name). It follows links only while they stay inside the root and refuses escapes, including a link swapped after resolution.
- The open handle must be a regular file with exactly one link, and its size must be 1 byte to 100 MiB.

All later reads come from this one handle. The feasibility work runs the abuse list from the spec natively on Windows (junctions, symlinks, hard links, alternate data streams, UNC, case aliases), Linux and macOS.

**Rationale:** A check on the path string alone is racy (security review F05). `os.Root` (Go 1.24+, available with the pinned Go 1.27) is the standard library's race-resistant confined open on every target. It needs no new dependency.

**Consequence:** A link inside one allowed directory that points into a different allowed directory is refused, because each open is confined to one root. This is stricter than the spec's "resolves inside one of the directories", and the contract states it.

**Alternatives considered:**

- `filepath.EvalSymlinks` plus a prefix check, then a plain open. This is racy.
- A hand-written `openat` walk. It is platform-specific code that `os.Root` already provides.
- Reusing `internal/private`. Its open refuses every link and requires private ownership, which is right for the data directory but wrong for an outbox the user fills.

## R5. Message type, MIME and voice notes

**Decision:** The §3.8 extension table, case-insensitive, with the specification's MIME types. `.ogg` passes through a bounded Ogg/Opus parser written in the project:

- It reads at most the file's bytes (≤ 100 MiB) page by page, with each page's segment table checked before use.
- It requires the `OggS` capture pattern and an `OpusHead` identification header, and reads the pre-skip.
- It takes the last page's granule position.
- Duration = (granule − pre-skip) / 48,000 s, rounded to the nearest second. Without a granule position it is bytes / 2,000. The result is clamped to 1–300 s.

The waveform is the synthetic 64-byte placeholder seeded by the duration. The parser is fuzzed, and malformed input returns an error.

**Rationale:** This preserves §3.8 behaviour (P6) with explicit bounds (F06). No dependency is needed for a parse this small.

**Alternatives considered:** A third-party Ogg library. It would be a new dependency for about 150 lines of parsing.

## R6. Conversion with ffmpeg (**feasibility**)

**Decision:**

- **Lookup:** At startup, `ffmpeg` must be an absolute path to an existing executable file, or startup fails. At call time, an unset setting means `exec.LookPath("ffmpeg")`; the standard library refuses results relative to the current directory. Not found is the explicit "ffmpeg missing" tool error.
- **Input:** The validated content is copied, under the size limit, into a private per-send working directory. The converter reads that copy only.
- **Invocation:** It runs directly, with an argument list and no shell:
  - `-nostdin -hide_banner -loglevel error`, and `-protocol_whitelist file` so the input cannot reach the network or other protocols;
  - an explicit demuxer chosen from the extension (mp3, wav, mov for m4a, aac, flac, ogg for ogg and opus, amr), with the input named by a fixed file name, never the user's;
  - `-map 0:a:0 -vn -c:a libopus -b:a 32k -ar 48000 -application voip -vbr on -frame_duration 60 -f ogg` to a fixed output name in the same directory.
- **Limits:** One conversion at a time, 60 s, output at most 100 MiB (checked while it runs and after), cancellation through the send context, then `Kill` with a short `WaitDelay`. The working directory is removed on every exit path.
- **Output:** It is then parsed by R5 and sent as a voice note.

Feasibility runs the real ffmpeg on Windows, Linux and macOS with these inputs:

- mp3, wav, m4a (including the index at the end of the file), aac, flac, amr and non-Opus ogg;
- a playlist or concat file disguised as `.mp3` that references another local file, which must be refused;
- a stall and an oversized output.

It records the ffmpeg versions.

**Rationale:** F06 asks for a trusted executable, no shell, restricted protocols and demuxers, time, size and concurrency limits, private working files and cleanup. A copy (rather than a pipe) lets demuxers that need seeking (m4a) work, while the converter still sees only validated bytes under a fixed name.

**Alternatives considered:**

- Piping through stdin. This fails for m4a files whose index is at the end.
- Passing the user's path. That lets a swapped file be read, or a crafted name be read as an option.
- Operating-system sandboxing. It is platform-specific and documented as a residual risk instead (spec Assumptions).

## R7. MCP admission and the 2-minute deadline

**Decision:** Keep the existing guarded transport: one tool call in flight per session, at most four active requests. The guard already knows the tool name at admission. For the three send tools it sets the request deadline to 2 minutes plus a 5-second reply margin, instead of 10 seconds. The handler runs the send under a 2-minute context and returns before the guard's deadline. While a send is in flight, any other tool call on that session is refused as `busy` by the existing guard path. This satisfies FR-012's one send at a time for the local session. The send service also holds a process-wide one-send slot for `serve` (feature 010).

**Rationale:** The guard's single tool credit and deadline handling are the reviewed admission mechanism (006 R2). A per-tool deadline is the smallest change. Reads are blocked for at most the length of a send, which is acceptable for a single local client that calls tools one at a time.

**Alternatives considered:** A separate send credit, so that reads and a send run together. It doubles the guard's concurrency states for little benefit to a sequential client. Reconsider in 010 if HTTP clients need parallel reads during sends.

## R8. Policy, rate limit and order of checks

**Decision:** One ordered pipeline; each refusal ends the call with no later step run.

1. Arguments.
2. Recipient normalization and refused address kinds.
3. Send policy: allow-list membership, with LID↔phone mapping in both directions from the local store and no guessing.
4. Rate limit: 10 per rolling minute, 200 per rolling 24 hours, in memory.
5. Connection.
6. Registration lookup: only for a phone recipient with no stored direct chat. It does not count toward the rate limit.
7. File: open and validate, conversion, upload.
8. Send.
9. Local record.

A send counts toward the limit when it is handed to WhatsApp (step 8 begins), whatever its outcome. The limiter stores at most 200 timestamps.

**Rationale:** This follows FR-002, FR-003 and FR-012 and the clarified order: no file is opened, nothing converted and no network contact made before the policy and the limit pass.

## R9. Outcome and local record

**Decision:**

- **Outcome:**
  - An error returned before the message node was written is a failure.
  - Once the library call to send has started, cancellation, a deadline or a disconnect is "outcome unknown", because the node may have left. No automatic retry.
  - A server acknowledgement is success. It carries the server timestamp and the message ID that was generated before the send.
- **Local record:** On success, the message is built with ingest's existing extraction from the sent protobuf (text, caption, media type and media metadata, including the upload's direct path, key and hashes). It is stored with `from_me = true` under the normalized chat and the account's own phone JID, using the ordinary store write, with a short bounded context that retirement waits for.
- **Record failure:** A failed record write reports success with `local_record: "missing"` and logs a warning. A later history sync of the same `(chat_jid, id)` updates the same row.

**Rationale:** One extraction path keeps stored sent and received messages identical in shape. The write happens before retirement continues (R1), so it never races the storage barrier.

## R10. Settings

**Decision:** Three descriptors in the existing registry:

- `send_allowed_dirs`: a list, separated by the OS path-list separator in a flag or environment variable, a TOML array in `config.toml`; default `<data dir>/outbox`.
- `send_allowed_recipients`: a list, comma-separated in a flag or environment variable, a TOML array; default empty.
- `ffmpeg`: a path; default unset.

Validation runs at startup (C4). The default outbox is created with `private.MkdirAll` by the commands that serve tools (`stdio`; `serve` in 010). An allow-list entry may not be the data directory, an ancestor of it, or inside it, except the default outbox and its subdirectories.

**Rationale:** The registry already generates flags, environment variables, TOML checks and the debug dump from one descriptor per setting.

## R11. Simulator coverage (**feasibility**)

**Decision:** Extend `internal/wasim` so that `wa`-level tests can run a real client send without a network. It answers:

- the device-list query, the prekey fetch and the registration (usync) query;
- the media-connection query, with a local upload endpoint reached through the test media transport;
- message acknowledgements.

Feasibility shows, on the simulator:

- a text and a media send succeed;
- a stop during each step of a send (lookup, upload, send waiting for the acknowledgement) passes the quiet-stop check, or the library work that remains is identified for the owner's allow-list decision;
- no send goroutine outlives retirement.

**Rationale:** The quiet-stop check sees every library goroutine in the process. A send path that leaves work behind would make every stop unsafe, so this must be proven before the tools exist.
