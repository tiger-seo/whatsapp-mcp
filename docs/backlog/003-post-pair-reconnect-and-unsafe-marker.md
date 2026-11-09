# Backlog: post-pair reconnect and the unsafe-run marker (feature 003 debt)

- **Created:** 2026-11-06
- **Status:** Open. The owner approved enabling `OwnerConfig.PostPairReconnect` for the `pair` command on 2026-11-06. Still to do: a permissioned real-account re-check and a fresh code review of the enabled path. Research done 2026-11-07 ("Research", "Options for the owner"); the owner chose option A, stage 1, on 2026-11-07 (specification §8.11); it is specified as feature 004 in `specs/004-shutdown-drain/`. Stage 2 stays open here.
- **Affected features:** `specs/003-whatsapp-pairing-status` now; feature 005 (message ingest, history sync; feature 004 before the 2026-11-07 renumbering) is blocked by the second item below.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** before feature 005's (message ingest) real-account test, before any release, and on any relevant upstream whatsmeow fix. Keep it next to the E003-RX-01 expiry (start of 2026-12-02 UTC) and review both together.
- **Source:** `specs/003-whatsapp-pairing-status/verification.md` ("Real-Account Observation", "T077 Candidate Post-Pair Reconnect Against the Simulator", "T077 Decision (approved)"), roadmap "Follow-up: WhatsApp logout and shutdown drain".

## Debt

1. **Unjoined upstream work.** `Disconnect` joins none of the goroutines whatsmeow starts. Any node handler still
   running when `Disconnect` is called is detached at once (the handler-queue loop returns without waiting for it),
   not only one that outlives 10 x 30 s; that is a second detach route on a live connection. Add
   `go sendUnifiedSession`, the post-connect and pairing goroutines, async acks and retry receipts, app-state and
   privacy-token work, and the queue-overflow goroutines (these only enqueue). After a post-pair reconnect on the same
   generation, a late send from them can reach the new socket, and their store writes are not gated by the socket.
   Corrected 2026-11-07 from "any node handler that outlives 10 x 30 s" after the source audit below. Not proven: a
   held pinned node handler or `LoggedOut` dispatch across Disconnect and retry, and the full cancel, retire and stop
   matrix with the real client.
2. **No reusable session.** Any production Connect leaves the run marker unsafe, so every pairing needs a separately authorized offline recovery before the next run. Until this is solved there is no connected session `stdio` or `serve` can reuse, and feature 005's (message ingest) real-account history sync cannot run.
3. **Thin evidence.** Simulator and Windows only for the candidate; one real-account pair run (515 about 0.6 s after `PairSuccess`; the link failed with the follow-up denied). No Linux or macOS run of the enabled path.
4. **A risky path behind a code-level switch.** `PostPairReconnect` must not become permanent by default or by being forgotten.

## Follow-up

