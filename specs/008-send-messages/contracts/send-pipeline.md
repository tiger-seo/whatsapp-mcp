# Contract: Send Pipeline and Lifecycle

## Components

| Component | Package | Holds | Never holds |
| --- | --- | --- | --- |
| Tool handlers | `internal/mcp` | argument validation, result encoding | owner, client, store writer, filesystem access |
| Send service | `internal/media` | ordered pipeline, policy, rate window, send slot, path containment, Ogg parser, converter runner, per-send working directory | owner, the session's library client |
| Send port | `internal/wa` | admission on the current generation, registration lookup, streaming upload, send, local record | MCP types, policy |
| Wiring | `cmd/whatsapp-mcp` | builds the service from config, the store read helpers and the owner's port; registers the tools | |

`internal/mcp` gets the service through an interface; the fake send port (A5) makes the service testable without a network.

## Pipeline order

Each step refuses with no later step run; see [tools.md](tools.md) for codes.

1. Validate arguments.
2. Normalize the recipient and refuse status, broadcast, newsletter, server and malformed addresses.
3. Policy: allow-list match, with LID↔phone mapping from the store in both directions.
4. Send rate window and process-wide send slot.
5. Connection check (connected; fails fast with `not_connected`).
6. Registration lookup, for a phone recipient without a stored direct chat only (a chat-existence read). A cached result is used if present; otherwise the lookup window (10 per minute, 100 per 24 hours) applies and the lookup is a port call. Results are cached for 24 hours (at most 1,000 numbers), failures are not.
7. File work, outside any port call:
   - open and validate the file;
   - check free disk space (512 MiB plus the worst case);
   - create the private working directory;
   - copy the plaintext there (context-checked, abandoned if a read is stuck);
   - analyse the Ogg data or convert.
8. Upload from the working copy (port call).
9. Send (port call). The rate window records the send at this point.
10. Record locally (port call through ingest's serialized live-write path).

Every port call is admitted individually.

**Budget**, measured from the guard's admission:

- the send has until 2:00 to reach WhatsApp's acceptance; the conversion has 60 s inside it;
- the record has at most 5 s;
- the guard deadline is 2:15, leaving at least 10 s for the reply.

## Owner admission and stop

- **Admission:** The port admits a call only in the connected phase on the current, non-retiring generation, and counts it. The call's context is derived from the generation's work context and the caller's deadline.
- **Retirement:** It cancels the work context, then waits (within the existing drain bound) until the admitted send calls have returned, including the local record write, before it blocks storage. Port calls touch only local private files and the network, never the user's source file, so a slow drive cannot hold one. The existing order (work cancel → `Disconnect` → fence → storage barrier → library context) is unchanged. A send call never outlives its generation, and a call still running at the drain bound makes the stop unsafe, as for any undrained work.
- **What the port never does:** connect, reconnect, pair or start recovery. A disconnected owner refuses with `not_connected`.
- **Shutdown order:** The MCP session's stop runs before the owner's, so in-flight tool contexts are already cancelled when retirement begins.
- **Quiet-stop evidence:** A stop during lookup, upload or the wait for the acknowledgement passed the quiet-stop check on the simulator ([feasibility](../feasibility.md)). A stop during a group send is tested in T010. Any library work that remains there needs an owner allow-list decision before the tools ship.
- **Local record:** It is written under ingest's live-write lock, with the LID relookup, so a chat merged during the send is not recreated.

## Network and resources

- **Network:** All requests go through the generation's fence. The upload request gets a deadline equal to the remaining send budget. Download requests keep their 10-second limit and 16 MiB response limit.
- **Disk:** No working file is written unless fresh free space on the data volume is at least 512 MiB plus the send's worst case. That is two times the file size for a file send, and the file size plus 200 MiB for a conversion. The process-wide slot keeps one working directory at a time.
- **Upload:** The plaintext is copied from the validated handle through a counting reader (≤ 100 MiB) into the working directory before any port call. `UploadReader` encrypts that copy into a second temporary file there.
- **Converter:** It reads the private copy under a fixed name, with `-protocol_whitelist file,pipe` and an explicit demuxer. It runs one at a time, for at most 60 s. Its stdout is counted against 100 MiB and written to `output.ogg` in the working directory. The send context cancels it by stopping its whole process tree (a Windows job object, a Unix process group), so a launcher's child cannot outlive it. The working directory is removed on every exit, and leftovers are removed at startup.
- **Concurrency:** One send per process. One tool call per MCP session, with a 2 min 15 s guard deadline for the send tools.

## Diagnostics

- **Per attempt:** Each send attempt emits one `info` audit event with the tool, the recipient kind (`phone`, `lid`, `group`, `self`), the result code, the media type and the duration. It never includes the recipient address, message text, caption, path, file contents or converter output.
- **Debug only:** Message text appears only at `debug`.
- **Warnings:** A missing local record logs a warning without content.
- **stdout:** It stays protocol-only.
