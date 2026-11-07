# Feature Specification: Reusable Session After a Proven Quiet Stop

**Feature Branch**: `004-shutdown-drain`

**Created**: 2026-11-07

**Status**: Draft

**Input**: User description: "@docs/backlog/003-post-pair-reconnect-and-unsafe-marker.md". This is stage 1 of the roadmap follow-up "WhatsApp logout and shutdown drain": clear the unsafe-run marker after an orderly stop proven quiet, move the post-pair reconnect onto a fresh client, and remove the code-level `PostPairReconnect` switch. No new automatic reconnects.

## Clarifications

### Session 2026-11-07

- Q: Is an observed quiet stop enough proof to clear the unsafe-run marker? A quiet stop means network traffic is blocked first, then the store, then none of the client library's work is still running and no call was refused, and every doubt fails closed. -> A: Yes, option A from the backlog item, with no fork. A whatsmeow fork with a structural join (option B) stays the fallback if the check rarely passes. An upstream proposal (option C) is a separate step the owner takes.
- Q: Which stages does this feature cover? -> A: Stage 1 only. Clear the marker after a proven quiet stop, run the post-pair reconnect on a fresh client and remove the switch. The 405 budget, startup retries, ordinary reconnect and same-process re-pair stay denied and belong to a later feature (stage 2).
- Q: How is the feature numbered? -> A: 004. Message ingest and every later roadmap feature move down by one number, because ingest depends on this feature.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Restart Without Offline Recovery After a Clean Stop (Priority: P1)

As the account owner, I stop `stdio` or `serve` normally and start it again later. The saved session connects again without an offline recovery and without a new pairing, provided the program could prove that nothing from the previous connection was still running when it stopped.

**Why this priority**: Today every run that connects to WhatsApp leaves the marker unsafe, so every restart needs an offline recovery and a fresh pairing. That makes a long-running server unusable and blocks the real-account history sync of message ingest (feature 005).

**Independent Test**: On the simulator, connect a saved session, stop it normally with no library work left, and restart: the second run connects without recovery. Repeat with leftover work, a refused call and an incomplete observation; each keeps the marker unsafe and the restart refuses to connect.

**Acceptance Scenarios**:

1. **Given** a connected session with no library work left at stop, **When** the owner stops the command normally, **Then** the program blocks network traffic, then storage, observes that no library work remains and that no call was refused, clears the marker, closes storage and releases the lock, and reports that the session can be reused.
2. **Given** that clean stop, **When** `stdio` or `serve` starts again, **Then** it connects with the saved session, opens no pairing window and needs no offline recovery.
3. **Given** library work that is still running when the observation deadline passes, **When** the command stops, **Then** the marker stays unsafe, storage still drains and closes in order, and the next start requires offline recovery as today.
4. **Given** a call to storage or the network that was refused after its block, **When** the check runs, **Then** it fails, whatever the observation shows, and the marker stays unsafe.
5. **Given** a confirmed logout or a request to delete the client during the run, **When** the stop passes the check, **Then** the marker clears only if the logout cleanup has been made durable, as today; a passing check never overrides a logout.
6. **Given** a crash, a forced kill, a second stop signal that forces exit, or a stop that exceeds its bound, **When** the process ends, **Then** the marker stays unsafe.
7. **Given** a run that never attempted a production connection, **When** it stops normally, **Then** the existing safe-clear rules apply unchanged.
8. **Given** the late-logout ordering that caused the 2026-11-04 fail-closed policy (a logout handled by the client library after retirement, finding its callback removed and its delete refused; regression `TestRunGuardLatePinnedLogoutAfterRetirement`), **When** the stop is checked, **Then** the check fails, either because the logout work is still running at the deadline or because its delete was refused, and the marker stays unsafe. A clean marker is never committed before that work has finished.
9. **Given** a client-library message handler that is still running (held) when its generation is retired, **When** the check runs, **Then** it fails until the handler has returned, and passes only if the handler made no call after the blocks.

---

