# Research: Reusable Session After a Proven Quiet Stop

Date: 2026-11-07. Pinned modules: `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`,
`github.com/coder/websocket v1.8.15`, `go.mau.fi/libsignal v0.2.2`, `go.mau.fi/util v0.11.0`. Read-only inspection of
the module cache and of this repository at `4cffef0`. Nothing was run against WhatsApp. The source audit behind the
owner's decision is in `docs/backlog/003-post-pair-reconnect-and-unsafe-marker.md` ("Research", "Option A: security
impact"); it is not repeated here.

## R1 How the client library's leftover work is observed

**Decision.** Take a full goroutine dump with `runtime.Stack(buf, true)` into a bounded buffer, parse it in memory,
and count the goroutines that belong to the client library. A goroutine belongs to it when any frame's function, or
its `created by` line, is in one of these packages (the match is on the package path followed by `.` or `/`, never a bare
string prefix): `go.mau.fi/whatsmeow` and its subpackages, `go.mau.fi/libsignal`
and its subpackages, and `github.com/coder/websocket` and its subpackages. The checking goroutine itself is excluded.
Poll until two consecutive observations, at least 100 ms apart, both find zero (after removing allow-listed work),
or until the deadline.

**Rationale.**
- The pinned client joins none of the about 64 `go` sites in 15 files (`appstate.go`, `client.go`,
  `connectionevents.go`, `keepalive.go`, `message.go`, `notification.go`, `pair.go`, `presence.go`, `qrchan.go`,
  `receipt.go`, `retry.go`, `send.go`, `socket/framesocket.go`, `socket/noisesocket.go`, `tctoken.go`). There is no
  public join, so an observation is the only no-fork evidence.
- Matching by package, not by a list of functions, fails closed: new spawn sites in a later pin are caught
  automatically. Exact function names appear only in the allow-list of work that may be ignored (R5).
- `github.com/coder/websocket` is included because the websocket's own goroutines (`read.go:76`) and its
  `context.AfterFunc` callbacks (`conn.go:176, :193`) run on the library's behalf with no whatsmeow frame.
  `libsignal` is included because session and pre-key work can run in its frames. `go.mau.fi/util` is not matched:
  its one goroutine (`dbutil/transaction.go:94`) is joined by the transaction itself, which the store barrier
  already joins, and later features will run their own `dbutil` transactions at the same time.
- Two clean observations: whatsmeow has no `time.AfterFunc`, but the websocket library registers
  `context.AfterFunc` callbacks that can start after a single clean observation. The second observation, after the
  network block, catches one that fires late.
- A goroutine blocked inside the program's own event callback still has whatsmeow frames lower in its stack, so it
  is counted.

**Alternatives considered.**
- *A whatsmeow fork with `WaitIdle`* (option B): stronger, but needs a separate owner decision; it is the fallback if
  the pass rate is low (spec SC-005).
- *`runtime.NumGoroutine` before and after*: cannot tell whose goroutines they are.
- *A goroutine profile (`pprof.Lookup("goroutine")`)*: the same data with a heavier format and an extra import; no
  benefit.
- *A process exit instead of an observation*: rejected in the backlog's "Correction" section. It does not repair
  refused writes, and `stdio` cannot restart itself.

## R2 Bounding the observation

**Decision.** Start with a 1 MiB buffer and double it while `runtime.Stack` fills it, up to 8 MiB. A dump that still
fills 8 MiB is *incomplete*, so the check fails. Polling stops at the deadline. The buffer is a local value of the
check, is never logged, stored, returned or wrapped into an error, and is dropped when the check ends. Only counts,
categories and the duration leave it.

**Rationale.** A goroutine dump holds raw argument words (pointers and integers) and function names. The spec
forbids exporting it (FR-008, SEC-004). At about 2 KB per goroutine, 8 MiB covers about 4,000 goroutines. Feature
003 measured 2,052 goroutines in its worst synthetic queue-overflow case. `runtime.Stack(all)` stops the world
briefly. It runs only during a stop, a few times per stop, so that pause is acceptable.

**Alternatives considered.** An unbounded buffer, rejected under constitution V. Sampling only a part of the dump,
rejected because a partial dump cannot prove absence.

