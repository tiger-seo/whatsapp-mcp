# Tasks: WhatsApp Connection, Pairing and Status

**Input**: Completed design in `specs/003-whatsapp-pairing-status/`: spec.md, plan.md, research.md, data-model.md, contracts/ and quickstart.md. Exception approval/evidence is in verification.md.

**Readiness**: Implementation requires a current owner-judge design review of the committed feature directory against constitution 2.0.0. The 2026-11-04 device-identification design change is not covered by the earlier design verdict; obtain a fresh review before implementing it or continuing implementation. Check E003-RX-01 remains valid at execution time as an approved temporary deviation, not a satisfied upstream resource bound. No account action is authorized by this design change.

**Tests**: Required by the spec's independent journeys, SC-001 through SC-010, security abuse cases and constitution Principles VI/X. Write the relevant tests before implementation and observe a meaningful failing case; do not substitute source-text assertions for runtime behaviour. Use channels/barriers and fake time, not sleeps, for concurrency. Keep OS/SQLite/HTTP fixtures outside testing/synctest bubbles.

**Organization**: Setup, shared safety foundation, four story phases in spec priority order, then cross-cutting evidence. Later features still own application message ingest, MCP/HTTP, sends, media and webhooks. No protocol fork or new settings are part of these tasks.

## Format: `[ID] [P?] [Story] Description`

- Every task has a checkbox, sequential ID and exact repository-relative file paths.
- `[P]` means distinct-file work within the explicit dependency wave, never permission to bypass a prerequisite or review.
- `[US1]` through `[US4]` identify story tasks; shared/setup/polish tasks have no story label.
- Quoted field constraints below come from data-model.md and are binding. Apply appropriate aggregate/field budgets, not an unrelated message count to a whole address book.

## Phase 1: Setup

**Purpose**: Prepare the approved, offline-only implementation environment after design review.

- [X] T001 Recheck `specs/003-whatsapp-pairing-status/verification.md` E003-RX-01 against the actual UTC date, named owner tiger-seo, expiry at the start of 2026-12-02 UTC and earlier review triggers; record implementation-start environment/limits without claiming new tests passed, and stop for owner reassessment if expired/triggered. Do not expand the receive exception to device safety or infer real-account permission.
- [X] T002 Add only `rsc.io/qr v0.2.0` root encoder to `go.mod` and `go.sum`, retain the exact WhatsApp/SQLite pins, verify checksums/reachable vulnerabilities and CGO-free compatibility, and explicitly exclude its C-backed libqrencode subpackage (SEC-008).
- [X] T003 Establish deterministic synthetic-key, forbidden-external-network, fake-provider and channel-barrier fixtures in `internal/wa/helpers_test.go`, extending existing `internal/store/helpers_test.go` only where shared fixtures require it; use isolated t.TempDir storage and approved fast Windows temp space, never normal user config/credentials, and reserve local HTTP/TLS only for controlled fixtures.

## Phase 2: Foundational Safety and Contracts

**Purpose**: Complete shared contracts, device admission, marker storage and truthful close before any user-story wiring can use a real adapter. Depends on T001-T003 and the design gate.