### User Story 2 - Pair Once and Keep the Device (Priority: P1)

As the account owner, I run `pair`, scan the QR code once, and the device stays linked and reusable. The connection that WhatsApp asks for right after pairing no longer runs on the client that did the pairing, so late traffic from the pairing session cannot reach the new connection.

**Why this priority**: Today the post-pair reconnect reuses the pairing client. Late work from the pairing session can send on the new socket, and the run still ends with the marker unsafe, so the newly linked device needs an offline recovery before first use.

**Independent Test**: On the simulator, pair, receive the restart request that follows pairing, and confirm that the pairing session's late sends never reach the new connection, that the new connection uses a fresh client and generation, and that a clean stop leaves the marker clear for the next `stdio` start.

**Acceptance Scenarios**:

1. **Given** a successful pairing followed by WhatsApp's restart request, **When** the pairing generation passes the quiet-stop check, **Then** the program starts exactly one connection on a fresh client and generation, loaded from the durably saved identity, under the single attempt slot and the 30-second watchdog.
2. **Given** that the fresh connection confirms, **When** `pair` stops normally and its own stop passes the check, **Then** `pair` exits successfully, the marker is clear and the next `stdio` or `serve` start reuses the session.
3. **Given** that the fresh connection confirms but `pair`'s own stop fails the check, **When** `pair` ends, **Then** it exits 0 with the existing "linked, offline recovery is required before the next run" notice and the marker unsafe, as the owner decided for feature 003 (design review 516648b). A storage fault or any other stop error still exits 1.
4. **Given** the restart request arrives before the pairing success has been handled (observed on the simulator in feature 003), **When** the owner receives it, **Then** it waits for the pairing success within the 30-second watchdog, as today; a pairing error, logout, stop or the watchdog refuses it and no connection starts.
5. **Given** the pairing generation fails the check, **When** the restart request arrives, **Then** no further connection starts; `pair` reports an incomplete link that needs offline recovery, keeps the marker unsafe and exits unsuccessfully.
6. **Given** the fresh connection fails, stalls past its watchdog or is cancelled, **When** `pair` ends, **Then** the failure is final: no second reconnect, an incomplete-link report, the marker unsafe and an unsuccessful exit.
7. **Given** late work from the pairing generation (a session announcement, a receipt, a key share, a resent request or a disconnect), **When** it tries to send or write after its generation's blocks, **Then** it is refused, never reaches the fresh connection, and counts as a refused call for the pairing generation.

---

### User Story 3 - Know Why the Session Cannot Be Reused (Priority: P2)

As an operator, when a stop does not pass the check, I can see the reason in plain words and know that offline recovery is needed. Over several runs I can see how often stops pass, which decides whether the fork fallback is needed.

**Why this priority**: A failed check must not look like a crash, and the owner needs a measured pass rate to judge option A against the fork.

**Independent Test**: Force each failure category on the simulator and read the stop diagnostic and the `status` output. No diagnostic may contain library state, arguments, keys or account identifiers.

**Acceptance Scenarios**:

1. **Given** a stop that fails the check, **When** it finishes, **Then** stderr reports one failure category (for example, work still running, a refused network call, a refused storage call, an incomplete observation or a deadline), the counts involved and that offline recovery is required, without raw observation data.
2. **Given** a stop that passes, **When** it finishes, **Then** stderr reports that the session can be reused.
3. **Given** either outcome, **When** `status` runs afterwards, **Then** it reports the marker state as it does today; nothing new is stored beyond the marker.

### Edge Cases