## R3 Blocking network traffic for one generation

**Decision.** Every generation gets its own HTTP clients for the websocket, pre-login and media traffic. Their
transports dial through a `fenceDialer` that wraps each `net.Conn`. The order at retirement is:

1. Disconnect the library client. This is its orderly close: it writes the close frame and sets its socket to nil.
2. Close the fence. Every wrapped connection is closed, every later dial is refused, and every later write on a
   wrapped connection is refused.

Refused dials and writes are counted. Reads that fail because the connection was closed are not counted, because
they are the expected end of the read loop.

**What the fence proves.** It proves **isolation**, not quiet. After step 1 the library's socket is nil, so a late
send by a detached handler, a retry receipt, a key request or a resent request returns `ErrNotConnected` inside the
library (`sendNodeAndGetData`) and never reaches the fenced `net.Conn`; nothing is counted. The fence does catch
anything that dials or writes through the fenced HTTP clients (media, pre-login, a new websocket dial). The evidence
that no library sender is still running is the goroutine observation (R1). The fence's other job is SEC-003: a fresh
client has its own socket and the old client's transports are closed, so nothing from the old client reaches the
fresh connection even if it calls `Disconnect()` again.

**Rationale.**
- The pinned client exposes `SetWebsocketHTTPClient`, `SetPreLoginHTTPClient` and `SetMediaHTTPClient`
  (`client.go:433-446`). Its websocket goes through `websocket.Dial` with the configured `http.Client`
  (`socket/framesocket.go:105`), so the transport's `DialContext` sees the real connection. The only other HTTP use
  is `update.go:45` (`GetLatestVersion`, with `http.DefaultClient`), which the project never calls: it has its own
  version provider.
- Production sets only the media client today. `SessionConfig.PreLoginHTTP` and `WebsocketHTTP` are for test
  injection, so the library's defaults (`ptr.Clone(baseHTTPClient)`) are used. Every generation now gets fenced
  clients built from the same defaults; test transports are wrapped, not replaced.
- Fencing after the library's own Disconnect avoids counting its close frame as a refusal.

**Tests that exercise the real path.** Writing straight to a wrapped connection proves only the wrapper (unit test).
The abuse-case tests drive the real client: a retry receipt or key request after `Disconnect` must return
`ErrNotConnected`, and the simulator, which counts frames per connection, must show zero frames from the old
generation on the fresh connection.

**Alternatives considered.** A proxy for the whole process, rejected because it is not per generation. Wrapping
only the websocket, rejected because media and pre-login requests are also network sends. Fencing before Disconnect,
rejected because the close frame would be counted and the fence could not tell it from a late send.

## R4 Counting late storage failures, late calls and dropped events

**Decision.**
- **Storage.** `DeviceBarrier.MarkRetiring()` is called when retirement starts. From then on the device session counts
  every guarded call that fails for a reason that comes from the guard or the context: `ErrRetired`, a context error,
  a stale lease and any other guard error, at every site (admission, `Save`, deferred `Delete`, nested and repair
  paths). A result that the database itself returns (for example "not found") is not counted.
  `DeviceBarrier.Refused()` reads the count.
- **Three contexts.** The stop signal cancels the owner context (`o.ctx`) through `BeginStop` (`owner.go:460`), and every
  generation lifetime is its child (`owner.go:222`), so the stop signal itself cancelled the library context before
  retirement. The library context (`BackgroundEventCtx` and the parent of the attempt context) is therefore created
  as `context.WithCancel(context.WithoutCancel(o.ctx))`; the program's own work (the abort of an unreturned Connect, the QR channel at
  `pairing.go:174`, the repair pass at `owner.go:922`) uses a separate work context; the attempt context is cancelled only until `ConnectContext` returns,
  through a recorded abort function called synchronously under `o.mu` (an `AfterFunc` would run in a new goroutine and
  let a Connect start after the stop began). `BeginStop` calls `MarkRetiring()` first. The
  program's own work (a Connect that has not returned, the repair pass, the pairing window) is
  cancelled at the start of retirement. The library context of a Connect that has returned is cancelled only after
  the fence and the barrier (contract steps 7-9).