- [X] T004 Write `internal/wa/client_test.go` contract tests for the lifecycle/status/pairing/event/health interfaces, value/copy semantics and pin-compatible service signatures, including Downloader's store.MediaMeta input with nullable FileLength rather than a full original protobuf (FR-012).
- [X] T005 Define `internal/wa/client.go` owner/capability types against T004: epoch is "Monotonically distinct within one owner; stale epoch events never change current state."; device/client is "At most one active pair; fresh device after confirmed revocation, same shared container. Never overlap a new client with unquiescent old access."; lifetime is "Cancellable generation context, not a 30-second expiring connection context."; admission is "Per-generation reject/drain plus the one shared store gate. Nested transaction calls reuse a live lease."; phase is "idle, connecting, pairing, connected, disconnected, logged_out, retiring, storage_failed, stopping or stopped."; health is "Separate from physical connection; startup repair pending, guarded-write fault and ready are internal, not extra login-tool fields." Declare narrow pin-typed Sender/Uploader/ContactLookup/GroupLookup and the contract's metadata-based Downloader, with no real send/media/tool routing.
- [X] T006 Add `internal/store/device_admission_test.go` tests covering guarded load, no raw container escape, one loaded session, every Container/session/LID method, shared limits/disk latch and overflow-safe rejection before copying/SQL. Hold first Save at barriers while concurrent pre-ready calls see stable guards and fail fast without consuming admission slots/SQL; test successful, failed, cancelled and late Save, JID switching/mutation after binding, deferred Device.Delete without live field reset, and nested DoDecryptionTxn/WithoutCancel. Post-Save pointer equality alone is not proof against temporary raw publication; rejected calls must not mutate data (FR-001/014/016, SEC-003/005).
- [X] T007 Refactor `internal/store/store.go` to retain the same configured dbutil.Database supplied to NewWithWrappedDB; replace exported raw Device() access with LoadDevice(ctx) returning only guarded device/barrier, perform admitted/disk-ready load, reject a second live session and adapt package-private fixtures in `internal/store/device_test.go` without retaining an unguarded compatibility accessor.
- [X] T008 Implement explicit generation wrappers in `internal/store/device.go` against T006/R22: install stable Container/all-twelve-session/LID guards before NewClient; complete wiring with Initialized=true before exposure, bind private NewSQLStore delegates on a validated JID and skip raw initialization. Reject identity switches and pass a validated frozen-JID snapshot to raw Save. Separate durable readiness from wiring; ordinary pre-ready calls fail fast before admission/SQL instead of starving Save, and raw fields are never published on failure/cancel. Client delete requests return typed deferred retirement, not a successful live-field reset. Share gate/disk/deadline/error policy and nested transaction values; reject stale/foreign leases/incompatible bulk work and avoid another writer/admission budget.
- [X] T009 Add `internal/store/device_contacts_test.go`, `internal/store/device_lids_test.go` and `internal/store/device_session_migration_test.go` runtime tests for cache-less contact/LID/PN-session migration semantics, old-value returns, misses, bulk rollback and transaction reuse; assert unused all-contact/all-key exports fail explicitly rather than allocate/truncate, and no raw migratedPNSessionsCache remains reachable.
- [X] T010 Implement cache-less views in `internal/store/device_contacts.go` and `internal/store/device_lids.go` plus cache-less MigratePNToLID in `internal/store/device.go`, preserving pinned SQL/return semantics through the same dbutil handle. Reuse 16-MiB aggregate input, 32-MiB output, 256-byte JIDs and 1-KiB names, charge typed metadata safely, validate before each call's SQL and use bounded sub-batches without a new total-address-book or lifetime-database quota.
- [X] T011 Write `internal/store/quiesce_test.go` held-operation tests showing generation retirement does not close shared storage, shared closing rejects new calls, expired close does not close pools/mark success, repeated close attempts create one completion waiter, and true drain permits final closure; cover cancellation-independent raw-device calls, not only application writes (SC-005).
- [X] T012 Correct `internal/store/admit.go` and `internal/store/store.go` and finish `internal/store/device.go` retirement against T011: open means "Existing accepted shared budgets apply to device and application operations."; retiring means "Old client cannot admit new calls; admitted calls drain. Shared store remains available for controlled revocation/fresh generation only after old drain."; closing means "Shared admission permanently closed; reject new calls; contexts cancelled after grace. Pools remain open while work is active."; quiescent means "Zero admitted operations and no future admissions; safe to close pools. Completion signal closes once."; closed means "Pools closed after quiescence; lock release may follow. Close errors are reported; timeout alone never establishes this state." Expose explicit completion/not-quiescent outcomes and never let a bounded wait returning imply drain.
- [X] T013 Write failing timeout/held-device-call and second-signal cases in `cmd/whatsapp-mcp/lifecycle_test.go`, then update `cmd/whatsapp-mcp/lifecycle.go`: ten seconds latches failure but continues waiting, remaining cleanup gets live contexts, owner -> store -> lock is enforced, and neither timeout nor still-active store error advances to lock release. Distinguish pool-close error after drain from active work; forced termination remains unclean.
- [X] T014 Add `internal/store/device_repair_migration_test.go` tests for fresh migration 1+2, upgrading populated schema 1, preserving chats/device keys/schema 16, foreign-key marker cleanup on genuine device deletion, invalid field rejection and atomic migration failure/reopen; retain refused-open no-database/sidecar-change checks.
- [X] T015 Add `internal/store/migrations/0002_device_repair.sql` through the existing four-digit embedded loader and update old latest-schema assertions in `internal/store/migrate_test.go` and `internal/store/open_test.go`. Implement device_repair primary key (device_jid,collection) with these exact constraints: device_jid "Required, canonical saved device identity, at most 256 bytes; foreign key to whatsmeow_device.jid with ON DELETE CASCADE."; collection "Required, exactly one of critical_block, critical_unblock_low, regular_high, regular, regular_low."; revision "Required positive signed 64-bit integer; increments before each protected mutation; overflow fails without resetting live state."; target_version "Nullable fixed 8-byte big-endian encoding of the protocol uint64 version; NULL means reset/unknown rather than an integer-domain truncation."; repair_ticket "Nullable fixed 16-byte non-secret uniqueness token for an owner-controlled repair; do not log it. Ordinary mutations do not inherit another repair's ticket."; updated_at_ms "Required UTC Unix milliseconds, diagnostic only, never a correctness/ordering substitute for revision." Enforce at most one row per owned device/known collection; do not alter device schema or message identity rules.
- [X] T016 Add `internal/store/repair_test.go` tests for mark-before-write, restart/crash between sibling writes, ticket/revision/target validation, discarded guarded errors, late-clear races, overflow/wrong device/collection, retirement and bounded enumeration. Also test early admission-mode primitives: untagged progress fails before SQL/admission, tagged repair and key delivery remain allowed, deferred names coalesce, and expected deferral is not a storage fault. Recording failure must prevent mutation; failed/superseded repair cannot erase uncertainty or keys/chats (SC-009).
- [X] T017 Implement bounded marker queries, BeginRepair/CompleteRepair and mutation/clear fences in `internal/store/repair.go`, with hooks in `internal/store/device.go` and `internal/store/device_contacts.go`. Provide the admission-mode/deferred-bitset primitives needed before T026, initialized from startup markers, without interrupting healthy sync merely because a normal dirty row appears. Commit markers before protected work, retain failures and compare-delete only verified current repair. Never hold mode/owner/SQL locks across SDK/network calls, clear on bare events, claim whole-sync rollback or deadlock nested Signal operations.
- [X] T018 Write all-level template/module/control-character/secret tests in `internal/walog/logger_test.go`, relocate the audited argument-dropping bridge to `internal/walog/logger.go`, and update/remove the old implementation in `internal/store/walog.go` while preserving `internal/store/walog_test.go` expectations; drop upstream format arguments, keep one stderr logger and safe level/module handling (FR-015, SEC-001/006).
- [X] T019 Write `internal/wa/fake_test.go` tests that call every declared capability without network, record calls, inject events/failures and prove copied status/QR/protobuf/media metadata/group/contact fixtures cannot alias mutable fake state (SC-008).
- [X] T020 Implement `internal/wa/fake.go` against T019 with narrow in-memory handlers/fixtures, cancel-aware lifecycle and deterministic events; real send/upload/download, message ingest, tool registration and listener paths remain unavailable in 003.
- [X] T021 Write consumed-body/TLS/redirect/stall/concurrency tests in `internal/wa/http_test.go` and `internal/wa/version_test.go`, then implement `internal/wa/http.go` and the initial selector/fetch in `internal/wa/version.go`: use one in-flight request, fixed verified origin and approved discovery 15-second/1-MiB profile; supply the app-state ten-second/16-MiB profile for adapter installation. Version is "a value triple of uint32 components" with pinned 2.3000.<uint32 decimal> grammar including zero, no invented floor; preserve bundled/latest fallback. Reject advertised/consumed oversize, incomplete/truncated prefixes, bad certificates and redirects without leaking bodies or changing a valid candidate; use no hidden proxy/credential headers.

