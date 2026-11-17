# Implementation Plan: MCP Over Stdio: Chat and Contact Tools

**Branch**: `006-mcp-stdio-tools` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-mcp-stdio-tools/spec.md`

**Status**: Phase 0 research and Phase 1 design complete. No implementation or runtime acceptance has run. Next is task generation, followed by the required committed design review before implementation.

## Summary

Expose five read tools through the official MCP SDK: `list_chats`, `get_chat`, `search_contacts`, `get_direct_chat_by_contact` and `get_contact_chats`. Use the existing store, local identity mappings, owner and lock. Local owner launch authorizes all stored chat/contact reads; no per-chat policy, login tool, QR disclosure, pairing opt-in, HTTP listener or proxy is added.

Add `internal/mcp` with explicit schemas, independently validated inputs, public result DTOs, safe JSON text and structured content. Place admission before SDK dispatch, not only in handlers. Use one process-lifetime stdio connection, four outstanding protocol requests, one admitted tool operation through response completion, and no tool queue. Bound framing, JSON shape, encoding and slow consumers. Reuse existing store queries with small correctness changes and a fail-fast read admission path; keep ingest write failures terminal. Integrate EOF, transport failures and stdio-only device-fault termination into the existing safe stop sequence.

## Technical Context

**Language/Version**: Go 1.27.0, toolchain `go1.27.4`; `CGO_ENABLED=0` for every build/test in this feature.

**Primary Dependencies**: Add `github.com/modelcontextprotocol/go-sdk v1.8.0` (stable release, Go 1.25 minimum), required by D15. Use its public MCP, JSON-RPC and in-memory APIs. Accept only reviewed pure-Go transitive additions; run module graph/integrity/vulnerability gates before implementation acceptance. Existing SQLite driver and WhatsApp fork remain unchanged. Do not upgrade `go.mau.fi/libsignal`, whatsmeow or the Go toolchain as part of this work.

**Storage**: Existing private `whatsapp.db` and device/contact tables; no migration, new result files or second database. A store-owned tool reader uses the same query implementations and cancellation/close accounting, with nonblocking shared-gate admission. It exposes no writes, device container or account operations.

**Testing**: Official SDK in-memory transport plus seeded temporary SQLite stores; fake owners and counted query interfaces; raw-frame tests for the custom connection; subprocess pipe tests on native Windows/Linux/macOS; existing ingest/quiet-stop/lock regressions. Native platform I/O behavior must be tested, not inferred from cross-compilation.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

**Project Type**: One CLI binary, one module, one account and one data-directory owner. The MCP transport is process-lifetime, not a reusable embedded-session framework.

**Performance Goals**: For unchanged synthetic data, stable paging of 235 chats at default and maximum list sizes; exact alias matching and no duplicate chat/person results. One MCP snapshot at a time, no queued tool reads, no MCP-added store waiters. A tool operation has a 10 s end-to-end budget; its SQLite snapshot has a 1 s budget. Oversized or expensive reads fail clearly rather than producing partial successful pages. These are resource ceilings, not promises of successful large-store latency.

**Constraints**: Proposed fixed bounds in [contracts/stdio.md](contracts/stdio.md): 64 KiB input frames, depth 16, four outstanding request IDs, one tool through response completion, 1,024 forwarded notifications per session, 2 MiB maximum encoded structured result and 2 MiB safe text, 8 MiB final response, 10 s partial-frame assembly, 5 s output completion. Two fixed protocol I/O pumps plus one fixed nonblocking-diagnostic pump; diagnostics queue 16 records of at most 4 KiB each. Existing store limits remain 200 rows, offset 100,000, 32 MiB result-field budget, 16 active/64 waiting operations and four reader connections. No receive-resource exception is inherited.

**Scale/Scope**: Five chat/contact tools and one stdio session. The store still retains messages indefinitely. Tool text/previews may be retained by the client's AI provider; local all-data authorization does not make prompt injection or downstream privacy risk disappear.

## Constitution Check

Checked against constitution 3.0.0 before research and re-evaluated after Phase 1. PASS below means design conformance, not tested implementation or owner-review approval.

| Principle | Pre-design | Post-design | Basis |
| --- | --- | --- | --- |
| I. Specification | PASS | PASS | Owner decisions in spec clarifications and source section 8.11 authorize all local reads and defer both login tools. Contracts implement the five-tool scope, literal queries and exact identity matching. No D1-D18 change. |
| II. One binary | PASS | PASS | One module; mandated SDK is pure Go; five CGO-free builds gate delivery. No subprocess/service required at runtime. |
| III. One owner | PASS | PASS | Same lock, store and owner; no schema or path change. Tool work drains before store close/lock release. |
| IV. Clean channel | PASS | PASS | Only framed MCP on stdout; secret-free diagnostics on stderr; no raw payload logging. Unix SIGPIPE must not bypass safe cleanup. |
| V. Secure defaults | PASS: bounded design required | PASS: bounded design specified | Pre-SDK admission, shape/byte limits, one snapshot, fail-fast store entry, bounded output and process-lifetime pumps. Limits count consumed/encoded data; OS-blocked pumps are fixed in number and cannot touch owner/store. See research R2-R5. No unbounded queue or new exception. |
| VI. Testability | PASS | PASS | All five tools through SDK in-memory transport; separate real-framing and subprocess tests; fake owner and temporary store only. |
| VII. Tool correctness | PASS | PASS | Real schema defaults plus handler defaults, snake_case, explicit false, RFC 3339, structured plus faithful text, clear errors and composite latest-message identity. |
| VIII. Real systems | PASS | PASS | No live account step required or authorized. A saved-session production stdio start may connect, so validation never uses the owner's real directory. |
| IX. Simplicity | PASS | PASS | Five tools, one narrow store facade, one connection, no config options or speculative restriction model. Custom framing is needed because the SDK dispatcher queues before middleware. |
| X. Security per feature | PASS: design must address enabled High findings | PASS: controls specified, evidence pending | F01's local read-policy question is decided by the owner; pairing/sending absent. F03 inherited private-store checks; F09 bounded paths; F15 dependency gates. Full disposition in spec and research R8. No claim that all F01 or F09 project work is closed. |

**Delivery gates, not waivers**: Resource values and compatibility trade-offs require review at the normal design checkpoint; implementations must satisfy the proposed ceilings or return for design revision. Process-level EOF/blocked-pipe/SIGPIPE behavior must pass on Windows, Linux and macOS before claiming those platforms verified. Flood tests must show no MCP-admission-induced ingest stop. A failed test blocks delivery; it is not authority to relax limits, retry ingest writes or retain unbounded work. The all-data policy is the selected product authorization boundary, not a general security exception.

## Project Structure

### Documentation (this feature)

```text
specs/006-mcp-stdio-tools/
  spec.md
  plan.md
  research.md
  data-model.md
  contracts/
    tools.md
    stdio.md
    store-and-lifecycle.md
  quickstart.md
  verification.md
  checklists/requirements.md
  tasks.md                       # generated later by /speckit-tasks