- **Calls.** The session counts every call to it from the program's own code after the retire bit is set (Connect, QR
  channel, repairs, Disconnect), except this sequence's own Disconnect.
- **Events.** The session keeps its event handler registered until the check ends. It counts every event it would have
  forwarded to the owner and dropped, except `Disconnected`. Only then is the handler removed.

**Rationale.**
- The count of `ErrRetired` alone is blind to the case that matters most. The attempt context derives from the
  generation lifetime (`owner.go` near line 710) and becomes the node-handler context in the library
  (`client.go:601`). Cancelling it while the socket is live makes a handler's next store call, for example
  `PutAppStateSyncKey` at `message.go:842` after a decrypt transaction has committed the ratchet, fail with
  `context.Canceled`. The library logs it and continues; `latchWriteFault` and `report` treat a context error as the
  caller's. The handler returns in milliseconds, so the observation finds nothing. The key or sender key is lost, and
  on redelivery the message is ignored as an old counter. Counting every guard or context failure after retirement
  began, and cancelling the library context only after the blocks, closes this.
- The 2026-11-04 late-logout regression (`TestRunGuardLatePinnedLogoutAfterRetirement`) is a refused delete after
  retirement. The dropped `LoggedOut` event covers it from the other side.
- `Disconnect` emits no event in the library (`client.go:693-696`); `onDisconnect` dispatches `Disconnected` only on
  a remote or forced close. The server closes the connection after every post-pair 515, so a `Disconnected` after
  retirement is expected and carries no owner state. It is excluded entirely. Counting all events would also fail
  every busy account on receipts and presence, which the idle simulator would not show.
- The owner-relevant set is the set of events `Session.handleEvent` translates for the owner; T010 reads it from the
  code rather than listing it here. Feature 005 must revisit the rule when messages are ingested.

**Spec impact.** FR-002 conditions (b) to (e) are reworded, and FR-007 gets implementation tasks. See `spec.md`.

**Alternatives considered.** Treating only storage refusals as evidence, rejected for the reason above. Counting
every event, rejected as unrepresentative of a real account.

## R5 The allow-list

**Decision.** The allow-list starts empty. An entry names one exact function, for example
`go.mau.fi/whatsmeow.(*Client).someLoop`, with the pin it was reviewed for and the evidence for why it is harmless
after both blocks. Adding an entry is a reviewed change. A test compares the allow-list's pin with the
`go.mau.fi/whatsmeow` line in `go.mod` and fails on any difference, so a version change forces revalidation (FR-010).

**Rationale.** The spec requires exact names and no prefixes. An empty start means the measurement in R6 shows
whether any entry is needed at all.

## R6 Deadlines and pass rate: a measurement spike before the production check

**Decision.** The first implementation phase builds the fence, the counts and the observation, and puts the
production retirement order in place (the three contexts, the fence between Disconnect and the barrier, the pairing
window ended at retirement), without wiring the check into the marker. A test-only measurement on `internal/wasim`
then measures that production order, not a harness copy of it. It runs at least 50 orderly stops of each
kind: `pair` with the post-pair reconnect, `stdio` connected, `serve` connected. It records the time until the
library's work disappears (p50, p95, max), each failure category and the pass rate under candidate deadlines.

The owner then approves two fixed constants, recorded in `verification.md`:
- `quietStopDeadline`, for an ordinary stop;
- `postPairQuietDeadline`, for the pairing generation before the fresh connection.

The owner approves them together with the total stop budget: `Disconnect` (up to 5 s), the barrier drain (up to
10 s) and the observation all count against the 10 s shutdown bound (contract "Stop bound"). The measurement also
records how many goroutines in the same process belong to the simulator's own websocket server (`internal/wasim`
uses `github.com/coder/websocket` server-side); the package rule counts them, so the pass rate could be
understated. The owner decides at the gate whether to accept the number, run the simulator out of process, or filter
by the `internal/wasim` frames in the measurement only.

No production path clears the marker before that approval. If the pass rate is under the spec's 90% bar for any
kind, the owner decides before the code review whether to keep option A, tune it, or move to the fork.