**Checkpoint**: Shared contracts/fakes and all non-excepted device/HTTP/close/marker controls are present and their synthetic tests pass. No account action has occurred. E003-RX-01 still accepts only the listed upstream receive bounds.

## Phase 3: User Story 1 - Link a Device and Reuse Its Session (Priority: P1)

**Goal**: Terminal QR linking, durable session reuse and truthful safe shutdown, plus connection-owning shells without future transports.

**Independent Test**: Fake protocol/version plus real isolated SQLite: link once, wait for saved identity and Connected, stop, reopen and reconnect with zero extra QR. Exercise save failure, locked/unsafe storage, long-lived connection beyond the old watchdog and both shells' zero tools/listener/QR output.

### Tests First

- [X] T022 [P] [US1] Add synthetic production-adapter/persistence tests in `internal/wa/session_test.go` for guarded load/options, version selected before Connect, payload clone/version/hash without global mutation, save failure, PairSuccess-before-Connected, valid-session restart without QR, rejection before network on unsafe storage and no credential replacement on network failure. Also exercise the real pinned QR channel's SkipHandoffUX path using test-only DangerousInternals.DispatchEvent: keep passkey cache unarmed/no SendPasskeyResponse or PairPhone capability, assert confirmation fails before IQ/network, and safely reject unsupported state rather than faking away that branch (FR-001/002/003, SEC-002).
- [X] T023 [P] [US1] Add `internal/wa/owner_test.go` state/watchdog/stop tests covering all four fields, copied snapshots, saved-but-unverified initial state, ordinary disconnect, stale epoch denial, cancelled dial before Disconnect and a healthy connection surviving the 30-second initial watchdog. Test saved-session startup failure and timeout, three five-second retries without a fourth, success, shutdown/logout/storage-fault cancellation, 405 handoff, preserved identity and no QR; prove stalled initial and retry calls retain the attempt slot until drain (FR-004/005/013/014, SC-010).
- [X] T024 [P] [US1] Add `internal/wa/qr_test.go` and `cmd/whatsapp-mcp/pair_test.go` contract tests for deterministic quiet-zone rendering, bounded invalid/oversize/encoder failures, no raw payload logs, saved-session confirmation, holder PID, interrupts/exit codes and injected owner/version dependencies with forbidden real-account access (SEC-001/003/005/007).

### Implementation

- [X] T025 [US1] Implement `internal/wa/owner.go` coherent copied state, unique epochs, one active generation, callback admission and BeginStop/Done. Status constraints are: connected "True only after the current generation's Connected confirmation; false on disconnect/logout/stop."; logged_in "Locally saved identity unless confirmed revoked: false immediately on confirmed logout, preserved on ordinary disconnect. Revoked-device retirement/cleanup must finish before a new generation. Not proof of current network acceptance. Fresh PairSuccess may establish saved identity, but pair command still waits for Connected."; pairing_required "Derived: not logged_in AND current code exists AND now is before its expiry. False after code expiry even if a window awaits replacement."; wa_version "Selected immutable candidate rendered as a three-part version, not a claim of server negotiation. Standalone status uses the bundled version only and labels it unrefreshed." Implement the separate per-owner-attempt watchdog and safe callback/disconnect/drain ordering, not a lifetime deadline. Implement the separate saved-session shell startup budget of three five-second retries after initial failure/timeout, ending on Connected, stop, logout, storage fault or 405 handoff; exhaustion retains identity and reports an actionable diagnostic without pairing.
- [X] T026 [US1] Implement `internal/wa/session.go` using only Store.LoadDevice and guarded state; configure pending-repair admission before client creation, link BackgroundEventCtx to generation lifetime, and install shared logging, bounded app-state HTTP and immutable version/hash callback before Connect. Set the three reviewed reconnect flags/serialized 515 reconnect and disable unneeded history/decrypted-event/retry buffering. Normal failure preserves identity; no real service/tool/media routing is enabled. Use the T021 selector without SetWAVersion or an operation/watchdog deadline as the client lifetime.
- [X] T027 [US1] Implement first-window control in `internal/wa/pairing.go` and safe rendering in `internal/wa/qr.go`. Attempt identity is "Bound to the owner epoch; one attempt at a time."; approval "Explicit request opens one window including upstream replacement codes, never a new window automatically."; current code "At most 4 KiB; empty/malformed/over-limit/encoder-failed input cannot replace valid state."; representation "At most one derived terminal/image representation; no QR history, files or logs."; expiry "Earlier of upstream expiry and 60 seconds from receipt; use monotonic elapsed time internally."; result "active, succeeded, timed_out, cancelled or failed. Terminal outcomes invalidate code and late events."; consumer "Exactly one fast channel consumer; callers share the snapshot/update signal rather than reading the upstream channel." Acquire the QR channel before Connect, reject unsupported passkey/phone-code paths and clear material on terminal state.
- [X] T028 [US1] Inject the owner/provider factory through `cmd/whatsapp-mcp/commands.go` and its test streams, replace pair/stdio/serve stubs, and register safe owner cleanup after store cleanup registration. Pair exits successfully only with durable identity plus Connected, with honest pending-repair warning when applicable; saved identity gets no QR and pair retains its failure exit. Both shells use T025's bounded saved-session startup recovery and remain alive disconnected with an actionable diagnostic after exhaustion; unpaired shells do not automatically Connect/QR, expose no listener/tools, and stdio EOF stops cleanly. Tests always supply the fake factory (FR-002/003/013/015).
- [X] T029 [US1] Update `cmd/whatsapp-mcp/store_test.go` and `cmd/whatsapp-mcp/e2e_test.go` for real owner-shell behaviour using isolated synthetic paths: lock-holder refusal, first Save/restart, no post-close device write, stdin/signals, zero stdio human stdout and zero unsolicited QR/tools/listener. Exercise saved-session startup failure and timeout for both shells, including three delayed retries, exhaustion, success, cancellation and 405 handoff. Record actual US1 synthetic counts in `specs/003-whatsapp-pairing-status/verification.md`, not live acceptance claims (SC-001/005/006/008/010).

