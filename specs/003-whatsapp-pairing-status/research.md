# Research: WhatsApp Connection, Pairing and Status

## Pinned Pairing Evidence - 2026-11-04

Read-only local module-cache inspection, exact whatsmeow `v0.0.0-20261027104441-1eec1136a50b` (commit `1eec1136a50b2ef148b05f1819e77a9d989e0457`):

- `handshake.go:95-105` calls the optional `GetClientPayload`, rejects a nil result, then marshals the client-finish payload. This is source evidence for rejection before client-finish encoding, not an executed Noise handshake or proof that no earlier handshake traffic occurs.
- `client.go:177-180` exposes the callback and per-client `QRClientType`; `pair.go:67-78,93-129` uses the override if nonempty, otherwise global device properties/base payload, and appends that type to the QR. `pair-code.go:30-36` defines Chrome as `"1"`. T065 must drive the pinned construction using synthetic data and a denying transport, not merely inspect source.
- `store/clientpayload.go:102-121,124-166,168-185,190-210` shows default companion OS `whatsmeow`, platform UNKNOWN, global `SetOSInfo` mutation, serialization of companion properties into registration, and separate saved-device login payload. T065-T067 require cloned-property/unknown-field, coherent version, saved-session and codec-failure runtime tests. WhatsApp's displayed phone label is not established by these sources.

No network or account operation and no runtime handshake test was performed in this inspection. Re-audit on a pin change.

**Date**: 2026-11-02
**Scope**: Source and public-upstream research for roadmap feature 003. Follow-up hardening investigation was explicitly authorized on 2026-11-02. No pairing, live device connection, message send or WhatsApp version request was performed. Public GitHub/module metadata requests are not account operations.

## R1 - Preserve the Pinned Storage and Toolchain Foundation

**Decision**: Retain Go 1.27.3, `CGO_ENABLED=0`, the existing whatsmeow pseudo-version `v0.0.0-20261027104441-1eec1136a50b`, util v0.11.0 and modernc SQLite v1.61.0 unless source research establishes a blocking incompatibility. Reuse the existing one-file device/message store and its private-file controls.

**Rationale**: Feature 002 already established device schema 16 and message schema 1 on the shared database. Replacing the persistence layer or updating the protocol client incidentally would expand the feature and invalidate established storage assumptions. The installed toolchain is `go version go1.27.3 windows/amd64`; `CGO_ENABLED=0 go mod verify` completed successfully on 2026-11-02 with `all modules verified`.

**Alternatives considered**: A second sqlstore database, a fresh connection library, the CGO driver and importing a previous linked device are outside the source requirements and decisions D2/D16.

**Evidence**: `go.mod`; `docs/specification.md` section 8; `specs/002-sqlite-storage/verification.md` environment and CI evidence. Module verification establishes cache integrity, not absence of vulnerabilities or correctness of future client integration.

## R2 - Use the Owner-Approved Acceptance Profile Without Silent Adjustments

**Decision**: Implement the spec's resource values as fixed constants and require explicit review of any proposed change: 30-second application connection deadline, 15-second/1-MiB discovery budget, 4-KiB current QR cap, at most 60 seconds' code lifetime, three five-second outdated retries, six-hour refresh and ten-second shutdown failure notification while retaining storage ownership until safe close.

**Rationale**: These values and the fixed-constant policy were accepted during clarification. A library default is evidence to evaluate, not authorization to override an accepted requirement. In particular, the connection deadline concerns initiating a connection, not the owner's time to scan all replacement codes in an approved pairing window.

**Alternatives considered**: New configuration settings, provisional unapproved values and treating a shutdown timeout as permission to release a live database all contradict the recorded decisions.

**Evidence**: `spec.md`, Clarifications, Functional Requirements and Bounded Acceptance Profile.

## R3 - Keep Real-Account Acceptance Manual and Separate From Automated Tests

**Decision**: Unit/integration/fuzz tests use only fake clients, synthetic credentials and controlled local responses. A future manually invoked developer harness under `internal/wa/testdata/cmd/session-check` may expose the production owner's status/pairing lifecycle for the same-process logout/re-pair check before feature 005 provides tools. It is not a new shipped command, network endpoint or `go test` integration.

**Rationale**: Production `pair` exits on success and the command shells do not expose pairing after logout in this feature. A separate interactive validation harness can keep the same owner process alive and request fresh pairing without adding an interim public API. Every connect/pair action requires separate explicit owner permission; unlinking is performed by the owner on the trusted phone, never automated. Record only redacted outcomes and clean up the dedicated test device/directory with permission.

**Alternatives considered**: Running real accounts from an opt-in automated test still violates constitution Principle VIII; leaving a new account-management command in the distributed CLI violates feature scope; claiming simulated logout proves real-account acceptance would misstate evidence.

**Evidence**: Constitution Principle VIII; `spec.md` SC-007 and assumptions; existing `internal/store/testdata/cmd` helper convention. `testdata` helpers are outside normal `./...` builds, so the implementation validation guide must explicitly build the harness and review its source.

## R4 - Inspect Existing Storage Through the Read-Only Committed View

**Decision**: Add `store.Inspect(ctx, dataDir)` beside the existing preflight implementation. It validates artifacts and schema versions, then asks only whether a saved device identity exists using the read-only committed SQLite/WAL view. It must not call `store.Open`, open a writer pool, probe writability, migrate, upgrade or create sidecars. Return a boolean rather than device keys or a full device object. Split command configuration loading from `config.PrepareDataDir`; `status` must not create a missing directory.

**Rationale**: Current `startup` always prepares the directory, while `store.Open` can create the database, perform a rolled-back write probe, open WAL pools and migrate. Neither is a safe implementation of the clarified offline status contract. Feature 002 already provides `checkArtifacts`, `checkHeader`, `preflight`, `readVersions` and `openCommittedViewIDs`; the new inspection belongs inside the same package so those checks are reused without exporting low-level internals. Missing storage returns an unpaired local snapshot; an existing directory is inspected only under the existing exclusive lock. Creating/truncating the lock file for an existing directory is distinct from changing account state; missing storage creates no artifacts.

**Alternatives considered**: `GetFirstDevice` through writable `store.Open` would mutate or reject otherwise readable storage and may create a new device; directly opening SQLite in read-only mode misses feature 002's WAL/sidecar invariants; querying a running owner would contradict the accepted offline behaviour and introduce premature IPC.

**Evidence**: `cmd/whatsapp-mcp/commands.go` startup and openStore; `internal/store/store.go` Open and probeWritable; `internal/store/preflight.go`; `internal/store/walview.go`; `internal/lock/lock.go` Acquire. Existing `snapshotDir` and synthetic `seedDevice` fixtures support unchanged-file assertions and saved-session status tests.

## R5 - Extend the Existing Shutdown Ordering, Not Just Disconnect

**Decision**: Register the connection owner's stop step after the store step, yielding owner -> store -> lock under the existing reverse-order lifecycle. The ten-second threshold becomes an error notification, not permission to return from shutdown while device writes remain admitted. On that failure path retain ownership, await the required barrier, then close and exit unsuccessfully. Keep the existing second-signal forced termination as an explicitly unclean OS/crash boundary.

**Rationale**: `lifecycle.shutdown` currently returns on timeout and permits process exit; `store.Close` only waits for application-store admissions, not the device container returned by `Store.Device`. Wrapping application event handlers alone is insufficient because protocol internals also persist device state. R7 establishes a feasible interface-level barrier from pinned source; its correctness still requires implementation and verification.

**Alternatives considered**: Relying on `sql.DB.Close`, relying solely on the existing application gate, sleeping after disconnect, or returning at ten seconds cannot prove the spec's no-write-after-close invariant.

**Evidence**: `cmd/whatsapp-mcp/lifecycle.go` shutdown/watch; `cmd/whatsapp-mcp/commands.go` runUntilStopped and stop registration; `internal/store/store.go` Device and Close; `docs/roadmap.md` 003's inherited device-write obligation.

## R6 - Update the Existing Tests and Privacy CI Deliberately

**Decision**: Replace stub-era status/lock/exit expectations and update the CI privacy fixture so it no longer relies on `status` creating a directory and failing. Use a deterministic offline fixture helper to create a private store, not a live `pair` or saved-session connection. Add command-level fake injection; default automated tests must never reach a real account even when a developer has valid credentials in the normal data directory.

**Rationale**: The current privacy workflow invokes `status` on a missing directory and expects failure, while the new contract requires success without creation. Existing status tests also assume it is not lock-blocked and creates the directory. Preserving those assertions would silently break the accepted behaviour. Device-write tests must drive direct container persistence; existing `TestShutdownUnderWriteLoad` tests only application-store writes.

