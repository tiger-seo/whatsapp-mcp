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
4. Rate window and process-wide send slot.
5. Port admission: connected, generation current and not retiring, owner not stopping.
6. Registration lookup, for a phone recipient without a stored direct chat only.
7. Open and validate the file, analyse the Ogg data or convert, then stream the upload.
8. Send. The rate window records the send at this point.
9. Record locally.

The whole call runs under a 2-minute deadline from its start. The conversion has its own 60-second limit inside it.

## Owner admission and stop

- **Admission:** The port admits a call only in the connected phase on the current, non-retiring generation, and counts it. The call's context is derived from the generation's work context and the caller's deadline.
- **Retirement:** It cancels the work context, then waits (within the existing drain bound) until the admitted send calls have returned, including the local record write, before it blocks storage. The existing order (work cancel → `Disconnect` → fence → storage barrier → library context) is unchanged. A send call never outlives its generation, and a call still running at the drain bound makes the stop unsafe, as for any undrained work.
- **What the port never does:** connect, reconnect, pair or start recovery. A disconnected owner refuses with `not_connected`.
- **Shutdown order:** The MCP session's stop runs before the owner's, so in-flight tool contexts are already cancelled when retirement begins.
- **Quiet-stop evidence:** A stop during lookup, upload or the wait for the acknowledgement must pass the quiet-stop check on the simulator. Any library work that remains needs an owner allow-list decision before implementation (see [feasibility](../plan.md#constitution-check)).

## Network and resources

- **Network:** All requests go through the generation's fence. The upload request gets a deadline equal to the remaining send budget. Download requests keep their 10-second limit and 16 MiB response limit.
- **Upload:** It streams from the validated handle through a counting reader (≤ 100 MiB). The encrypted temporary file lives in the per-send private working directory.
- **Converter:** It reads a size-checked private copy under a fixed name, with `-protocol_whitelist file` and an explicit demuxer. It runs one at a time, for at most 60 s, and its output is checked against 100 MiB. The send context cancels it by stopping its whole process tree (a Windows job object, a Unix process group), so a launcher's child cannot outlive it. The working directory is removed on every exit, and leftovers are removed at startup.
- **Concurrency:** One send per process. One tool call per MCP session, with a 2 min + 5 s guard deadline for the send tools.

## Diagnostics

- **Per attempt:** Each send attempt emits one `info` audit event with the tool, the recipient kind (`phone`, `lid`, `group`, `self`), the result code, the media type and the duration. It never includes the recipient address, message text, caption, path, file contents or converter output.
- **Debug only:** Message text appears only at `debug`.
- **Warnings:** A missing local record logs a warning without content.
- **stdout:** It stays protocol-only.
