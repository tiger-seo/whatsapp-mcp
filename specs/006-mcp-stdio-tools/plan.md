# Implementation Plan: MCP Over Stdio: Chat and Contact Tools

**Branch**: `006-mcp-stdio-tools` | **Date**: 2026-11-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-mcp-stdio-tools/spec.md`

**Status**: The store-isolation/completion prerequisite has a specified policy from T001's synthetic spike and the owner's decisions of 2026-11-18 (`contracts/store-and-lifecycle.md`, "Isolation Policy"). T001 closes only with the owner-judge design review of the committed design. No implementation, runtime acceptance or owner verdict is inferred.

## Summary

Expose five read tools through the official MCP SDK: `list_chats`, `get_chat`, `search_contacts`, `get_direct_chat_by_contact` and `get_contact_chats`. Use the existing store, local identity mappings, owner and lock. Local owner launch authorizes all stored chat/contact reads; no per-chat policy, login tool, QR disclosure, pairing opt-in, HTTP listener or proxy is added.

Add `internal/mcp` with explicit schemas, independently validated inputs, public result DTOs, safe JSON text and structured content. Place admission before SDK dispatch, not only in handlers. Use one process-lifetime stdio connection, four outstanding protocol requests, one admitted tool operation through response completion, and no tool queue. Bound framing, JSON shape, encoding and slow consumers. Tool reads run in a single first step with walk and contact ceilings, under a coordinated gate and a WAL guard that protect ingest (`contracts/store-and-lifecycle.md`). Keep ingest write failures terminal. Integrate EOF, transport failures, mandatory-reporting failure and stdio-only device-fault termination into existing safe stop.

## Technical Context

**Language/Version**: Go 1.27.0, toolchain `go1.27.4`; `CGO_ENABLED=0` for every build/test in this feature.

**Primary Dependencies**: Add `github.com/modelcontextprotocol/go-sdk v1.8.0` (stable release, Go 1.25 minimum), required by D15. Use its public MCP, JSON-RPC and in-memory APIs. Accept only reviewed pure-Go transitive additions; run module graph/integrity/vulnerability gates before implementation acceptance. Existing SQLite driver and WhatsApp fork remain unchanged. Do not upgrade `go.mau.fi/libsignal`, whatsmeow or the Go toolchain as part of this work.

**Storage**: Existing private `whatsapp.db`, device/contact tables and `modernc.org/sqlite v1.62.0` inherited from `main`; no migration, new result files or second database. A store-owned tool reader exposes no writes/device/account authority. Its admission/query/resource policy is the isolation policy in `contracts/store-and-lifecycle.md`: first-step aggregate reads, C = 100,200 chat walk, contact ceilings (100,000 entries, 4 MiB chosen names, 16 MiB all names, 1 KiB per field) sized for a 32 MiB tool heap share, a coordinated gate with one extra ordinary waiter place while a tool holds a slot, and a -wal/free-space guard. No extra active slot, reader connection, feature-specific pin or global-budget change.

**Testing**: Official SDK in-memory transport plus seeded temporary SQLite stores; fake owners and counted query interfaces; raw-frame tests for the custom connection; subprocess pipe tests on native Windows/Linux/macOS; existing ingest/quiet-stop/lock regressions. Native platform I/O behavior must be tested, not inferred from cross-compilation.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

**Project Type**: One CLI binary, one module, one account and one data-directory owner. The MCP transport is process-lifetime, not a reusable embedded-session framework.

**Performance Goals**: For unchanged synthetic data, stable paging of 235 chats at omitted/default limit 30 and maximum 200; exact alias matching and no duplicate chat/person results. One MCP snapshot at a time, no queued tool reads or MCP-added normal admission waiters. Request tool cancellation at 10 s and store cancellation at 5 s, earlier when ordinary work must wait, a fourth ordinary read needs the reader, the -wal passes 128 MiB, or the caller cancels. The first-step form makes the request effective (measured ≤ 37 ms to return, Windows/amd64). Measured with the 5 s timer on the real store: contact search at the ceilings 1.6–2.7 s, chat walks ≤ 357 ms, no timeout; a slow host can still return `timeout`. Expensive reads fail explicitly, never as partial successful pages.

**Constraints**: Proposed fixed bounds in [contracts/stdio.md](contracts/stdio.md): 64 KiB input frames, depth 16, four outstanding request IDs, one tool through response completion, 1,024 forwarded notifications per session, 2 MiB structured JSON/2 MiB safe text/8 MiB final response, 10 s partial-frame assembly and 5 s public output-wait limit. Two fixed protocol pumps plus one stderr pump: ordinary queue 16 x 4 KiB; mandatory queue 16 x 1 KiB, 5 s acknowledgment deadline and at most 128 fixed count/time cells. Mandatory-reporting overflow/failure terminates unsuccessfully, not silently; only ordinary logs are discardable. Existing store limits remain 200 rows, offset 100,000, 32 MiB result fields, 16 active slots and four readers; the ordinary waiter cap is 65 only while a tool read holds a slot. Tool-read ceilings and the WAL guard are in `contracts/store-and-lifecycle.md`; no receive-resource exception is inherited.

**Scale/Scope**: Five chat/contact tools and one stdio session. The store still retains messages indefinitely. Tool text/previews may be retained by the client's AI provider; local all-data authorization does not make prompt injection or downstream privacy risk disappear.

## Constitution Check

Checked against constitution 3.0.0 before research and re-evaluated after Phase 1. PASS below means design conformance, not tested implementation or owner-review approval.

| Principle | Pre-design | Post-design | Basis |
| --- | --- | --- | --- |
| I. Specification | PASS | PASS | Owner decisions in spec clarifications and source section 8.11 authorize all local reads and defer both login tools. Contracts implement the five-tool scope, literal queries and exact identity matching. No D1-D18 change. |
| II. One binary | PASS | PASS | One module; mandated SDK is pure Go; five CGO-free builds gate delivery. No subprocess/service required at runtime. |
| III. One owner | PASS | PASS | Same lock, store and owner; no schema or path change. Tool work drains before store close/lock release. |
| IV. Clean channel | PASS | PASS | Only framed MCP on stdout; secret-free diagnostics on stderr; no raw payload logging. Unix SIGPIPE must not bypass safe cleanup. |
| V. Secure defaults | Bounded design required | PASS, pending design review | Protocol/output/pump bounds and mandatory loss-reporting failure latch are specified. Store isolation: first-step reads, walk/contact ceilings, coordinated admission and WAL guard, with spike evidence and a stated control assumption (cancelled reads return within the admission wait). Linux/macOS not yet verified. No exception requested. |
| VI. Testability | PASS | PASS | All five tools through SDK in-memory transport; separate real-framing and subprocess tests; fake owner and temporary store only. |
| VII. Tool correctness | PASS | PASS | Real schema defaults plus handler defaults, snake_case, explicit false, RFC 3339, structured plus faithful text, clear errors and composite latest-message identity. |
| VIII. Real systems | PASS | PASS | No live account step required or authorized. A saved-session production stdio start may connect, so validation never uses the owner's real directory. |
| IX. Simplicity | PASS | PASS | Five tools, one narrow store facade, one connection, no config options or speculative restriction model. Custom framing is needed because the SDK dispatcher queues before middleware. |
| X. Security per feature | Design must address enabled High findings | PASS, pending design review | F01 local-read policy is decided; pairing/sending absent, F03 inherited controls and F15 gates remain. C1 mandatory-reporting semantics are corrected. F09: the isolation policy and its spike evidence are specified for review; implementation evidence follows in T004/T034/T035. No claim of implementation mitigation, general finding closure or owner approval. |

**Gates, not waivers**: T001's isolation policy and the owner's prerequisite decisions (2026-11-18: 32 MiB tool heap share, offset cap kept, conservative `resource_limit`, capacity-neutral admission with one extra waiter place, WAL guard, platform plan; after the design review of `68ce167`: 5 s store timer, recovery TRUNCATE checkpoint, low-disk refusal accepted) are recorded; T002 waits for a renewed design review of the amended design. FR-017 and terminal ingest errors are unchanged. Native pipe tests/flood tests remain delivery evidence in addition to the mechanism/assumptions argument; record actual OS/architecture, never infer untested runtime coverage from cross-builds. The all-data read policy is a selected product boundary, not a security exception.

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
  admit.go                       # coordinated gate: tool class, cancellation triggers, 65-waiter cap
  ops.go                         # read admission policy and actual drain
  tool_reader.go                 # five first-step reads, ceilings, WAL guard
  queries.go                     # withLatest, folded JID, contact membership
  contacts.go                    # literal query and supported person domains
  *_test.go                      # shared semantics, admission and cancellation
go.mod / go.sum                  # exact SDK and reviewed transitive modules
OPERATIONS.md                    # implemented vs verified, stdio limits/privacy
```

