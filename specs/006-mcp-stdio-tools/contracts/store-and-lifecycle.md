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

`GetChat`'s final bool is a planned API change to control latest-message retrieval. Update existing internal callers/tests; do not retain a speculative compatibility layer. The other method signatures reflect the existing API. A store-owned `ToolReader()` facade implements these operations with the isolation policy below. Shared helpers may be reused only where their SQL satisfies that policy; the existing streamed and grouped/sorted queries are not safe tool paths as they stand. The store remains the authority for validation, normalization, snapshots, close accounting and errors. The facade cannot expose writes, `Device()`, arbitrary SQL or account methods.

Each tool makes at most one of these operations. In particular, a contact tool must not normalize through a separate `wa.Owner` or device call before querying; resolve known aliases in the same reader snapshot. Tool defaults, argument byte/type checks and offset calculation happen before store admission, and store validation still runs independently.

## Isolation Policy

**Status: specified from T001's synthetic spike (2026-11-17/18) and the owner's prerequisite decisions of 2026-11-18; subject to the owner-judge design review.** Evidence, commands and limits are in [research.md](../research.md) R6, [spike.md](../spike.md) and [verification.md](../verification.md). The pin stays `modernc.org/sqlite v1.61.0`; only its public APIs are used; there is no driver change, extra process/database, global budget change or retry of ingest `ErrBusy`. The values below are approved inputs for review, not measured upper bounds.

### 1. Every read finishes in its first step

The pinned driver installs its interrupt watcher and progress handler only around a statement's first step (`stmt.go:287-299`, `sqlite.go:80-139`); a later `rows.Next` is not interrupted (measured: up to 2.5 s after cancellation). Therefore each multi-row tool read is one statement that returns a single aggregate row, `SELECT count(*), json_group_array(json_array(...) ORDER BY <page order>), ... FROM (<page query>)`, so all its SQLite work happens inside the first step. The aggregate's own `ORDER BY` repeats the page order; it sorts at most one page.

- Per-row work outside SQLite (the `mcp_fold` Go callback) runs only on length-checked values (section 3), so one callback is bounded.
- Measured cancel-to-return with this form: ≤ 125 µs for chat walks, ≤ 37 ms for contact grouping at the ceilings (Windows/amd64; not an upper bound).
- Each read takes a pooled reader connection and closes it afterwards, as `Store.read` does. `database/sql` rolls a cancelled transaction back in a separate goroutine and only `Conn.Close` waits for it; a connection held across reads left the old transaction open in 33–72% of cancelled runs.
- A read that returns successfully after its context ended is reported as `timeout`/`cancelled`, never as success.
- Latest-message details are separate single-row point lookups in the same snapshot, at most one per page row.

### 2. Walk and grouping ceilings

| Read | Ceiling | Behaviour |
| --- | --- | --- |
| `list_chats` (filtered or not), `get_contact_chats` | Walk at most C = 100,200 chats (maxOffset + maxListSize) in index order: `... FROM (SELECT ... FROM chats ORDER BY <index order> LIMIT C)`, then filter, order and page. | A full page is exact. A short page is exact only if the store has at most C chats; this is checked in the same statement, only for short pages, by counting the primary key up to C+1. Otherwise `resource_limit`, never a partial page. Unfiltered pages never reach the ceiling. |
| `search_contacts` | Before any grouping or folding, the same statement reads at most E+1 of the current device's person entries and requires: E ≤ 100,000 entries, chosen-name bytes ≤ 4 MiB, bytes of all four name fields ≤ 16 MiB, no name field over 1 KiB. | Over any ceiling: `resource_limit` with zero `mcp_fold` calls. Measured worst case at the ceilings: 23.6 MB SQLite heap (approved share: 32 MiB). |
| `get_chat`, `get_direct_chat_by_contact` | Point lookups; no walk. | Guarded values only (section 3). |

