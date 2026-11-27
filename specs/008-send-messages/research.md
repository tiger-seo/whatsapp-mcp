# Research: Sending Text, Files and Voice Notes

These findings come from reading the code at `163b114` and the pinned whatsmeow fork (`tiger-seo/whatsmeow@bedcf01`). They describe the design to verify; nothing here is measured yet. Items marked **feasibility** need scratch evidence in `feasibility.md` before implementation (see the plan's gate).

## R1. Who performs a send

**Decision:** The connection owner gets a narrow send port. The MCP tools receive a send service built on that port, never the owner. The port lets the service:

- ask whether a phone number is registered,
- upload media from a private working copy (R3),
- send a message,
- record the sent message locally.

Each call is admitted only while the owner is connected, its current generation is not retiring and the owner is not stopping. An admitted call runs under a context derived from that generation's work context. The owner counts admitted send calls, as it tracks the repair pass. Retirement cancels the work context, then waits for admitted send calls to return (bounded by the existing drain bound) before `Retire` blocks storage. Every network path of the call already runs through the generation's fence. Port calls read only local private files, never the user's source file, so no port call can be held by a slow or stuck drive.

**Evidence:** On the simulator, a send held at any step returned as soon as retirement cancelled the work context: before `Disconnect`, the fence and the storage barrier. The quiet-stop check then passed with no library work left ([feasibility.md](feasibility.md#item-1-simulator-send)).

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

**Decision:**

- **Source stage:** Everything that touches the user's file runs in one goroutine that the handler can abandon: the syntactic check, `EvalSymlinks`, the confined open, the handle checks, the disk check (R12, which needs the size from the handle), and the copy into the private per-send working directory. A hung network share can block any of these in the kernel; `EvalSymlinks` alone does an `Lstat` per path component. All of it happens before any port call.
- **Copy limit:** The copy is limited to the size seen on the opened handle plus one byte. More bytes than that refuses the send as `file_refused` ("the file changed while it was read"), so a growing file is never sent and never exceeds the disk check. The copy also checks the call's context between 1 MiB chunks.
- **Abandoned stage:** If the stage does not return, the handler still returns on cancellation, so the MCP stop step and shutdown are never held. The process-wide send slot stays taken until the stage goroutine actually returns. While a stage is stuck, later calls get `busy` ("a previous file read has not finished"). So at most one stuck goroutine, handle and working directory exist at a time. The startup sweep removes a leftover directory.
- **Upload:** The library's streaming upload (`UploadReader`) then reads that local copy and encrypts it into a second temporary file in the same directory.
- **Cleanup:** The working directory is removed on every exit path.

**Rationale:**

- **Memory:** The byte-slice `Upload` holds the plaintext and the ciphertext in memory, about 200 MiB at the size limit. Streaming keeps memory flat (feasibility item 2: +0 MiB).
- **Cancellation:** `UploadReader` encrypts the whole input (`cbcutil.EncryptStream`) before it looks at the context again. On the user's file, a slow or stuck drive (a mapped network drive, a USB disk) would hold an admitted port call past the retirement drain bound and make the stop unsafe. Reading a local private copy keeps every admitted call short (design review of `3254097`).

**Alternatives considered:** `Upload` with a 100 MiB buffer. It is simpler, but its peak memory depends on the input size.

## R4. Path containment (**feasibility** on each platform)

**Decision:** The call-time steps below run inside the abandonable source stage of R3. When sending can happen (R10), each `send_allowed_dirs` entry is resolved at startup to an absolute real path, symlinks included, and must be an existing directory. Its `os.Root` is opened then and kept for the life of the process, so a root later swapped for a link cannot redirect an open. At call time:

- `media_path` must be absolute and pass a syntactic check that refuses UNC, device and `\\?\` paths, alternate data streams (`:` after the volume), NUL and empty components.
- The path is resolved with its links followed (`filepath.EvalSymlinks`). The allowed root that contains the resolved path is chosen, and the resolved name relative to that root is opened through that root's kept `os.Root`. A link or junction swapped in after resolution makes the confined open fail ("path escapes from parent"), so the open can never leave the root. On Unix the open adds `O_NONBLOCK`, so a FIFO swapped in cannot block it. On Windows, `os.Root` refuses reserved device names (`CON`, `NUL.txt`).
- The open handle must be a regular file with exactly one link, and its size must be 1 byte to 100 MiB.

All later reads come from this one handle. Feasibility ran the abuse list on Windows, WSL2 and the native CI runners ([feasibility.md](feasibility.md#item-3-containment)).

**Rationale:** A check on the path string alone is racy (security review F05). `os.Root` (Go 1.24+, available with the pinned Go 1.27) is the standard library's race-resistant confined open on every target. It needs no new dependency. Resolving first means a link from one allowed directory into another is accepted, as FR-006 says.

**Fail-closed consequences:**

- On Windows, a path through a junction does not resolve with the pinned Go, so it is refused, even when the junction points inside the root.
- An allow-list entry whose path contains such a junction fails at startup, but only when sending is enabled (R10), so a user whose profile sits behind a junction can still run `stdio` with sending off.
- A trailing dot or space on a Windows file name opens the same file inside the root, which is harmless.

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
  - `-nostdin -hide_banner -loglevel error`, and `-protocol_whitelist file,pipe` so the input cannot reach the network or other protocols;
  - an explicit demuxer chosen from the extension (mp3, wav, mov for m4a, aac, flac, ogg for ogg and opus, amr), with the input named `input`, never the user's name;
  - `-map 0:a:0 -vn -c:a libopus -b:a 32k -ar 48000 -application voip -vbr on -frame_duration 60 -f ogg pipe:1`.
- **Process:** The converter runs in the working directory, with an empty environment (on Windows only `SystemRoot`). It runs as a process tree that is stopped as a whole:
  - On Windows it is started suspended, placed in a job object that kills every process in it when terminated or closed, and then resumed (`golang.org/x/sys/windows`, already a module dependency).
  - On Unix it gets its own process group, which is killed as a group.

  Feasibility found that a package-manager launcher (the Chocolatey `ffmpeg.exe` shim on the Windows runner) does not pass a kill on. With a plain process kill, the real converter outlived every limit and kept the working directory open. The configured or found `ffmpeg` may be such a launcher, so the tree handling is required, not optional.
- **Limits:**
  - one conversion at a time, for at most 60 s;
  - the output, read from the converter's stdout, is counted as it arrives, stopped at 100 MiB, and written to `output.ogg` in the working directory, so memory stays flat (the disk check of R12 counts it);
  - cancellation through the send context, then `Kill` with a 2-second `WaitDelay`;
  - the working directory is removed on every exit path.

  Feasibility found that polling the size of an output file does not work on Windows (the reported size lags while ffmpeg writes), so the output goes through the counted pipe.
- **Output:** It is then parsed by R5 and sent as a voice note.
- **Protocol list:** `-protocol_whitelist file,pipe` stays as verified in feasibility. It is an input option, and the output goes to `pipe:1`.

Feasibility ran the real ffmpeg with every allowed input, disguised playlist and concat files, and the time, output and cancellation limits ([feasibility.md](feasibility.md#item-4-converter)).

**Rationale:** F06 asks for a trusted executable, no shell, restricted protocols and demuxers, time, size and concurrency limits, private working files and cleanup. An input copy (rather than a stdin pipe) lets demuxers that need seeking (m4a with its index at the end) work, while the converter still sees only validated bytes under a fixed name. The Ogg muxer streams, so the output can use a pipe.

**Alternatives considered:**

- Piping through stdin. This fails for m4a files whose index is at the end.
- Passing the user's path. That lets a swapped file be read, or a crafted name be read as an option.
- Operating-system sandboxing. It is platform-specific and documented as a residual risk instead (spec Assumptions).

## R7. MCP admission and the 2-minute deadline

**Decision:** Keep the existing guarded transport: one tool call in flight per session, at most four active requests. The guard already knows the tool name at admission. For the three send tools it sets the request deadline to 2 minutes 15 seconds instead of 10 seconds. One budget, measured from the guard's admission time. The guard passes that time to the handler in the request context, and the service derives its own deadlines from it, so dispatch delay is not taken from the reply's share. The guard's clock and timers become injectable, and the budget test uses a fake clock rather than real minutes.

- the send has until 2:00 to reach WhatsApp's acceptance;
- the local record then has at most 5 s, so the handler returns by 2:05;
- 10 s remain for encoding and writing the reply.

A test acknowledges a send at 1:59.9, delays the record for its full 5 s, and checks that the reply arrives with no cancellation and no closed connection. A delivered message must never look like a transport failure, which would invite a resend (design review of `3254097`). While a send is in flight, any other tool call on that session is refused as `busy` by the existing guard path. This satisfies FR-012's one send at a time for the local session. The send service also holds a process-wide one-send slot for `serve` (feature 010).

**Rationale:** The guard's single tool credit and deadline handling are the reviewed admission mechanism (006 R2). A per-tool deadline is the smallest change. Reads are blocked for at most the length of a send, which is acceptable for a single local client that calls tools one at a time.

**Alternatives considered:** A separate send credit, so that reads and a send run together. It doubles the guard's concurrency states for little benefit to a sequential client. Reconsider in 010 if HTTP clients need parallel reads during sends.

## R8. Policy, rate limit and order of checks

**Decision:** One ordered pipeline; each refusal ends the call with no later step run.

1. Arguments.
2. Recipient normalization and refused address kinds.
3. Send policy: allow-list membership, with LID↔phone mapping in both directions from the local store and no guessing.
4. Rate limit: 10 per rolling minute, 200 per rolling 24 hours, in memory; and the process-wide send slot.
5. Connection.
6. Registration lookup, only for a phone recipient with no stored direct chat (a chat-existence read, named or not). It does not count toward the send rate limit. Its own rules:
   - a cached result (registered with its LID, or unregistered) is used for 24 hours, at most 1,000 numbers with the oldest evicted first;
   - otherwise the lookup's own window applies: 10 per rolling minute, 100 per rolling 24 hours, at most 100 timestamps;
   - a failed lookup is neither cached nor retried.
7. File: open and validate, the disk check (R12), the working copy (R3), conversion, upload.
8. Send.
9. Local record.

A send counts toward the limit when it is handed to WhatsApp (step 8 begins), whatever its outcome. The limiter stores at most 200 timestamps. A lookup counts when it is sent, whatever its outcome.

**Rationale:** This follows FR-002, FR-003 and FR-012 and the clarified order: no file is opened, nothing converted and no network contact made before the policy and the limit pass.

## R9. Outcome and local record

**Decision:**

- **Outcome:**
  - Only two kinds of error are a failure (`send_failed`): WhatsApp's own refusal (an error code in the acknowledgement), and errors from a fixed list known to happen before the node is written, such as recipient not on WhatsApp, a failed device or prekey fetch, or a failed encryption.
  - Once the library call to send has started, anything else is "outcome unknown", because the node may have left: cancellation, a deadline, a disconnect, or an unrecognised error. No automatic retry.
  - A server acknowledgement is success. It carries the server timestamp and the message ID that was generated before the send.
- **Local record:** On success, the message is built with ingest's existing extraction from the sent protobuf (text, caption, media type and media metadata, including the upload's direct path, key and hashes). It is stored with `from_me = true` under the normalized chat and the account's own phone JID. The write goes through ingest's serialized live-write path: the ingest lock, then `relookup` of any LID chat under the lock, then the store write, all within a 5-second bound (or less, to fit the budget) that retirement waits for. The existing `lockForLive` waits on a bare channel send with no context; a history flush or a merge can hold the lock for about 30 s. So the record takes the lock through a new cancellable variant: a `select` on the lock and `ctx.Done()`, with `liveWaiting` decremented on both paths. A record that cannot get the lock in time returns "record missing", and retirement never waits on it (design review of `2596af3`). So a LID chat merged into its phone chat during the send is not recreated (design review of `3254097`; AGENTS.md "Late Name Offers Can Recreate Merged Chats").
- **Record failure:** A failed record write reports success with `local_record: "missing"` and logs a warning. A later history sync of the same `(chat_jid, id)` updates the same row.

- **Recipient addressing:** The pinned library sends a direct message to the recipient's LID. For a phone recipient without a stored LID mapping, it first runs its own user-info lookup inside `SendMessage`, and refuses an unregistered number itself ("not on WhatsApp"). The registration lookup of R8 step 6 stores the LID mapping as a side effect, so the send that follows does not repeat it. Because that library lookup would otherwise run only after the upload, the early lookup still saves a useless upload. The local record uses the normalized phone chat (spec FR-002), not the LID.

**Rationale:** One extraction path keeps stored sent and received messages identical in shape. The write happens before retirement continues (R1), so it never races the storage barrier.

## R10. Settings

**Decision:** Three descriptors in the existing registry:

- `send_allowed_dirs`: a list, separated by the OS path-list separator in a flag or environment variable, a TOML array in `config.toml`; default `<data dir>/outbox`.
- `send_allowed_recipients`: a list, comma-separated in a flag or environment variable, a TOML array; default empty.
- `ffmpeg`: a path; default unset.

Validation runs at startup (C4). Every entry is checked for syntax.

When sending can happen (`send_allowed_recipients` not empty), each entry is also:

- resolved;
- compared with the data directory by identity: the entry and each of its ancestors with `os.SameFile` against the data directory, and the data directory's ancestors against the entry. Path text is not enough, because on a case-insensitive macOS volume a different spelling would pass a text comparison (design review of `3254097`).
- opened as an `os.Root` and kept (R4).

An entry may not be the data directory, an ancestor of it, or inside it, except the default outbox and its subdirectories. Only then is the default outbox created with `private.MkdirAll`, by the commands that serve tools (`stdio`; `serve` in 010). With sending off, `stdio` starts without touching the allowed directories.

**Rationale:** The registry already generates flags, environment variables, TOML checks and the debug dump from one descriptor per setting.

## R11. Simulator coverage (**feasibility**)

**Decision:** Extend `internal/wasim` so that `wa`-level tests can run a real client send without a network. It answers:

- the device-list query, the prekey fetch and the registration (usync) query;
- the media-connection query, with a local upload endpoint reached through the test media transport;
- message acknowledgements;
- the user-info lookup that returns a LID (`<lid val=…>`), with the registration lookup answering `jid` = LID and `pn_jid` = phone for a registered number.

The feasibility scratch test answered all of these from a test IQ handler and a routed upload endpoint, without changing `internal/wasim` ([feasibility.md](feasibility.md#item-1-simulator-send)). Implementation moves them into the simulator.

Feasibility shows, on the simulator:

- a text and a media send succeed;
- a stop during each step of a send (lookup, upload, send waiting for the acknowledgement) passes the quiet-stop check, or the library work that remains is identified for the owner's allow-list decision;
- no send goroutine outlives retirement.

Group sends (group info, sender-key distribution) and a stop during them were not exercised in feasibility. T010 covers them on the simulator. If library work remains after such a stop, that is an owner decision on the quiet-stop allow-list, taken before the tools ship.

**Rationale:** The quiet-stop check sees every library goroutine in the process. A send path that leaves work behind would make every stop unsafe, so this must be proven before the tools exist.

## R12. Disk space for working files

**Decision:**

- **Check:** Before any working file is written, the send service measures free space on the data directory's volume afresh, with the store's own measurement function but not its 30-second cached value. If the measurement fails, the send is refused with `insufficient_space`. The store's guard lets writes through on a failed measurement, but a send must not, because nothing would then bound its working files. It requires at least the store's resume threshold (512 MiB) plus the send's worst case:
  - a file send: two times the file size (the plaintext copy and the ciphertext, plus 1 MiB for padding and MAC);
  - a conversion: the file size (input copy) plus two times 100 MiB (the converter's output, and the ciphertext of that output);
  - a text send: nothing, so no check.

  Otherwise the call is refused with `insufficient_space`, before the working directory is created, with no side effect.
- **Working directory:** The process-wide send slot means one working directory exists at a time. It is removed on every exit path and swept at startup.

**Rationale:**

- **Disk faults:** The store refuses guarded writes below 256 MiB free and latches a device-write fault. That fault retires the generation, keeps the run marker unsafe and requires offline recovery and a new pairing; a message-store refusal also ends ingest.
- **Who drives the size:** The assistant chooses the file, so the size of a send's working files is input-driven, and Constitution V requires a disk limit on it. Requiring the resume threshold plus the worst case means that a send, if nothing else writes meanwhile, leaves at least 512 MiB free, above the 256 MiB reserve.
- **Shared data folder:** The working files stay in the private data directory (F03), so the bound is placed on them rather than moving them to another volume.

**Alternatives considered:**

- Working files in the system temporary directory. This avoids the data volume, but may not be private and may share the volume anyway.
- Relying on the store's guard. That guard reacts after the fact, by faulting, which is what has to be avoided.

**Test:** A negative test injects a low free-space value and checks that the call is refused with no working directory, no upload, no send and no store write.