- WhatsApp sends the restart request while pairing work is still finishing. The fresh connection waits for the check only up to the check deadline; it never starts while work from the pairing generation may still run.
- A logout arrives during the observation. It is latched as today; the marker clears only after durable cleanup.
- The disk is low when the marker would be cleared. The existing low-disk admission refuses the write; the marker stays unsafe and the operator is told to free space and run offline recovery.
- The observation of running work is larger than its bounded buffer. The result counts as incomplete and fails.
- A third-party library starts work on the client library's behalf. Only work named exactly in an allow-list may be ignored; anything else fails the check.
- The program's own event handlers or future tool code still hold the retired client. Their calls go through the same generation block and are refused and counted, so they cannot start new library work unseen.
- A dependency version change. The allow-list and the check must be revalidated against the new version before release.
- `stdio` cannot restart itself while a client holds its standard input and output, so the check runs inside the process. A process exit alone is not proof.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Stopping a generation MUST happen in this order: block that generation's network traffic (socket and HTTP), then block its storage access, then observe the client library's remaining work. Storage MUST NOT be blocked while the generation's network can still receive, because a refused write during decryption can make the library acknowledge and lose a message.
- **FR-002**: A generation passes the quiet-stop check only if, before a fixed deadline, (a) none of the client library's work is running, except work named exactly in a reviewed allow-list, (b) no network call was refused after the network block, (c) no storage call was refused after the storage block, and (d) the observation is complete. Every other outcome, including an error, a cut-off observation or the deadline, fails.
- **FR-003**: After a production connection attempt, the marker MAY be cleared at orderly shutdown only when every generation of the run passed the check and all existing safe-clear prerequisites hold: storage drained, any latched logout or client deletion durably cleaned up, and low-disk admission granted. Otherwise the marker MUST stay unsafe and the next start requires offline recovery, as today.
- **FR-004**: The post-pair reconnect MUST run on a fresh client and a new generation, built from the durably saved identity, and only after the pairing success has been handled and the pairing generation has passed the check. It keeps feature 003's rules: a restart request before the pairing success waits for it within the 30-second watchdog, and a pairing error, logout, stop or the watchdog refuses it. It is one owner-initiated attempt under the single attempt slot and the 30-second watchdog. The fresh client MUST log in with the saved identity (no new registration) and the currently selected WhatsApp Web version and Linux/Chrome identification, and a test MUST assert the selected version on this connection. A failure, stall, cancellation or failed check is final: report an incomplete link that needs offline recovery, keep the marker unsafe and exit unsuccessfully.
- **FR-005**: The code-level `PostPairReconnect` switch MUST be removed. The post-pair reconnect is part of `pair` only; `stdio` and `serve` do not pair and have no reconnect.
- **FR-006**: No other automatic or repeated connection is added. Startup retries, the 405 budget, ordinary reconnect, any other 515 reconnect and same-process re-pair stay denied with their existing diagnostics.
- **FR-007**: Every path from the program's own code to a client MUST go through that client's generation block. After retirement, such calls are refused and counted toward the check.
- **FR-008**: The observation MUST have a bounded memory size and a bounded duration. Its raw contents MUST NOT be logged, stored, returned or included in errors at any level. Only the outcome, a failure category, counts and the duration may be reported.
- **FR-009**: Each check MUST report its outcome on stderr in operator wording: reusable, or offline recovery required with the category. The check's outcome replaces the blanket "upstream drain unproven" latch and its `upstream_drain_unproven` event; the `run_guard_recovery_required` event stays for every stop that leaves the marker unsafe. stdout in stdio mode stays protocol-only.
- **FR-010**: The allow-list MUST name exact functions, never a module or package prefix. It is tied to the pinned client library version; a version change MUST fail a test until the allow-list is revalidated.
- **FR-011**: Shutdown keeps its existing guarantees: every admitted device write finishes before storage closes, and the lock is released only after that. The check MUST NOT lengthen the existing 10-second stop bound for writes. A failed check is not a failed shutdown; the process exits after an orderly close with the marker unsafe.
- **FR-012**: Offline recovery, `status` and the marker's storage format stay as they are. A clean marker after a passed stop MUST look exactly like a clean marker after a run without a production connection.
- **FR-013**: The specification's temporary fail-closed limitation (§8.11, 2026-11-04) and the post-pair reconnect approval (§8.11, 2026-11-06) MUST be updated to the 2026-11-07 decision before implementation starts. Feature 003's spec, contracts and `verification.md` stay as the historical record; `OPERATIONS.md` and `AGENTS.md` MUST be updated to describe a reusable session after a passed check, and to say that offline recovery is still needed after a failed one.