```

### Source Code (repository root)

```text
cmd/whatsapp-mcp/
  commands.go                    # stdio MCP integration, device-fault watcher
  lifecycle.go                   # retain existing cleanup machinery
  mcp_stdio_test.go              # command/fake-owner/lock/exit tests
  mcp_pipe_test.go                # real subprocess pipe tests
internal/mcp/
  server.go                      # identity, five registrations and middleware
  tools.go                       # explicit schemas, defaults, input/error mapping
  results.go                     # public DTOs, preflight sizing, safe text
  transport.go                   # frame/credit limits, fixed pumps, stop signal
  connection.go                  # common SDK connection guard, also in-memory
  diagnostics.go                 # bounded nonblocking stderr sink/pump
  signal_unix.go                 # SIGPIPE installed before runShell diagnostics
  signal_windows.go              # platform counterpart, no Unix signal use
  *_test.go                      # SDK in-memory and bounded-transport tests
internal/store/
  admit.go                       # fail-fast entry without waiting allocation
  ops.go                         # read admission policy and actual drain
  tool_reader.go                 # five read-only operations, shared SQL paths
  queries.go                     # withLatest, folded JID, contact membership
  contacts.go                    # literal query and supported person domains
  *_test.go                      # shared semantics, admission and cancellation
go.mod / go.sum                  # exact SDK and reviewed transitive modules
OPERATIONS.md                    # implemented vs verified, stdio limits/privacy
```

**Structure Decision**: Keep all protocol logic in `internal/mcp`, storage policy and SQL inside `internal/store`, and command ownership in `cmd/whatsapp-mcp`. Alias the imported SDK as `sdkmcp` where needed. Do not give tools a `wa.Owner`: the tools need only a narrow read interface. Fake owners belong to command acceptance, not tool authority.

The common connection guard handles admission, request-ID tracking and protocol-error sanitization for both production framing and the official in-memory transport. In-memory acceptance wraps the server end of `NewInMemoryTransports`; it does not replace that SDK transport or bypass the guard. Production adds the fixed OS pumps beneath the same guard.

## Phase 0: Research Decisions

Completed [research.md](research.md), R1-R8. SDK version/APIs, pre-dispatch hazards, bounded process-lifetime I/O, independent output accounting, store/query deltas, admission isolation and stdio-only fault handling have decisions and rejected alternatives. No product clarification remains. Numeric proposals are selected design values awaiting the normal review, not measured guarantees.

## Phase 1: Design and Contracts

- [data-model.md](data-model.md): public objects, identity rules, omission/null behavior and state transitions; no persistence changes.
- [contracts/tools.md](contracts/tools.md): discoverable inputs/defaults, output schemas, exact matching, errors and acceptance cases.
- [contracts/stdio.md](contracts/stdio.md): pre-SDK resource accounting, framing, overload, process-lifetime I/O and cancellation/exit policy.
- [contracts/store-and-lifecycle.md](contracts/store-and-lifecycle.md): store API deltas, admission/SQL bounds and owner/store/lock ordering.
- [quickstart.md](quickstart.md): synthetic validation commands and required results. It does not start a real account.
- [verification.md](verification.md): research/artifact evidence and explicit not-run runtime gates; owner-review table initially empty.

## Complexity Tracking

No constitutional deviation requested. The fixed-pump transport is additional code, justified by actual SDK pre-middleware queues and non-interruptible inherited streams, not a replacement MCP implementation. SDK protocol handling, client interoperability and in-memory tests remain authoritative. Adding general policy profiles, changing the WhatsApp pin, raising store limits or sharing a session across processes is excluded.