**Required query shape:** the walk exposes its first order term as a named column (`last_message_time IS NULL AS k0`, `name_quality = 0 AS k0`) and the outer query orders by `k0, ...`. Written with the raw expression, SQLite sorts up to offset + limit rows in a temp B-tree (7.6 MB measured at offset 100,000 with short names). Tests must assert the plans: index scan for the walk, no `USE TEMP B-TREE FOR ORDER BY`, and only the `json_group_array(ORDER BY)` sort over at most one page.

Owner-accepted consequence: on a store with more than 100,200 chats, a short page (including a contact whose chats are all older than the newest 100,200) returns `resource_limit`.

### 3. Source values

Values that can exceed the store's write limits (device-store names, legacy or damaged message text and captions) are measured with `octet_length`, which does not load the value, and selected or folded only within their limits: names 1 KiB, text and caption 256 KiB. An oversized value is a `resource_limit`, never a truncated or omitted success. Measured: a 64 MiB latest message is reported without loading under a 32 MiB heap limit, where the current query fails with out-of-memory.

### 4. Admission

One mutex in the store gate decides ordinary operations (message writes, store reads, guarded device calls; nested device calls reuse their lease and do not re-enter) and tool reads together:

- A tool read is admitted only if no ordinary operation is waiting, an active slot is free (ordinary active + 1 ≤ 16) and a reader connection is free (ordinary reads + 1 ≤ 4). It never waits: otherwise `busy`.
- It is cancelled when an ordinary operation must wait for a slot, when an ordinary read would need its reader (a fourth concurrent ordinary read), when the WAL guard trips (section 5) or when the store closes.
- Its slot and reader stay accounted until the read has actually returned and its connection is closed; no replacement tool read is admitted meanwhile.
- While a tool read holds a slot, the ordinary waiter cap is 65 (owner-approved), so ordinary capacity stays 16 active + 64 waiting = 80. The totals of 16 active slots and four readers are unchanged.
- Close cancels the tool read, refuses both classes and waits for its actual release.

In the scratch model, an ordinary `ErrBusy` occurs only with 80 ordinary operations in flight, exactly as without tools, and a tool read delays ordinary work only by its release latency. Tools starve under saturation, by design. **Control assumption:** a cancelled tool read returns well within the 5 s admission wait. If it does not, the waiting ordinary operation gets `ErrBusy`; that is a control failure, handled below.

### 5. WAL guard

A held snapshot stops the WAL from resetting. Back-to-back tool reads keep a snapshot open all the time, so the WAL grows with all ingest: 603–757 MB in 20 s on the real store, against 41 MB without tools. A per-read byte budget does not prevent this, because the snapshots chain. Therefore:

- A tool read is admitted only while the `-wal` file is at most `walLimitBytes` (64 MiB, the existing `journal_size_limit`). A larger file means the WAL has not reset since it grew.
- A running tool read is cancelled when the `-wal` file passes 2 × `walLimitBytes`.
- A tool read is admitted only while the disk guard is not refusing and its last free-space measurement is at least `diskResume` + 2 × `walLimitBytes` (640 MiB), so the tool-attributable WAL cannot by itself bring free space below the 256 MiB reserve.

The pinned driver exposes no public WAL hook (only commit, rollback and pre-update hooks), so the guard reads the file size. Measured under continuous maximal ingest: peak 160.5 MB, no ingest errors, back to 64 MiB within two batches after the tool reads stopped. Under light ingest every tool read completed and the WAL peaked at 2.8 MB. The free-space condition is reasoning, not a test; the measurement is cached for up to 30 s.

### 6. Completion, control failure and platforms

The 1 s store and 10 s tool timers request cancellation (section 1 makes that request effective). At the contact ceilings a read took 1.2–1.6 s in scratch and 3.1–4.5 s on the real store (once over 10 s); such reads return `timeout` on slow hosts. The ceilings bound memory; the timer bounds time. If a cancelled tool read has not returned within the 5 s admission wait, latch `tool_store_not_quiescent`: stop tool admission, keep its accounting and enter normal unsuccessful shutdown; never release the slot early or admit a replacement.

