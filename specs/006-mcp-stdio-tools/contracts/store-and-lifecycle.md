# Contract: Store Reads and Stdio Ownership

## Narrow Tool Reader

`internal/mcp` depends on a read-only interface, not the device store or WhatsApp owner. The methods use existing store DTOs and checked pagination:

```go
type ChatContactReader interface {
    ListChats(context.Context, string, store.ChatSort, bool, store.Page) ([]store.ChatView, error)
    GetChat(context.Context, string, bool) (store.ChatView, error)
    SearchContacts(context.Context, string, store.Page) ([]store.Contact, error)
    DirectChat(context.Context, string) (store.ChatView, error)
    ContactChats(context.Context, string, store.Page) ([]store.ChatView, error)
}
```

`GetChat`'s final bool is a planned API change to control latest-message retrieval. Update existing internal callers/tests; do not retain a speculative compatibility layer. The other method signatures reflect the existing API. A store-owned `ToolReader()` facade implements these operations with the isolation policy established by the blocking prerequisite below. Shared helpers may be reused only where their SQL plans satisfy that policy; the existing grouped/sorted queries are not automatically safe tool paths. The store remains the authority for validation, normalization, snapshots, close accounting and errors. The facade cannot expose writes, `Device()`, arbitrary SQL or account methods.

Each tool makes at most one of these operations. In particular, a contact tool must not normalize through a separate `wa.Owner` or device call before querying; resolve known aliases in the same reader snapshot. Tool defaults, argument byte/type checks and offset calculation happen before store admission, and store validation still runs independently.

## Blocking Isolation Prerequisite

**Status: BLOCKED, before implementation and before a design PASS.** The former immediate shared-gate proposal bounded MCP's contribution but did not establish FR-017. A single MCP read can share SQLite allocations, reader capacity and WAL retention with message/device writes. Neither zero tool waiters nor a passing finite flood workload constitutes resource isolation. `ToolReader` must not be enabled against that proposal.

T001 must first produce a synthetic, scratch-only isolation/cancellation spike against the actual `go.mod` pin, `modernc.org/sqlite v1.61.0`. Its required outputs in research.md and verification.md are:

1. An atomic admission policy covering every normal store/device path and tool reads, with priority/reservation sufficient for FR-017 under a written adversarial schedule. At most one tool snapshot, no queued tool reads and no MCP acquisition ahead of pending ordinary work. Test arrivals before admission, immediately after admission, during query iteration and repeated readmissions. A separate MCP admission class is a candidate, not permission to add a seventeenth active lease or a fifth reader. Existing 16-active/64-waiting/four-reader totals remain binding unless the owner explicitly approves a revised profile and covered design is reviewed again.
2. A per-query bound for the resources shared with writes: SQLite/cache/intermediate memory, source values, scanned candidates, Go grouping/top-page state, temporary disk and WAL retention. Audit query plans, not just returned LIMIT rows. Prefer indexed keyset batches with explicit total candidate/byte budgets over unbounded SQL GROUP BY/sort. Exceeding a scan budget returns resource_limit, never an incomplete successful page. Choose concrete candidate/batch/byte ceilings in the spike and review them before implementation; none are silently inferred here.
3. A supported control path under the pinned driver. Its exported `sqlite.Limit` controls particular SQLite limits, not a general per-connection heap quota or VM-step budget. `hard_heap_limit` is process-wide; changing it on a tool connection does not isolate ingest. Private page caches and file-backed sorts are candidates only after their actual bounds are established; advisory cache settings and moving unbounded temp data to disk are not proofs. Do not access private driver handles, fork/upgrade the driver, change the global heap budget or add a process/database implicitly.
4. Separate cancellation-request and actual completion evidence for initial execution, expensive later `rows.Next`, user-defined folding, connection acquisition and transaction close. Identify a supported interrupt/progress mechanism or another enforceable work/termination bound; a context timer alone is insufficient. Define the validated environment/OS assumptions and the behavior on a control failure. Run supported native OS/architecture tests and state unverified combinations precisely.

If any output cannot be justified within the existing architecture/pins/budgets, T001 stops for an explicit owner prerequisite decision. Do not mark this blocker resolved, obtain a design PASS premised on it, start T002-T042, or weaken FR-017/terminal ingest errors based only on passing stress tests. This is a known unresolved technical prerequisite, not a parked finding or security exception.

## Admission, Work and Cancellation Invariants

- Acquire the single protocol tool credit before SDK dispatch and retain it through response output. Reject competing tools with busy; no tool semaphore wait queue.
- The eventual ToolReader must use the reviewed isolation policy above, fail fast rather than join normal admission waiters, and register real running/close accounting including acquire/close races. Normal message and guarded device operations must retain their protected capacity; no normal writer waits on a tool semaphore or on tool output/diagnostics.
- Ordinary activity must prevent new speculative MCP snapshots and request cancellation of an active one according to the reviewed policy; do not claim the cancellation instantly frees shared resources. Retain every real lease/transaction until it has returned. No token can be handed to another operation merely because a timer fired.
- The 1 s tool-store timer and 10 s overall tool timer are **cancellation-request deadlines**, not completed-work promises. A cancelled call must not later succeed. A timeout response may be produced if output is safe, but no subsequent snapshot is admitted while its previous work remains active.
- Actual completion/resource-release bounds must be established in T001, not assigned a guessed grace interval. If a required bound is not established, tools remain disabled and implementation readiness remains blocked. If a validated control subsequently fails, latch tool_store_not_quiescent, stop further tool work and enter normal unsuccessful shutdown; retain ownership until actual drainage. The existing explicit forced-exit rule is not automatic permission to kill account work or release locks early.
- Length-check source fields before selecting/folding them into Go; bind query values, use only fixed sort choices and preserve literal query semantics. Alias grouping must precede pagination and remain bounded under the reviewed scan policy. No partial result or silently truncated legacy data may be returned as success.
- The existing 32 MiB result-field, SQLite heap and disk limits remain binding. Synthetic flood tests must accompany the policy/plan argument and measure actual accounting, heap/WAL headroom and return, not replace it. Ingest ErrBusy remains terminal; the backlog stays open until isolation and acceptance evidence exist.