**Checkpoint**: The prior US1 linking/restart journey has synthetic evidence, but the Linux/Chrome registration compatibility follow-up T065-T067 remains incomplete. The full US1 checkpoint is reopened until that follow-up passes. This is not permission for a real account, public release or skipping US4's long-running compatibility and final security/evidence gates.

## Phase 4: User Story 2 - Recover Pairing Without Restarting (Priority: P1)

**Goal**: Code rotation, explicit timeout retry, same-process logout/re-pair and restart-safe bounded device-data repair without erasing conversations or session keys.

**Independent Test**: In one synthetic owner process expire a code/window, retry explicitly, revoke and re-pair with fresh generation; inject old events and late durable Save. Separately interrupt real SQLite version/MAC/contact operations, reopen and drive success/failure/interleaving through a fake upstream repair API, checking retained/cleared markers and unchanged credentials/chats.

### Tests First

- [ ] T030 [P] [US2] Add `internal/wa/pairing_test.go` tests for shared concurrent requests while code is valid or awaiting replacement, per-code vs final timeout, wait-context cancellation vs window approval, shutdown rejection, stale/late events, one consumer/representation and no automatic new window (FR-005/006/007).
- [ ] T031 [P] [US2] Add `cmd/whatsapp-mcp/pair_retry_test.go` tests showing Enter alone authorizes retry after final timeout, EOF/cancellation exits 1 without another attempt, input is bounded/cancel-aware and cannot compete with stdio input; cover invalid/unsupported QR events and no QR in errors/logs.
- [ ] T032 [P] [US2] Add `internal/wa/logout_test.go` synthetic guarded-store tests for immediate false login/connection, no unsolicited pairing, old-generation drain before new client, preserving a late saved identity, and confirmed-revocation cleanup. Failed cleanup must block new generations without deleting chats/messages; explicit stop still closes/unlocks after true drain with unsuccessful exit/unresolved-state warning, not an indefinite post-quiescence hang (FR-003/004/006/014).

### Implementation

- [ ] T033 [US2] Extend `internal/wa/pairing.go` with shared request/update signalling, automatic replacement inside one approval, explicit cancel/final timeout, bounded representation replacement and request-context isolation; do not stop the dedicated consumer when one observer returns or let expired-code reads create competing windows.
- [ ] T034 [US2] Implement terminal-window retirement/new generation in `internal/wa/session.go` and `internal/wa/owner.go`: invalidate callback admission, cancel/remove/disconnect outside dispatch locks, await barrier drain, reload durable identity before fresh keys and bind new callbacks to a new client epoch. Never relabel old untagged client events or overlap clients (SC-002).
- [ ] T035 [US2] Implement confirmed-logout maintenance in `internal/store/device.go` and `internal/wa/session.go`: coalesce deferred delete requests, drain old access and persist only genuine revocation through the private container, never successful Device.Delete on a live object. Failure blocks account work/new generations; explicit stop drains, closes/unlocks and exits unsuccessfully with remediation warning instead of hanging after quiescence. Preserve identity on ordinary disconnect/failed pairing, do not claim deletion that could not persist, and never use remote unlink (SEC-003).
- [ ] T036 [US2] Add `internal/wa/repair_test.go` tests for marker reload/sibling failures, discarded errors, late clear and 16-MiB consumption. Exercise real pinned FetchAppState mutex contention via held GetVersion/cancellation with no network: caller completes before SDK return, no replacement call or late/deadline-race clear. Fake-time channel work separately, not a real mutex. Verify the pin's WithoutCancel key-request detachment with test-only ApplyAppStatePatches hooks, required key crypto/delivery and disconnect/retired-guard stop safety; do not assert a ten-second SDK-tail deadline. Cover early deferral, incremental replay/no autonomous retry, fault restart and unchanged full-resync count (SC-009).
- [ ] T037 [US2] Implement `internal/wa/recovery.go`/`internal/wa/owner.go`, real FetchAppState, ticket/error latches and T017 clear. Actual-call constraint: "At most one owner-invoked FetchAppState call is outstanding across client generations; timeout/retirement does not release its slot before that invocation returns." Deadline outcome: "Ten-second caller wait; cancel/invalidate the repair ticket, retain the marker, discard late results, and start no replacement FetchAppState call while the prior invocation remains outstanding." Select result/cancellation, recheck deadline and make result delivery nonblocking. Do not invent a ten-second timer/slot for SDK-detached key-request jobs; enforce their generation guard/disconnect safety per R23. No extra full-resync call, SQL lease across network, key/chat deletion or false rollback (FR-016).
- [ ] T038 [US2] Integrate `internal/wa/owner.go`, `internal/wa/session.go` and `internal/store/device.go`. Admission mode: "Before connecting with pending repair, ordinary untagged app-state progress/MAC and bulk-snapshot calls fail fast; tagged repair and sync-key delivery remain permitted. Resume ordinary sync only after required recovery succeeds." Deferred work: "Coalesce deferred ordinary sync demand in a five-collection bitset; replay through the same FetchAppState slot after repair, never as another full-resync retry or an unbounded per-notification goroutine." Treat deferral/missing keys separately from write faults, preserve normal bounded key-request crypto despite inactive repair tags, and disconnect/retire on stop even for WithoutCancel. Real write fault requires restart; a healthy normal dirty row alone does not activate the gate. Health remains private, not a login-schema field.
- [ ] T039 [US2] Add final-timeout Enter handling and safe cancellation/EOF cleanup in `cmd/whatsapp-mcp/commands.go`, using the existing owner rather than launching a second process/client. A repeated request can observe a late durable paired identity and reconnect without overwriting it; recoverable 405 work may not reopen a terminal/cancelled logical window.
- [ ] T040 [US2] Exercise the full adverse interleaving matrix in `internal/wa/recovery_races_test.go`: replacement QR vs logout, late PairSuccess vs timeout/retire, final timeout vs delayed 405, nested/WithoutCancel persistence vs stop, new mutation vs repair clear, repair completion vs newer client epoch, and failed revocation cleanup vs fresh Pair; show no rejected action mutates current account/QR or loses durable repair need.
- [ ] T041 [US2] Run the end-to-end synthetic expiry/logout/re-pair and crash/reopen-repair journeys in `cmd/whatsapp-mcp/recovery_test.go`, seed chats/messages/keys as sentinels, and record exact counts/error outcomes/unchanged data in `specs/003-whatsapp-pairing-status/verification.md`; distinguish committed sibling work from rollback and leave real-account recovery unverified (SC-002/005/009).

