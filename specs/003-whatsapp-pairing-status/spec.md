# Feature Specification: WhatsApp Connection, Pairing and Status

**Feature Branch**: `003-whatsapp-pairing-status`

**Created**: 2026-11-02

**Status**: Draft

**Input**: User description: "@docs/roadmap.md 003". Roadmap specify input: "Connect whatsmeow from the shared database, pair with a terminal QR code, report status, keep the WhatsApp Web version current, and recover from logout without a restart."

## Clarifications

### Session 2026-11-02

- Q: Should feature 003 adopt the proposed resource limits as fixed acceptance requirements? -> A: Approve the proposed limits as fixed constants; changes require explicit review. The existing retry budget and refresh interval remain unchanged.
- Q: Should `whatsapp-mcp status` remain an offline local inspection that fails when another process owns the data directory? -> A: Yes. No network or pairing; saved credentials are labelled as unverified online, and an active owner causes a lock-holder error. Live owner queries remain outside feature 003.
- Q: During one owner-approved pairing attempt, should replacement QR codes be displayed automatically until the attempt times out? -> A: Yes. Replacement codes rotate automatically within that attempt. The original Enter-to-retry decision after final timeout is superseded by the 2026-11-04 unsafe-run recovery decision below.
- Q: How should the upstream receive-resource gaps be handled for feature 003? -> A: Use temporary exception E003-RX-01 in verification.md, owner tiger-seo, expiry 2026-12-02, with earlier reassessment before public release/broader deployment and on an upstream fix or resource incident. Application limits, device-store safeguards and other security requirements remain mandatory; no fork or protocol patch is authorized.
- Q: Should device calls reuse the approved store budgets and persist repair needs across restart? -> A: Yes. Add an embedded migration for bounded collection-level repair markers and bounded full-resync through the existing API after an authorized connection. Retain markers on failure; do not claim whole-sync rollback or delete chats, credentials or session keys. The later unsafe-run decision adds a separate migration.

### Session 2026-11-03

- Q: How should a long-running shell recover when its initial saved-session connection fails or times out? -> A: Retry at most three times after the initial attempt, waiting five cancellable seconds before each; each attempt retains the 30-second watchdog. On exhaustion remain disconnected with an actionable diagnostic. This is separate from the 405 budget and cannot bypass logout or shutdown.

### Session 2026-11-04

- Q: What device identification should fresh QR pairing send? -> A: Preserve the previous service's Linux/Chrome companion identification, rather than the pinned library's default unknown platform. WhatsApp controls the phone's displayed label, so the exact label is not guaranteed. This does not authorize connecting, pairing, unlinking or re-pairing a real account.
- Q: How should restart handle a confirmed logout whose local cleanup may have failed? -> A: Persist a conservative unsafe-run marker before account work; an unclean or unresolved run blocks automatic account work on restart. Recovery requires offline verification and, where necessary, phone-side unlink, separately authorized local credential removal, then a separately authorized fresh pairing. Preserve chats and never infer revocation from the marker. Even an ordinary crash can require re-pairing. These decisions authorize the design, not any real-account action.
- Q: May Enter immediately retry after final pairing timeout? -> A: No. An unresolved attempt may have left a phone-side link even after orderly stop; require the same explicit offline recovery before a separately approved new pairing. QR replacement inside the same still-active attempt remains automatic.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Link a Device and Reuse Its Session (Priority: P1)

As the account owner, I can run `whatsapp-mcp pair`, scan a terminal QR with my phone, and reuse that linked device on later runs without scanning again.

**Why this priority**: A durable connection is the prerequisite for every later WhatsApp capability.

**Independent Test**: Use a simulated connection and an isolated temporary data directory to complete pairing, stop, and start again with the saved session. A separate, explicitly authorized manual check demonstrates the same journey with a real phone.

**Acceptance Scenarios**:

1. **Given** an unpaired data directory, **When** the owner runs `pair`, **Then** the current QR is displayed in the terminal and the command waits for success, expiry, cancellation or a connection failure.
2. **Given** an active pairing attempt, **When** the phone successfully links the device and connection is confirmed, **Then** the connection owner's in-process status snapshot reports connected and logged in, the QR is cleared, the session is saved, and `pair` exits successfully after orderly shutdown.
3. **Given** a saved, valid session, **When** the owner starts a connection-owning command, **Then** it reconnects without asking for a QR; `pair` reports that the account is already paired and exits successfully after confirming the connection.
4. **Given** another process owns the data directory, **When** `pair` starts, **Then** it fails with the existing lock-holder diagnostic and neither creates a second client nor modifies the owner's session.
5. **Given** an active connection or pairing attempt, **When** the owner interrupts the command, **Then** connection work and device writes stop before storage closes and the data-directory lock is released.
6. **Given** either existing `stdio` or `serve` command shell, **When** it starts with a saved session, **Then** it connects and remains running until cancellation; with no saved session it remains running with an unpaired in-process snapshot and no QR. Neither shell exposes tools or a network listener in this feature, and both shut down with device writes quiescent before releasing ownership.
7. **Given** a saved-session startup attempt fails or times out in `stdio` or `serve`, **When** it is not superseded by a terminal event or 405 recovery, **Then** the shell makes at most three additional attempts after five cancellable seconds each; on exhaustion it remains disconnected, preserves the saved identity and reports an actionable diagnostic without opening a pairing window.
8. **Given** a fresh device and an explicitly approved QR pairing attempt, **When** the registration payload and QR client type are prepared, **Then** they identify the companion as Linux/Chrome as the previous service did, without modifying global library state or saved credentials. The phone's displayed label is observed separately and is not guaranteed by this application.

---

### User Story 2 - Recover Pairing Without Restarting (Priority: P1)

As the account owner, I can receive a replacement QR within an active attempt or request a fresh QR after a confirmed logout is durably cleaned up, without restarting the process that owns the session. An unresolved final timeout instead requires offline recovery.

**Why this priority**: Recoverability fixes the existing failure that leaves a running service permanently unpaired.

**Independent Test**: Simulate expiry, logout and a new pairing request in one process, then verify that fresh pairing succeeds without recreating the process or losing stored conversations.

**Acceptance Scenarios**:

1. **Given** a fresh pairing attempt reaches final timeout without durable identity and current-generation Connected, **When** the owner presses Enter, **Then** no new attempt or QR starts; the owner retires the attempt and requires explicit offline recovery before a separate new pairing.
2. **Given** a running session receives a logout event, **When** its in-process status snapshot is inspected, **Then** connected and logged-in are false and no previous QR is available; logout alone does not open a new pairing window.
3. **Given** that logged-out process, **When** a new owner-approved pairing request is made through the connection boundary, **Then** fresh pairing can complete in the same process while existing conversation data is preserved.
4. **Given** pairing is already in progress, **When** concurrent requests ask for a QR, **Then** they share the current unexpired attempt rather than starting competing connections.
5. **Given** an old attempt completes after it was cancelled or superseded, **When** its delayed events arrive, **Then** they cannot replace the current QR or incorrectly mark the new session as connected.
6. **Given** an active owner-approved attempt, **When** WhatsApp supplies a replacement QR, **Then** it replaces the previous code automatically without another approval and only the current unexpired code is available. Expiry of one code alone does not authorize starting another attempt.
7. **Given** an unresolved final timeout, **When** the owner cancels, presses Enter or input ends, **Then** `pair` exits unsuccessfully after orderly shutdown with the marker unsafe and starts no further attempt.
8. **Given** an interrupted app-state/contact sync with a durable repair indication, **When** the owner restarts and authorizes the connection, **Then** bounded recovery preserves chats/credentials/session keys, clears only successfully repaired state, and retains the indication after failed or superseded recovery rather than silently accepting stale state.
9. **Given** an unsafe-run marker after a crash or failed logout cleanup, **When** the process restarts, **Then** it starts no automatic connection, pairing or QR and reports that offline operator recovery is required, even if a saved identity exists or no identity was saved before the crash.
10. **Given** that unresolved marker, **When** an operator explicitly selects recovery in `pair`, verifies the named account/device and separately authorizes any required phone-side unlink and local identity removal, **Then** local recovery preserves chats/messages and remains offline; a fresh pairing needs a separate explicit approval. EOF, refusal, insufficient verification or a definite failed commit preserves the block; an ambiguous commit exits unsuccessfully and requires committed-state reinspection before further action.

---

### User Story 3 - Understand Local Session Status (Priority: P2)

As an operator, I can run `whatsapp-mcp status` to inspect whether a local session exists and which version is available, without accidentally starting pairing or waiting for a network connection.

**Why this priority**: Operators need an honest, quick diagnostic that does not change account state.

**Independent Test**: Run `status` against missing, unpaired, paired, unsafe and locked temporary data directories with network access prohibited.

**Acceptance Scenarios**:

1. **Given** no data directory or database/sidecar artifacts exist, **When** `status` runs, **Then** it reports not connected, not logged in, pairing-required false and the bundled version labelled as not refreshed, exits successfully, and creates no data directory, database or lock file; an existing held lock still causes the holder error even before its owner creates the database.
2. **Given** a saved session and no running owner, **When** `status` runs, **Then** it reports a local snapshot with connected false, logged-in true based on saved credentials, pairing-required false, and the bundled version labelled as not refreshed; it explicitly says the saved session has not been verified with WhatsApp.
3. **Given** another process owns the data directory, **When** `status` runs, **Then** it exits with a clear locked-data-directory error naming the holder rather than reading concurrently or claiming to know that process's live connection state.
4. **Given** unsafe, corrupt or unsupported existing storage, **When** `status` runs, **Then** it reports an actionable error without repairing, creating or migrating that storage.
5. **Given** an unsafe-run marker or a legacy database without a marker, **When** `status` inspects it, **Then** it warns that offline recovery is required and exits 1; if the database is otherwise inspectable, its four fields still describe local identity presence rather than claiming account verification.

---

### User Story 4 - Stay Compatible With WhatsApp (Priority: P2)

As an operator, I can keep a connection running while the application refreshes its WhatsApp Web version and makes a limited recovery attempt if WhatsApp rejects an outdated client.

**Why this priority**: Compatibility updates should not require routine operator intervention or cause endless reconnect loops.

**Independent Test**: Simulate time, version responses and outdated-client events, and inspect version changes, retry counts and resulting status without contacting WhatsApp.

**Acceptance Scenarios**:

1. **Given** a connection-owning command starts, **When** startup version discovery succeeds, **Then** the discovered version is selected before its first connection attempt.
2. **Given** discovery fails, **When** the command starts, **Then** a warning is reported and connection proceeds with the last usable version, or the bundled version if none has been discovered.
3. **Given** a connected session, **When** six hours elapse and refresh succeeds, **Then** the selected version changes for the next connection without disconnecting the current session.
4. **Given** WhatsApp repeatedly reports an outdated client (405), **When** recovery is attempted, **Then** each retry waits five seconds and refreshes the version before connecting, with at most three retries until a connection succeeds.
5. **Given** that budget is exhausted, **When** more outdated-client events arrive, **Then** no further automatic retries occur and an actionable diagnostic is reported; a confirmed successful connection resets the budget.

### Edge Cases

