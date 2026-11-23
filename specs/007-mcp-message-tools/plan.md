# Implementation Plan: MCP Message Tools

**Branch**: `007-mcp-message-tools` | **Date**: 2026-11-22 | **Spec**: [spec.md](spec.md)

**Input**: [Feature 007 specification](spec.md), including the clarification on unfinished searches.

**Feasibility gate**: required

## Summary

Add `list_messages`, `get_message_context` and `get_last_interaction` to the existing owner-launched local stdio session. Extend its store-owned read facade with bounded, read-only message queries. Verify a page before shaping the reply: an unfinished search returns an error with no data; a verified context page may return fewer complete matches or neighbours, or a marked preview in the exceptional first-match case. Reuse the guarded MCP transport and keep all account actions outside the tools.

## Technical Context

**Language/Version**: Go 1.27, toolchain 1.27.4, `CGO_ENABLED=0`.

**Primary Dependencies**: Existing pins in [go.mod](../../go.mod): `go-sdk` v1.8.0, `jsonschema-go` v0.4.3, `modernc.org/sqlite` v1.62.0 and `golang.org/x/text` v0.43.0. No new dependencies or whatsmeow pin change.

**Storage**: Existing private SQLite file, embedded migrations and single process lock. Reuse message schema, local contact/LID mapping and sender-name precedence. No migration is planned; verify the existing indexes can support all required bounded shapes before implementation.

**Testing**: Real SQLite in temporary private directories, guarded SDK in-memory transport, and native stdio regressions with synthetic account fixtures. See [quickstart.md](quickstart.md).

**Target Platform**: Windows/amd64, Linux/amd64 and arm64, Darwin/amd64 and arm64; native Windows, Linux and macOS for runtime controls.

**Project Type**: One existing CLI binary; no new service, transport or runtime configuration.

**Performance Goals**: Default 50-match maximum-field-size context fixture completes within the existing 10-second tool budget; the store request remains subject to the 5-second cancellation trigger. Limit tests distinguish cancellation requested from actual return.

**Constraints**: Preserve store/admission, wire, stderr, shutdown and WAL controls. Use a single bounded snapshot per call and retain accounting through actual release. The local owner read boundary stays unchanged. No real account access.

**Scale/Scope**: Three read-only tools; base page 1–200, checked offset at most 100,000, context at most 20 per side and 2,000 context rows per call. Require complete results for the representative sparse and no-match 400,000-message fixtures and the large-field history fixture before accepting production search-work ceilings. This is not an unbounded history scan guarantee. See [research.md](research.md) and the [store contract](contracts/store-and-lifecycle.md).

## Constitution Check

*Gate evaluated before research for scope and again after design against the contracts. This is a planning assessment, not runtime evidence or an owner verdict.*

| Principles | Design assessment and required evidence |
| --- | --- |
| I, VII | Preserve project S3, S6, P1, M4–M7, M10, D9 and D18; trace FR-001–FR-012 and SC-001–SC-005 through the tool contract and acceptance fixtures. Validate real defaults, identity, ordering and message shape through the SDK. |
| II, IX | One pure-Go module and binary; no dependency or account backend change. Retain the five build targets; no new configuration, API transport or compatibility layer. |
| III, VIII | Single store owner and lock, no network/account capability passed to tools, no message or identity writes; verify on synthetic data only. |
| IV, V, X | Preserve private storage and existing local-read scope (F01/F03), bound search and encoding without bypassing admission or drain (F09/F10), and use redacted diagnostics (F11). The [store contract](contracts/store-and-lifecycle.md) specifies enforcement and evidence gates. No exception, deployment approval, or broader finding closure is claimed. |
| VI | Verify store, SDK and native transport layers separately, plus mixed reader/ingest schedules; cross-builds do not substitute for native tests. Record commands, dates, counts, exit status and unverified platforms in `verification.md` as evidence is gathered. |

**Post-design gate**: No design deviation assumed. Run T001–T003 as synthetic scratch work outside `/speckit-implement`, with no production wiring. Put results and fixed, measured production ceilings in a committed `feasibility.md`, mark it `**Status**: Verified` only when all feasibility criteria pass, and align the contract and tasks before requesting a fresh owner design `PASS` on that commit. The implementation hook denies both missing/stale evidence and `CONCERNS`, even with a recorded decision. Record the verdict in `verification.md`; the evidence itself is not exempt from review freshness. A current code review and native matrix evidence are required before PR creation/merge.

## Project Structure

### Documentation (this feature)

```text
specs/007-mcp-message-tools/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── contracts/
│   ├── tools.md
│   └── store-and-lifecycle.md
├── quickstart.md
├── tasks.md          # later, /speckit.tasks
├── feasibility.md    # required before implementation; reviewed evidence, not yet verified
└── verification.md   # planning evidence and owner review log
```

### Source Code (repository root)

```text
internal/mcp/          # registration, strict arguments, public DTOs, output accounting and SDK tests
internal/store/        # read facade, bounded message queries, SQLite and concurrency tests
cmd/whatsapp-mcp/     # reuse lifecycle wiring; native synthetic stdio integration tests
.github/workflows/    # existing native CI and cross-build gate
```

**Structure Decision**: Add the methods to the existing narrow read boundary; the MCP package does not get a SQLite handle, writer, WhatsApp client or owner. Leave normal store queries and the five existing chat/contact tools unchanged. Any shared helper refactor requires parity tests. The command layer changes only if necessary to wire the facade and to cover native regressions.

## Complexity Tracking

No constitutional deviations proposed.
