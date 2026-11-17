# Implementation Plan: MCP Over Stdio: Chat and Contact Tools

**Branch**: `006-mcp-stdio-tools` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-mcp-stdio-tools/spec.md`

**Status**: Design artifacts/tasks exist, but implementation readiness is BLOCKED by the store-isolation/completion prerequisite in `contracts/store-and-lifecycle.md`. T001 first resolves that prerequisite with synthetic scratch research, then obtains the required committed design review. No implementation, runtime acceptance or owner verdict is inferred from these corrections.

## Summary

Expose five read tools through the official MCP SDK: `list_chats`, `get_chat`, `search_contacts`, `get_direct_chat_by_contact` and `get_contact_chats`. Use the existing store, local identity mappings, owner and lock. Local owner launch authorizes all stored chat/contact reads; no per-chat policy, login tool, QR disclosure, pairing opt-in, HTTP listener or proxy is added.

Add `internal/mcp` with explicit schemas, independently validated inputs, public result DTOs, safe JSON text and structured content. Place admission before SDK dispatch, not only in handlers. Use one process-lifetime stdio connection, four outstanding protocol requests, one admitted tool operation through response completion, and no tool queue. Bound framing, JSON shape, encoding and slow consumers. Store query reuse is conditional on the blocking isolation/work-bound proof; opportunistic shared admission is not accepted as protection of ingest. Keep ingest write failures terminal. Integrate EOF, transport failures, mandatory-reporting failure and stdio-only device-fault termination into existing safe stop.

## Technical Context

**Language/Version**: Go 1.27.0, toolchain `go1.27.4`; `CGO_ENABLED=0` for every build/test in this feature.

**Primary Dependencies**: Add `github.com/modelcontextprotocol/go-sdk v1.8.0` (stable release, Go 1.25 minimum), required by D15. Use its public MCP, JSON-RPC and in-memory APIs. Accept only reviewed pure-Go transitive additions; run module graph/integrity/vulnerability gates before implementation acceptance. Existing SQLite driver and WhatsApp fork remain unchanged. Do not upgrade `go.mau.fi/libsignal`, whatsmeow or the Go toolchain as part of this work.

**Storage**: Existing private `whatsapp.db`, device/contact tables and `modernc.org/sqlite v1.61.0`; no migration, new result files or second database. A store-owned tool reader exposes no writes/device/account authority. Its supported admission/query/resource policy is a blocking T001 output, not the former immediate shared-gate proposal. No extra active lease, reader connection, pin or global-budget change is approved implicitly.

**Testing**: Official SDK in-memory transport plus seeded temporary SQLite stores; fake owners and counted query interfaces; raw-frame tests for the custom connection; subprocess pipe tests on native Windows/Linux/macOS; existing ingest/quiet-stop/lock regressions. Native platform I/O behavior must be tested, not inferred from cross-compilation.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

**Project Type**: One CLI binary, one module, one account and one data-directory owner. The MCP transport is process-lifetime, not a reusable embedded-session framework.

**Performance Goals**: For unchanged synthetic data, stable paging of 235 chats at omitted/default limit 30 and maximum 200; exact alias matching and no duplicate chat/person results. One MCP snapshot at a time, no queued tool reads or MCP-added normal admission waiters. Request tool cancellation at 10 s and store cancellation at 1 s, earlier on ordinary activity/caller cancellation. These are cancellation-request deadlines, not actual completion/drain bounds. T001 must establish supported query/resource-release bounds before design PASS or implementation; until then readiness is blocked. Expensive reads fail explicitly, never as partial successful pages.

**Constraints**: Proposed fixed bounds in [contracts/stdio.md](contracts/stdio.md): 64 KiB input frames, depth 16, four outstanding request IDs, one tool through response completion, 1,024 forwarded notifications per session, 2 MiB structured JSON/2 MiB safe text/8 MiB final response, 10 s partial-frame assembly and 5 s public output-wait limit. Two fixed protocol pumps plus one stderr pump: ordinary queue 16 x 4 KiB; mandatory queue 16 x 1 KiB, 5 s acknowledgment deadline and at most 128 fixed count/time cells. Mandatory-reporting overflow/failure terminates unsuccessfully, not silently; only ordinary logs are discardable. Existing store limits remain 200 rows, offset 100,000, 32 MiB result fields, 16 active/64 waiting and four readers. Unresolved completion/isolation bounds block implementation; no receive-resource exception is inherited.

**Scale/Scope**: Five chat/contact tools and one stdio session. The store still retains messages indefinitely. Tool text/previews may be retained by the client's AI provider; local all-data authorization does not make prompt injection or downstream privacy risk disappear.

## Constitution Check

Checked against constitution 3.0.0 before research and re-evaluated after Phase 1. PASS below means design conformance, not tested implementation or owner-review approval.

| Principle | Pre-design | Post-design | Basis |
| --- | --- | --- | --- |
| I. Specification | PASS | PASS | Owner decisions in spec clarifications and source section 8.11 authorize all local reads and defer both login tools. Contracts implement the five-tool scope, literal queries and exact identity matching. No D1-D18 change. |
| II. One binary | PASS | PASS | One module; mandated SDK is pure Go; five CGO-free builds gate delivery. No subprocess/service required at runtime. |
| III. One owner | PASS | PASS | Same lock, store and owner; no schema or path change. Tool work drains before store close/lock release. |
| IV. Clean channel | PASS | PASS | Only framed MCP on stdout; secret-free diagnostics on stderr; no raw payload logging. Unix SIGPIPE must not bypass safe cleanup. |
| V. Secure defaults | Bounded design required | BLOCKED: prerequisite proof required | Protocol/output/pump bounds and mandatory loss-reporting failure latch are specified. Actual supported store isolation/query-completion bounds remain unresolved under the pinned driver; T001 blocks a design PASS/implementation. No stress result, context timer or silent warning drop is a substitute. No exception requested. |
| VI. Testability | PASS | PASS | All five tools through SDK in-memory transport; separate real-framing and subprocess tests; fake owner and temporary store only. |
| VII. Tool correctness | PASS | PASS | Real schema defaults plus handler defaults, snake_case, explicit false, RFC 3339, structured plus faithful text, clear errors and composite latest-message identity. |
| VIII. Real systems | PASS | PASS | No live account step required or authorized. A saved-session production stdio start may connect, so validation never uses the owner's real directory. |
| IX. Simplicity | PASS | PASS | Five tools, one narrow store facade, one connection, no config options or speculative restriction model. Custom framing is needed because the SDK dispatcher queues before middleware. |
| X. Security per feature | Design must address enabled High findings | BLOCKED: F09 store boundary unresolved | F01 local-read policy is decided; pairing/sending absent, F03 inherited controls and F15 gates remain. C1 mandatory-reporting semantics are corrected, but F09 isolation/completion requires T001's supported control evidence and review. No claim of implementation mitigation, general finding closure or owner approval. |

**Blocking gates, not waivers**: T001 must establish a supported store-isolation/completion policy before seeking a design PASS or starting implementation. If that cannot be established within existing pins/budgets/architecture, obtain an explicit prerequisite owner decision rather than weakening FR-017 or claiming deadlines prove drain. Resource values and compatibility trade-offs then require normal review. Native pipe tests/flood tests remain delivery evidence in addition to the mechanism/assumptions argument; record actual OS/architecture, never infer untested runtime coverage from cross-builds. The all-data read policy is a selected product boundary, not a security exception.

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
  tasks.md                       # dependency-ordered work; design review first
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
  diagnostics.go                 # ordinary/mandatory queues, failure latch, pump
  signal_unix.go                 # SIGPIPE installed before runShell diagnostics
  signal_windows.go              # platform counterpart, no Unix signal use
  *_test.go                      # SDK in-memory and bounded-transport tests
internal/store/
  admit.go                       # reviewed isolation policy; T001 prerequisite
  ops.go                         # read admission policy and actual drain
  tool_reader.go                 # five reads; only proven bounded query paths
  queries.go                     # withLatest, folded JID, contact membership
  contacts.go                    # literal query and supported person domains
  *_test.go                      # shared semantics, admission and cancellation
go.mod / go.sum                  # exact SDK and reviewed transitive modules
OPERATIONS.md                    # implemented vs verified, stdio limits/privacy
```