- A network disconnect leaves saved login state intact, marks connected false and does not generate a QR. Ordinary network reconnection remains the protocol client's responsibility as in the reference behaviour, stops on shutdown and cannot bypass terminal logout or the 405 budget. Planning must document its resource limits under the security profile below.
- A saved device identifier is not proof that WhatsApp still accepts the device. Live connection failure must not be presented as a successful login verification.
- Expiry, successful connection, logout and cancellation racing each other must leave one consistent status snapshot and no stale pairing material.
- A code's expiry is distinct from the pairing attempt's final timeout. An expired code is cleared immediately; the same approved attempt may supply a replacement. An unresolved terminal timeout requires explicit offline recovery and a separate pairing approval; Enter or end-of-input cannot authorize either.
- Cancelling during the five-second retry wait or version discovery prevents subsequent connection attempts.
- Version discovery can stall, redirect unexpectedly, return oversized content, omit its revision, or return an invalid revision. These cases preserve the prior usable version and do not consume unlimited resources.
- An empty or malformed QR event cannot be rendered or retained as a valid QR. Untrusted diagnostic text containing terminal controls cannot execute terminal actions.
- Storage failure during pairing is reported as failure, never as a durable successful pairing. Recovery must not delete conversation tables or other application data.
- A shutdown timeout is not permission to close the database or release the lock while a device write remains active.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Connection-owning commands MUST use the single device session in the existing shared data directory, after taking its exclusive lock and validating storage. They MUST NOT introduce another device database or import an earlier deployment.
- **FR-002**: `pair` MUST support QR-only interactive linking, display each current QR in the terminal, report success only after confirmed connection and saved credentials, and exit successfully after quiescing the connection. During one owner-approved attempt, replacement codes MUST be displayed automatically while invalidating the previous code. An unresolved final timeout, cancellation or failure MUST exit unsuccessfully with a useful offline-recovery diagnostic; Enter MUST NOT start another attempt. Recovery and a subsequent pairing each require separate explicit approvals.
- **FR-003**: A valid saved session MUST be reused without starting QR pairing. Connection failure alone MUST NOT erase that session or silently replace it with a new device.
- **FR-004**: The running connection owner MUST maintain a coherent status snapshot with `connected`, `logged_in`, `pairing_required` and `wa_version`. Confirmed connection sets connected and logged-in true and clears QR material. Disconnect clears only connected; confirmed logout clears connected and logged-in and invalidates pairing material.
- **FR-005**: `pairing_required` MUST retain the reference meaning: true only when not logged in and an unexpired QR is available. An expired QR or logged-out session without a new pairing request MUST report false. A returned QR MUST represent the current pairing attempt only.
- **FR-006**: After confirmed logout and durable cleanup, a new explicit pairing request MUST work within the existing owner process without deleting conversation data, reusing revoked credentials or requiring a process restart. An unresolved final timeout instead requires FR-018 offline recovery and a separate new pairing. Logout MUST NOT itself authorize a new pairing window.
- **FR-007**: Concurrent pairing requests MUST share one active attempt, including while its current code is expired and awaiting replacement; they MUST NOT silently start another attempt. Requests during shutdown MUST be rejected. Events from cancelled, timed-out or superseded attempts MUST NOT mutate current pairing or login state. Expiry of an individual code invalidates that code, not its still-active attempt.
- **FR-008**: `status` MUST print the four status fields and exit without connecting, fetching a version, generating a QR, migrating storage or altering saved account state. Missing storage MUST be reported as an unpaired local snapshot without creating artifacts. Existing storage inspection MUST respect the exclusive owner lock and private-storage checks; corrupt, unsupported, unsafe or locked storage MUST cause a clear failure.
- **FR-009**: Standalone `status` MUST distinguish local saved-session presence from live authentication: connected is false, logged-in reflects saved device identity, pairing-required is false, and the output warns that credentials have not been verified online. Its version is the locally available bundled version, explicitly labelled as not refreshed. It MUST NOT query or imply live knowledge of a running daemon.
- **FR-010**: Connection startup MUST attempt to discover the current WhatsApp Web version before connecting and MUST refresh it every six hours while running. Successful refresh applies to subsequent connections only; failure retains the last usable version or the bundled fallback and emits a warning.
- **FR-011**: Outdated-client recovery MUST make at most three retries after the initial rejection, each after a cancellable five-second delay and a version-refresh attempt. Repeated or concurrent 405 events MUST NOT multiply this budget. A confirmed connection resets it; a failed refresh does not reset it. Exhaustion MUST stop automatic outdated-client reconnection and report the cause.
- **FR-012**: The connection boundary MUST support offline substitutes for lifecycle, pairing, status and events, and the send, upload, download, contact-lookup and group-info capabilities required by later features. This feature MUST NOT enable message sending, downloading, ingest or MCP tools merely to establish that boundary.
- **FR-013**: In this feature, the existing `stdio` and `serve` command shells MUST start the embedded connection owner, reuse a saved session or retain recoverable unpaired status when none exists, and remain running until cancellation. On initial saved-session connection failure or timeout, they MUST make at most three retries after the initial attempt, each after a cancellable five-second wait; exhaustion leaves the shell disconnected with an actionable diagnostic and saved identity intact. This startup budget MUST NOT bypass 405 recovery, logout or shutdown. They MUST NOT print terminal QRs or automatically start pairing without an explicit pairing request. Their transport/tool implementations remain in features 005 and 009; these shells MUST NOT expose tools or a network listener here.
- **FR-014**: Shutdown MUST stop version refresh, pairing, reconnect work, event handling and all client-originated device writes before closing shared storage, then release the lock. It MUST not report clean shutdown or release ownership while those writes can still occur.
- **FR-015**: WhatsApp client and device-store diagnostics MUST use the existing unified, level-controlled logger. All logs and any human pairing output in stdio mode MUST stay off stdout. No transport behaviour introduced by this feature may consume or contaminate protocol stdout.
- **FR-016**: Device operations MUST share the approved store admission, deadline, input/output and disk policy. Before changing app-state sync progress or a bulk contact snapshot, the owner MUST durably record the affected collection's repair need. An interrupted/failed sync MUST retain that need across restart. After an explicitly authorized connection, bounded re-synchronization MAY repair only the affected protocol metadata/contact snapshot; clear the indication only after complete verified success. Failure MUST remain visible and retryable without automatic unpairing, deleting chats/credentials/session keys, silently truncating data or claiming rollback of earlier committed operations.
- **FR-017**: Fresh QR registration MUST use the previous service's companion OS `Linux` and platform `CHROME`, including the matching Chrome QR client type. Its registration version and user-agent OS version/build MUST use one selected handshake version, as in the previous service. The adapter MUST preserve credentials and all unrelated payload properties, including unknown protobuf fields, keep shared library globals immutable, and reuse an existing saved identity without forcing re-pairing when FR-018 permits reuse. A failure to decode or encode companion properties MUST fail the handshake without sending an incorrectly identified registration, preserve saved credentials and record a secret-free diagnostic category for reporting outside the callback. Repeated failures may coalesce; forced termination or an indefinitely stalled logger cannot guarantee delivery. WhatsApp determines the displayed name; no exact phone label or retroactive relabelling is promised.
- **FR-018**: Before any account-capable work, including first pairing, the owner MUST durably arm an unsafe-run marker in the existing database; failed or ambiguous arming admits no account work. It remains armed through reconnects, pairing and confirmed logout, and MUST be cleared only after orderly generation drain and durable completion of any confirmed-revocation cleanup. An opened fresh-pairing attempt without both a durably saved identity and current-generation Connected confirmation remains unresolved even after an orderly timeout, failure or cancellation; keep the marker unsafe because a phone-side link might exist. Unresolved, unreadable, invalid or ambiguous marker state MUST block automatic connection, pairing and QR after restart. The marker does not prove revocation: offline recovery requires explicit operator verification of the account/device, separately authorized phone-side unlink if needed, and separately authorized local removal of only the device identity if present before clearing the marker; a later fresh pairing requires separate approval. Never silently reuse an uncertain identity, infer safe state from a missing device row, remove chats/messages, or treat an automatic restart or ordinary Enter as consent. An existing pre-marker database is unknown, not implicitly safe; a newly created database may start clean only when its creation is known under the exclusive lock. `status` remains offline and non-migrating, reports local identity presence honestly when inspection succeeds, emits an unsafe/unknown warning and exits unsuccessfully for unsafe or legacy-unknown state without adding a login field.

  Verification must identify the exact account and linked-device entry, or establish that the exact device is absent; a generic Linux/Chrome label, timestamp, guess, or absence of a local row is not sufficient. If the operator cannot distinguish the device from others, recovery remains blocked, with no local removal or marker clear. No exact phone label or reliable identifying attribute is guaranteed. Offline recovery requires verified interactive input and output as specified in the CLI contract; unsupported or redirected terminals fail closed.

  A logout or client deletion request recognized before its generation's callback and device admissions have drained is latched through retirement: clearing requires durable cleanup of that revocation, otherwise the marker stays unsafe. Low-disk admission, including its existing resume hysteresis, applies to cleanup and marker clearing without exemption; refusal leaves unsafe state and an actionable instruction to restore free space and perform offline recovery. Durability against power loss is conditional on the actual platform synchronization implementation and storage honoring it; SQLite WAL FULL alone does not establish macOS full hardware flush. Native evidence and limitations are required before making platform-specific claims.

  Identity removal, whether after confirmed revocation or separately authorized offline recovery, MUST retain pending FR-016 collection-repair needs across device-row cascades, restart and fresh pairing. Materialize those needs for the new saved identity before ordinary app-state progress and clear them only after successful fenced repair. Device-specific credentials/keys may be removed only as part of genuine confirmed revocation or separately authorized offline identity removal, never as a shortcut for app-state repair.