### Key Entities *(include if feature involves data)*

- **Generation**: one client instance and its connection, from creation to retirement. It is the unit the blocks and the check apply to.
- **Network block / storage block**: per-generation gates that refuse new calls after retirement and count the refusals.
- **Quiet-stop result**: pass or fail with a category, counts and the duration. It exists in memory and in the stop diagnostic only.
- **Allow-list**: the reviewed, exact set of library work that may outlive a generation without failing the check, tied to one library version.
- **Run marker**: the existing durable unsafe-run marker; its format does not change.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the simulator, every scripted orderly stop with no leftover work and no refused calls clears the marker, and the next start connects with zero offline recoveries and zero QR codes.
- **SC-002**: Every scripted fault (leftover work at the deadline, a refused network call, a refused storage call, a cut-off observation, a logout without durable cleanup, a crash or a forced exit) leaves the marker unsafe: zero false clears across all fault scripts.
- **SC-003**: Across scripted post-pair runs that inject late sends from the pairing generation, zero of them reach the fresh connection.
- **SC-004**: A test proves that storage is never blocked while that generation's network can still receive, and that reversing the order fails the test.
- **SC-005**: The pass rate of the check is measured over at least 50 simulated orderly stops of each kind (`pair` with post-pair reconnect, `stdio`, `serve`) and recorded in `verification.md`. It is a measurement for the owner's choice between option A and the fork, not a target.
- **SC-006**: Captured diagnostics at every log level contain zero raw observation data, keys, QR payloads or full account identifiers.
- **SC-007**: With separate permission for each step, one real-account run of `pair`, a normal stop, and a restart with the saved session (`stdio`, or the `session-check -action connect` helper) without recovery is recorded with redacted results, or recorded as not verified if not performed. This closes feature 003's open gap "a real reuse of a clean saved session". The observed pass or fail of each check is recorded.

## Assumptions

- The check's observation deadline is a fixed constant, proposed at 5 seconds after the storage block (the same as the library's own bounded wait in its disconnect). Planning measures it on the simulator, and any change needs the owner's review.
- Stopping the pairing generation and loading the device again between pairing and the fresh connection may change what WhatsApp sees. On the real account the restart request came 0.6 to 0.8 s after the pairing success and the follow-up connection was made about 0.2 s later (feature 003 `verification.md`); the check adds up to its deadline to that gap. This is unverified and needs a permitted real-account run.
- If the permitted real-account run shows that the fresh-client reconnect does not complete a link, the owner decides before the code review whether to fix it, keep feature 003's same-client post-pair reconnect with the marker left unsafe, or stop the feature. Removing the code-level switch (FR-005) removes feature 003's fallback of turning it off, so this decision replaces that fallback.
- The existing store block already refuses calls after retirement; this feature adds a refusal count and a matching block for network traffic.
- The client library version stays pinned. A version change (including a pin past upstream change `2fc844f`) is a separate decision with its own E003-RX-01 reassessment.
- Feature 003's recovery prompt wording is tracked separately in `docs/backlog/003-offline-recovery-prompt-usability.md` and is not changed here.

## Scope and References

This feature covers stage 1 of the roadmap follow-up "WhatsApp logout and shutdown drain" and the backlog item `docs/backlog/003-post-pair-reconnect-and-unsafe-marker.md` (debts 1, 2 and 4). It revises the §8.11 entries of 2026-11-04 (temporary fail-closed connection limitation) and 2026-11-06 (post-pair reconnect) as recorded in the clarification above, and keeps M10's shutdown ordering. Decisions D1–D18 are not reopened.

Included: the per-generation network block and refusal count, the storage refusal count, the quiet-stop check and its allow-list, clearing the marker after a passed stop, the fresh-client post-pair reconnect, removal of the code-level switch, stop diagnostics, and simulator tests and measurements.