**Checkpoint**: Pairing can recover within one owner process, and interrupted app-state/contact work remains recoverable across restart. Only successful current fenced repair clears its marker; login success does not claim complete contact/history sync.

## Phase 5: User Story 3 - Understand Local Session Status (Priority: P2)

**Goal**: Honest offline local inspection with no connect/discovery/pairing/migration/repair, missing-storage artifacts or concurrent-owner reads.

**Independent Test**: Forbid owner/provider/network construction and test absent/empty directories, pre-DB held lock, paired/unpaired schema 1/2, committed WAL, orphan/unsafe/substituted/corrupt/unsupported storage. Compare database/sidecar bytes and identities before/after, separately allowing the contract's existing-lock PID bookkeeping.

### Tests First and Independent Lock Primitive

- [ ] T042 [P] [US3] Add `internal/store/inspect_test.go` read-only tests using existing preflight/WAL/synthetic identity fixtures: absence, schema 1 and 2 identity presence, pending repair without running it, committed WAL, orphan/unsafe artifacts, corrupt/unsupported schema and swapped handles; assert no writable probe/upgrade/migration/key loading or database/sidecar changes (FR-008/009).
- [ ] T043 [P] [US3] Add `cmd/whatsapp-mcp/status_test.go` contract tests for all four fields, offline-credentials and bundled-not-refreshed labels, zero client/provider-start/discovery/IPC/QR calls, no missing artifacts, pre-DB holder PID and operational/usage error distinctions; inject dependencies that fail if account/network work is attempted (SC-003).
- [ ] T044 [P] [US3] Add no-create/private-handle/holder/race tests in `internal/lock/existing_test.go`, then implement AcquireExisting in `internal/lock/lock.go` using the existing safe acquisition machinery; absent lock returns fs.ErrNotExist, no link/reparse following or permission repair, and no duplicated unsafe locking implementation.

### Implementation

- [ ] T045 [US3] Implement `internal/store/inspect.go` using validated artifact/header/schema preflight, the read-only committed WAL view and an identity-existence-only query. Never call writable Open/GetFirstDevice, migrate, create sidecars, repair markers or return secret keys; preserve compatible-old-schema inspection and existing classified refusal errors.
- [ ] T046 [US3] Refactor configuration/logger loading vs directory preparation and implement runStatus in `cmd/whatsapp-mcp/commands.go`: try an existing lock before deciding no DB exists, return a no-artifact absent snapshot, acquire/recheck before inspecting present DB, report held PID and reject orphan/unsafe data. Print connected=false, pairing_required=false, saved identity presence and bundled/unrefreshed version with stderr warning; no live-owner query or hidden repair.
- [ ] T047 [US3] Replace conflicting status stub/lock/directory/exit expectations in `cmd/whatsapp-mcp/cli_test.go`, `cmd/whatsapp-mcp/store_test.go` and `cmd/whatsapp-mcp/e2e_test.go`; keep version independent and ensure invalid storage leaves database/sidecars/unrelated targets unchanged, with only the CLI contract's prescribed lock bookkeeping permitted.
- [ ] T048 [US3] Update `.github/workflows/ci.yml` privacy setup to use the existing offline seed helper `internal/store/testdata/cmd/seed/main.go` rather than expecting status to create a directory/fail; retain cross-identity denial, SHA-pinned actions, native-platform coverage and synthetic-only setup (SEC-003/007).

**Checkpoint**: The entire status matrix passes without successful pairing, a live service, MCP/HTTP or version discovery. This story can be validated independently on synthetic stored identities.

## Phase 6: User Story 4 - Stay Compatible With WhatsApp (Priority: P2)

**Goal**: Refresh the selected version without interrupting a healthy socket and recover from 405 within the fixed budget while preserving pairing consent and stop semantics.

**Independent Test**: Controlled responses plus fake time prove startup fallback, strict consumption, six-hour refresh, immutable handshake/hash snapshots during ordinary/515 reconnect, exactly three five-second 405 retries, coalescing, terminal denial and cancellation. No live WhatsApp request is used.

### Tests First

- [ ] T049 [P] [US4] Add `internal/wa/version_refresh_test.go` tests for six-hour fake-time scheduling, one overlapping fetch, waiting-caller cancellation vs shared fetch, acceptance of zero/max-uint32 revisions and rejection of malformed/overflow revisions, exact/unknown-length body boundary and safe non-200/redirect/TLS/stall failures, preserving candidate/QR/session and healthy connection. Reuse the initial fetch/body tests from T021 (FR-010, SEC-004).
- [ ] T050 [P] [US4] Add `internal/wa/version_recovery_test.go` tests for three retries after the initial rejection, cancellable five-second waits and refresh before each, no fourth retry, failed refresh not resetting budget, reset only on current Connected, duplicate/concurrent 405 and stop/logout/terminal-window denial. Stall 405 and 515 owner-initiated connects and verify the 30-second watchdog, cancellation before Disconnect, retained attempt slot until drain and no startup-budget bypass (FR-011, SC-010).
- [ ] T051 [P] [US4] Add `internal/wa/session_version_test.go` pin-level callback tests: one cloned payload/version/hash snapshot per handshake, preserved identity/key fields, immutable globals, no callback I/O/socket-lock recursion and selected-version use on explicit/ordinary/515 reconnect without timer-killing a healthy session. Use a barrier to refresh the candidate after a handshake snapshots it: verify AppVersion, OS version/build and, for fresh registration, companion version and hash remain mutually consistent and the existing payload unchanged; verify the next fresh and saved-session handshakes use the new candidate (FR-017, SC-004).

### Implementation