**Structure Decision**: Keep all protocol logic in `internal/mcp`, storage policy and SQL inside `internal/store`, and command ownership in `cmd/whatsapp-mcp`. Alias the imported SDK as `sdkmcp` where needed. Do not give tools a `wa.Owner`: the tools need only a narrow read interface. Fake owners belong to command acceptance, not tool authority.

The common connection guard handles admission, request-ID tracking and protocol-error sanitization for both production framing and the official in-memory transport. In-memory acceptance wraps the server end of `NewInMemoryTransports`; it does not replace that SDK transport or bypass the guard. Production adds the fixed OS pumps beneath the same guard.

## Phase 0: Research Decisions

See [research.md](research.md), R1-R8. SDK/protocol/pump/DTO decisions remain; R3 now distinguishes mandatory reports from discardable logs. R6 records the store-isolation spike and its resulting policy: first-step reads, ceilings, coordinated admission and the WAL guard, with the owner's prerequisite decisions. There is no unresolved product-scope question; design readiness still requires the owner-judge design review. Numeric values remain proposals, not measured guarantees.

## Phase 1: Design and Contracts

- [data-model.md](data-model.md): public objects, identity rules, omission/null behavior and state transitions; no persistence changes.
- [contracts/tools.md](contracts/tools.md): discoverable inputs/defaults, output schemas, exact matching, errors and acceptance cases.
- [contracts/stdio.md](contracts/stdio.md): pre-SDK resource accounting, framing, overload, process-lifetime I/O and cancellation/exit policy.
- [contracts/store-and-lifecycle.md](contracts/store-and-lifecycle.md): store API deltas, isolation policy (first-step reads, ceilings, admission, WAL guard) and owner/store/lock ordering.
- [spike.md](spike.md): the T001 spike guide and its results.
- [quickstart.md](quickstart.md): synthetic validation commands and required results. It does not start a real account.
- [verification.md](verification.md): research/artifact evidence and explicit not-run runtime gates; owner-review table initially empty.

## Implementation Sequence

The [task dependencies](tasks.md#dependencies-and-execution-order) stage diagnostic shutdown separately from MCP integration. After the authorized design commit and current acceptable `owner-judge` design verdict, T045/T046 verify and wire diagnostic shutdown with stdin-discard intact. The one reporter applies the resolved ordinary log level in place after config loads. Passing local command tests and the focused native Linux/macOS CI step, with named case and child exit evidence, close T015/T016 and the foundation. Phase 3 retains chat registration and T024's MCP transport and actual drain revalidation. The full integrated native process matrix remains T036-T038.

## Complexity Tracking

No constitutional deviation requested. The fixed-pump transport is justified by SDK pre-middleware queues and non-interruptible inherited streams; mandatory reporting adds bounded state, not synchronous cleanup I/O. Store isolation adds a tool class to the existing gate, one extra ordinary waiter place while a tool holds a slot, and a -wal/free-space guard; all approved by the owner as prerequisite decisions. General policy profiles, pin/budget changes and a second process/database are not authorized.