1. Find and verify a way to drain or account for all relevant upstream work after a Connect (the roadmap's "logout and shutdown drain" item), so the marker can be cleared after an orderly stop without offline recovery.
2. Replace the code-level switch and the unsafe-marker policy with that mechanism, or close this item by recording a reviewed decision to keep them.
3. Re-run the enabled path on Linux and macOS and with the real client's cancel, retire and stop matrix.
4. Recheck upstream whatsmeow for a fix to the unjoined goroutines.

## Research (2026-11-07)

Read-only research on the pinned source, upstream and the project's documents. Nothing here is decided or
authorized; the owner decides. Full report: `reports/Whatsmeow goroutine drain options.md`. Notes:
`research_notes/Whatsmeow goroutine drain options/`. Line counts are estimates from spawn sites, not from a written
patch. Paths below are in the pinned module (`go.mau.fi/whatsmeow@v0.0.0-20261027104441-1eec1136a50b`).

### Source audit

- **Disconnect.** It stops the socket, sets `cli.socket = nil`, wakes pending IQ waiters, and waits at most 5 s for
  the handler-queue loop to return (`client.go:697-738`). The loop returns on socket close without waiting for the
  running node handler (`client.go:931-969`), which runs with the Connect context that Disconnect does not cancel.
  Upstream's own `// TODO wait for handler queue to close here?` (`client.go:649`) shows the gap is known.
- **Goroutines.** About 60 `go` sites in 16 files, no `time.AfterFunc`, no WaitGroup. About 35 are reachable with the
  project's options; auto-reconnect, the 515 reconnect goroutine and automatic history download are not. Reachable
  writers or senders after Disconnect: the detached node handler (`client.go:941`, including `Store.Delete` on logout,
  `connectionevents.go:44, :129`), the post-connect goroutine (`connectionevents.go:187-209`), the pairing goroutine
  and its `go sendUnifiedSession` (`pair.go:148-159`), async acks and the retry receipt under `WithoutCancel`
  (`message.go:429-430`), push and business names (`message.go:57, :60`), notification handlers
  (`notification.go:479-483`), app-state key share and key requests under `WithoutCancel` (`message.go:924`,
  `appstate.go:165`), privacy tokens on `BackgroundEventCtx` (`tctoken.go:149-180`), and the QR emitter, which can call
  `Disconnect()` itself (`qrchan.go:74-91`). Overflow goroutines only enqueue (`client.go:871-908`); `readPump` can
  leak on a bare channel send (`socket/framesocket.go:157-164`). Five `WithoutCancel` and four `BackgroundEventCtx`
  uses are deliberate, so no caller context can stop all of it.
- **Late store writes.** Not gated by the socket. The project's store guard rejects them after retire (`ErrRetired`),
  but a rejected write is crash-like, which is why the marker stays unsafe.
- **Late sends after a same-Client reconnect.** Sends read `cli.socket` at send time (`client.go:972-990`), so after
  the post-pair `Connect` on the same Client they go out on the new socket: `sendUnifiedSession`, retry receipts,
  app-state and privacy-token work, the old post-connect `Connected` dispatch, and a late `Disconnect()` from the
  pairing error tail or QR emitter. An IQ interrupted by Disconnect resends its frame on the new socket within 5 s
  (`request.go:185-216`; `unlockedConnect` clears the flag that would stop it). That last race is from reading the
  source, not observed.
- **Patch sizing (estimate).** A client-wide tracker with a re-armed "drained" channel (about 30 lines), about 45
  one-line `go f()` to `cli.goBg(f)` substitutions, about 10 lines in `socket/` (including a select in
  `frameComplete`), and a new `WaitIdle(ctx) error`: about 100-150 lines in about 15 files, no protocol change for
  callers that never call it. `Disconnect` stays non-blocking: library goroutines and handlers call it, so a wait
  inside it would deadlock. Optional (about 15 lines, a behaviour change): a cancellable drain context in place of the
  `WithoutCancel` sites, so the wait does not sit out 75 s IQ timeouts. Whether the wait fits the 30 s connect
  watchdog is unmeasured.

### Upstream status

- **No fix.** No issue, PR or commit proposes a Disconnect that cancels and joins all goroutines, and nothing touches
  `sendUnifiedSession`'s lifecycle. HEAD on 2026-11-07 keeps the 5 s bounded wait and detached handlers. Upstream
  fixes narrow pieces itself (per-connection queue, overflow goroutines on the socket context).
- **Related.** PR #1327 (open, collaborator, 2026-11-07): a keepalive-forced reconnect wipes a concurrent caller's
  `Disconnect`, so a dropped client keeps reconnecting. Track it.
- **Check on upgrade.** #1308 (merged 2026-10-29, commit `2fc844f`) makes handshake timeouts and closed sockets
  retryable, so `ConnectContext` starts `autoReconnect` in more cases when both auto-reconnect flags are on. Any pin
  past it must re-check that the project keeps them off. None of the 11 commits since the pin touches `Disconnect`.
- **Contribution policy.** Changes of "more than a few lines" must be explained first in the Matrix room
  `#whatsmeow:maunium.net`, or the PR may be closed without comment; PR text must be written by a human; a PR may be
  rewritten instead of reviewed. In 2026, 28 PRs were merged and 94 closed unmerged; outside lifecycle PRs were closed
  silently.
- **Cadence.** About 23 commits a month in 2026, mostly by tulir, no tags, no releases. A fork needs rebases every few
  weeks, in the files the patch touches.

### Patch-carrying mechanics

- **Replace to a fork keeping the module path.** `replace go.mau.fi/whatsmeow => github.com/<owner>/whatsmeow
  <pseudo-version>`, the fork keeping `module go.mau.fi/whatsmeow`: no import rewrites, `go.sum` lines, and the
  existing `go mod verify` step still covers it. Rollback is removing one line plus `go mod tidy`. `go install
  module@version` stops working; the project does not promise it (releases ship through GoReleaser).
- **Why vendor and `third_party/` fail constitution V** ("exact versions verified against `go.sum`"). The go command
  does not detect edits in `vendor/`, `go mod vendor` deletes them, and a directory replacement has no sum. Vendoring
  also pulls every dependency into the repository.
- **MPL-2.0.** whatsmeow is file-level copyleft. Modified files stay MPL-2.0 with their notices, and binary
  recipients must be told where to get that source. A public fork plus a NOTICE naming fork and commit satisfies it; a
  private fork does not once binaries reach others, and publishing the fork is itself an act needing approval. The
  repository has no third-party notice today even unpatched. Not legal advice.
- **CI.** A weekly and manual drift job that rebases the patch onto upstream HEAD in a scratch clone, runs the tests
  and opens an issue (the patch's own regression test shows when upstream fixed it); a provenance check that the fork
  diff touches only allow-listed files; whatsmeow on Dependabot's `ignore` list, since bumps to the overridden
  `require` line change nothing.

### No-fork combination

- **Fresh Client per connection.** The post-pair reconnect opens a new generation and `whatsmeow.Client` instead of
  reconnecting the paired one. This removes the same-Client crossing by construction. The 2026-11-04 limitation in
  specification §8.11 forbids another Connect after retiring a generation "whose drain is unproven, including by
  replacing its client", so this needs the check below to pass first, or a new owner decision. Unverified: whether that
  pause, or loading a new device generation between `PairSuccess` and the 515 reconnect, changes what WhatsApp sees.
- **Socket and HTTP fencing.** Per-generation websocket, pre-login and media `http.Client`s with a fenceable
  `net.Conn` from `DialContext` and a refusal counter, through the public setters the project already uses. Fence the
  socket before the store: with the store fenced and the socket live, a decrypt that fails with `ErrRetired` gets acked
  and is lost from the server queue (`message.go:420-432`). The current retire order already does this.
- **Existing store fence.** Already gives "nothing writes after the stop". Add a count of admissions rejected after
  the retire flag.
- **Runtime goroutine check.** At stop, poll `runtime.Stack(all)` up to a deadline until no goroutine has a whatsmeow
  frame or "created by" frame, and require zero fence hits; only then clear the marker, otherwise keep today's unsafe
  path. It observes quiescence; it does not join. It needs an allow-list of goroutines third-party libraries start for
  whatsmeow (not yet traced), and how often a real stop passes is unknown until measured.
- **Size (estimate).** About 100 lines for the fenced dialer, 100-150 for the check, a small counter change, and a
  moderate but security-sensitive change to `runPostPairReconnect`. All testable with `internal/wasim`.

### Project constraints

- **Fork authority is withheld.** No D1-D18 decision mentions forks. But E003-RX-01 "does not ... authorize
  implementation/fork/publication", the feature 003 verification keeps "the exact current dependency ... no private
  fork, protocol patch", the 2026-11-04 limitation "authorizes no ... dependency change", and the receive-bounds
  backlog asks for separate approval of any fork, licence, rebase maintenance or publication.
- **Constitution IX.** A new dependency must be pure Go and justified in the feature's plan, so a fork fits best as
  its own roadmap feature ("logout and shutdown drain"), not inside 003.
- **Constitution X.** Reassessment is required on a dependency version change; E003-RX-01 says a pin change requires
  reassessment. A fork therefore reassesses E003-RX-01 when it lands, not at the 2026-12-02 expiry. Its scope covers
  the same queued and overflow work, so take both decisions in one owner review before 2026-12-02.
- **No-fork route.** Needs no dependency decision and leaves E003-RX-01 on the unchanged pin, but it changes the
  recovery policy and the pairing path, so it also needs a recorded owner decision and design and code reviews.

## Options for the owner

| Option | Fixes among debts 1-4 | Cost (estimate) | Risk |
| --- | --- | --- | --- |
| **A. No-fork combination with a quiet-stop runtime check** | 1 by construction, 2 when the check passes, 4; not 3 | A few hundred lines in `internal/wa`; no rebase burden | Proof is observed, not structural; may pass rarely; the fresh-Client reconnect needs the check to pass first |
| **B. Fork with `WaitIdle`** | 1, 2 and 4 structurally; not 3 | About 100-150 patch lines, a public fork, drift job, Dependabot ignore, MPL notice | Rebase churn in upstream's busiest files; owner decisions on fork, publication and E003-RX-01; still needs the store fence and socket ordering |
| **C. Upstream proposal (Matrix first)** | As B, if accepted, with no carried code | Small: a hand-written plan in Matrix, then a small additive PR | Silence or a rewrite by tulir; timing outside the project's control |
| **D. Keep the current policy** | None; closes the item only by a reviewed decision | Nothing now; an offline recovery after every pairing | Feature 004's real-account history sync stays blocked |

The research's recommendation, not a decision: **A**, measured on simulator and permissioned real-account stops,
with **C** opened in parallel because a Matrix proposal for an opt-in `WaitIdle` costs little, and **B** as the
fallback if A's check rarely passes. **D** stays in force until the owner records a decision.

### Staged recommendation (2026-11-07, not decided)

Refined after the security analysis below: take A in two stages, with C in parallel and B as the fallback.

- **Why A over B.** Both fix debts 1, 2 and 4. B's guarantee is stronger (it joins every goroutine instead of
  observing them), but it means owning a public fork of a library that handles keys and pairing, in upstream's
  busiest files (about 23 commits a month), and every recent project decision withholds authority for a fork. A
  gets most of the benefit without that, and a failed check falls back to today's offline recovery.
- **Stage 1: clear the marker, add no reconnects.**
  - Run the post-pair reconnect on a fresh Client.
  - Block the old socket and HTTP traffic, then the store, in that order.
  - Clear the marker only after the quiet-stop check passes.
  - Fixes debt 2 (unblocks feature 005's history sync), removes the `PostPairReconnect` switch and the late sends
    on the new socket. Adds no new automatic network activity, so it carries the least risk.
- **Stage 2: bring back 405 and ordinary reconnects (T050, T051, T053, T054, T056).**
  - This is the part that adds exposure: unattended connections, possible abuse signals to WhatsApp, more receive
    work in E003-RX-01's scope.
  - Take it only after stage 1 has measured how often the check passes, with the gate rewritten, a rate limit on
    ordinary reconnect and E003-RX-01 reassessed.
- **The deciding question for the owner.** Is an observed quiet stop (no whatsmeow goroutines left and no refused
  calls) enough proof to clear the marker? If yes, A as above. If no, only B (or C, if upstream accepts it) meets
  the bar, and D stays until then.
- **Deadline.** Record the decision by the E003-RX-01 expiry (start of 2026-12-02 UTC), since the two items are
  reviewed together.

## Option A and the open US4 tasks (analysis 2026-11-07, not decided)

Question: would option A let feature 003 finish T050, T051, T053, T054 and T056 (`specs/003-whatsapp-pairing-status/
tasks.md`, Current State row 4)? Short answer: partly, and only after a design change.

- **The gate as written does not allow it.** `contracts/connection-owner.md` (the proof gate) permits a retry only
  if it "stays in the same active generation", and says "never start another Connect on or after retirement of an
  unproven upstream generation, including a replacement client". `spec.md` (the startup/405/515/ordinary reconnect
  clause) and `data-model.md` (the budgets paragraph) say the same. Option A does the opposite: a fresh Client and
  a new generation for each reconnect, allowed only after the old generation passes the quiet-stop check.
- **Design change needed.** The gate would read: fresh Client and generation, allowed only after the old one is
  observed drained (no whatsmeow goroutines, no fence hits). That revises spec, contract, data model and tasks,
  makes the design review stale and needs a new owner-judge design review. Whether an observation counts as the
  gate's "demonstrating" is the owner's decision, not a fact.
- **Per task, if that change is approved:**
  - **T053 (405 controller):** yes. A 405 is a rejection during the handshake, before login, so little upstream
    work has started; the check likely passes inside the five-second wait (not measured).
  - **T054 (refresh/retry/515/ordinary integration):** mostly. 405 and 515 should work. An ordinary reconnect after
    a live session dropped may wait on running handlers (requests can take up to 75 s), so that path will sometimes
    fail closed and report unavailable. Acceptable, but not "ordinary reconnect always works".
  - **T050 (405 budget tests):** yes, on the simulator; the task text needs rewording for fresh-Client retries.
  - **T051 (version snapshot tests):** the untested clauses (selected version on explicit, ordinary and 515
    reconnect) become testable, because each fresh Client runs a full handshake.
  - **T056 (synthetic command cases):** yes, once T053 and T054 exist; it also needs measured quiet-check timings.
- **Knock-on effects.**
  - Moving the post-pair reconnect onto a fresh Client changes the pairing flow. That reopens US2 integration
    under the owner's 2026-11-06 condition and needs a new review of the T077 path.
  - Two entries under "Decisions already made" in `tasks.md` would need the owner to reopen them: the default
    denial of startup, 405 and ordinary reconnect, and "any production Connect leaves the marker unsafe".
  - All of this is simulator evidence; real-account runs need the owner's permission each time.
- **Suggested order.**
  1. Build A's parts.
  2. Measure how often the quiet-stop check passes on the simulator.
  3. Revise the gate and task wording, then run owner-judge in design mode.
  4. T053, then T054, then T050 and T051, then T056.

  If the check rarely passes for ordinary reconnects, rescope T054 to 405 and 515 only.

## Option A: security impact (analysis 2026-11-07, not decided)

Overall a modest gain if it fails closed. It removes the most concrete integrity problem (late sends from an old
session landing on the new socket) and adds no supply-chain risk. The cost is that clearing the marker would rest on
a runtime check, not on a structural guarantee; its new failure modes can each be contained.

**What improves**

- **No cross-session sends.** Today, after the post-pair reconnect on the same Client, late retry receipts,
  app-state key shares, acks and resent requests can go out on the new socket. With a fresh Client and the old
  socket blocked, they cannot.
- **No supply-chain risk.** No owned fork of a library that handles keys and pairing. Option B would need a public
  fork, its own review of the pairing path, and frequent rebases that each need checking.
- **Fewer manual recoveries.** Offline recovery after every run is an operator step that can go wrong (see
  `003-offline-recovery-prompt-usability.md`). Needing it only after a stop that fails the check reduces that
  exposure.
- **The `PostPairReconnect` switch goes away.** A code-level switch that someone forgets to turn off is a latent risk.

**New risks and how to contain them**

1. **A false pass clears the marker while work is still running.** Worst case: lost Signal session, pre-key or
   app-state key writes, meaning messages that fail to decrypt, pre-keys used twice, or app-state that no longer
   matches.
   - The store block already limits this: any call it lets in finishes before the block returns, and later calls
     are refused.
   - The check must be "no whatsmeow goroutines **and** zero refused calls **and** the socket blocked first", with
     the refused-call count recorded before the marker decision.
   - The allow-list of third-party goroutines must name exact functions, never a broad prefix such as
     `go.mau.fi/`, and be retested against the pinned whatsmeow version on every upgrade.
2. **Blocking the store before the socket loses messages.** If the store is blocked while the socket is live, a
   decryption that fails because its write was refused makes whatsmeow send an ack and a retry receipt, and the
   server may drop the message. "Close the socket and HTTP traffic, then block the store" must be a stated, tested
   invariant; the current code follows it, but nothing documents it.
3. **Reconnects come back.** 405 and ordinary reconnects are denied by default today, the safest position. Allowing
   them means connections the operator did not start.
   - A hostile or flaky network could provoke disconnects to drive reconnect loops. The 405 budget limits this;
     ordinary reconnect needs its own rate limit.
   - Each fresh Client does a full handshake. Repeated logins may look like abuse to WhatsApp, a risk to the account.
   - More connections mean more receive work in E003-RX-01's scope (no upstream receive-resource bounds): a reason
     to reassess E003-RX-01, not merely an operational change.
4. **The goroutine check runs in production.**
   - `runtime.Stack(all)` briefly pauses all goroutines and needs a buffer sized to the goroutine count. Cap the
     buffer and fail closed if the dump is cut off.
   - The dump holds function arguments as raw words (pointers and integers), so it must never be logged or written
     out. Report only a category and a count, as the project's diagnostics rule already requires.
5. **The project's own code can still reach the old Client.** The project's event handlers or MCP tools could
   still hold the old Client after it is retired. They must go through the same generation block, or the check can
   pass while project code starts new whatsmeow work.
6. **The post-pair reconnect on a fresh Client is unverified.** Loading the new device from the store between
   `PairSuccess` and the 515 reconnect is a new path on the pairing flow. A mistake there affects the real account;
   it needs the owner code review and a real-account run the owner authorizes.

**On balance**

- **Integrity of the WhatsApp session:** better than today's same-Client post-pair reconnect.
- **Availability and unattended network activity:** more exposure than today's policy, because reconnects return.
- **Confidence:** lower than option B, because it rests on observation rather than joining every goroutine.

If the check can only keep the marker unsafe and never clear it on partial evidence, the worst outcome is today's
offline recovery. Conditions to make A acceptable:

- the check fails closed in every case of doubt;
- the socket-before-store order is tested;
- ordinary reconnect has a rate limit;
- E003-RX-01 is reassessed before the reconnect paths are enabled.

## Correction: a process exit does not prove the data is intact (analysis 2026-11-07, not decided)

An idea raised the same day: let every retry be "exit and start a new process", on the argument that the unsafe
marker only guards in-process work, so a dead process proves nothing can still send or write. The research above
contradicts the premise.

- **The marker also guards durable state.** A store write rejected after retire is crash-like, and a decrypt that fails
  because its write was refused makes whatsmeow send an ack, so the server may drop the message for good
  (`message.go:420-432`). A process exit repairs neither. Exit alone is not proof.
- **What survives from the idea.** Option A's conditions already cover the durable part: close the socket and HTTP
  traffic first, block the store second, and require a refused-call count of zero. If that holds at an orderly stop,
  goroutines that had not yet written anything die with the process and leave no inconsistent state. This might let
  the `runtime.Stack` scan be dropped from stage 1 and be replaced by the process exit. **Hypothesis, not a finding:**
  it needs a simulator test (a stop with a goroutine pending, a stop with a refused call) and an owner decision. Its
  cost for `stdio` is open: a process cannot restart itself under a client that holds stdin and stdout.
- **Scope for feature 003.** The open US4 tasks (T050, T051, T053, T054, T056) are stage 2 above. They need the
  revised gate, a rate limit for ordinary reconnect and a reassessment of E003-RX-01, so they cannot be finished by
  implementation inside 003. Options for 003: rewrite them as "denial by default and diagnostics" (already built and
  tested; needs a `tasks.md` change outside Current State and a fresh owner design review) or leave them open into
  the code review. Decided 2026-11-07: the tasks were rewritten as denial-only and this work is deferred to a separate feature (roadmap follow-up).

## Closure

Close when the marker clears safely after an orderly stop and the switch is gone, or when the owner records a reviewed decision to accept the current policy permanently. Do not close it by silence.

## Update 2026-11-08: feature 004 delivered debts 1, 2 and 4 (not yet code-reviewed or run on a real account)

Branch `004-shutdown-drain` (specs/004-shutdown-drain/): the stop retires each generation behind a network fence and a
storage barrier and checks the process for the library's goroutines (`internal/wa/quiet.go`); a pass clears the marker
(debt 2). The connection requested after pairing now runs on a fresh client after the pairing client passed that check,
so late sends of the old client cannot reach it (debt 1, for the pinned library and the simulator), and
`PostPairReconnect` is removed (debt 4). Debt 3 (thin evidence) stays open: simulator and Windows only, no real-account
run of the new path yet. **Stage 2 stays open**: joining or accounting for the library's goroutines at source, startup
retries, the 405 budget and ordinary reconnect remain denied. Do not close this item until the code review and the
permitted real-account journey are recorded.

## Update 2026-11-09: real-account journey done, code review open

The permitted real-account journey ran on 2026-11-08 (one account, one phone, Windows): the fresh-client link
completed twice, and restarts reused the saved session; after the fence fix `3882532` seven of seven stop checks
passed (`specs/004-shutdown-drain/verification.md`, "Real-account journey"). Debt 3 is narrower but not closed: one
account, one phone, and the current library pin (`f1b7985`, 2026-11-09) has not run on a real account. The feature 004
code review is still open, so this item stays open.