### Key Entities *(include if feature involves data)*

- **Device Session**: The one saved linked-device identity and credentials for the selected data directory; may exist locally while offline or be revoked by the phone.
- **Connection Status**: A consistent snapshot of connection, login, active-pairing availability and selected WhatsApp Web version, distinguished from the standalone offline snapshot.
- **Pairing Attempt**: A single owner-requested linking window that may supply multiple replacement codes, retaining only the current code and its expiry. A code's expiry does not end the attempt; final timeout, cancellation, logout or success ends it and invalidates its QR and delayed events.
- **Version Selection**: The bundled fallback and the latest successfully discovered WhatsApp Web version, used on the next connection.
- **Recovery Budget**: Up to three delayed outdated-client retries since the last confirmed connection; distinct from pairing requests and ordinary network recovery.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In all scripted fresh-link and valid-session restart scenarios, linking completes once and the next start requests zero additional QR scans; the separately authorized phone check confirms the same outcome.
- **SC-002**: Scripted confirmed-logout recovery permits a fresh owner-requested QR and successful linking within the same process after durable cleanup. Unresolved timeout instead blocks a new attempt until explicit offline recovery and a separate pairing approval. Both paths delete zero conversation records.
- **SC-003**: Every scripted status transition reports the four required fields consistently; standalone status performs zero network requests and makes zero changes to saved account state.
- **SC-004**: A continuously running six-hour test interval triggers version refresh without interrupting an established connection. A repeated outdated-client sequence produces exactly three delayed retries, no fourth retry, and a renewed budget only after confirmed connection.
- **SC-005**: All cancellation and shutdown race scenarios demonstrate zero device writes after storage closes and zero lock release before device activity is quiescent.
- **SC-006**: Captured diagnostics at every supported log level contain zero QR payloads or session credentials; stdio stdout contains zero human-readable pairing or log output.
- **SC-007**: The owner can follow the documented manual journey of fresh pairing, local status, restart without another scan, phone logout, and same-process re-pairing. Each account-affecting step has separate recorded permission and a redacted result; unperformed checks are reported as unverified.
- **SC-008**: Offline substitute tests cover every connection-boundary capability in FR-012 without a real account. Simulated command tests cover both `stdio` and `serve`, with and without saved sessions, and observe zero exposed tools, listeners or unsolicited QR attempts in this feature.
- **SC-009**: Every scripted interruption between app-state version, MAC and contact writes leaves a durable repair indication; restart observes it, success clears only the repaired indication, and failed/interleaved recovery retains it. Tests show app-state repair itself deletes no chats, credentials or session keys and has no unbounded retry loop; separately authorized identity removal under FR-018 may remove only the affected device's credentials/keys while preserving the repair need and conversations.
- **SC-010**: Synthetic saved-session startup failures and timeouts in both shells produce at most three five-second-delayed retries and no fourth; successful connection or cancellation stops that sequence. Stalled owner-initiated initial, startup-retry, 405 and 515 connections each initiate cancellation at 30 seconds, retaining the attempt slot and ownership until drain and never opening an unsolicited pairing window.