- [ ] T052 [US4] Extend `internal/wa/version.go` with one owner-lifetime six-hour scheduler/single-flight refresh, preserving the bundled/latest fallback and candidate publication without disconnecting current sockets; "Handshake freezes one candidate and registration hash together in a fresh cloned payload. Library globals remain immutable. Refresh never changes an in-flight payload or disconnects a healthy socket." No new setting, global SetWAVersion or hidden parallel fetch.
- [ ] T053 [US4] Add the coalesced 405 controller to `internal/wa/recovery.go`: "The 405 budget is three retries after the initial rejection, each five seconds and a refresh attempt; duplicate events share it. Only current Connected resets it. Ordinary network backoff is separate and cannot bypass terminal state." Keep its accounting separate from US2's once-per-collection durable repair worker and from explicit pairing approval.
- [ ] T054 [US4] Integrate refresh/retry/515/ordinary reconnection in `internal/wa/session.go` and `internal/wa/owner.go`, retaining the initial safe callback from T026. Apply the 30-second attempt watchdog to each owner-invoked 405/515 connect; retain the single attempt slot until the call and generation access drain. Use lifetime cancellation/expected disconnect as primary terminal brakes because AutoReconnectHook is post-error; serialize manual work, retire superseded clients safely and never turn a delayed 405 into a new unapproved window or bypass its budget with startup recovery.
- [ ] T055 [US4] Wire the complete provider/recovery lifecycle through `cmd/whatsapp-mcp/commands.go` without duplicating the initial fetch or creating another client, emit bounded category-only fallback/exhaustion diagnostics and cancel refresh/delays before store close. Offline status must still use only the immutable bundled getter, never start this provider.
- [ ] T056 [US4] Add/run synthetic command cases in `cmd/whatsapp-mcp/version_connection_test.go` for discovery-before-Connect, fallback, running refresh, 405 exhaustion/reset, shared request cancellation and stop; assert no raw upstream bodies or stdio pollution and record actual timing/budget results in `specs/003-whatsapp-pairing-status/verification.md` (SC-003/004/006).

**Checkpoint**: Initial and subsequent handshakes use coherent selected versions, refresh does not disturb a healthy connection, and all automatic recovery obeys budget/approval/stop rules. This completes the long-running compatibility policy required before live acceptance.

## Phase 7: Polish and Cross-Cutting Evidence

- [ ] T057 Add all-level adversarial secrecy and bounded-monitoring tests in `internal/wa/security_test.go` and `cmd/whatsapp-mcp/output_test.go`, then finish fixed one-minute debug heap/goroutine samples and owned lifecycle reason events in `internal/wa/owner.go`; no in-process sample history, QR/keys/bodies/tickets/account IDs, injected terminal controls or stdio human stdout. Verify stop cancels monitoring and treat it as observation, never a hard upstream cap (SEC-001/006, exception controls).
- [ ] T058 Execute/extend `internal/store/abuse_test.go`, `internal/store/logsafety_test.go`, `internal/wa/security_test.go` and `cmd/whatsapp-mcp/recovery_test.go` negative cases for invalid consumed input, permission/identity swap, queue floods, raw-device/bulk/nested work, held writes, partial markers, epoch races and bounded HTTP; capture actual rejection-point side effects and native control results in `specs/003-whatsapp-pairing-status/verification.md`. Separate prescribed lock bookkeeping and previously committed sibling work from forbidden data/network/unrelated-artifact mutation; never claim the receive exception covers device safety.
- [ ] T059 [P] Update `OPERATIONS.md`, `AGENTS.md` Current state and `specs/003-whatsapp-pairing-status/quickstart.md` for offline status, QR/Enter/EOF, marker recovery/pending health, limits, version fallback, safe timeout/forced kill and local monitoring/incident response; document a separately permissioned phone-label observation without guaranteeing its text, and accurately distinguish implemented from verified. Keep E003-RX-01 and follow-up `docs/backlog/003-whatsmeow-receive-resource-bounds.md` open with owner/expiry/triggers, no total-retention or upstream-fix claim, and no unrelated README/release work.
- [ ] T060 Build the manual-only helper in `internal/wa/testdata/cmd/session-check/main.go` using the production guarded owner/store/lock and explicit per-connect/per-pair interaction; require a named isolated private acceptance directory, support same-process logout observation/re-pair, expose no shipped command/listener, never invoke from go test and never automate phone-side unlink. Add safe-input/permission flow tests in `internal/wa/testdata/cmd/session-check/main_test.go` with a fake backend and no account (SC-007).
- [ ] T061 Run and record formatting, vet, `CGO_ENABLED=0 go test -timeout 30m ./...`, module integrity, CI-pinned govulncheck, driver/libc consistency, five CGO-free target builds and explicit helper build/test in `specs/003-whatsapp-pairing-status/verification.md`. Obtain native Linux/Windows/macOS permissions/signals/failure evidence; record unavailable checks unverified, retain heavy-test opt-in, and request any needed external CI/push permission rather than treating this task as authorization to push or act on an account.
- [ ] T062 Obtain fresh user permission separately for every real-account connect/pair/phone-unlink/re-pair step in `specs/003-whatsapp-pairing-status/quickstart.md`, name the exact directory/account/device, record intended changes and approved cleanup, then run the manual journey and redacted results in `specs/003-whatsapp-pairing-status/verification.md`. Confirm initial pair, offline status, saved reconnect with no QR and same-process logout/re-pair; separately observe the phone's displayed device label after T065-T067 without promising its exact text. Declined/unperformed steps remain unverified and the task incomplete, never infer permission from exception approval (FR-017, SC-001/007, SEC-007).
- [ ] T063 Reconcile every FR/SC/SEC and applicable security finding with actual commands/dates/counts/platforms/results in `specs/003-whatsapp-pairing-status/verification.md`, rerun permitted `specs/003-whatsapp-pairing-status/quickstart.md` checks and recheck exception expiry/earlier triggers; preserve baseline-probe vs feature-control distinction, report remaining proof gaps and do not close the feature on missing required manual/native evidence.
- [ ] T064 Commit the implementation and `specs/003-whatsapp-pairing-status/verification.md` evidence, obtain owner-judge code review of that commit, record/commit its exact row and resolve BLOCK or owner-decided CONCERNS before a PR. Re-review changed covered design/code and keep any still-valid design review record; never fabricate a verdict, automatically park a finding, create a PR, push or release without the required separate authorization.