**Alternatives considered**: Weakening the new offline status semantics to preserve old tests, dropping cross-identity checks or using a production pairing command as the CI fixture would respectively violate the spec, reduce security evidence or risk account activity.

**Evidence**: `.github/workflows/ci.yml` privacy fixture, `cmd/whatsapp-mcp/cli_test.go`, `store_test.go`, `e2e_test.go`, `lifecycle_test.go`; `internal/store/helpers_test.go` and `device_test.go`. Retain the five CGO-free builds, native Linux/Windows/macOS tests, pinned `govulncheck@v1.9.0`, module verification and existing heavy-test opt-in policy.

## R7 - Gate Every Pinned Device Persistence Interface

**Decision**: A future owner must attach one closeable admission barrier to `Device.Container` (`PutDevice` and `DeleteDevice`), the 12 interfaces in `AllSessionSpecificStores`, and `device.LIDs` (`LIDStore`, the sole `AllGlobalStores` member). Admit/drain reads as well as writes; treat `GetOrGenPreKeys` and `DoDecryptionTxn` as write-capable. On stop, reject new admissions and wait for admitted operations before storage closes. A ten-second warning cannot release ownership. This is a feasible candidate, not implemented evidence.

**Rationale**: The pinned library has detached node-handler, pairing and prekey goroutines. `Disconnect` waits only on the handler queue loop, gives up after five seconds and does not join those writers. Some persistence uses `context.WithoutCancel` or `BackgroundEventCtx`. All inspected paths route through the device fields. The original reattachment candidate was insufficient: first PutDevice can publish raw fields before the wrapper resumes, and successful Device.Delete rewrites them to no-ops. R22 supersedes that candidate with stable pre-client guard wiring, a save-readiness fence and deferred deletion.

**Alternatives considered**: Context-only cancellation and event-handler WaitGroups miss internal writers. A dbutil `UnderlyingExecable` proxy misses `LoggingTxn` operations on raw transactions. A SQL-text filter is neither reliable admission nor robust query policy. Merely setting Initialized without valid guards/delegates/readiness is unsafe; R22 uses the verified flag semantics, exported constructor and stable guards rather than that shortcut or post-publication reattachment.

**Evidence**: Pinned whatsmeow `client.go:724-738, 941-958`; `pair.go:148-159, 226-264`; `connectionevents.go:187-199`; `store/store.go:123-126, 195-212, 279-299`; `store/sqlstore/container.go:151, 251-263`; util v0.11.0 `dbutil/transaction.go:59-64`. Re-audit all persistence fields when changing the pin; embedding a raw interface and accidentally promoting unguarded new methods is not a complete guard.

## R8 - Separate Connection Lifetime, Attempt Deadline and Pairing Window

**Decision**: Keep a cancellable per-generation lifetime context and an owner-side 30-second watchdog for a decisive connection outcome, not a 30-second deadline on the lifetime context. Set `InitialAutoReconnect=false` so initial connection errors cannot be hidden by a background retry. QR acquisition occurs before Connect; one dedicated consumer drains each channel and updates a single current-code snapshot. Confirm both durable pairing and `Connected`, not merely `PairSuccess`, before successful command exit.

**Owner clarification, 2026-11-03**: For saved-session startup failure/timeout in `stdio`/`serve`, use at most three retries after the initial attempt, with a cancellable five-second wait before each; exhaustion leaves the shell disconnected with an actionable diagnostic. The existing 30-second watchdog also covers each owner-initiated 405 and 515 connection. Cancel and drain before another attempt, retaining ownership while undrained. Startup retries end on Connected, shutdown, logout, storage fault or 405 handoff; they do not change the separate 405 budget or `pair` failure behavior. Runtime proof and fresh design review remain pending.

**Rationale**: `ConnectContext` reuses its context for keepalive and handlers; a deadline on it would kill a healthy session. First QR validity is 60 seconds, later codes 20 seconds, and exhaustion emits timeout/disconnect. The library permits reuse after timeout, but the selected owner retires/replaces the client before a different window to isolate old events. Unwrapped logout deletes/resets Device; R22 instead defers its live-field reset, drains old access and performs controlled local cleanup. Same-process re-pair then uses a fresh device/client on the same container without deleting conversations or overlapping clients.

**Alternatives considered**: Starting a separate client for each QR request breaks the one-client/attempt rule; reusing a revoked device cannot work; replacing only `Client.Store` leaves the private app-state processor pointing at the wrong device; interpreting every code expiry as permission for a new attempt violates clarification.

**Evidence**: `client.go:518-553, 568-601`; `qrchan.go:74-107, 117-124, 221-240`; `pair.go:148-159, 226-240`; `connectionevents.go:26-47, 126-141, 163-209`; `store/store.go:279-299`. Outdated events have no library auto-retry: the owner must enforce its independent three-retry budget. Ordinary auto-reconnect has no finite attempt cap; its hook must prevent reconnect after stop/logout/405 exhaustion and the accepted design must account for every handshake, not just explicit calls.

## R9 - Bound Discovery and Avoid Runtime Version-Global Races

**Decision**: Do not call `GetLatestVersion(ctx, nil)`. Use a dedicated secure HTTP client with a real 15-second context, redirect rejection, no credential-bearing headers and explicit body-consumption enforcement. Use a bounded wrapper around the library fetcher only if it reports over-limit/partial-response failure correctly; otherwise use a small local fetcher matching the pinned source's revision format. Default to direct discovery with normal certificate verification; do not add implicit environment-proxy configuration in this feature. Keep the selected candidate separate from the version frozen for any in-flight handshake.

**Rationale**: The pinned fetcher reads the whole body before checking status and can put non-200 response bytes into error text. Its URL is the fixed `https://web.whatsapp.com`, matching `"client_revision":(\d+),` and producing `2.3000.<revision>`. HTTP redirects and unbounded reads are controllable at the supplied client seam. A plain `io.LimitReader` can silently make an oversized prefix look valid; `limit+1` lookahead consumes more than the approved strict budget. The eventual contract must reject advertised oversized responses before reading and reject streaming responses that exhaust the consumption budget without proving completion; never publish a candidate extracted from a truncated body. Preserve the old candidate on failure and emit only safe error categories.

**Version synchronization**: `SetWAVersion` changes globals, their hash and `BaseClientPayload` without synchronization. A mutex around only explicit application `Connect` calls is insufficient while the client can perform internal handshakes. A candidate design is to leave globals immutable and use the exported `GetClientPayload` callback to clone the library-built device payload, apply a selected version snapshot and its registration BuildHash consistently, with no runtime global writes. Its documentation warns against general WhatsApp payload customization: restrict this candidate to version/hash replacement and require pinned-payload conformance tests in Phase 1. An alternative is controlling and serializing every manual/ordinary/post-login reconnect before changing globals. Do not assume simple `Disconnect` proves all readers joined.

**Alternatives considered**: Background `SetWAVersion` violates both concurrency safety and "next connection only"; changing the protocol version while a handshake reads it is not fixed by a refresh mutex; carrying the entire page/error body into logs violates redaction and availability constraints.

**Evidence**: `update.go:21, 32-64`; `socket/constants.go:21`; `store/clientpayload.go:79-102, 168-211`; `handshake.go:96-99`; `client.go:177-179`; `connectionevents.go:139-141`. The bundled fallback at this pin is `2.3000.1050728964`. No real version request was made during research.

## R10 - Share the Existing Secret-Free Bridge and Use One Small QR Encoder

**Decision**: Reuse the existing `internal/store/walog.go` argument-dropping bridge through a small shared internal home rather than adding a second formatting logger. Map levels/modules/format templates to the existing stderr slog logger, never raw format arguments. Emit separate bounded lifecycle outcome events. Candidate QR encoder is `rsc.io/qr v0.2.0` with a deterministic no-ANSI half-block terminal renderer, fixed quiet zone and encoder-capacity failures handled without persisting input. Reject non-QR passkey/phone-code challenges with a safe unsupported-flow diagnostic; do not auto-confirm new authentication flows under a QR-only feature.

**Rationale**: The QR channel explicitly logs its full code with `%s` at debug, and the code includes sensitive pairing material. Dropping all arguments also excludes raw JIDs, decrypted nodes and upstream error bodies. The root rsc encoder is pure Go, tagged, zero-dependency and usable for both terminal matrices and the later PNG transport. A 4-KiB admission bound does not promise every byte string of that size is encodable; reject encoder errors safely. Fixed renderer symbols are not arbitrary upstream terminal text.

**Alternatives considered**: `waLog.Stdout` corrupts protocol stdout; forwarding formatted upstream arguments leaks secrets even at debug; `mdp/qrterminal/v3 v3.2.1` adds terminal/color dependencies; `skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e` is a zero-dependency alternative but lacks a tagged version and has a larger renderer surface than needed. No QR dependency was downloaded, added or approved by a build result here.