## Assumptions

- Feature 002 is the storage foundation. Its existing permissions, migrations, lock behaviour and data-directory rules remain binding; this feature extends lifecycle ownership rather than redesigning storage.
- The owner confirmed `status` as an offline local inspection for feature 003, not a health probe for another process. It may take the existing lock for safe inspection, but does not create a lock file for absent storage. It fails with a lock-holder error when another process owns the directory; live owner queries remain outside this feature.
- `pair` ends after successful linking. Same-process logout recovery is a capability of a long-running connection owner, tested with simulated requests here and exposed through the later pairing tool in feature 005. Running `pair` beside an existing owner still fails on the lock; no implicit proxy is introduced.
- The terminal `pair` invocation is the owner's authorization to start one pairing attempt, including automatically supplied replacement codes within it. After an unresolved final timeout, cancellation or end-of-input exits unsuccessfully; Enter does not authorize a new attempt or offline recovery. Future MCP authorization policy is not inferred from tool arguments or authentication alone.
- The phone, WhatsApp service and network may delay or refuse linking. No fixed end-to-end pairing duration or guarantee of upstream availability is promised.
- The bounded acceptance profile below is owner-approved as fixed constants, not configurable settings. Any incompatibility discovered during planning must be raised for explicit review before changing those limits.
- The owner approved reusing the existing store budgets on device calls, an embedded repair-metadata migration and a separate unsafe-run marker migration. Schema/transport safeguards are mandatory and outside the receive exception; metadata does not imply message ingest or a new account-management command.

## Scope and References

This feature covers `docs/roadmap.md` 003 and `docs/specification.md` section 8: A3 (`pair` and `status`), A5 (testable connection boundary), P4 (pairing/status state), P5 (version refresh and 405 recovery), M9 (recoverable QR pairing), the WhatsApp log bridge and shutdown portions of M10, and D11 (QR only). Sections 3.4 and 3.6 define preserved events, retry timings and pairing semantics; section 6 defect 16 is explicitly corrected. A2/D14, G1 and constitution Principles I-X remain constraints. Decisions D1-D18 are not reopened.

Included: connection lifecycle, shared saved-session use, terminal pairing, local status, current QR availability for future callers, offline substitutes, version refresh, bounded outdated-client recovery and device-write shutdown ordering identified in feature 002's design review.

Excluded: message/history ingest, LID merging and name resolution (004); MCP tool registration and QR image transport (005); sending and media handling (007-008); HTTP, proxying and daemon status queries (009); webhooks (010); phone-code pairing, new account-management commands, multiple accounts, REST and old-deployment migration. Device-session persistence needed by linking is included; application message persistence is not.