**Structure Decision**: Keep all protocol logic in `internal/mcp`, storage policy and SQL inside `internal/store`, and command ownership in `cmd/whatsapp-mcp`. Alias the imported SDK as `sdkmcp` where needed. Do not give tools a `wa.Owner`: the tools need only a narrow read interface. Fake owners belong to command acceptance, not tool authority.

The common connection guard handles admission, request-ID tracking and protocol-error sanitization for both production framing and the official in-memory transport. In-memory acceptance wraps the server end of `NewInMemoryTransports`; it does not replace that SDK transport or bypass the guard. Production adds the fixed OS pumps beneath the same guard.

## Phase 0: Research Decisions

See [research.md](research.md), R1-R8. SDK/protocol/pump/DTO decisions remain; R3 now distinguishes mandatory reports from discardable logs. R6 is reopened as a blocking technical prerequisite: supported store isolation, intermediate-work budgets and actual cancellation/drain bounds are not established. There is no unresolved product-scope question, but research/design readiness must not be called complete until T001 resolves this technical blocker and the design is reviewed. Numeric values remain proposals, not measured guarantees.

## Phase 1: Design and Contracts

- [data-model.md](data-model.md): public objects, identity rules, omission/null behavior and state transitions; no persistence changes.
- [contracts/tools.md](contracts/tools.md): discoverable inputs/defaults, output schemas, exact matching, errors and acceptance cases.
- [contracts/stdio.md](contracts/stdio.md): pre-SDK resource accounting, framing, overload, process-lifetime I/O and cancellation/exit policy.
- [contracts/store-and-lifecycle.md](contracts/store-and-lifecycle.md): store API deltas, admission/SQL bounds and owner/store/lock ordering.
- [quickstart.md](quickstart.md): synthetic validation commands and required results. It does not start a real account.
- [verification.md](verification.md): research/artifact evidence and explicit not-run runtime gates; owner-review table initially empty.

## Complexity Tracking

No constitutional deviation requested. The fixed-pump transport is justified by SDK pre-middleware queues and non-interruptible inherited streams; mandatory reporting adds bounded state, not synchronous cleanup I/O. Store isolation remains blocked rather than falsely justified by a concurrency cap. General policy profiles, pin/budget changes and a second process/database are not authorized; any necessary architecture change needs an explicit owner prerequisite decision and renewed design review.