**Evidence**: `util/log/log.go:17-23, 56-66`; `qrchan.go:97, 158-185`; `pair.go:125-130, 155`; `client.go:884, 988`; existing `internal/store/walog.go`. Public module metadata: `https://proxy.golang.org/rsc.io/qr/@v/v0.2.0.mod` and `https://pkg.go.dev/rsc.io/qr@v0.2.0`. The C-backed `rsc.io/qr/libqrencode` subpackage is explicitly excluded from the candidate dependency surface.

## R11 - Use Deterministic Offline Time and Capability-Narrowed Fakes

**Decision**: Use `testing/synctest.Test`/`Wait` and Go 1.27's `synctest.Sleep` for owner-level timer/race tests with purely in-memory dependencies. Keep filesystem/SQLite/real HTTP fixtures in ordinary tests. Define narrow lifecycle/status/pairing and send/upload/download/contact/group interfaces so later consumers can use fake subsets without exposing real sending or downloading through this feature's commands.

**Rationale**: Six hours of refresh scheduling and five-second recovery delays can be exercised without real waits, provided all relevant goroutines/timers are inside the same bubble. OS I/O is not durably blocked for fake-time advancement. Retain pinned WhatsApp types where they are already domain objects; do not build a second protocol representation solely for tests. The media boundary must later cover stored-metadata download, not assume an entire original protobuf is persisted by feature 002.

**Alternatives considered**: Global mutable sleep overrides cause parallel-test races; real six-hour tests waste time; broad test doubles of the entire concrete client make later features depend on unrelated capabilities; running real accounts under opt-in `go test` still violates the constitution.

**Evidence**: Go 1.27.3 `src/testing/synctest/synctest.go` and `api/go1.25.txt`/`api/go1.27.txt`; pinned client `send.go:191`, `upload.go:73`, `download.go:127, 214`, `user.go:232`, `group.go:626`. Native race-detector commands are not part of the default validation profile because Go's race detector requires CGO and this repository mandates `CGO_ENABLED=0`.

## R12 - Receive-Path Security Gate Fails Before Phase 1

**Decision**: Stop after Phase 0 and record a constitution gate failure. Do not infer a trusted-upstream risk exception, silently fork/change dependencies, weaken accepted bounds or claim the application callbacks enforce pre-allocation limits. Phase 1 requires an explicit hardening direction or a properly recorded owner risk exception.

**Evidence at the exact pin**:

| Exposure | Source evidence | Why application-side limits are insufficient |
| --- | --- | --- |
| Decompression expansion | `binary/unpack.go:21-30` calls unbounded `io.ReadAll` on zlib data; `client.go:871-883` invokes it before node dispatch. | `FrameMaxSize = 1 << 24` (`socket/constants.go:35`, `framesocket.go:113`) caps encoded websocket input, not output allocation. QR/event/store wrappers run later. |
| Pending-node/goroutine growth | The full-queue branch in `client.go:892-904` creates one goroutine per overflowed node retaining that node. | A 2048-element channel (`client.go:249`) is not a bound on all pending nodes, goroutines or bytes. |
| Protocol-managed persistence and caches | App-state processor is always constructed (`client.go:287`); contact/LID/app-state updates persist through device interfaces and caches. | The drain barrier proves lifecycle safety, not bounded operations, cache residency or low-disk admission. These paths already run in 003. A finite total-history quota is not automatically required; R15 corrects the earlier overly broad growth-policy interpretation. |

**Supported partial mitigations**: `ManualHistorySyncDownload=true` and `DisableManualHistorySyncReceipt=true` avoid automatic history blob transfer (`message.go:892-906`); retain disabled decrypted-event buffering, retry-message storage and automatic rerequest defaults; reject future device admissions during shutdown; bound application attempts and discovery. Exported registration history preferences can request smaller batches, but are server requests rather than hard local receive quotas, require accepted values and do not cap later incremental updates. Initial prekeys (812) and later batches (50) are already fixed; they are not unbounded exposures.

**No supported complete intercept found**: `handshake.go:124-125` wires the internal frame handler into Noise; callbacks occur after unpack. Deprecated DangerousInternals exposes methods for calling, not a supported setter that replaces the handler on the live private socket. Reimplementing the handshake/socket stack to gain interception would substantially expand protocol ownership and still conflict with private client send paths. A soft `debug.SetMemoryLimit` is not a hard cap and does not control all pending-node concurrency or disk writes.

**Threat boundary and limitation**: TLS and the pinned Noise certificate verification (`handshake.go:85-87, 147-186`) authenticate the real WhatsApp service. This is a trusted-upstream availability risk, not evidence that an arbitrary contact can directly supply a crafted Noise frame. No exploit or live-account test was run. Treat it as an unresolved High design risk under F09 because allocation/process exhaustion is possible before the accepted controls; this is not a new CVE assertion or vulnerability-scanner result.

**Alternatives requiring owner action**: Investigate a pinned upstream fix or narrowly scoped reviewed patch with accepted receive/operation/cache limits and CGO-free security tests; or record an explicit temporary exception with named owner, compensating controls, scope and expiry in `verification.md`. Investigation was subsequently authorized; see R13-R16 for its result. That permission does not approve a patch, new limits or an exception. Deferring already-live protocol operations to 004 cannot resolve the gate.

## R13 - Upgrading to the Inspected Upstream Revision Does Not Fix the Receive Gates

**Authorization**: On 2026-11-02 the owner approved research into an upstream fix or minimal patch, not production implementation, dependency changes, account access, a fork, publication or an exception. The owner removed the premature task draft; it has not been recreated or committed by this investigation.

**Decision**: Do not propose a routine dependency upgrade as the mitigation. Keep the current pin for a focused patch design unless a separately reviewed upstream change is needed. No dependency was changed.

**Evidence observed on 2026-11-02**:

| Item | Verified result |
| --- | --- |
| Authoritative upstream | `go.mau.fi/whatsmeow` resolves to `github.com/tulir/whatsmeow`. |
| Current project pin | `1eec1136a50b2ef148b05f1819e77a9d989e0457`, 2026-10-27T10:44:41Z. |
| Inspected upstream HEAD | `54038813c47f3f351eb6fcaafab0e1b99d676850`, 2026-11-02T15:27:15Z; proxy latest reported `v0.0.0-20261102152715-54038813c47f`. This is a dated observation, not a claim about future HEAD. |
| Comparison | HEAD is seven commits ahead, zero behind; 22 changed files. Neither `binary/unpack.go` nor the queue-overflow logic gains a bound. |
| Upgrade compatibility evidence | No `go.mod` or device schema migration change in that comparison; exported APIs appear additive. Handshake-error autoreconnect and app-state backoff behaviour do change. This is source comparison, not build/test or account compatibility evidence. |

The inspected HEAD still uses unbounded zlib `io.ReadAll` in `binary/unpack.go:21-31`, and `client.go:900-907` still creates a sender goroutine when the queue is full. The project-pin equivalents are `client.go:892-904`. File-history and issue/PR searches found no merged fix for either path in the inspected upstream main history. Searches cannot establish that no private, unindexed or future fix exists.

**Relevant public references**:

