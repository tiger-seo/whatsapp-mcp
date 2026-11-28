# Implementation Plan: Sending Text, Files and Voice Notes

**Branch**: `008-send-messages` | **Date**: 2026-11-27 | **Spec**: [spec.md](spec.md)

**Input**: [Feature 008 specification](spec.md), including the 2026-11-27 clarifications: recipient allow-list, rate window, caption, send deadline and registration lookup.

**Feasibility gate**: required

## Summary

Add `send_message`, `send_file` and `send_audio_message` to the local stdio session:

- **Send service** (`internal/media`): it runs one ordered pipeline. The pipeline checks the owner's recipient allow-list and the rate window before anything else. It then admits the call through a narrow send port on the connection owner, opens the file confined to an allowed directory, analyses or converts the audio, streams the upload, sends, and records the sent message in local history.
- **Send port** (`internal/wa`): it admits sends only on the current connected generation, under the generation's work context and network fence. Retirement waits for admitted sends before storage is blocked, so the stop order and the quiet-stop rules are unchanged.
- **MCP guard**: it keeps its single tool credit and gives the send tools a 2-minute deadline.

No new dependency, no migration, no whatsmeow pin change.

## Technical Context

**Language/Version**: Go 1.27, toolchain 1.27.4, `CGO_ENABLED=0`.

**Primary Dependencies**: Existing pins in [go.mod](../../go.mod): the owner-approved whatsmeow fork `bedcf01` (`SendMessage`, `UploadReader`, `IsOnWhatsApp`), `go-sdk`, `modernc.org/sqlite`. The standard library's `os.Root` provides confined opens and `os/exec` runs the converter. ffmpeg is an optional external program the user installs, not a dependency. No new modules.

**Storage**: Existing private SQLite file. Sent messages are stored with the existing single-message upsert; no migration. The per-send private working directories live under the data directory and are removed after each send and at startup.

**Testing**:

- Unit and fuzz tests for the Ogg parser, path containment, the rate window, the policy and settings.
- In-memory MCP tests with the A5 fake send port.
- Simulator (`internal/wasim`) tests for a real client's lookup, upload and send, and for stopping during each of them.
- A stand-in converter for the limits and cleanup, plus an environment-guarded real-ffmpeg test.
- Native platform runs for path controls.

See [quickstart.md](quickstart.md).

**Target Platform**: Windows/amd64, Linux/amd64 and arm64, Darwin/amd64 and arm64. Native Windows, Linux and macOS for path and converter controls.

**Project Type**: The existing CLI binary; three tools and three settings added.

**Performance Goals**:

- A send reaches WhatsApp's acceptance or is stopped within 2 minutes; conversion within 60 s. The record takes at most 5 s, and the guard deadline of 2 min 15 s leaves 10 s for the reply.
- A stopped converter and its files are gone within 5 s.
- A 100 MiB upload streams with memory that does not grow with file size (measured in feasibility).

**Constraints**:

- Stop order and quiet-stop invariants unchanged.
- No reconnect, pairing or recovery reachable from a tool.
- No content in logs above `debug`.
- stdout protocol-only.
- Every refusal before the hand-over to WhatsApp has no upload, no send and no store write.
- The session's 64 KiB input-frame and parameter limit (006) is raised to 512 KiB for a call of a send tool only,
  so that the 65,536-byte message and caption can arrive; everything else keeps 64 KiB. Owner decision 2026-11-27
  ([tools contract](contracts/tools.md#input-size)).

**Scale/Scope**:

- 3 tools.
- 1 send in flight per process.
- 10 sends per minute, 200 per rolling 24 hours.
- 10 registration lookups per minute, 100 per rolling 24 hours; results cached 24 hours (at most 1,000 numbers).
- Working files only with fresh free space ≥ 512 MiB plus the send's worst case.
- Files 1 byte to 100 MiB; text and caption up to 65,536 bytes.

## Constitution Check

*Gate evaluated before research and again after design against the contracts. This is a planning assessment, not runtime evidence or an owner verdict.*

| Principles | Design assessment and required evidence |
| --- | --- |
| I, VII | Traces spec FR-001–FR-017 and SC-001–SC-005 to project P1, P6, M1, M4–M6, M10, §3.8 and the 2026-11-27 §8.11 decision. Real defaults (`caption` `""`), snake_case, structured plus text results, tool errors for refusals ([tools.md](contracts/tools.md)). D1–D18 unchanged. |
| II, IX | One module, no new dependency (`os.Root` is standard library), five targets. ffmpeg stays optional and external, as P6 already states. Three settings, each required by M1 or the owner's F01 decision; no compatibility layer. |
| III, VIII | Sends go through the single owner's port on the current generation; no second client. Automated tests use the fake and the simulator only. Real-account steps are listed in [quickstart.md](quickstart.md) and each needs the owner's permission. |
| IV | Content only at `debug`; one redacted audit event per attempt; stdout untouched ([send-pipeline.md](contracts/send-pipeline.md#diagnostics)). |
| V, X | F01 is resolved for sending by the owner's allow-list decision. F05 is handled by confined opens on the handle, link and kind checks, and native abuse tests. F06 is handled by the bounded parser with a fuzz test, and by the converter's protocol and demuxer restriction, limits and cleanup. F09 is handled by the size, time, concurrency, send-rate and lookup-rate bounds on consumed bytes, and by a disk bound (R12) that keeps the store above its write reserve. F03 is handled by private working directories. No High finding remains open for the enabled capability; OS sandboxing of ffmpeg is a documented residual risk (F06), not an exception. No receive-resource exception. |
| VI | Each layer is tested separately: unit, fuzz, MCP in-memory, simulator, native. Platform controls are reported per platform, untested ones as not verified. |

**Feasibility gate (required before `/speckit-implement`)**: Run as scratch work outside `/speckit-implement`, with no production wiring, and record in a committed `feasibility.md` marked `**Status**: Verified` only when all items pass:

1. **Simulator send (R11)**: lookup, streaming upload and send of text and media on `internal/wasim`. A stop during each step passes the quiet-stop check, or the leftover library work is identified for an owner allow-list decision.
2. **Upload bound (R2)**: a per-request upload deadline in the fenced media transport, with download bounds unchanged and a 100 MiB upload completing locally. Peak memory with `UploadReader` (R3).
3. **Containment (R4)**: `os.Root` against the abuse list, natively on Windows, Linux and macOS.
4. **Converter (R6)**: the ffmpeg flag set with every allowed input (including m4a with its index at the end), refusal of a disguised playlist or concat file, the stall and output limits, versions recorded.

Then align the contracts and tasks, and request a fresh owner design `PASS` on that commit.

**Post-design re-check**: The contracts add no deviation. Feasibility changed three details, all within the spec:

- Paths are resolved before the confined open, so a link between allowed directories is accepted as FR-006 says.
- The converter's output goes through a counted pipe.
- Junction paths fail closed on Windows.

See [feasibility.md](feasibility.md).

## Project Structure

### Documentation (this feature)

```text
specs/008-send-messages/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── contracts/
│   ├── tools.md
│   └── send-pipeline.md
├── quickstart.md
├── checklists/requirements.md   # ignored by git
├── tasks.md          # later, /speckit-tasks
├── feasibility.md    # required before implementation
└── verification.md   # evidence and owner review log
```

### Source Code (repository root)

```text
internal/media/        # new: send service, pipeline, policy, rate window, path containment, Ogg parser, converter
internal/wa/           # send port on the owner, admission and drain, session lookup/upload/send, local record, fake port
internal/wasim/        # simulator: device list, prekeys, usync, media connection, upload endpoint, acks
internal/mcp/          # three tool handlers, schemas, result DTOs, error codes, per-tool guard deadline, audit
internal/config/       # three setting descriptors and validation
cmd/whatsapp-mcp/      # wiring for stdio, outbox creation, startup cleanup of working directories, native tests
.github/workflows/     # native path tests and the guarded converter test where ffmpeg is available
```

**Structure Decision**: `internal/media` follows the A4 layout and owns everything that touches files and the converter. It reaches the account only through the `wa` send port interface. `internal/mcp` gets the service interface, never the owner, the client or the store writer. `serve` gets the tools in feature 010; the send service and its process-wide limits are built so that 010 can share them.

## Complexity Tracking

No constitutional deviations proposed.