Excluded: the 405 budget, startup retries, ordinary reconnect, other 515 reconnects and same-process re-pair (stage 2, a later feature, after the pass rate is known, with a rate limit and a reassessed E003-RX-01); a whatsmeow fork or any dependency change (option B, a separate owner decision); the upstream proposal in the whatsmeow Matrix room (option C, a separate outward-facing step by the owner); replacing the observation with a process exit (an untested idea); message ingest (feature 005); the recovery prompt wording.

## Security

### Assets and Trust Boundaries

Assets are the Signal sessions, pre-keys and app-state keys in the device store, the integrity of the server's message queue, the run marker, and the real account's standing with WhatsApp. Boundaries are the client library's background work and the program's storage and network, the old generation and the new one, and the stop diagnostic and the operator's terminal.

### Security Requirements

- **SEC-001**: The check fails closed in every case of doubt (FR-002). A false pass is the main new risk: it would let a later run start while lost writes leave sessions, pre-keys or app-state out of step.
- **SEC-002**: Network before storage is an invariant with its own test (FR-001, SC-004).
- **SEC-003**: No traffic from a retired generation reaches another generation's connection (FR-004, FR-007, SC-003).
- **SEC-004**: Observation data never leaves the check (FR-008). It can hold raw memory words.
- **SEC-005**: No new unattended network activity. The only reconnect is the existing post-pair one in `pair`, which the owner starts (FR-005, FR-006).
- **SEC-006**: Automated tests use the simulator and synthetic data only. Each real-account step needs the owner's separate permission and is recorded with redacted results.
- **SEC-007**: No new dependency and no change to the pinned client library version.

### Finding Disposition

| Finding | Treatment in this feature |
| --- | --- |
| F09 - resource exhaustion | The observation is bounded in memory and time (FR-008). No new connection path: the post-pair connection already existed. But a reusable session makes repeated, long-running connected `stdio` and `serve` runs possible for the first time, which increases receive work in practice. E003-RX-01 asks for a recheck "before another feature materially expands the receive workload or claims this exception", so this feature records that recheck at implementation readiness and code review in its `verification.md` (scope still private, local and single-owner; pin unchanged; expiry at the start of 2026-12-02 UTC, no renewal). Its full reassessment belongs with stage 2 or the expiry, whichever comes first. If the exception expires before this feature's code review, the work stops until the owner records a new decision. |
| F11 - sensitive logs | Only the outcome, category, counts and duration are reported (FR-008, FR-009). |
| F13 - recovery | Offline recovery is unchanged and remains the fallback for every failed check. |
| F14 - incident response | A logout latch still needs durable cleanup before the marker clears (FR-003). |
| F15 - supply chain | No fork and no dependency change (SEC-007). The allow-list is revalidated on any version change (FR-010). |
| Others | Not touched by this feature; dispositions in feature 003 stand. |

### Abuse Cases and Verification

- Leave a library task running past the deadline: the marker stays unsafe and nothing is written after the storage block.
- Send from the pairing generation after its network block: the send is refused, counted and never seen by the fresh connection.
- Write to storage after its block: the write is refused, counted, the check fails and no row changes.
- Cut the observation off at its size bound: the check fails.
- Deliver a logout during the check, and a stop signal during the check: the marker stays unsafe unless the logout cleanup is durable, and the drain order holds.
- Replay feature 003's late-logout ordering (`TestRunGuardLatePinnedLogoutAfterRetirement`) and a held client-library message handler across retirement, which feature 003 left unproven: the check fails and no clean marker is committed while either is pending.
- Make the pairing generation's error tail or QR emitter call disconnect after the fresh connection starts: the call reaches only the retired client and the fresh connection stays up.
- Refuse the marker clear for low disk: the marker stays unsafe and the operator is told what to do.
- Capture every log level during each case: no raw observation data, keys or account identifiers.

`verification.md` records the simulator results, the measured pass rate, the platforms covered and any permitted real-account results. Anything not run is reported as not verified.