### US1 Compatibility Follow-up

These new tasks retain the existing task IDs and reopen the US1 checkpoint. After a current committed design review, execute T065 -> T066 -> T067 before claiming US1 complete or starting dependent US2 integration. No task authorizes an account action.

- [ ] T065 [US1] Add tests first in `internal/wa/session_test.go` for actual fresh-device registration payloads: decode companion properties and assert Linux, CHROME and the selected version triple; assert matching cloned user-agent OS version/build, AppVersion and registration hash. Drive the pinned QR construction path without network and assert the emitted synthetic QR's final client-type field is Chrome (`1`), with its synthetic reference/key fields preserved and no QR printed in failures or evidence. Test multiple candidate versions, fresh cloned payloads, preserved unrelated properties/keys and protobuf unknown fields, unchanged library globals, and saved-session reuse without re-pairing, including its selected-version user-agent fields. Inject decode/encode failures and assert the pinned handshake rejects a nil payload, no incorrectly identified or partial registration is sent, credentials stay unchanged and diagnostics contain no secrets. Observe an intended runtime failure before implementation (FR-017, SC-001, SEC-007).
- [ ] T066 [US1] Update `internal/wa/session.go` against T065 to set per-client Chrome QR type before QR acquisition, modify cloned companion properties for Linux/Chrome on fresh registration, and set cloned user-agent OS version/build on both fresh and saved-session handshakes from one frozen selected version; retain unknown protobuf fields and all unrelated payload/identity fields. On companion-property decode/encode failure return nil for the pin's handshake rejection and record a bounded nonblocking failure category for a secret-free diagnostic outside the callback; never send an unmodified or partially modified registration or panic. Do not call global SetOSInfo/SetWAVersion, mutate library globals, change the WhatsApp pin or force a new device for saved sessions (FR-017).
- [ ] T067 [US1] Run and record the T065 runtime tests and relevant existing session/QR/restart tests in `specs/003-whatsapp-pairing-status/verification.md`, including commands, date, counts and unchanged-global/saved-identity outcomes. Record the phone label as unverified until the separately permissioned T062 check; do not infer display text from protocol properties (FR-017, SC-001, SEC-007).

## Dependencies and Execution Order

### Gate and Phase Graph

```text
Completed plan + valid E003-RX-01 + current committed design review
  -> Setup T001-T003
  -> Shared foundation T004-T021
  -> US1 T022-T029 + T065-T067 -> US2 T030-T041
  -> US3 T042-T048 (independent branch; shared CLI integration after T028)
  -> US4 T049-T056 (independent test wave; shared production integration after US2)
All story branches -> Cross-cutting T057-T064 -> current code review before any PR
```

The requirement to obtain design review is a pre-implementation gate, not a task executed during generation and not a fake completed checkbox. No park/exception expansion is introduced. T001 rechecks approval at actual start, even if task generation reviewed it earlier.

### Foundation Ordering

Run T001 -> T002 -> T003. T004 precedes T005; T006 precedes T007/T008; T009 precedes T010; T011 precedes T012; T013 uses proven store quiescence; T014 precedes T015; T016 precedes T017 (which also needs T008/T010/T015); T018 supplies the shared bridge; T019 precedes T020 (which needs T005); T021 supplies bounded HTTP/initial versions before any production adapter. Treat all foundation source changes as sequential; no blanket cross-package concurrency is promised while interfaces/helpers are being introduced.

No story implementation begins before foundation is green. Tests written first may initially fail because their new behaviour is absent, but foundation checkpoint requires runtime proof rather than only a compile failure.

### Story Ordering and File Conflicts

- US1: T022/T023/T024 form a parallel test-authoring wave after foundation. T025 -> T026 -> T027 -> T028 -> T029 was the original sequence. After fresh design review, T065 -> T066 -> T067 completes the added identification compatibility; T065 must demonstrate the missing behaviour at runtime before T066. T067 precedes US2 integration and the US1 checkpoint.
- US2: depends on US1's owner/session/CLI. T030/T031/T032 form a parallel test wave. Then T033 -> T034 -> T035; T036 must be written before T037/T038; T039 depends on the timeout/retirement implementation; T040/T041 exercise the integrated negative/end-to-end paths.
- US3: needs foundation and the shared command-factory shape, not successful pairing/recovery. T042/T043/T044 can run concurrently in distinct store/cmd/lock files; T045 depends on T042, T046 on T043/T044/T045, then T047 and T048. If executing this branch before US1's command factory, introduce a typed fake seam without enabling a production account path, then serialize the actual shared commands.go integration after T028.
- US4: T049/T050/T051 form a separate test wave after foundation and typed session contracts; T052 then T053 then T054/T055/T056. T053/T054 share recovery.go/session.go/owner.go with US2 and must wait for T037/T038 or be integrated serially; T055 shares commands.go with US1/US2/US3. Never claim all stories can be implemented concurrently.
- Polish: T057/T058 require all production story paths. T059 may overlap final test execution only after behaviour is stable and while no other worker edits its docs. T060 precedes explicit helper validation in T061; T061 precedes permissioned T062; T063 accounts for both; T064 needs completed required evidence, never a skipped real/native check marked passed.

### Parallel Examples per Story

| Story | Safe wave after listed prerequisites | Join point |
| --- | --- | --- |
| US1 | T022 session_test.go, T023 owner_test.go, T024 qr_test.go + pair_test.go | Owner/adapter/CLI implementation T025-T028; validate T029. |
| US2 | T030 pairing_test.go, T031 pair_retry_test.go, T032 logout_test.go | Shared mutation T033-T035; validate repair/races T036-T041. |
| US3 | T042 inspect_test.go, T043 status_test.go, T044 existing_test.go/lock.go | Inspect/status integration T045-T047; CI fixture T048. |
| US4 | T049 version_refresh_test.go, T050 version_recovery_test.go, T051 session_version_test.go | Refresh/retry/owner integration T052-T055; validate T056. |

