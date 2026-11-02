# Research: WhatsApp Connection, Pairing and Status

**Date**: 2026-11-02
**Scope**: Offline source research for roadmap feature 003. No pairing, live device connection, message send or WhatsApp version request was performed.

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

**Rationale**: The pinned library has detached node-handler, pairing and prekey goroutines. `Disconnect` waits only on the handler queue loop, gives up after five seconds and does not join those writers. Some persistence uses `context.WithoutCancel` or `BackgroundEventCtx`. All inspected paths still route through the replaceable device fields, including libsignal adapters and the app-state processor's shared device. `initializeDevice` resets those fields after load and after the first `PutDevice` of an uninitialized device; wrapper reattachment is mandatory before returning to client code. A later `Device.Delete` replaces session fields with no-ops and makes Save return `ErrDeviceDeleted`.

**Alternatives considered**: Context-only cancellation and event-handler WaitGroups miss internal writers. A dbutil `UnderlyingExecable` proxy misses `LoggingTxn` operations on raw transactions. A SQL-text filter is neither a reliable admission barrier nor a robust query policy. Pre-setting `Initialized` merely to bypass initialization risks relying on an undocumented initialization invariant; prefer explicit reattachment.

**Evidence**: Pinned whatsmeow `client.go:724-738, 941-958`; `pair.go:148-159, 226-264`; `connectionevents.go:187-199`; `store/store.go:123-126, 195-212, 279-299`; `store/sqlstore/container.go:151, 251-263`; util v0.11.0 `dbutil/transaction.go:59-64`. Re-audit all persistence fields when changing the pin; embedding a raw interface and accidentally promoting unguarded new methods is not a complete guard.

## R8 - Separate Connection Lifetime, Attempt Deadline and Pairing Window

**Decision**: Keep a cancellable per-generation lifetime context and an owner-side 30-second watchdog for a decisive connection outcome, not a 30-second deadline on the lifetime context. Set `InitialAutoReconnect=false` so initial connection errors cannot be hidden by a background retry. QR acquisition occurs before Connect; one dedicated consumer drains each channel and updates a single current-code snapshot. Confirm both durable pairing and `Connected`, not merely `PairSuccess`, before successful command exit.

**Rationale**: `ConnectContext` reuses its context for keepalive and handlers; a deadline on it would kill an otherwise healthy session. First QR validity is 60 seconds, later codes 20 seconds, and exhaustion emits final timeout/disconnect. Replacement codes stay within the approved attempt; the channel's buffer of eight is not a retained QR history for callers. Timeout retry can reuse an unpaired client after disconnect. Confirmed logout deletes the old device permanently, so same-process re-pair must retire its generation and create a fresh device/client on the same container, without deleting conversations. Never overlap active clients.

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
| Protocol-managed persistence growth | App-state processor is always constructed (`client.go:287`); contact/LID/app-state updates persist through device interfaces. | The drain barrier proves lifecycle safety, not total data volume or disk consumption. These writes already occur in 003, before application ingest 004. |

**Supported partial mitigations**: `ManualHistorySyncDownload=true` and `DisableManualHistorySyncReceipt=true` avoid automatic history blob transfer (`message.go:892-906`); retain disabled decrypted-event buffering, retry-message storage and automatic rerequest defaults; reject future device admissions during shutdown; bound application attempts and discovery. Exported registration history preferences can request smaller batches, but are server requests rather than hard local receive quotas, require accepted values and do not cap later incremental updates. Initial prekeys (812) and later batches (50) are already fixed; they are not unbounded exposures.

**No supported complete intercept found**: `handshake.go:124-125` wires the internal frame handler into Noise; callbacks occur after unpack. Deprecated DangerousInternals exposes methods for calling, not a supported setter that replaces the handler on the live private socket. Reimplementing the handshake/socket stack to gain interception would substantially expand protocol ownership and still conflict with private client send paths. A soft `debug.SetMemoryLimit` is not a hard cap and does not control all pending-node concurrency or disk writes.

**Threat boundary and limitation**: TLS and the pinned Noise certificate verification (`handshake.go:85-87, 147-186`) authenticate the real WhatsApp service. This is a trusted-upstream availability risk, not evidence that an arbitrary contact can directly supply a crafted Noise frame. No exploit or live-account test was run. Treat it as an unresolved High design risk under F09 because allocation/process exhaustion is possible before the accepted controls; this is not a new CVE assertion or vulnerability-scanner result.

**Alternatives requiring owner action**: Investigate a pinned upstream fix or narrowly scoped reviewed patch with accepted output/queue/device-growth limits and CGO-free security tests; or record an explicit temporary exception with named owner, compensating controls, scope and expiry in `verification.md`. The owner-approved profile says to enumerate remaining library exposure before implementation approval; it does not itself grant the constitution's required exception. Deferring already-live protocol operations to 004 cannot resolve the gate.

## Research Completion Gate

The source unknowns have been investigated. Phase 0 reached an explicit error, not a successful design approval: Principles V/X fail on R12 and no exception exists. Candidate decisions R1-R11 are retained so planning can resume without repeating research. No Phase 1 data model, contracts or quickstart are generated until the owner decision is recorded and the gate is re-evaluated. No implementation, real-account activity or dependency update was performed.
