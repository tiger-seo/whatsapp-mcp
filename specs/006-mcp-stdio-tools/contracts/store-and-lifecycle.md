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

`GetChat`'s final bool is a planned API change to control latest-message retrieval. Update existing internal callers/tests; do not retain a speculative compatibility layer. The other method signatures reflect the existing API. A store-owned `ToolReader()` facade implements these operations with tool admission policy while reusing their SQL bodies. The existing store remains the authority for validation, normalization, snapshots, close accounting and error classification. The facade cannot expose writes, `Device()`, arbitrary SQL or account methods.

Each tool makes at most one of these operations. In particular, a contact tool must not normalize through a separate `wa.Owner` or device call before querying; resolve known aliases in the same reader snapshot. Tool defaults, argument byte/type checks and offset calculation happen before store admission, and store validation still runs independently.

## Admission and SQL Work

- Acquire the single global MCP tool credit before SDK dispatch and retain it until response output completes. Reject competing tools with `busy`; no tool semaphore wait queue.
- ToolReader uses immediate shared-gate entry: if an active slot is unavailable, return busy without incrementing `waiting`, creating an admission timer or launching a waiter goroutine. Register successful entry in the same running/close accounting as ordinary operations, including the closed-after-acquire race check.
- Keep normal store entry, its 16-active/64-waiting policy, four-reader pool, single writer and device leases unchanged. The tool path contributes at most one active slot/reader and zero admission waiters. This is a bound on contribution, not a hard reservation of all other resources against unrelated traffic.
- Apply the earlier of caller cancellation and the 1 s snapshot deadline to connection acquisition, SQL and transaction lifetime. A tool's total deadline is 10 s including encoding/output. Release the reader/gate only after the actual transaction and connection are returned, never on timer expiry alone.
- Length-check device-origin name fields in SQL before selecting/folding them into Go results. Preserve supported field ceilings and return a bounded sentinel error for oversized relevant data; never truncate then claim exact matching. Output-byte checks after Scan do not substitute for source checks.
- Bind query values. Dynamic sort selection comes only from the fixed enum. Use literal `instr` with folded name/JID; no query-built identifiers or LIKE wildcard behavior. Contact search performs identity grouping before matching/order/page with bounded field inputs and cancellable SQL.
- Expensive sorts/scans may time out or hit existing SQLite resource limits. Return a clear error without a partial page or unbounded application candidate list. The store's 32 MiB field budget, global SQLite heap and disk controls remain unchanged.
- End-of-input/cancellation must interrupt tool SQL, and close accounting must prove drainage. Flood tests also measure WAL and heap recovery: one reader can still hold a snapshot or share SQLite memory, so concurrency alone is not evidence of complete resource isolation.

`ErrBusy` from ingest writes remains terminal. Do not retry, swallow or downgrade it to a warning. The backlog item remains open until request floods with concurrent synthetic live/history/device activity show no MCP-induced busy ingest failure. Under unrelated OS/storage failure the existing terminal policy still applies.

## Required Store Deltas

| Path | Change | Regression check |
| --- | --- | --- |
| `queries.go`, GetChat | Do not select latest message at all when false. | Oversized latest data cannot fail a metadata-only lookup. |
| Chat filter | Apply the same NFC/case folding to stored JID as query/name. | Mixed-case JID user parts and Unicode queries match predictably. |
| `contacts.go` | Literal folded query over names/phone digits; whitelist phone/LID identities. | Formatted search is literal; formatted contact inputs still normalize; no group/unsupported-domain result. |
| Contact membership | Direct identities plus EXISTS over matching sender aliases in any stored chat. | Repeated messages return one chat; another direct-chat fixture follows FR-008 rather than old group-only predicate. |
| Source field reads | Guard device-name lengths before Scan/fold. | Legacy oversized name yields bounded clear failure; no giant Go string. |
| Admission | Tool facade plus immediate gate entry; no normal-path limit changes. | One tool operation, no MCP waiting-counter contribution, correct close races and cancellation. |

Preserve latest timestamp/ID tie ordering, chosen-name precedence, phone-over-LID singular preference, local mapping behavior and same-snapshot consistency. Changing a query's documented semantics requires updating its relevant tests, not silently inheriting an incompatible old expectation.

## Command Integration

1. Before entering `runShell`, install the stdio-specific nonblocking diagnostic sink and Unix SIGPIPE policy, so startup/owner/lifecycle diagnostics cannot block cleanup or terminate the process. Then retain `runShell` lifecycle setup, exclusive lock, private store open, safe-run-marker check and identity reconciliation. A startup refusal creates no MCP session or account client beyond existing behavior. Do not change serve's logger/signal behavior as an accidental consequence.
2. In `runStdio`, attach the existing owner and its stop step. Owner Start returns after publishing its generation, while saved-session connection continues asynchronously. Construct MCP against the tool-reader facade; no tool calls the owner.
3. Replace the old stdin-discard goroutine. There is exactly one protocol stdin consumer. Construct transport from injected command streams, not hard-coded globals; use production process-lifetime pumps for inherited streams and cooperative streams for in-process tests.
4. Register MCP drain after owner stop registration, because stop steps run in reverse. On context cancellation/transport completion, immediately stop MCP admission and cancel request work. The owner may already be retiring due to the same context; never delay its safety fence waiting for client output.
5. Drain actual handlers and SDK session with transport already stopped, then join owner work before store close and lock release. Preserve owner internal order: work cancellation, Disconnect, network fence, device storage barrier, library cancellation/quiet observation. Marker clearing stays exclusively with the owner under existing rules.
6. Treat terminal ingest failure as the current shared shell path does. For device storage faults add only a stdio subscription/wake-up/latched-health check, not a change to `Owner.Terminal()`.
7. On session creation/start failure after owner attachment, use all registered cleanup steps and exit 1. Never return directly while ownership/account work remains active.

## Device-Fault Watcher

Subscribe and then immediately inspect latched health to cover faults before subscription. The callback runs under the owner's lock: it may only perform a nonblocking send to a capacity-one channel, never call `Health`, unsubscribe or perform I/O. Outside the callback recheck `Health().StorageFault`, latch the exit reason and request normal stop. Unsubscribe outside callbacks during cleanup. Inspect health again after owner drain for faults discovered during retirement or racing EOF.

Do not label a device-only fault as `ingest_failed`; emit the existing safe device-fault diagnostic/category and exit 1. Preserve the owner's unsafe marker on storage fault. A cleanup success cannot erase a latched operational fault or produce a reusable-session claim in that case.

## Tests and Scope Boundaries

Command tests use `newOwner` injection, existing scripted owners and temporary private data. Add a controllable owner Done channel where asserting real join order; the current fake's preclosed Done cannot prove drainage. Use a real held data-directory lock to test startup refusal. Keep both `serve` ingest-terminal regression tests and `TestOwnerTerminalIgnoresADeviceStorageFault`; device fault alone must not newly terminate serve in 006.

Separate raw transport/subprocess tests prove framing, bounded pumps, closed-pipe/SIGPIPE behavior and process exit. Do not use saved real credentials for these tests. A subprocess test may use a deliberately unpaired synthetic directory; startup with no saved identity starts no connection or QR. If fault injection is needed, use a test-only helper entry point rather than a production fake-owner flag.