- [Pin-to-inspected-HEAD comparison](https://github.com/tulir/whatsmeow/compare/1eec1136a50b2ef148b05f1819e77a9d989e0457...54038813c47f3f351eb6fcaafab0e1b99d676850).
- [Unpack at the inspected HEAD](https://github.com/tulir/whatsmeow/blob/54038813c47f3f351eb6fcaafab0e1b99d676850/binary/unpack.go#L21-L31).
- [Client at the inspected HEAD](https://github.com/tulir/whatsmeow/blob/54038813c47f3f351eb6fcaafab0e1b99d676850/client.go#L875-L908).
- [Issue 602](https://github.com/tulir/whatsmeow/issues/602) reports handler-queue/goroutine/RAM growth; closed not-planned. It is evidence of a reported symptom, not proof of every exploit path.
- [PR 1236](https://github.com/tulir/whatsmeow/pull/1236) proposed disconnect-time escape for parked senders; closed unmerged. Escaping on disconnect alone would still not bound live overflow. Closure without discussion is not maintainer approval of the proposal or evidence of why it was declined.
- [PR 1139](https://github.com/tulir/whatsmeow/pull/1139) proposes interception hooks but was unmerged at inspection; no supported pre-Unpack hook can be assumed from it.

**Alternatives considered**: Updating seven commits may provide unrelated fixes, but claiming that it fixes these gates would be false. Waiting for upstream may be reasonable operationally, but acceptance/timing is unknown. An upstream issue/PR or fork must not be created without separate approval.

## R14 - A Receive Patch Is Feasible to Design, but a Two-Line Queue Fix Is Not Sufficient

**Recommendation**: Prefer a small, reviewed upstream-compatible patch over reimplementing handshake/Noise/QR. Design it against the current exact pin first; decide separately whether to wait for upstream or maintain an exact fork revision. The following is a proposed direction, not accepted limits, implemented code or proof that the gate is resolved.

### Receive and Response Constraints

The single Noise consumer decrypts and calls `handleFrame`, which does Unpack -> Unmarshal -> `receiveResponse` -> handler-queue admission (`socket/noisesocket.go:54-70, 111-119`; `client.go:871-908`). IQ/ack responses are delivered before ordinary node dispatch (`request.go:71-89`). Blocking that consumer on a full application queue also blocks future IQ/ping responses needed by handlers and keepalive. A simple blocking send therefore risks a stall even though it bounds sender goroutines.

The handler loop also detaches a still-running handler after repeated timeouts (`client.go:941-963`); additional protocol code starts background jobs. Bounding queue capacity alone cannot bound those retained tasks. No exported setter was found that intercepts plaintext before Unpack or replaces the live private socket handler. DangerousInternals call-through methods do not provide that setter.

### Proposed Patch Responsibilities

1. Bound decompressed output before large growth, close the zlib reader on every path, reject empty/malformed input safely and return a typed over-limit error. Check uncompressed frames too. Do not return a truncated prefix as valid data. A bounded output buffer/reader must distinguish actual payload limits from documented sentinel/lookahead and allocator overhead; this is not the discovery endpoint's already-approved strict consumed-byte budget.
2. Bound decoder depth, node/attribute counts and allocation before recursion or `make`. Output-byte limits alone do not prevent decoded object amplification. In particular, `binary/decoder.go:338` allocates a declared child list before validating it. Rejecting `size > remaining/4` is NOT a valid shortcut: `readNode` accepts a List8 marker, size=1 and one tag token, a three-byte child (`decoder.go:355-379`). Compatibility tests must cover minimal valid nodes and token tables.
3. Replace unbounded overflow senders with nonblocking admission to finite count AND charged-byte budgets. On overload, signal one typed terminal receive failure and cancel/disconnect that generation; let the owner enforce the recovery policy. Do not silently drop pairing/control nodes, promise server redelivery or start an unconditional reset/reconnect loop. Normal accepted nodes remain ordered; IQ response routing remains independent of queue admission. No extra socket/handshake implementation is needed.
4. Retain budget leases until the work that owns the node actually finishes. Budgets must cover queued nodes, active/timed-out handlers and audited detached tasks, with stable ownership across reconnects. Never reset/clamp counters while stale tasks retain memory, or count a popped node as freed while its handler still runs. A new device generation cannot evade the remaining old-generation budget.
5. Audit every reachable client-owned background dispatch before claiming a total concurrency bound. A bounded queue does not automatically bound tasks whose parent handler returns immediately. Apply nonblocking job admission or demonstrate a separate finite existing bound. `SetMaxParallelRetryReceiptHandling` is a usable adapter knob for one such path, not a universal limit.

### Draft Sizing for Design Review Only

| Resource | Candidate starting point | Evidence and limitation |
| --- | --- | --- |
| Expanded frame payload | 16 MiB | Same order as the current encoded frame ceiling. A compressed legitimate payload may exceed it, so equivalence with all valid server traffic is NOT proven. |
| Decoder | 64 nesting levels; 65,536 total decoded nodes; separate attribute/allocation charging | Finite design candidates, not measured WhatsApp requirements. Validate against encoder output and large synthetic documents before adoption. |
| Pending receive work | Preserve at most 2,048 queued nodes, add a 64 MiB charged-data budget covering all retained receive work | Count and byte limits both apply. Charged data is not an exact RSS bound; buffers, object overhead and SQLite memory need a separate finite envelope. |
| Overflow sender goroutines | Zero | Admission either succeeds immediately or reports overload. No hidden per-frame waiting goroutine. |
| Timed-out node handlers | Retain the existing effectively serial healthy path; do not start replacements indefinitely while old handlers remain active | Avoids inventing a 256-handler pool as a "small" patch. Any nonzero extra concurrency and its timeout need explicit review. |

All values in this table are proposals. They do not change the feature's existing approved 30-second connection, 15-second/1-MiB discovery, QR, retry, refresh or shutdown bounds. No arbitrary whole-database quota is proposed.

**Patch footprint**: Primary production locations are `binary/unpack.go`, `binary/decoder.go`, `client.go` and possibly `handshake.go` for queue ownership/accounting. A typed overload event/error and audit-driven background-job admission can require changes to event/error definitions and specific spawning sites. Exact file count and line count are not established; claiming a complete two-file or eight-line solution would understate the work. Add offline tests next to each changed package. The received proposals to silently drop unpaired nodes, reset counters on reconnect or assume downstream jobs are bounded were rejected during integration.

**Failure compatibility**: Healthy traffic should retain current behaviour, but overload now becomes a visible connection/pairing failure rather than unlimited retention. Pairing may need a fresh explicitly requested attempt after an overload; preserving a running pairing window by silently discarding control data is not a safe guarantee. No wire-format, cryptographic, credential, phone-code or account-action extension is intended. Exact CLI treatment must be reconciled with FR-002/006 and the error contracts in Phase 1.

**Unproven parts**: Legitimate maximum frame/decoder shapes, a safe accounting representation, detached-job coverage, overload cleanup/recovery and device-sync failure behaviour require synthetic proof and review. No patch or proof-of-concept implementation has been made in this investigation.

## R15 - Device Storage Can Be Hardened at the Adapter Boundary, With Important Limits

**Correction to the earlier blocker**: Feature 002 FR-045 explicitly states "No finite total-history retention is implied" and accepts disk-low admission plus safe disk-full failure. FR-049 covers indefinite retention/growth disclosure. Therefore legitimate long-term device/history growth alone does not establish a requirement for a finite total database or per-table quota. The unresolved control is device-path bounded work, cache residency, disk admission and failure semantics, not an automatically mandatory lifetime row cap.

| Concern | Feasible adapter/store direction | What it does not prove |
| --- | --- | --- |
| Operation count/time/disk admission | Extend the same approved store admission/disk policy to guarded device calls; reuse one writer and the existing 16-active/64-waiter, 5-second admission, 10-second operation, 256-MiB reserve/512-MiB resume, 30-second disk-check profile where compatible. Reuse transaction-aware admission through nested callbacks rather than acquiring a second lease/deadlocking the writer. | The existing cached free-space check is not a hard reservation, total DB-size quota or guarantee that another process cannot fill the disk. SQLite-full errors must still fail safely. Values approved for application calls are a reuse proposal for device calls, not an implicit policy change. |
| Per-call inputs and bulk writes | Validate total items, JID/name/blob lengths and aggregate bytes before delegating to raw sqlstore, including write-capable reads such as `GetOrGenPreKeys`. Select bounds compatible with initial sync, not blindly the application's 1,000-message limit. | SQL chunks of 300 contacts or 400 MACs do not bound a whole caller-supplied slice/transaction. Input objects already allocated by decoding need earlier receive/download limits too. |
| Device read/list output | Use a bounded iterator or reject based on safe preflight before materializing all results; keep SQL implementations in `internal/store`. | Checking length after `GetAllContacts`/`GetAllAppStateSyncKeys` returns does not bound its allocation. |
| Contact/LID cache residency | Supply bounded implementations through `Device.Contacts`/`Device.LIDs`, or a separately reviewed small sqlstore cache change. Read-only misses count too; evict only cache entries, never persisted keys/sessions or required MACs. | A wrapper that merely delegates still leaves private raw caches unbounded. A bound on persisted rows does not bound negative cache entries. |
| Automatic external app-state downloads | Install a dedicated bounded/deadlined HTTP client via exported `SetMediaHTTPClient` before Connect; cap actual consumed response bytes and redirects and reject truncated data. Keep automatic history download disabled here. | `ManualHistorySyncDownload` does not disable external app-state snapshots. A body limit does not itself bound protobuf object count, all sync pages or a complete logical sync. Values and page/work policy remain design decisions. |

**Evidence**: `internal/store/limits.go:35-55`, `disk.go:8-46`, `ops.go:33-62`, and `store.go:254-287` show the existing policy and device bypass. Pinned sqlstore bulk methods use internal transactions/chunks (`store/sqlstore/store.go:540-551, 681-708`) without overall caller-item caps. `GetAllContacts` builds both an output map and cache (`805-814`), point-contact misses are cached (`747-767`), and `CachedLIDMap` caches lookup misses (`store/sqlstore/lidmap.go:91-115`). `GetAllAppStateSyncKeys` materializes an entire list (`store/sqlstore/store.go:454-455`). `SetMediaHTTPClient` is exported at `client.go:431-435`; `appstate.go:462-463` calls Download and `download.go:386-393` uses `io.ReadAll`. `appstate/decode.go:38-59, 76-89` unmarshals external snapshots/mutations, while `appstate.go:73-90` can fetch more pages.

**Transaction/failure caveat**: The public device-method boundary is a chokepoint for admission and shutdown, not automatically an atomic whole-sync boundary. `appstate/decode.go:229-240` stores the app-state version before deleting/inserting MACs; contact updates follow decode in `appstate.go:162-188`. Injecting a limit error at a later method can leave earlier operations committed. A proof must show early validation before those commits and/or a correct supported recovery/atomicity mechanism. Do not claim "batch rejected, sync untouched", silently truncate input, split cryptographic sync groups or delete keys to make a quota fit. This may require additional narrowly targeted upstream cooperation; adapter feasibility is not a completed transactional design.

**Remaining design work**: Choose device input/output/cache and external-sync byte/page/time bounds, decide where bounded implementations live, and prove failure/recovery without real accounts. A global SQLite page cap or quota triggers are not recommended as a shortcut: they affect shared message storage or vendor tables, do not cap live cache/decoder memory, and need a separate schema/retention decision. None was authorized or implemented.

## R16 - Recommended Direction, Supply-Chain Choice and Proof Required

**Recommendation**: Pursue the hardening design, not a routine upgrade or an assumed exception. Use the current pin as the initial base; keep network frame/decoder/queue control in a focused protocol patch and put feasible storage/HTTP controls in our adapter. Before completing Phase 1, agree the changed overload semantics, limit profile, failure/recovery requirements and who owns any patched dependency. A complete patch estimate requires the detached-work and app-state atomicity proof, so no reliable fixed implementation-time estimate is available yet.

| Delivery route | Benefit | Constraint |
| --- | --- | --- |
| Upstream accepts a reviewed patch; re-pin its exact commit | Smallest long-term maintenance burden | Acceptance/timing unknown; prior related PRs were not merged. Creating an issue/PR is a separately authorized external action. |
| Exact reviewed fork revision via Go module replacement | Can carry a limited diff while preserving imports and one root Go module | Owner must accept maintaining/rebasing it, provenance/checksum/license obligations and repeated five-platform/security/lifecycle checks. MPL-2.0 requirements for modified upstream files must be satisfied. No fork or replacement was created. |
| Vendored/local module-cache edits | Superficially quick | Reject: ad hoc cache edits are unreproducible; regeneration can overwrite a vendor patch. Do not silently vendor the full dependency tree as a small fix. |
| Risk exception | Avoids immediate patch maintenance | Not selected by the owner. Would need explicit named ownership, controls, scope and expiry under Principle X; this research grants none. |

**Required offline proof before a control is marked passed**:

1. Decompression at/below/above the chosen cap, malformed/empty input, reader closure, checksums and bounded transient allocation; over-limit input must not publish a valid prefix.
2. Minimal valid three-byte child nodes, deep nesting, huge declared lists, dense attributes and encoder round trips; reject before expensive allocation and prove no invalid minimum-size shortcut.
3. Queue count and charged-byte exhaustion independently; no overflow waiter creation; IQ/keepalive responses remain deliverable while normal dispatch is saturated; typed failure cancels exactly one generation.
4. Stuck/timed-out handlers, reconnect, logout and late releases; old work stays charged, no negative/reset accounting, no new-generation budget bypass, no uncapped downstream background jobs.
5. Device bulk input/output and cache limits, low-disk/full errors, nested transaction callbacks, stable first-Save publication/readiness, deferred deletion and stop/drain; inject failures between version/MAC/contact writes and prove correct recovery rather than assuming whole-sync rollback.
6. Stalled/oversized/truncated external app-state responses and repeated pages; cap actual work while retaining valid pairing/reconnect semantics and secret-free diagnostics.
7. CGO-free build/test and pinned-dependency integrity/security checks on every supported target; live-account checks remain separate, optional until explicitly authorized for each step, and never automated.

These are proposed hardening-validation scenarios, not a completed patch test matrix. Public-source comparison and local code tracing were performed, followed by the separately authorized baseline reproductions in R17. No security exploit, account traffic capture, patch benchmark, project dependency upgrade or production-code change was performed. The owner may review this recommendation before authorizing further design/implementation or publication.

## R17 - Authorized Offline Baseline Reproductions

**Date/environment**: 2026-11-02, Windows amd64, Go 1.27.3, `CGO_ENABLED=0`, `GOMAXPROCS=2`. The owner explicitly approved isolated tests and required the temporary folder on drive D. The scratch directory is `D:\wa-003-repro-20261102-202105`, restricted to the current user, SYSTEM and Administrators.

**Isolation and source identity**: A separate Go module pins the same whatsmeow version, with its own D-drive module cache and disposable vendor tree. Missing module versions were downloaded from the public Go proxy with checksum verification; no shared module-cache source was modified. A nested module-boundary marker prevents Go from traversing the scratch cache as application source. The project code, go.mod, go.sum, Git index and deleted task file were not changed. No client constructor, real node handler, device store, credentials, sockets or account connection is used in the queue test. Only a new in-package test file is added to the disposable vendor copy; all 135 vendored non-test production Go files were verified byte-for-byte against the checksum-verified pinned source, with SHA-256 values recorded in `summary.json`.

**Harness and evidence paths**, relative to the D-drive scratch directory:

- `unpack_test.go`: public `waBinary.Unpack` test, SHA-256 checks the output against synthetic repeated-byte input, one fresh worker subprocess per size.
- `vendor/go.mau.fi/whatsmeow/offline_queue_repro_test.go`: calls the unchanged private `handleFrame` and `handlerQueueLoop` methods, substituting a deliberately blocked `message` handler. Uses real binary Marshal/Unmarshal and `receiveResponse`; no mock of the overflow implementation.
- `run-repro.ps1`: module verification, CGO-free compilation and three repetitions of both families, with 20-second worker test timeouts, 30-second parent subprocess deadlines and outer 90/120-second suite timeouts. `GOMEMLIMIT=384MiB` is a soft runtime target, not an OS-enforced memory cap. Inputs are separately fixed at safe finite sizes; no deliberate OOM test is run.
- `unpack-*-pid-*.json`, `queue-overflow-*-pid-*.json`: 21 worker records from the formal three-repetition run. Earlier exploratory executions are not included in that count.
- `cmd/summarize/main.go` and `summary.json`: validate seven case groups with three records each, check exact overflow growth and cleanup, and record the raw measurements/source hashes. The command passed with 21 records; tests characterize baseline behaviour rather than pass a security fix.

Reproduce from that scratch directory with PowerShell `./run-repro.ps1`. It uses `GOPROXY=off` after setup and runs no account/network test. New runs retain additional worker records; the summarizer accepts equal complete sets of three repetitions per case. The directory is temporary, not a portable tracked test suite or a patch distribution.

Additional completed checks: scratch `go mod verify` passed; `gofmt -l` on the four authored Go source files produced no output; `CGO_ENABLED=0 go vet -mod=vendor -p=2 . ./vendor/go.mau.fi/whatsmeow ./cmd/summarize` passed with `GOPROXY=off`; scoped repository `git diff --check` passed. These checks do not substitute for a security-patch test matrix or native Linux/macOS evidence.

### Decompression Measurements

Synthetic input is a valid zlib stream of repeated bytes with the WhatsApp compressed-frame flag. Its output is not a full valid WhatsApp node; the live caller nevertheless runs Unpack before node decoding. This reproduces the library allocation boundary, not an authenticated incoming server packet.

| Encoded frame bytes, including flag | Returned output | Allocated during Unpack, three-run range | Unpack elapsed time, three-run range | Result |
| --- | --- | --- | --- | --- |
| 2,054 | 1 MiB | 2,268,608 bytes | 1.0831-2.0057 ms | Returned complete output, no limit error |
| 16,428 | 8 MiB | 21,413,296-21,413,440 bytes | 14.0018-14.5071 ms | Returned complete output, no limit error |
| 65,770 | 32 MiB | 77,290,944-77,291,072 bytes | 50.0049-55.0314 ms | Returned complete output, no limit error |

The largest case expands approximately 510 times and cumulatively allocates about 73.7 MiB during the call. `runtime.MemStats.TotalAlloc` deltas measure allocation churn, not peak live heap, working set or RSS. Separate live-output heap measurements are in the JSON. No failure threshold, upper process capacity, larger payload, memory-exhaustion crash or decompression CPU-denial threshold was tested.

### Queue Saturation and Cleanup Measurements

The handler starts and intentionally blocks on its event context. The test then fills the real 2,048-node queue with distinct serialized synthetic `message` nodes containing 1 KiB payloads, and feeds the listed additional node counts. All three repetitions show the same goroutine counts:

| Additional nodes beyond a full queue | Goroutines with queue full | Goroutines after overflow | After connection-context cancellation | After handler-context cancellation and releasing references |
| --- | --- | --- | --- | --- |
| 0, control | 4 | 4 | 3 | 2, baseline |
| 128 | 4 | 132 | 3 | 2, baseline |
| 512 | 4 | 516 | 3 | 2, baseline |
| 2,048 | 4 | 2,052 | 3 | 2, baseline |

There is exactly one additional parked sender goroutine per overflow node while the connection context remains active. For 2,048 overflow nodes, the first formal run's post-GC HeapAlloc rises from 4,504,888 to 9,229,136 bytes and StackInuse rises from 163,840 to 16,941,056 bytes, approximately 20.5 MiB combined growth above the already-full queue. This is not a whole-process peak/RSS or a universal per-node cost.

A registered synthetic IQ response was delivered while the queue remained full in every worker, confirming the actual pre-queue response path still functions. Reported duration rounded to zero at this timer resolution; this is a correctness check with a finite timeout, not a zero-latency or keepalive benchmark. The queue loop and overflow senders clean up after connection-context cancellation; in the 2,048-overflow runs, observed cleanup was 3.9636-5.5191 ms. The remaining intentionally blocked handler exits on its separate event-context cancellation, after which goroutine count returns to baseline. Runtime heap/stack caches do not necessarily return all memory to the OS immediately.

**Important correction**: The tested overflow is active-connection resource growth, not a demonstrated permanent goroutine leak after connection cancellation. The pinned sender already selects on `ctx.Done()`, and the experiment verifies that path. Keeping an independently blocked handler alive after connection cancellation demonstrates the lifecycle-context distinction; it does not prove that every real handler is permanently stuck. No real socket `Disconnect`, reconnect, phone logout or handler-timeout-over-300-seconds path was exercised.

### Interpretation and Remaining Scope

- Confirmed: substantial output/allocation expansion is accepted by the pinned Unpack function; full-queue overflow creates linear live goroutine growth with no additional sender cap in the tested range; the 2,048-node channel is not a total-work bound.
- Confirmed mitigating behaviour: response routing still bypasses the saturated queue, overflow senders observe cancellation, and the synthetic handler exits when its own context is cancelled.
- Not established: whether ordinary contacts can cause attacker-chosen compressed frames, realistic burst/handler latency distributions, account/service compromise requirements, OOM or process failure, data corruption/disclosure, or an exploit severity/CVSS score. These remain availability design risks with potentially high impact and unmeasured real-world likelihood, not a demonstrated Critical or ordinary-contact remote vulnerability.
- Not tested: decoder amplification/depth limits, long handler-timeout accumulation, device persistence/cache limits, app-state atomicity/recovery, a hardening patch, Linux/macOS or cross-platform performance. No accepted limit or constitution exception follows automatically from these baseline tests.
- The evidence justifies an explicit capacity/overload policy, but does not by itself decide that maintaining a fork is proportionate. Use the measured behaviour and cancellation cleanup when weighing a scoped fix against a named, time-limited exception; retain the constitution gate until the selected policy is accepted.

## R18 - Owner-Approved Temporary Exception, Not a Fork

**Decision**: On 2026-11-02 the user accepted the exception recommendation and explicitly confirmed `tiger-seo` as owner, expiry `2026-12-02`, and earlier reassessment before public release/broader deployment or on a relevant upstream fix/resource incident. Canonical approval, exclusions, controls and evidence are in `verification.md` E003-RX-01; follow-up is `docs/backlog/003-whatsmeow-receive-resource-bounds.md`. The source specification records the product/security decision in section 8.11.

**Rationale**: R17 confirmed active growth but also verified overflow cancellation cleanup; it did not establish ordinary-contact exploitability, OOM or a permanent sender leak. A comprehensive maintained protocol fork is disproportionate to assume automatically for this first feature. The 2026-11-02 decision recorded a named, compensated, expiring risk acceptance, not a constitutional amendment or a claim that F09 was fixed.

**Governance alignment (2026-11-03)**: Constitution 2.0.0 now explicitly permits only identified upstream receive-resource deferrals under Principles V/X. That separate amendment supplies the authority previously missing from Principle V; it does not renew or expand E003-RX-01. The existing approval, scope, controls and expiry in verification.md remain binding. Receive bounds remain unmet, monitoring is not enforcement, and the aligned design needs a fresh committed owner-judge review.

**Boundaries**: Retain the exact protocol pin. No proposed 16-MiB/64-MiB upstream threshold becomes binding, and no patch, fork or publication is authorized. Device-store safety, application-owned limits, private files, credentials, shutdown and per-step account permission are not excepted. At expiry or a review trigger, pause the triggering workflow/deployment or continued operation pending a recorded new decision; the binary does not gain an undocumented calendar kill switch.

**Alternatives considered**: The inspected newer upstream does not fix the problem; a private fork requires maintenance not chosen here; an unowned/open-ended exception violates Principle X. Baseline tests are evidence supporting the decision, not proof of a mitigation.

## R19 - Immutable Version Globals and Per-Handshake Payload Snapshots

**Decision**: Never call runtime `SetWAVersion`. Install `Client.GetClientPayload` before first Connect. Each invocation obtains a fresh device payload, clones it and reads one immutable selected-version snapshot. The original version-only field restriction is superseded by the 2026-11-04 Linux/Chrome compatibility decision: also patch the cloned user-agent OS version/build on both registration and saved-session handshakes, and the cloned serialized companion properties on fresh registration as specified in plan.md. Keep unrelated metadata, keys and identity intact; never mutate global defaults or the pinned protocol. The callback takes no socket lock and performs no network/database I/O. A refresh changes the selected candidate, not an in-flight payload.

**Rationale**: Globals in `store/clientpayload.go` are unsynchronized; a mutex around explicit owner Connect calls cannot cover ordinary internal reconnect. The exported callback runs for every handshake, including library reconnects (`handshake.go:96-99`). The default device payload is cloned before use; registration carries a version hash whereas a saved-session login payload does not. Clone before mutation and snapshot version/hash together. Library global readers remain safe because globals never change.

**Lifecycle choices**: `EnableAutoReconnect=true`, `InitialAutoReconnect=false`, and `DisableLoginAutoReconnect=true`. An AutoReconnectHook denies retries after stop/logout/405 terminal state or while manual owner recovery owns the connection. Handle ManualLoginReconnect (515) through one serialized owner attempt; 405 uses the separate three-by-five-second budget. A fresh owner lifetime is passed to ConnectContext; a separate watchdog cancels a stalled attempt at 30 seconds, disarming on first valid QR or Connected. Cancellation precedes Disconnect so a dial holding socketLock can unwind. Do not cancel a healthy connection after 30 seconds. Ordinary library backoff remains as pinned, guarded against terminal-state bypass; no new automatic-pairing loop is introduced.

**Discovery validation**: Accept the pinned parser's `2.3000.<client_revision>` shape with the revision parsed as an unsigned 32-bit decimal, including zero; do not invent an upstream floor or future maximum beyond representability. Use the current bundled triple on initial failure, keep the latest successful candidate on later failure. A dedicated fetcher can enforce exact consumed bytes and safe error categories more clearly than truncating the library's unbounded ReadAll. Reject redirects/TLS failures, enforce the 15-second/1-MiB profile and never accept an incomplete response prefix.

**Alternatives considered**: Mutating globals after Disconnect still lacks a full old-reader join. Rebuilding the complete registration payload duplicates cryptographic metadata. Converting ordinary reconnects into a new owner retry algorithm changes preserved behaviour unnecessarily. Rejecting revision zero or adding an arbitrary upper version cap is not justified by the pinned source.

**Evidence**: `client.go:177-179, 518-601, 645-679`; `connectionevents.go:26-39, 139-141`; `store/clientpayload.go:79-102, 168-211`; `handshake.go:96-99`; version researcher verified these exact paths against the pin. Payload/hash and reconnect race tests remain implementation obligations.

**Reconnect qualification**: AutoReconnectHook runs after an error, not as a pre-attempt permission check. Lifetime cancellation and expected-disconnect are the primary terminal brakes; the source checks both while waiting. Bind a client generation to one logical QR window and retire it before a new window, since raw events have no caller attempt ID. Re-read saved identity after drain to preserve a pairing that became durable late. Recoverable outdated-client transport work remains within the current approval/budget, never reopening a terminal window automatically.

## R20 - Offline Status Without Creating Missing Storage

**Decision**: `store.Inspect(ctx, dataDir)` uses the validated committed SQLite/WAL view and an existence-only identity query, never writable Open, migrations or GetFirstDevice. Separate configuration/logger loading from PrepareDataDir. Add a no-create variant `lock.AcquireExisting(dataDir)` reusing Acquire's private-file/identity/PID rules but returning fs.ErrNotExist rather than creating a missing lock file.

**Algorithm**: Missing directory produces the offline unpaired snapshot without creation. For an existing directory, try an existing lock first so an owner that has not created its database yet is still reported. After taking that lock, inspect the current artifacts. If neither lock nor database/sidecar artifacts exists, return the point-in-time absent-storage snapshot without creating anything or reading database pages. If a database exists without a lock, normal Acquire may establish the required owner lock, then recheck artifacts and inspect. Orphan/unsafe sidecars fail rather than being ignored. Recheck existence/identities after ownership is established; never read a present database under an observed live owner. Existing lock-file PID bookkeeping is allowed, not a database/account-state mutation.

**Rationale**: Current startup creates the directory and current writable store Open probes/upgrades/migrates. Existing Acquire also creates the lock, violating missing-storage no-artifact behaviour if used unconditionally. A no-create helper shares the existing safe lock machinery rather than making status a second locking implementation. An absent-storage result is a local observation, not a claim that no process can start immediately afterwards.

**Output**: Four stable snake_case lines, connected=false and pairing_required=false; logged_in is saved identity presence only. wa_version is the immutable bundled version, labelled not refreshed; warn on stderr that stored credentials are not verified online. No HTTP, IPC, QR or version-discovery call. Preserve usage/config/storage error categories. Compatible old schemas are inspected only where the existing preflight and identity column support it; unsupported/invalid shape fails without upgrading.

**Evidence**: `internal/lock/lock.go:55-85, 108-113`; `internal/store/preflight.go:45-90, 103-205`; `internal/store/walview.go:174-234`; command startup/read-only constraints from R4. Add absence/held-owner/committed-WAL/identity-swap tests; snapshot database/sidecar bytes, separately accounting for existing-lock metadata.

## R21 - Shared Device Admission, Durable Repair and Honest Quiescence

**Decision and approval**: The user explicitly selected durable recovery, reusing the already approved store profile rather than accepting stale contact sync across restart. Plan one embedded application migration (message schema 1 -> 2); device schema remains 16. Do not change the protocol dependency or claim sibling device calls become one transaction.

**Same-store boundary**: Retain a `*dbutil.Database` in Store and construct the sqlstore container with `NewWithWrappedDB`, preserving NewWithDB's dialect/version-table/upgrade configuration. This exported constructor exists at pinned `store/sqlstore/container.go:78-97`. Cache-less ContactStore/LIDStore implementations and raw session delegates then use the SAME dbutil instance/context transaction key over the same writer pool. Device calls take the same 16-active/64-waiter, 5-second admission, 10-second operation and disk-guard budgets as application calls, not a second independent gate. Batch accounting reuses the 16-MiB aggregate/32-MiB output, 256-byte JID and 1-KiB name limits; existing message-count limits are not blindly reinterpreted as a total address-book limit. Bound SQL sub-batches and charge typed element overhead as well as fields before copying. Reject unsupported whole-contact/whole-key exports in the device adapter rather than materializing an unbounded map; current 003 paths do not call them and later tools use the bounded application queries.

**Nested calls**: The outer EventBuffer.DoDecryptionTxn owns one lease/deadline. Nested identity/session calls reuse a private live-lease token and the SAME incoming context values, including dbutil's transaction key; reconstruct cancellation/deadline linkage if WithoutCancel removed it. Never re-admit or acquire another writer while the transaction is held. Reject expired/foreign leases and incompatible nested bulk/marker work before mutation. R22 strengthens the original first-Save design: prevent raw-field publication rather than reassigning live fields after the call.

**No false atomicity**: Policy, admission, deadline, disk and driver failures can occur after an earlier sibling method committed. There is no supported grouping seam around the library's version/delete-MAC/put-MAC/contact sequence. Input validation precedes each call's writes but does not prove a whole logical sync untouched. No goroutine-identity transaction heuristic, silent truncation or crypto-state eviction is accepted.

**Durable recovery design**: Migration 2 adds `device_repair`, keyed by the saved device JID and one of the five pinned app-state collection names. It stores a revision, target version/reset state and nullable repair ticket, never keys/message bodies. At most one row per collection for the owned device. Upsert/commit a dirty marker before a version/MAC mutation or full contact-snapshot mutation; if recording fails, do not delegate that mutation. Do not clear on PutMAC completion, physical Connected, process exit or an uncorrelated library event. A conservative dirty marker may remain after successful ordinary incremental sync and cause one extra verification resync on a later start; this trades bounded extra reads for durable uncertainty tracking.

After an authorized connection, one serialized worker reads pending collections and uses the existing `FetchAppState(ctx, name, true, false)` API. At most one attempt per pending collection per owner generation, with the reused 10-second operation deadline; preserve the marker and report recovery-pending on timeout/failure, not an endless retry. The externally fetched app-state HTTP body uses the reused 16-MiB batch budget and 10-second deadline via SetMediaHTTPClient. Application discovery keeps its distinct approved 15-second/1-MiB profile. A manual restart after fixing the cause can retry; no phone unlink or new recovery command is introduced.

Prepare a unique non-secret repair ticket and track guarded write failures. Clear with compare-and-delete only after the owner-controlled full-resync call returns successfully, no guarded error occurred, and a marker/admission fence verifies that no later/interleaved write has superseded the ticket/revision/target. Unrelated writes invalidate the clear condition; conservative retention is safe. Do not clear on bare AppStateSyncComplete: that event can race a later sync and some contact errors are logged rather than propagated. New mutations must establish their own dirty indication before changing state, including when they race a clear. Test the fence, not only the happy path. Marker rows cascade with local deletion of the genuinely revoked device; they never cascade to application chats/messages.

**Quiescence correction**: Existing gate.close unconditionally returns nil after its bounded waits (`internal/store/admit.go:105-124`), and Store.Close marks closed before draining (`store.go:301-317`). Neither proves zero active calls. Add explicit shared quiescence completion and a distinct not-quiescent result; leave pools open while active. Use one completion waiter, not a new goroutine per retry. The generation barrier separately rejects/drains old-client calls on logout without permanently closing the shared store needed for a new device. Full shutdown closes shared admission, cancels contexts, waits for zero active work, then closes pools and releases the lock. Ten-second notification records failure but the lifecycle continues waiting with a live cleanup context. Pool-close I/O errors after proven quiescence are failures, not evidence that a device write remains admitted.

**Logout ordering**: Callbacks only update bounded state/schedule retirement; remove/disconnect outside dispatch locks. After old drain, controlled maintenance must remove confirmed revocation before a new device. Failure blocks account work/new generations and cannot report successful cleanup; explicit shutdown nevertheless closes/unlocks after true quiescence with unsuccessful exit and unresolved-state warning, as R22 clarifies. Do not remove identity for ordinary network failure. Forced kill is outside clean-shutdown guarantees.

**Evidence**: Pinned `appstate.go:31-99, 162-200` returns full-sync completion only after collection, while some per-mutation errors are merely logged; `appstate/decode.go:229-243` commits sibling metadata methods separately. The supported healing API deletes only app-state progress before re-fetching, not identity/session keys. Current contacts/LID caches are private and unbounded, so a delegating wrapper cannot evict them; implement cache-less device views inside internal/store, using the same tables and transaction-aware handle. Re-audit on any pin change.

**Alternatives considered**: In-process-only retry can leave stale state after restart; the user chose durable markers instead. Assuming admission failures cannot occur mid-sequence is incorrect. A marker is not rollback, and full resync is not proof that failed work was never committed. A fork, arbitrary total DB quota, quota triggers on vendor tables and deleting session keys are unnecessary and not selected.

**Cache-scope correction**: The raw SQLStore's migratedPNSessionsCache is also store-resident and not part of the receive exception. Implement cache-less MigratePNToLID through the retained transaction-aware device DB, preserving its SQL semantics. Do not merely replace contacts/LIDs and claim every device-store cache is bounded. Upstream client/processor receive caches are a separate documented residual surface under the narrow exception.

**Enforce the factory boundary**: Replace the raw exported Store.Device() container accessor with LoadDevice(ctx), which performs admitted/disk-ready load and attaches the guard before returning device/barrier. Keep raw container and wrapper constructor inside internal/store; disallow a second live device session until retirement. Adapt internal fixture seeding rather than retaining an unguarded compatibility method. This closes a caller bypass that relying only on "Owner promises to wrap" would leave open.

## R22 - Stable Guard Publication and Deferred Device Deletion

**Decision**: Keep device interface fields stable after handing the object to NewClient. The prior recommendation to rewrap after first Save is superseded: another goroutine could observe the raw initializer's temporary assignments before the wrapper resumes. Avoid that window, do not merely test final pointer equality.

**Pinned source evidence**: SQLStore.NewSQLStore(container,jid) is exported (`store/sqlstore/store.go:56-67`). The Initialized flag is read only by raw PutDevice and set by initializeDevice (`container.go:251,262`, verified module-wide search); it denotes interface wiring, not SQLite persistence or authenticated login. Device.Save delegates to Container; Device.Delete returns immediately on Container.DeleteDevice error, otherwise rewrites ID/LID/Deleted and all session interfaces (`store/store.go:279-298`).

**Construction/first Save**: Load existing devices before exposure. For fresh devices, install stable Container/session/LID guards and complete interface wiring (Initialized=true) before client creation. Once first PutDevice has a valid frozen JID, construct private NewSQLStore delegates, preserving cache-less contact/LID/session views. Raw PutDevice skips its initializer and cannot replace published fields. Ordinary calls before durable readiness fail fast before admission/SQL; holding every active slot waiting for Save could otherwise starve that Save's own admission. Failed/cancelled Save never signals success; Initialized is not login evidence. Integrity failures stop rather than repair live fields in place.

Raw Save receives a validated input snapshot whose JID is the frozen binding; reject a different later identity and do not pass an ID pointer that a caller can change between validation and SQL argument capture. Input-size validation precedes copying. Include the concurrent-mutation case in tests rather than relying only on one sequential successful Save.

**Deletion**: Client-originated Container.DeleteDevice requests coalesce retirement and return a typed deferred result, preventing Device.Delete's successful live-field reset. Cancel generation work, drain admitted calls and let owner-controlled, admitted private-container cleanup remove only a confirmed revoked device. A failed pairing/network operation does not automatically delete a saved identity; preserve it for a later explicit verification. Expected deferred cleanup is distinct from a real SQL failure and never reported as completed revocation. No destructive remote unlink, protocol patch or second database is introduced.

The original decision blocked new generations/account work only while this owner ran and relied on an operator warning after shutdown. The user superseded that limitation on 2026-11-04: a durable unsafe-run marker is armed before account work, remains armed when revocation cleanup fails and blocks automatic account work on restart. Explicit shutdown still closes/unlocks after true quiescence, exits unsuccessfully and does not wait forever solely because deletion could not persist. Recovery is offline, explicitly interactive and uses separately authorized phone-side verification/unlink and local identity removal followed by a separate fresh-pair approval. The marker means uncertainty, not confirmed revocation; no saved-credential reuse is offered from uncertain state. The marker's durability and the legacy-schema/first-pair crash cases are specified in plan.md and data-model.md.

**Tests/limitations**: Hold first Save using barriers and attempt concurrent store access, retirement and cancellation; assert no raw field is ever observable and readiness cannot falsely open. Inject Device.Delete through the real guarded object, verify deferred result/no field reset/no premature SQL deletion, then explicit confirmed-revocation cleanup after drain. Cover late durable Save and restore-on-restart behaviour, not only end-state type checks. These are source-backed design obligations, not tests already executed.

**QR-only source check**: The default QR helper auto-confirms a SkipHandoffUX passkey event (`qrchan.go:164-173`), so ignoring its output alone is not prevention. At this pin the cache is armed only by SendPasskeyResponse, and continuation/confirmation fail before sending when unarmed (`pair-passkey.go:111-115,166-168,211-216`). Do not expose/call that method or PairPhone through 003's private adapter. Exercise the real helper offline with test-only DangerousInternals.DispatchEvent and a forbidden-network client; raw helpers stay out of production. Re-audit on a pin change. This establishes a testable rejection invariant without adding a new authorization flow or a fork.

## R23 - Repair Before Competing SDK Sync, Not a Context-Only Mutex Timeout

**Finding addressed**: Design review of c8455f4 returned CONCERNS because FetchAppState takes a plain appStateSyncLock before ctx-aware work (`appstate.go:46-62`, `client.go:107`). Notifications and key-share handlers can invoke it concurrently (`notification.go:481`, `message.go:856`). Passing a ten-second context alone cannot preempt Lock and could consume the only repair attempt waiting for ordinary sync. The concern is amended/re-reviewed, not accepted or added to the receive exception.

**Decision**: Load pending markers and configure a startup-recovery admission mode before creating/connecting the client. While required startup repair is pending, untagged app-state progress/MAC and bulk-snapshot operations for ALL collections return expected recovery-pending immediately, before shared admission/SQL/HTTP. The SDK's first progress call is then short, so normal automatic sync cannot take its global mutex across a long fetch ahead of repair. Tagged owned repair is allowed, and AppStateSyncKey delivery remains allowed so bootstrap does not deadlock. Marker existence created by a healthy normal update alone does not activate this mode. Expected deferrals are not storage faults or full-resync attempts.

Coalesce deferred ordinary collection demand in a fixed five-name bitset and replay incrementally after required repair, through the same one actual SDK-call slot. These replay the ordinary requests deferred by the gate, not additional full-resync retries. Failure waits for new notification/connection demand; no tight autonomous retry or unbounded per-notification goroutine. Admission checks/signals are short and hold no mode/owner/SQL lock across SDK calls or I/O. Stale-generation demand is discarded, while the actual-call slot is NOT reset on generation change.

**Deadline mechanics**: The controller selects result/cancellation/deadline around a single tracked SDK invocation. It retains the actual-call lease until the method really returns, even if logical caller timeout has already returned pending. Timeout cancels/invalidates the ticket, preserves its marker and ignores late completion; it does not free the lease or launch another blocked call. Check cancellation/deadline again before a selected success can clear state. No goroutine may independently clear a marker after controller completion. This bounds caller wait and retained owned call count without claiming Go can forcibly interrupt a library mutex.

Link BackgroundEventCtx to the generation lifetime before use, not an operation/watchdog context. An active guarded-write fault pauses/cancels the generation for operator-authorized restart; that fresh client's startup admission is configured before background sync begins. Do not start an automatic same-client repair loop behind the faulting SDK call. Missing sync keys are transient: preserve pending state and allow key delivery, with no extra full-resync attempt invented.

**Tests required**: Real pinned FetchAppState with a first GetVersion deliberately held, explicit cancellation of the controller, no network, no second invocation while the SDK call remains blocked, then release and verify late result cannot clear/mutate. Use an ordinary channel-barrier test for the real plain mutex, not fake time (mutex waits are not synctest-durably blocked). Separately fake-time a channel-blocked backend to prove ten-second caller budget. Exercise startup automatic-sync deferral, allowed key delivery, coalesced ordinary replay, simultaneous success/deadline, and unchanged once-per-collection full-resync accounting. No test/implementation PASS is claimed here.

**Missing-key source correction after re-review**: `applyAppStatePatches` explicitly calls `go requestMissingAppStateKeys(context.WithoutCancel(ctx), patches)` (`appstate.go:164-165`), then the request uses SendPeerMessage (`496-535`). The previous tail-ctx recommendation was wrong: WithoutCancel removes both deadline and cancellation, so early caller cancellation cannot suppress it and gen cancellation alone does not stop it. Remove that mechanism. Scope the owner-wide lease to actual owner-invoked FetchAppState invocations, NOT SDK-spawned key-request jobs. The pin supplies a default 75-second message-response wait, 24-hour per-key dedup, disconnect-cleared response waiters and retryFrame's up-to-five-second connection wait (`request.go:150,197-202`; `send.go:213-214,440-452`; `client.go:616,718,728`). Those are component behaviours, not a hard total tail/RSS bound or new app profile.

Stop calls Disconnect before finalization and retires the generation guard; no detached caller can acquire new SQL work or keep an admitted lease uncancelled after retirement. Inactive repair-ticket checks govern protected app-state progress/clear, not necessary generation-valid protocol key crypto/delivery, which still takes normal shared leases/deadlines. Existing nested transaction-token validation remains strict. Record residual SDK jobs as upstream receive work under the existing scoped exception, not a new waiver of device safety. Offline runtime tests must verify detached ctx behaviour and stop ordering, not fake a ten-second tail or claim every SDK goroutine is joined.

**Alternatives rejected**: Adding an unapproved second full-resync retry, resetting call budget on timeout/generation, assuming ctx preempts Mutex.Lock, blocking key delivery, repeatedly spawning workers, or requiring a protocol fork/exception extension. The amendment preserves the approved policy and limits and makes admission/caller-wait semantics explicit.

## Research Completion Gate

Research and Phase 1 choices are complete. R18 records the explicit named/expiring exception; R19-R23 resolve version, status, durable recovery, stable device publication and contention-aware repair scheduling. The upstream risk is excepted, not fixed; device safeguards remain mandatory. R17's baseline measurements are not feature-control evidence. No fork, production implementation or account action is authorized by this research. Implementation requires the latest committed owner-judge design verdict; the earlier CONCERNS is not self-accepted.