## Security

### Assets and Trust Boundaries

Assets are device credentials and identities, shared storage and its sidecars, temporary pairing material, accurate status, protocol stdout, and exclusive ownership until the last device write finishes. Boundaries are owner terminal to pairing control; WhatsApp/network responses to lifecycle and version handling; upstream diagnostics to local logs/terminal; and the connection owner to private shared storage. Neither a retrieved message nor a diagnostic string grants authorization.

### Security Requirements

- **SEC-001**: QR material MUST remain transient and available only in the active owner-approved attempt. Each code MUST be cleared on its expiry or replacement; all pairing material MUST be cleared on the attempt's final timeout, cancellation, success or logout. QR payloads and session secrets MUST NOT appear in logs, errors, ordinary status output, temporary files or verification evidence, including at debug level. Deliberate terminal QR display is the sole human-display exception in this feature.
- **SEC-002**: Pairing MUST only begin on explicit owner request, not on status reads, startup without credentials, network disconnection or unsolicited events. This feature MUST NOT expose an unauthenticated listener or a remotely callable pairing endpoint.
- **SEC-003**: Existing private-file and handle-identity protections MUST remain effective for client-created device data and sidecars. Unsafe storage MUST fail before account connection or pairing. No revoked-session cleanup may delete chats, messages or unrelated files. Private storage is not encryption; host and backup encryption remain operator responsibilities.
- **SEC-004**: Version discovery MUST validate a secure upstream response, reject invalid certificates and unexpected redirects, consume bounded time and bytes, and accept only a well-formed supported version. Rejected discovery MUST preserve the prior version, expose no credentials, and never redirect account credentials to another service.
- **SEC-005**: Concurrent lifecycle work and externally supplied QR/version data MUST be bounded by the acceptance profile below. Cancellation MUST prevent delayed work from starting another connection. Over-limit input MUST be rejected without changing saved session state or replacing a valid current version/QR.
- **SEC-006**: Upstream text MUST be safely encoded for logs and terminal diagnostics, with no attacker-controlled terminal escapes. Logs MUST record pairing requested/succeeded/expired/failed, logout, version refresh failure, retry exhaustion and shutdown failure using time, outcome and a non-secret reason, without raw QR data, keys or unnecessary account identifiers.
- **SEC-007**: Automated tests MUST use synthetic credentials, simulated events and controlled version responses, never real account data or live account connections. Real-account verification MUST obtain the user's explicit permission separately for each account-affecting step and record changes and cleanup without secrets.
- **SEC-008**: Dependencies MUST preserve the existing exact-version, module-integrity, reachable-vulnerability and five-platform build rules. No connection or QR dependency may require a C toolchain.

### Bounded Acceptance Profile

These owner-approved fixed limits concern application-owned discovery and lifecycle work; they do not claim to bound all resources internal to the WhatsApp service or its protocol library. Planning must enumerate the library's relevant limits and any remaining exposure before implementation is approved. Changes to this profile require explicit review rather than silent adjustment during implementation.

| Resource | Required bound and failure behaviour |
| --- | --- |
| Active client and pairing | One owned client and one active pairing attempt per process; concurrent QR requests reuse that attempt rather than queueing new attempts. |
| Application-initiated connection attempts | One in flight; each owner-initiated initial, explicit, startup-retry, 405 or 515 connection attempt has a 30-second watchdog, cancellable by shutdown. On timeout cancel and drain before another attempt; an undrained attempt retains ownership. Ordinary library recovery must not spin on immediate failures or bypass terminal logout/405 failure. |
| Saved-session shell startup recovery | After an initial failure or timeout, at most three retries, each after a cancellable five-second wait; on exhaustion remain disconnected with an actionable diagnostic. This budget is separate from and cannot bypass 405 recovery, logout or shutdown. |
| Version discovery | One request in flight; 15-second overall deadline; at most 1 MiB of actual response-body consumption; unexpected redirects rejected. |
| QR payload | At most 4 KiB per current code; retain at most one code and one derived display/image representation; no history of codes. Validity ends at upstream expiry or after 60 seconds from receipt, whichever occurs first. |
| Automatic outdated recovery | Three retries after the initial rejection, five seconds before each, reset only by confirmed connection. |
| Version scheduling | One six-hour refresh schedule; overlapping refresh requests share or skip the in-flight request rather than accumulating work. |
| Stop | Cancel outstanding work immediately; report failure if quiescence is not achieved within 10 seconds. The process remains alive holding the store and lock until writes are quiescent, then closes and exits unsuccessfully. A forced process kill remains an OS/crash boundary, not clean shutdown. |