Measured only on native Windows/amd64 (the admission model also under `-race` on WSL2). Linux and macOS are not verified. The cancellation, ceiling, query-plan, admission and WAL-guard tests are implementation tests (T004, T034, T035) and run in CI on Linux, Windows and macOS; their native results are recorded before delivery.

## Admission, Work and Cancellation Invariants

- Acquire the single protocol tool credit before SDK dispatch and retain it through response output. Reject competing tools with busy; no tool semaphore wait queue.
- The ToolReader uses the isolation policy above, fails fast rather than joining normal admission waiters, and registers real running/close accounting including acquire/close races. Normal message and guarded device operations keep their capacity; no normal writer waits on a tool semaphore or on tool output/diagnostics.
- A waiting ordinary operation prevents new MCP snapshots and requests cancellation of an active one (section 4); the cancellation does not by itself free shared resources. Retain every real lease/transaction until it has returned. No token can be handed to another operation merely because a timer fired.
- The 1 s tool-store timer and 10 s overall tool timer are **cancellation-request deadlines**, not completed-work promises. A cancelled call must not later succeed. A timeout response may be produced if output is safe, but no subsequent snapshot is admitted while its previous work remains active.
- Actual completion rests on the first-step form (section 1), not on a guessed grace interval. If a cancelled read has not returned within the 5 s admission wait, latch tool_store_not_quiescent, stop further tool work and enter normal unsuccessful shutdown; retain ownership until actual drainage. The existing explicit forced-exit rule is not automatic permission to kill account work or release locks early.
- Length-check source fields before selecting/folding them into Go; bind query values, use only fixed sort choices and preserve literal query semantics. Alias grouping must precede pagination and stays bounded by the contact ceilings. No partial result or silently truncated legacy data may be returned as success.
- The existing 32 MiB result-field, SQLite heap and disk limits remain binding. Synthetic flood tests must accompany the policy/plan argument and measure actual accounting, heap/WAL headroom and return, not replace it. Ingest ErrBusy remains terminal; the backlog stays open until isolation and acceptance evidence exist.

## Required Store Deltas

| Path | Change | Regression check |
| --- | --- | --- |
| `queries.go`, GetChat | Do not select latest message at all when false. | Oversized latest data cannot fail a metadata-only lookup. |
| Chat filter | Apply the same NFC/case folding to stored JID as query/name. | Mixed-case JID user parts and Unicode queries match predictably. |
| `contacts.go` | Literal folded query over names/phone digits; whitelist phone/LID identities. | Formatted search is literal; formatted contact inputs still normalize; no group/unsupported-domain result. |
| Contact membership | Direct identities plus EXISTS over matching sender aliases in any stored chat. | Repeated messages return one chat; another direct-chat fixture follows FR-008 rather than old group-only predicate. |
| Source field reads | Guard device-name lengths before Scan/fold. | Legacy oversized name yields bounded clear failure; no giant Go string. |
| Tool reads (`tool_reader.go`) | First-step aggregate form, walk and contact ceilings, `octet_length` guards, connection per read, context check after success (sections 1-3). | Equivalence with the current semantics on the spike's fixtures, plans asserted, `resource_limit` exactly over a ceiling, cancellation returns promptly. |
| Admission (`admit.go`, `ops.go`) | Coordinated gate with the tool class, 65-waiter cap while a tool holds a slot, cancellation triggers, close handling (section 4). | The spike's schedules on the real gate: waiting ordinary work refuses tools, the 81st ordinary operation alone is refused, reader contention, close waits for actual release, no admission-wait timeouts under stress; one tool operation, zero tool waiters. |
| WAL guard | Admission at ≤ 64 MiB -wal and ≥ 640 MiB free; cancellation above 128 MiB (section 5). | Continuous ingest with back-to-back tool reads keeps the -wal ≤ 2 × 64 MiB plus one batch, no ingest errors, recovery to 64 MiB. |

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