**Rationale.** The clarification of 2026-11-07 chose measurement over a guess. The library's own disconnect waits
up to 5 s for its queue, a reference point only. The post-pair measurement drives the new retire order at the 515
(window ended, generation retired), not the old same-client path, because that is the state the deadline governs. On the real account the follow-up connection started about 0.2 s
after the 515, so a long post-pair deadline is a real-account risk that only a permitted run can show.

## R7 The fresh-client post-pair reconnect

**Decision.**
1. The 515 is handled as today: it waits for `PairSuccess` within the 30-second watchdog, and is refused on
   `PairError`, logout, stop or the watchdog.
2. The pairing window is ended, so its QR channel context ends and the emitter goroutine (`qrchan.go`) can return.
3. The pairing generation is retired in the contract's order, then the check runs with `postPairQuietDeadline`.
4. Only on a pass, a new generation is opened. `LoadDevice` reads the durably saved identity, so the handshake uses
   the saved-identity payload with the currently selected version and Linux/Chrome properties, with no
   registration.
5. One `attemptPostPair` runs under the single attempt slot and the 30-second watchdog.

Any failure or a failed check is final, with the existing incomplete-link report. `OwnerConfig.PostPairReconnect`
is deleted; the path is part of `pair` because only `pair` opens a pairing window.

**Rationale.**
- Feature 003's same-client reconnect is what lets late sends reach the new socket. A fresh client with a fenced
  old transport removes that by construction.
- `TestOwnerPostPairReconnectReachesConnectedAgainstSimulator` already shows that a saved-identity handshake
  completes on the simulator; the new path reuses it on a new generation.
- `PairSuccess` proves the guarded `Save` completed (feature 003), so the reload finds the identity.

**Alternatives considered.** Keeping the same client and only observing, rejected because the old client's late
`Disconnect()` and resent requests would still act on the new socket.

## R8 Where the result decides the marker

**Decision.** `Session.UpstreamDrainUnproven` and the `upstream_drain_unproven` event are replaced by
`Session.QuietStop(ctx, deadline) QuietStopResult`, called by `retireGeneration` after the store barrier has drained
and before `FinishRevocation`. The owner keeps a run-level flag: any failed check of a generation that attempted a
production Connect sets `unsafeStop`. A generation that never attempted Connect needs no check: today's rules apply.
`finishStop` keeps its other conditions unchanged: unresolved pairing, storage fault, latched logout cleanup, the
low-disk refusal of `ClearRunGuard`.

**Rationale.** It is the smallest change to an owner that feature 003 has already reviewed. The check is one more
condition before the existing clear.

## R9 Exit codes and messages

**Decision.**
- `stdio` and `serve` exit 0 after a passed check, as they do after an unconnected run today. After a failed check
  they keep today's exit 1 with `run_guard_recovery_required`.
- `pair`:
  - linked and passed: exit 0, "linked";
  - linked and failed: exit 0 with the existing recovery notice (owner decision 516648b);
  - incomplete link: exit 1, unchanged.
- New log events: `quiet_stop_passed` (info) and `quiet_stop_failed` (error), with a category and counts.
- A check whose decision comes after the 10 s shutdown bound fails with `stop_bound_exceeded`, so a late decision never
  clears the marker. A passed decision inside the bound can still be followed by `shutdown timed out` and exit 1 with a
  clean marker; that is safe.

Details are in `contracts/cli.md`.

## R10 E003-RX-01 recheck

**Decision.** Whether feature 004 is within E003-RX-01 is the owner's decision, not the plan's. The exception names
feature 003's private, local, single-owner deployment, the constitution says it is not inherited by another feature
or workload, and its follow-up asks for a recheck before another feature "materially expands the receive workload".
Reusable sessions make long connected runs possible for the first time. T001 therefore records the facts (owner, pin,
scope, expiry at the start of 2026-12-02 UTC, that the pin is unchanged, that no incident is recorded in the
available evidence, and that this feature makes long connected runs possible), asks the owner whether the exception
covers feature 004, and records the owner's answer. The implementer does not write "no trigger fired" on its own. The
same recheck is repeated at code review. If the exception has expired or the owner says it does not cover this
feature, work stops until the owner records a new decision (constitution X). The full reassessment stays with stage 2
or the expiry.