## Required Store Deltas

| Path | Change | Regression check |
| --- | --- | --- |
| `queries.go`, GetChat | Do not select latest message at all when false. | Oversized latest data cannot fail a metadata-only lookup. |
| Chat filter | Apply the same NFC/case folding to stored JID as query/name. | Mixed-case JID user parts and Unicode queries match predictably. |
| `contacts.go` | Literal folded query over names/phone digits; whitelist phone/LID identities. | Formatted search is literal; formatted contact inputs still normalize; no group/unsupported-domain result. |
| Contact membership | Direct identities plus EXISTS over matching sender aliases in any stored chat. | Repeated messages return one chat; another direct-chat fixture follows FR-008 rather than old group-only predicate. |
| Source field reads | Guard device-name lengths before Scan/fold. | Legacy oversized name yields bounded clear failure; no giant Go string. |
| Admission/resource isolation | Blocked on T001's reviewed supported policy; no opportunistic shared-gate proof or implicit budget increase. | Adversarial ordinary/tool schedules, bounded query/intermediate resources and actual cancellation/close accounting; one tool operation, zero tool waiters. |

Preserve latest timestamp/ID tie ordering, chosen-name precedence, phone-over-LID singular preference, local mapping behavior and same-snapshot consistency. Changing a query's documented semantics requires updating its relevant tests, not silently inheriting an incompatible old expectation.

## Command Integration

1. Before entering `runShell`, install the stdio-specific nonblocking diagnostic sink and Unix SIGPIPE policy, so startup/owner/lifecycle diagnostics cannot block cleanup or terminate the process. Then retain `runShell` lifecycle setup, exclusive lock, private store open, safe-run-marker check and identity reconciliation. A startup refusal creates no MCP session or account client beyond existing behavior. Do not change serve's logger/signal behavior as an accidental consequence.
2. In `runStdio`, attach the existing owner and its stop step. Owner Start returns after publishing its generation, while saved-session connection continues asynchronously. Construct MCP against the tool-reader facade; no tool calls the owner.
3. Replace the old stdin-discard goroutine. There is exactly one protocol stdin consumer. Construct transport from injected command streams, not hard-coded globals; use production process-lifetime pumps for inherited streams and cooperative streams for in-process tests.
4. Register MCP drain after owner stop registration, because stop steps run in reverse. Observe the mandatory-reporting latch through its capacity-one wake-up and an immediate post-install check; inspect again after owner drain/before final exit. Reporter/logging callbacks only latch/wake, never call owner/cleanup methods under locks. On reporting failure, context cancellation or transport completion, stop admission and cancel requests outside those callbacks. Owner retirement may already run concurrently; never delay its safety fence on client/diagnostic output.
5. Drain actual handlers and SDK session with transport already stopped, then join owner work before store close and lock release. Preserve owner internal order: work cancellation, Disconnect, network fence, device storage barrier, library cancellation/quiet observation. Marker clearing stays exclusively with the owner under existing rules.
6. Treat terminal ingest failure as the current shared shell path does. For device storage faults add only a stdio subscription/wake-up/latched-health check, not a change to `Owner.Terminal()`.
7. On session creation/start failure after owner attachment, use all registered cleanup steps and exit 1. Never return directly while ownership/account work remains active.

## Device-Fault Watcher

Subscribe and then immediately inspect latched health to cover faults before subscription. The callback runs under the owner's lock: it may only perform a nonblocking send to a capacity-one channel, never call `Health`, unsubscribe or perform I/O. Outside the callback recheck `Health().StorageFault`, latch the exit reason and request normal stop. Unsubscribe outside callbacks during cleanup. Inspect health again after owner drain for faults discovered during retirement or racing EOF.

Do not label a device-only fault as `ingest_failed`; emit the existing safe device-fault diagnostic/category and exit 1. Preserve the owner's unsafe marker on storage fault. A cleanup success cannot erase a latched operational fault or produce a reusable-session claim in that case.

## Tests and Scope Boundaries

Command tests use `newOwner` injection, existing scripted owners and temporary private data. Add a controllable owner Done channel where asserting real join order; the current fake's preclosed Done cannot prove drainage. Use a real held data-directory lock to test startup refusal. Keep both `serve` ingest-terminal regression tests and `TestOwnerTerminalIgnoresADeviceStorageFault`; device fault alone must not newly terminate serve in 006.

Separate raw transport/subprocess tests prove framing, bounded pumps, closed-pipe/SIGPIPE behavior and process exit. Do not use saved real credentials for these tests. A subprocess test may use a deliberately unpaired synthetic directory; startup with no saved identity starts no connection or QR. If fault injection is needed, use a test-only helper entry point rather than a production fake-owner flag.