These are internal scheduling examples, not requests to create Agent Manager sessions/worktrees. `[P]` tasks may still have prerequisites; they are runnable concurrently only once their stated wave is ready.

## Requirement and Test Coverage

Mappings are planned work, not passed implementation evidence. The fake/public-provider boundary and real SQLite device path must both be exercised; do not fake away the persistence or lease behaviour under test.

| Requirement | Build/integration tasks | Test/evidence tasks |
| --- | --- | --- |
| FR-001 | T007-T008, T026, T028 | T006, T022, T024, T029 |
| FR-002 | T027-T028, T033, T039 | T022, T024, T030-T031, T041 |
| FR-003 | T025-T026, T034-T035 | T022-T024, T032, T040 |
| FR-004 | T005, T025, T034-T035 | T004, T023, T032, T040 |
| FR-005 | T025, T027, T033 | T023-T024, T030 |
| FR-006 | T033-T035, T039 | T030-T032, T040-T041 |
| FR-007 | T025, T033-T034 | T030, T032, T040 |
| FR-008 | T044-T046 | T042-T044, T047 |
| FR-009 | T045-T046 | T042-T043, T047, T056 |
| FR-010 | T021, T026, T052, T055 | T021-T022, T049, T051, T056 |
| FR-011 | T053-T055 | T040, T050-T051, T056 |
| FR-012 | T005, T020, T026 | T004, T019, T024, T029 |
| FR-013 | T025, T028 | T023-T024, T029, T056 |
| FR-014 | T008, T012-T013, T025, T028 | T006, T011, T013, T023, T029, T040, T058 |
| FR-015 | T018, T028, T055, T057 | T018, T024, T029, T056-T058 |
| FR-016 | T008, T010, T015, T017, T037-T038 | T006, T009, T014, T016, T036, T040-T041, T058 |
| FR-017 | T066 | T051, T062, T065, T067 |
| SC-001 | T025-T028, T066 | T022-T024, T029, T062, T065, T067 |
| SC-002 | T033-T035, T039 | T030-T032, T040-T041, T062 |
| SC-003 | T025, T045-T046 | T023, T042-T043, T047, T056 |
| SC-004 | T021, T026, T052-T055 | T021, T049-T051, T056 |
| SC-005 | T008, T012-T013, T025 | T006, T011, T013, T023, T029, T040, T058 |
| SC-006 | T018, T027-T028, T057 | T018, T024, T029, T031, T056-T058 |
| SC-007 | T060 | T059, T061-T063 |
| SC-008 | T005, T020, T028 | T004, T019, T024, T029 |
| SC-009 | T015, T017, T037-T038 | T014, T016, T036, T040-T041, T058 |
| SC-010 | T025, T028, T053-T054 | T023, T029, T050, T056 |
| SEC-001 | T018, T027, T033 | T018, T024, T030-T031, T040, T057-T058 |
| SEC-002 | T025, T028, T033-T035, T039 | T023-T024, T029-T032, T040, T043 |
| SEC-003 | T007-T008, T010, T035, T044-T046 | T006, T009, T014, T022, T024, T032, T042, T044, T058, T061 |
| SEC-004 | T021, T026, T052 | T021, T049, T051, T056 |
| SEC-005 | T008, T010, T021, T025, T027, T033, T053 | T006, T009, T021, T023-T024, T030, T040, T049-T051, T058 |
| SEC-006 | T018, T028, T055, T057 | T018, T024, T031, T043, T056-T059 |
| SEC-007 | T003, T020, T048, T060 | T019, T024, T043, T060-T063, T065, T067 |
| SEC-008 | T001-T002 | T002, T061, T063-T064 |

E003-RX-01 compensating controls map to T001 (scope/expiry), T008/T010/T012/T013/T017 (device safety), T021/T026/T027/T033/T052-T055 (own limits/reduced surface), T057 (monitoring), T059 (incident guide/backlog), and T061-T064 (evidence/reassessment/review). These do not close F09 or claim hard upstream limits. Named later-feature deferrals remain in spec.md; do not build those capabilities here.

## Implementation Strategy

### MVP First

1. Obtain the current committed design verdict, recheck E003-RX-01 and execute setup/foundation.
2. Complete US1 and validate the isolated link/save/restart path. This is the first synthetic functional MVP, with the real initial bounded fetch and guarded store in place.
3. Do not treat a short-lived pairing demo as a released feature: complete US2 recovery, US3 diagnostics, US4 long-running compatibility and cross-cutting controls/evidence before real acceptance/code review. Public release independently triggers exception reassessment.

### Incremental Delivery

Proceed through the story checkpoints, keeping main's existing tests/build policy intact and recording actual results as work finishes. Use parallel test waves, not concurrent edits to shared owner/session/recovery/command files. Mark a checkbox complete only after its code and required verification actually pass; never tick manual/native work because it was planned or declined.

### Stop Conditions

Stop for expired/triggered exception, a stale/failing design review, a new independently unaccepted security/product decision or absent real-account permission. Do not auto-park High findings, silently enlarge limits, claim rollback of partial sync or create an unrequested fork. Keep implementation/PR/code-review gates distinct.

## Counts and Format Validation

| Area | Tasks |
| --- | --- |
| Setup T001-T003 | 3 |
| Foundation T004-T021 | 18 |
| US1 T022-T029, T065-T067 | 11 |
| US2 T030-T041 | 12 |
| US3 T042-T048 | 7 |
| US4 T049-T056 | 8 |
| Polish T057-T064 | 8 |
| Total | 67 |

Thirteen `[P]` tasks: T022-T024, T030-T032, T042-T044, T049-T051 and T059. T001-T029 retain their earlier completed status; T030-T067 are unchecked. All 35 FR/SC/SEC identifiers have build/test mappings. Validate this table/IDs/paths/constraints after any task edit. The earlier design review does not cover this change; its exception acceptance is not a substitute for a current reviewer verdict.