### Finding Disposition

| Finding | Treatment in this feature and remaining owner |
| --- | --- |
| F01 - authorization and consent | Local owner pairing window is required by SEC-001/002. General read/tool pairing policy belongs to 005-006, send approval requires an owner decision before 007, and transport parity belongs to 009. No MCP pairing capability is enabled here. |
| F02 - remote confidentiality | No HTTP/proxy capability here; defer to 009. Secure version discovery remains required by SEC-004. |
| F03 - private storage | Inherit 001/002 controls and verify device-write paths under SEC-003. Encryption and backup custody remain operator responsibilities; broader deployment guidance belongs to 011. |
| F04 - secret lifecycle | QR lifetime and credential-free diagnostics are addressed by SEC-001/006; HTTP token and webhook key generation/rotation remain in 009/010. |
| F05 - filesystem containment | Device-store paths retain 002's controls under SEC-003; outbound paths and cache namespace/cleanup remain in 007/008. |
| F06 - media processing | No media parser/converter enabled; defer to 007/008. |
| F07/F08 - webhook destination/replay | No webhook enabled; defer to 010. |
| F09 - resource exhaustion | SEC-004/005 and the bounded profile cover discovery/pairing/retry work. Under constitution 2.0.0 Principles V/X, existing E003-RX-01 in verification.md is an approved temporary deviation only for documented upstream receive-resource gaps: owner tiger-seo, expiry 2026-12-02, compensating controls and earlier-review triggers. The receive bounds remain unmet; device/application-owned controls are not excepted and F09 is not closed. Follow-up: docs/backlog/003-whatsmeow-receive-resource-bounds.md. Ingest bounds belong to 004, tool bounds to 005-006, media bounds to 007-008, transport bounds to 009 and webhook bounds to 010. |
| F10 - input and output validation | SEC-004/005/006 cover version, QR and terminal input boundaries; message/tool/media validation remains with 004-008 and HTTP validation with 009. |
| F11 - sensitive logs and audit | SEC-001/006 cover lifecycle events and redaction here. Durable audit collection, protected retention and operator review guidance remain in 011; this feature does not claim a complete security audit trail. |
| F12 - retention/privacy | No QR persistence or transcript evidence under SEC-001. Message/client data flows remain with 004-006; media/webhook flows with 008/010; full retention and decommissioning guidance with 011, extending existing storage operations guidance. |
| F13 - recovery and backups | Retain 002's storage recovery controls; FR-014 prevents active device writes during close. Synthetic tests only for restored device state; broader operator recovery rehearsal remains in 011. |
| F14 - incident response | Revoked credentials cannot be reused by FR-006. Full compromise/rotation/unlink runbook belongs to 011 with credential-specific steps in 009/010; this feature does not auto-unlink a phone or destroy data. |
| F15 - supply chain | SEC-008 applies now; release signatures, provenance and distribution guidance remain in 011. |
| F16 - assurance/ownership | Record feature-specific security evidence and limitations; deployment/control ownership and any regulated cryptographic assurance profile remain operator decisions in 011. No compliance claim is made. |

### Abuse Cases and Verification

- Flood concurrent pairing and 405 events: verify one active attempt, no multiplied retry budget and no unexpected session writes.
- Replay expired QR/success/logout events from an older attempt: verify they cannot reveal stale material or mutate the current attempt's saved state.
- Submit oversized, malformed, stalled or redirected version responses and malformed/oversized QR payloads: verify deadlines and consumption bounds, unchanged valid state and no persisted rejected input.
- Inject terminal controls and synthetic secrets into upstream error paths: capture every log level and status output; verify safe rendering and no credential or QR leakage.
- Point the process at unsafe or substituted device storage: verify refusal before any account network operation and no application writes to the rejected target. Exercise Windows and Unix protections separately.
- Interrupt pairing, retry sleep, discovery and an in-flight device write: verify no post-cancellation reconnect, no database close before writes finish, and no early lock release.
- Run standalone status with network access prohibited, missing storage and another active owner: verify no connection, QR creation, migration or saved-state mutation and an honest snapshot/error.

The feature's later `verification.md` must record synthetic test evidence, supported-platform coverage, measured bounds, explicitly authorized manual results and any unverified limitations. Specifying a requirement is not evidence that its control has passed.
