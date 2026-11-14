# Verification: Message Ingest

Sections up to "Rebuilt Fork Branch" describe the superseded prototype in `../whatsmeow`; from "Owner Decisions
2026-11-13" on, the pinned revision.

## Local Fork Test

- Date: 2026-11-10, native Windows.
- Dependency: local `../whatsmeow`, clean at commit `be96244c768496c2393c193c97ba5dc109d266e1`; the `go.mod` replacement follows the directory, not an immutable revision.
- Command: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 30m`.
- Result: all 9 packages printed `ok`; the tracked process exited, but the process tool did not expose its numeric exit code. Exit code: not captured. No real account was used.
- This does not establish feature 005 acceptance or close F09: the fork still has unbounded receive queue overflow work, and the selected frame/decode limits have not been validated against real traffic. E003-RX-01 is not automatically extended to this feature.

## Decoder Allocation Recheck

- Date: 2026-11-10, native Windows; local fork has uncommitted changes on top of `be96244`.
- Command: `$env:CGO_ENABLED='0'; go test ./binary -count=1`; exit code 0, one package passed.
- The fork now grows decoded child lists only after successfully parsing each child; synthetic tests exercise nested wide declarations and a valid sibling list. This does not establish an aggregate decoder memory ceiling or resolve the history and queue limits.
- At this check queue overflow policy was undecided; see the later decision below. No exception or real-account test was authorized. Existing receive-resource and owner-approval gates remain open.

## Decoder Resource Counts

- Date: 2026-11-10, native Windows; local fork has uncommitted changes on top of `be96244`.
- Command from the local fork: `$env:CGO_ENABLED='0'; go test ./binary -count=1`; exit code 0, one package passed.
- The decoder now applies per-decode node and attribute counts and refuses direct oversized inputs. These experimental limits are not a measured heap ceiling or owner-approved compatibility thresholds.
- The user chose fail-closed disconnect with an explicit incomplete-history warning on queue overload. This is a behavior decision, not proof that the queue changes are implemented or safe and not an exception for F09.

## Fork History Limits And Session Test

- Date: 2026-11-10, native Windows; local fork has uncommitted modifications on top of `be96244`.
- From the fork: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m`; exit code 0; two packages passed, remaining packages had no tests.
- From this project: `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m`; exit code 0; one package passed.
- The fork experimentally caps compressed history chunks at 32 MiB and decompressed chunks at 128 MiB; automatic history storage is performed synchronously. These per-chunk limits do not prove a process-memory bound, cap concurrent manual downloads, or establish history replay, receipt or deletion safety. Numeric compatibility limits are not approved for deployment.
- Receive queue overflow and work spanning generations remain unbounded. No overload-disconnect implementation or overload-warning test was completed; F09 and the constitution gate remain open. The focused session test does not establish real-account compatibility or feature 005 acceptance.

## Receive Prototype Recheck

- Date: 2026-11-10, native Windows; local fork contains uncommitted changes on top of `be96244`.
- Fork: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m`; exit code 0, two packages passed; other packages reported no tests.
- Fork: `$env:CGO_ENABLED='0'; go vet ./binary/...`; exit code 0.
- Fork: `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 30s -parallel 1`; exit code 0, 62,322 executions. Fuzzing is not proof of a resource ceiling or compatibility.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m`; exit code 0, one package passed. No real account was used.
- The prototype closes the originating socket on ordinary queue overflow and logs a possible gap, waits for the direct handler and frame producer before a new connection on the same client, adds a history failure event and a process-wide history admission slot, and bounds decoder structure counts. It does not yet report overflow to the application as a typed event, account for receive queue bytes, or track detached children of handlers. Completion of the direct handler is not proof of generation quiescence; distinct clients may overlap work.
- History storage helpers may log failures without returning them; the history failure event therefore does not prove complete failure reporting. The history limits, resource trade-offs and compatibility have not been approved. F09 and the constitution gate remain open; the tests above do not establish feature 005 acceptance or real-account compatibility.

## Decoder Allocation Measurement

- Date: 2026-11-10, native Windows amd64; local fork has uncommitted changes on top of `be96244`.
- Command in the fork: `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -bench '^BenchmarkUnmarshalWideNode$' -benchtime=3x -count=1`; exit code 0.
- Three iterations of a synthetic 60,000-child node with 180,006 input bytes reported 17,751,504 allocated bytes/op and 120,033 allocations/op. This is cumulative allocation, not peak heap or RSS; it is not a compatibility corpus and does not establish a safe deployment limit.

## Receive Prototype Continuation

- Date: 2026-11-10, native Windows; local fork contains uncommitted changes on top of `be96244`.
- Fork: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m`; exit code 0, two packages passed; remaining packages had no tests.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m`; exit code 0, one package passed. No real account was used.
- The fork exposes `LatestReceiveQueueOverload` and provisionally caps queued decoded-frame sizes at 64 MiB per connection. The counter excludes active handlers, decoded-object amplification and detached work; this is not a heap limit. The main session adapter does not yet consume the typed status.
- Synchronous history storage failures now return `ErrHistorySyncStorageIncomplete` and asynchronous or automatic failures emit `HistorySyncError`. History writes are not atomic; some malformed or unmapped entries remain best-effort, so a successful return is not proof of complete history.
- A direct-handler/producer completion barrier still does not join detached child work, and cross-client isolation is not established. F09 remains open and no limit is approved for deployment. Race detection and native non-Windows platforms were not tested.

## Receive Accounting And Limit Recheck

- Date: 2026-11-10, native Windows amd64; local fork contains uncommitted changes on top of `be96244`.
- Fork: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m`; exit code 0, two packages passed; remaining packages reported no tests.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m`; exit code 0, one package passed. No real account was used.
- The fork accounts for an active handler until it completes and closes the affected connection on receive frame or decoder resource-limit failures. Synthetic tests exercise accounting and limit failure handling. These counters are not a heap or RSS bound; detached work and cross-client lifecycle safety remain unproved.
- History protobuf amplification, receipt and deletion ordering, retirement integration, application failure reporting and compatibility limits remain unresolved. The local replacement is mutable, its limits are not approved for deployment, and F09 and the feature 005 constitution gate remain open.

## Fork History Structure And Reconnect Recheck

- Date: 2026-11-10, native Windows amd64; local fork contains uncommitted changes on top of `be96244`.
- Fork: `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m`; exit code 0, two packages passed; remaining packages reported no tests.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m`; exit code 0, one package passed.
- Fork: `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 30s -parallel 1`; exit code 0, 37,050 executions. The count stopped advancing during the run; this result is not a liveness or resource-bound proof.
- Before these final changes, fork `go vet ./...` and five-target `go build ./...` passed with `CGO_ENABLED=0`. These results do not verify the subsequent edits.
- The prototype now scans history protobuf structure before unmarshalling and storage, retries history settings persistence after failed saves, and waits for the prior receive handler before the fork's automatic 515 reconnect. The project disables that automatic reconnect. Synthetic tests do not establish real-server compatibility or a peak heap ceiling.
- Automatic-history notification admission remains unbounded in retained bytes and can block on a full queue. Its handler lacks the originating socket generation, so safely closing the correct connection on overload needs additional lifecycle design. Application failure propagation, history receipt/deletion and generation drain remain unresolved. Numeric limits remain provisional and unapproved; F09 and the constitution gate remain open. No real account was used.

## Fork Lifecycle And Store Recheck

- Date: 2026-11-10, native Windows amd64; local fork has uncommitted changes on top of `be96244`.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/store -count=1 -timeout 30m`; exit code 1. `TestConcurrentWritersAndReaders` reported 10 deadline errors and 1,590 of 1,600 messages; `TestPerContactMemory/even` reported 54.3406 ms against a 50 ms bound. Other subtests are not individually counted here.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/store -run '^TestConcurrentWritersAndReaders$' -count=1 -timeout 5m`; exit code 1. The isolated rerun reported one deadline error and 1,599 of 1,600 messages. The cause and any relationship to the fork are not established.
- Fork: a focused synthetic lifecycle and history test passed after the socket-lock and reconnect changes; these changes do not prove that detached history workers drain or that automatic notification admission is bounded. An independent source review identified a possible five-second self-wait when an event handler calls `Disconnect`, and automatic reconnect may treat a busy prior handler as an ordinary connection failure. These remain open.
- A bounded preflight differential fuzz run passed for 49,771 executions with `CGO_ENABLED=0`; it does not establish compatibility of provisional limits or an RSS bound. No real account was used.
- Fork: `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 30s -parallel 1`; exit code 0, 54,655 executions. Fork decoder cross-builds for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 also passed with `CGO_ENABLED=0`; these are builds, not native runtime tests.
- Project: `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m`; exit code 0, one package passed against a local fork snapshot. This does not establish history integration or feature acceptance.
- After generation-owned automatic history admission changes, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` and `go vet ./...` each exited 0; two fork packages had tests. The full fork cross-built for all five targets with `CGO_ENABLED=0`.
- Project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` then exited 1: `TestSimulatorStopDuringQueuedAppStateFetches` reported no refused read and a 5,129 ms quiet stop. A fork worker also reported one pass and two failures of the same assertion on earlier runs. The cause was under investigation; this is not a verified quiet-stop integration.
- Project `$env:CGO_ENABLED='0'; $env:WA_APPSTATE_STOP_RUNS='3'; go test ./internal/wa -run '^TestSimulatorStopDuringQueuedAppStateFetches$' -count=1 -v -timeout 3m` exited 1: two of three runs did not observe a refused read, although all three quiet checks passed. Earlier completion before the barrier is a source-supported explanation, not directly measured; the test records failed calls only. Restoring a lock-held handler wait to force the interleaving would risk deadlock.
- Independent source review of the prototype identified possible lost queued nodes at shutdown, premature history receipt/deletion, a transient-only history failure signal and callback self-wait. Those findings require fixes and retesting; passing unit tests do not clear them.
- After receipt-flow changes, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` and `go vet ./...` exited 0. Automatic history now waits for enclosing message-handler success before history receipt/deletion and retains a sanitized current-generation failure category; independent re-review is required. Cleanup after an acknowledged receipt is best effort, not evidence of replay.
- Re-review found that another encrypted child in the same stanza could still fail after a history receipt and deletion, and retirement could race those operations. Those findings remain open until corrected and verified. A subsequent project `go test ./internal/wa -count=1 -timeout 30m` exited 1 on the same queued-app-state assertion, with a 5,103 ms quiet stop.
- The fork now exposes a generation-tagged possible-gap signal for admitted receive nodes discarded on cancellation; focused synthetic tests passed 20 repetitions. The application does not yet consume that signal, and it is not proof of quiet or replay. A concurrent full fork run failed automatic-history tests while that code was being edited; a final non-concurrent run is required.
- The fork worker subsequently reports stanza-scoped history completion and cancellation-bound network operations, tested with synthetic multiple-child handling and cancellation, not a fully encrypted multi-child simulator stanza. Multiple history notifications in one stanza are provisionally rejected; compatibility is unverified. Independent review and final non-concurrent tests remain necessary.
- A further static review found a possible early acknowledgement when decryption of an earlier child fails before a later history child is recognized, and a decoded node discarded on cancellation before admission is not covered by the admitted-node discard signal. Both require correction and verification. The application still does not consume discard or history-failure signals, and a synchronous history callback can wait on its own retirement for up to five seconds.
- The fork discard signal was extended to decoded nodes dropped on cancellation before admission; focused synthetic tests passed 20 repetitions. This remains a possible-gap signal and does not by itself change the application's quiet-stop decision.
- After the multi-child acknowledgement and decoded-node discard changes, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` and `go vet ./...` each exited 0; two packages had tests. Synthetic multi-child decryption tests exercise failure and panic paths without a socket, not a real encrypted multi-child exchange. Single-child failure behavior is retained; failed multi-child stanzas are not acknowledged and replay is not established.
- Fork decoder fuzz: `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 2m -parallel 1`; exit code 0, 963,626 executions. Periods with no advancing counter occurred; this is not a liveness or memory-bound proof.

## Fork Decoder And Cancellation Recheck

- Date: 2026-11-11 UTC, native Windows amd64, Go 1.27.3; uncommitted local fork on `be96244`.
- Fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` and `$env:CGO_ENABLED='0'; go vet ./...` each exited 0; two packages ran tests. Full fork `go build ./...` passed for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 with CGO disabled. Cross-builds are not native platform tests.
- Project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0, one package. Earlier intermittent `TestSimulatorStopDuringQueuedAppStateFetches` failures and store-test failures remain unresolved; one pass does not clear them.
- Fork `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 30s -parallel 1` exited 0, 276,008 executions. The synthetic wide-node benchmark, three iterations on 180,006 input bytes, reported 17,749,805 B/op; this is cumulative allocation, not peak heap or RSS.
- Added a provisional logical decoder work budget, not a proven memory or RSS bound, plus synthetic independent charge-boundary and redaction tests. Compatibility of its threshold is unverified. History decompression and preflight cooperate with cancellation; synchronous storage and network completion report observed cancellation rather than false success. In-flight unmarshalling and storage cannot be interrupted by these checks and completed remote operations cannot be undone.
- Independent review found no remaining concrete high-severity issue in the revised cancellation handoffs. Subsequent channel-controlled synthetic tests covered cancellation during a successful receipt and cancellation of either context during a fake in-flight HTTP request, passing 20 repetitions with CGO disabled; these do not prove real network behavior or a hard deadline. The callback `Disconnect` self-wait, application loss-signal integration and feature 005 resource gate remain open. No real account was used.

## Configured-Path Contract Recheck

- Date: 2026-11-11 UTC, native Windows; fork has uncommitted changes on `be96244`. Fork `$env:CGO_ENABLED='0'; go test ./binary -count=1` exited 0, one package passed. This does not measure peak memory or establish compatibility.
- Fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0; two packages ran tests, the remaining packages reported no tests. This verifies the current synthetic suite, not the acceptance contract or a release revision.
- Project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0, one package passed. Earlier intermittent failures remain unresolved; this pass is not proof of quiet-stop safety.
- Fork `$env:CGO_ENABLED='0'; go test . -run 'TestDisconnectDoesNotHoldSocketLockWhileWaiting|TestReconnect515RejectsStaleAndStoppedGeneration' -count=1 -timeout 2m` exited 0, one package passed. These tests do not reproduce callback-initiated disconnect.
- Source review confirms that synchronous `Disconnect` from a receive callback can wait for its own handler until the five-second timeout. The API cannot distinguish that caller from an external caller waiting for the same callback; skipping the wait whenever a callback is active would weaken external shutdown. No fix or regression test was added. The configured application must use an external retirement coordinator rather than assume that a callback can synchronously join itself; its full shutdown behavior remains to be verified.
- The plan now records a configured-path fork acceptance contract. Existing local modifications were retained, and the receive-resource gate remains failed. No real-account operation or release pin was approved.
- Source inspection identified detached receive descendants reachable under manual history and disabled auto-reconnect, including contact updates, acknowledgements, retries, manual-history receipts, app-state and notifications. They are not covered by the direct handler's queue reservation. Their resource and lifetime bounds remain unverified; automatic-history hardening alone cannot close this gap.
- Subsequent fork decoder checks: `$env:CGO_ENABLED='0'; go vet ./binary/...` exited 0; `$env:CGO_ENABLED='0'; go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 30s -parallel 1` exited 0 after 122,232 executions. The execution counter stopped advancing near the end; this is not evidence of a liveness or peak-memory bound.

## Message Name Work Recheck

- Date: 2026-11-11 UTC, native Windows; local fork has uncommitted changes on `be96244`. Message-triggered push-name and business-name updates now run in the direct receive handler instead of detached goroutines. This delays message processing and acknowledgement if a name update or its callback blocks; the existing queue can eventually overload. The application currently ignores name-change events.
- Synthetic tests exercise blocked push and business updates, queue ordering, and IQ response routing while a name update blocks. Fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0; two packages ran tests. Fork `$env:CGO_ENABLED='0'; go vet ./...` exited 0. Project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0, one package passed.
- These checks do not prove a memory ceiling, real-server compatibility, or complete retirement safety. Other receive-originated detached work remains unbounded or unverified; application loss-signal integration and the feature 005 constitution gate remain open. No real account was used.

## Fork Prerequisite Continuation

- Date: 2026-11-11 UTC, native Windows; local fork has uncommitted changes on `be96244`. During receipt and socket investigation, project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 1: `TestSimulatorStopSignalLetsTheHeldPairingSaveFinish` found zero identity rows instead of one. Its cause and relationship to fork changes are under investigation; it is not cleared by a passing rerun.
- A focused `$env:CGO_ENABLED='0'; go test ./internal/wa -run '^TestSimulatorStopDuringQueuedAppStateFetches$' -count=1 -timeout 3m` first exited 0, then exited 1 while fork edits were in progress because no post-barrier read was refused. The earlier intermittent interleaving failure remains unresolved; neither run establishes behavior of a final fork snapshot.
- A subsequent project `$env:CGO_ENABLED='0'; go test ./internal/wa -run '^TestSimulatorStopSignalLetsTheHeldPairingSaveFinish$' -count=1 -timeout 3m` exited 0. The full-suite failure remains unexplained; this isolated pass does not establish that it is resolved.
- Project `$env:CGO_ENABLED='0'; go test ./internal/wa -run '^TestSimulatorStopSignalLetsTheHeldPairingSaveFinish$' -count=5 -timeout 3m` exited 0. Five isolated passes do not explain the earlier full-suite failure.
- An independent investigation found no direct receipt-code path in that pairing-only scenario, but the mutable fork's shared queue and shutdown changes can affect it. The test waits for a 515 log and does not directly prove the pairing save has entered the guarded store before retirement; this is a plausible race, not a demonstrated cause. On Windows with `CGO_ENABLED=0`, an investigator reported the focused test passed 20 runs, passed 50 runs with `GOMAXPROCS=1`, and the complete session package passed once. The failure remains unexplained.
- On the current fork snapshot, project `$env:CGO_ENABLED='0'; $env:WA_APPSTATE_STOP_RUNS='3'; go test ./internal/wa -run '^TestSimulatorStopDuringQueuedAppStateFetches$' -count=1 -v -timeout 3m` exited 1: two of three runs observed no refused read after the barrier, although all three quiet checks passed. This does not establish whether the intended interleaving occurred or the underlying safety invariant holds.
- Source review found the test observes queued fetches before stop but does not synchronize their store reads with barrier retirement. Reads may finish before the barrier, but the test records failed calls only, so that explanation was not observed directly. A test-only gate before a guarded read and release after barrier retirement is being investigated to prove the intended ordering without changing production shutdown behavior.
- Fork `$env:CGO_ENABLED='0'; $env:WA_DECODER_HEAP_MEASURE='1'; go test ./binary -run '^TestDecoderHeapMeasurement$' -count=1 -v -timeout 2m` exited 0. A 180,006-byte wide node produced a sampled post-GC process heap delta of 8,545,128 bytes; a 180,306-byte deep-wide node produced 7,905,856 bytes. Forced collections perturb execution and sampling may miss peaks; these are not peak RSS, a worst-case ceiling or compatibility evidence.
- On the current fork snapshot, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` and `go vet ./...` each exited 0; three packages ran tests. `$env:CGO_ENABLED='0'; go test ./socket -count=10 -timeout 3m` exited 0. Fork `go build ./...` exited 0 for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 with CGO disabled. Cross-builds are not native runtime tests. Project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` subsequently exited 0; prior failures remain unresolved.
- The prototype now runs manual-history and manual-mode peer protocol receipts within the direct handler, and performs serialized socket writes without a detached per-send worker. Send cancellation during a write can close that connection; a returned cancellation does not prove the peer did not receive the frame. Synthetic tests cover blocked receipt-handler completion and response routing, socket send ordering and cancellation, not real-server receipt behavior. Other detached receive work and aggregate resource bounds remain open.
- Independent review noted synchronous manual receipts can hold the serialized receive queue behind a slow write, and the receipt-specific test blocks a failed-send logger rather than an actual successful write. Socket tests exercise a blocked write separately, not the complete receipt-to-transport integration. Review also identified a possible delay when a failed send closes a socket concurrently with graceful close; correction and interleaving verification are in progress. A separate handshake/disconnect-handler registration race appears pre-existing and remains unverified.
- The fork was subsequently changed so a failed send marks its Noise socket unusable before scheduling connection-specific cleanup instead of waiting behind a graceful close. Synthetic tests cover a blocked graceful close, cancellation while the close lock is held, and stale cleanup not closing a different connection. The cleanup goroutine itself can wait for an in-progress close; its full retirement accounting remains unverified. An investigator reported fork `$env:CGO_ENABLED='0'; go test ./socket -count=10 -timeout 120s` and `go vet ./socket` exited 0 after this change; full-snapshot verification is still needed.
- The queued-app-state test was changed to gate a guarded read until the `barrier_retired` trace, retaining its refused-read, write and quiet assertions. An investigator reported `$env:CGO_ENABLED='0'; $env:WA_APPSTATE_STOP_RUNS='3'; go test ./internal/wa -run '^TestSimulatorStopDuringQueuedAppStateFetches$' -count=1 -timeout 5m -v` exited 0 with three passes; fork socket edits overlapped that snapshot, so a final rerun is needed.
- After the socket changes, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` and `go vet ./...` each exited 0; three packages ran tests. Fork `go build ./...` passed all five target cross-builds with CGO disabled. Project `$env:CGO_ENABLED='0'; $env:WA_APPSTATE_STOP_RUNS='3'; go test ./internal/wa -run '^TestSimulatorStopDuringQueuedAppStateFetches$' -count=1 -v -timeout 5m` exited 0, three runs; `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0, one package. Cross-builds are not native runtime evidence, and the earlier held-pairing-save failure remains unexplained.
- Review of that socket snapshot found oversized sends could consume a Noise counter and unnecessarily close the connection. A subsequent correction checks length before consuming the counter; the socket regression verifies a later valid send succeeds. After this correction, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0, three packages with tests, and project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0, one package. Five cross-builds above preceded this last correction.
- Further source review identified a potentially stale read-pump close that could close a replacement connection on the same `FrameSocket`, as well as a disconnect-handler registration race during handshake. Both appear pre-existing and remain unverified in the configured lifecycle; neither is evidence that generation closure is safe. Review also noted the fork's changed `FrameSocket` disconnect API affects external consumers; application configuration rather than general fork compatibility is the 005 scope.
- Project `go mod verify` timed out after 120 seconds during this investigation; it is not recorded as passed for this run. No real account was used.
- A later project `$env:CGO_ENABLED='0'; go mod verify` exited 0 and printed `all modules verified`; the earlier timeout remains recorded above.

## Direct Receive Weighted Accounting

- Date: 2026-11-11 UTC, native Windows; local fork has uncommitted changes. Fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0, three packages ran tests; `$env:CGO_ENABLED='0'; go vet ./binary/...` exited 0.
- Direct queue admission now charges unpacked input length plus provisional decoder work for queued and active nodes. Synthetic tests cover compact amplified nodes, exact saturation, response routing, overload and release. Subtraction checks precede charge addition. These weighted units are not heap bytes and do not establish a memory ceiling, real-traffic compatibility, detached-work bounds or feature acceptance. No real account was used.
- Diagnostic correction, 2026-11-11 UTC: `FrameBytes` means notification protobuf size for automatic-history queue overload, rather than received frame length. Fork `$env:CGO_ENABLED='0'; go test ./binary -count=1` and `$env:CGO_ENABLED='0'; go test . -run 'TestAutomaticHistoryRejectionDoesNotAcknowledge|TestReceiveResourceLimitsCloseConnection|TestReceiveQueueWeightedAmplification' -count=1 -timeout 2m` each exited 0, one package per command. Synthetic tests assert this diagnostic and explicit decoder baseline results; they do not establish real-traffic compatibility.

## Account Sync Queue Accounting

- Date: 2026-11-11, native Windows; mutable local fork with uncommitted changes. With `CGO_ENABLED=0`, fork `go test . -run '^TestAccountSyncQueueAccountingAndResponseRouting$' -count=50 -timeout 2m` exited 0 (50 repetitions), and `go test ./... -count=1 -timeout 10m` exited 0 (three packages with tests). `gofmt -l notification.go account_sync_queue_test.go` printed nothing.
- In manual-history mode with asynchronous acknowledgements, account-sync handling now stays in the direct queue. A synthetic blocked callback retained active and queued accounting, allowed IQ response routing and an early acknowledgement attempt, and released accounting on cancellation. The disconnected test does not demonstrate acknowledgement delivery or a real privacy IQ exchange. The existing asynchronous acknowledgement worker remains detached; synchronous-ack and automatic-history behavior are unchanged. Blocking a callback or privacy fetch can delay subsequent notifications and trigger overload. This is not a complete receive-resource bound, shutdown proof, compatibility assessment or feature acceptance. No real account was used.

## Configured Retry And Socket Lifecycle

- Date: 2026-11-11 UTC, native Windows, mutable local fork. An investigator reported `CGO_ENABLED=0 go test . -count=1 -timeout 5m` and five repetitions of targeted retry lifecycle tests passed after moving incoming retry handling and its early acknowledgement into the direct queue in manual-history/async-ack mode. Tests simulate response routing rather than exercising an end-to-end prekey IQ. Receipt event timing changes, and a blocked retry can delay the receive queue and retirement.
- An investigator reported `CGO_ENABLED=0 go test ./socket -count=20 -timeout 2m` passed after making stale read-pump cleanup close only its captured connection. Reuse of a `FrameSocket` while an old pump processes data is not established as safe; normal same-client reconnects construct a new `FrameSocket`.
- After serializing handshake disconnect-handler registration with close, the investigator reported `CGO_ENABLED=0 go test ./socket -count=30 -timeout 3m`, `go vet ./socket`, and `go test ./ -run 'Test.*(Generation|Disconnect|Reconnect|Handshake)' -count=1 -timeout 3m` passed. Socket-level tests cover close before and after registration, not an end-to-end handshake with a real server. Combined-snapshot verification remains necessary.
- The missing app-state key request still launches detached work. A synthetic lock test demonstrated that cancellation does not release a request waiting on `messageSendLock`; the investigator reported ten repetitions passed. A synchronous replacement was not made because it can hold the app-state lock while waiting on that lock or a peer acknowledgement. Cancellation and lifecycle treatment remain open.
- Subsequent fork changes make message-send serialization cancellation-aware using a shared semaphore. An investigator reported `CGO_ENABLED=0 go test ./... -count=1 -timeout 5m` in the fork and `go test ./... -count=1 -timeout 30m` in the project exited 0, but concurrent edits may have overlapped the snapshot. Synthetic tests cover cancellation while the lock is held and serialization between send paths; they do not establish a successful end-to-end send or bound missing-key request lifetime. `appstate.go` still uses `context.WithoutCancel` for that detached request.
- Response cancellation now removes or closes a waiter only when the registration still refers to that channel. An investigator reported `CGO_ENABLED=0 go test ./ -run 'Test(CancelResponseOwnership|CancelResponseAfterClear|ConcurrentResponseCancellation)$' -count=10 -timeout 2m` exited 0, including 100,000 concurrent cancellation/delivery iterations. The race detector was not run with CGO disabled. Timeout cleanup and a bound on delivered or retained responses need separate evidence.
- Timeout and cancellation branches in synchronous IQ and retry requests, and the keepalive caller, now clean up their identity-held waiter. An investigator reported `CGO_ENABLED=0 go test ./ -run 'Test(RequestWaiter|CancelResponse|ConcurrentResponseCancellation)' -count=10 -timeout 2m` and `go vet ./` exited 0. The first test repetition attempt failed due to a fixture cancellation racing an actual socket write; the fixture was synchronized past the send gate before the passing run. Generic async retention remains unchanged; no aggregate response-memory bound is claimed.
- `retryFrame` now observes its context while waiting for a connection. An investigator reported focused retry and request-lifecycle tests passed with `CGO_ENABLED=0`; independent source review found no concrete regression but identified missing simultaneous cancellation/reconnect and socket-lock contention cases. Public `WaitForConnection` remains unchanged.
- Missing-key requests now release their own matching 24-hour throttle reservation only when message-send admission was cancelled before the send lock was acquired. An investigator reported focused app-state, send-lock and socket cancellation tests passed with `CGO_ENABLED=0`. Ambiguous outcomes after a write may have been attempted retain the reservation. The detached missing-key request and wider lifecycle remain open.
- Date: 2026-11-11 UTC, native Windows. With manual history and asynchronous acknowledgements, malformed-receipt NACKs and ordinary early ACKs now run within the direct handler; ACK timing remains before the handler body, rather than using the separate after-handler `SynchronousAck` mode. An investigator reported connected loopback and lifecycle tests passed 20 repetitions with `CGO_ENABLED=0`, and `go vet .` passed. Handler delays, notification descendants and other detached paths remain open.
- On the integrated mutable fork snapshot after those edits, `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0, with three packages running tests; project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0, one package. The fork also avoids launching the no-op delayed phone rerequest worker when that feature is disabled. These synthetic passes do not establish the aggregate receive bound or real-server compatibility.
- On that snapshot, project `$env:CGO_ENABLED='0'; $env:WA_APPSTATE_STOP_RUNS='3'; go test ./internal/wa -run '^TestSimulatorStopDuringQueuedAppStateFetches$' -count=1 -timeout 5m -v` exited 0, with three passing runs; fork `$env:CGO_ENABLED='0'; go vet ./...` exited 0. These runs do not explain the previously recorded held-pairing-save failure or verify native non-Windows platforms.
- Source review of a possible process-wide pre-decode admission limit found that rejecting a still-unclassified frame can also discard `stream:error`, whose logout, replacement and disconnect handling must remain observable. No pre-decode limit was added on that basis. A bounded strategy must demonstrate both aggregate allocation control and required terminal-control handling; a candidate that waits only for CPU-bound decoding is under analysis, not approved or verified.
- Source review of cumulative app-state work found unbounded `has_more` iteration and accumulated derived events. A page-count-only guard would not prove retained bytes; externally invoked repairs also lack explicit originating-generation provenance for a safe fail-closed signal. No partial limit was added or treated as a solution.
- The fork's latest overload report now uses generation-ordered atomic replacement, so a later report from an older generation cannot overwrite a newer report. On native Windows, fork `$env:CGO_ENABLED='0'; go test . -run '^TestReceiveQueue(OverloadGenerationOrdering|DiscardHandledAndGenerationOrdering)$' -count=20 -timeout 2m` exited 0. This checks status ordering, not complete application consumption of the signal or a proof of generation-safe retirement.
- On the later mutable snapshot including that change and the synthetic app-state measurement, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0 with three packages running tests; project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0. No release pin or compatibility acceptance is implied.
- Project `$env:CGO_ENABLED='0'; go test ./internal/wa -run 'Test(KeyRequest|KeyRequests|.*Repair)' -count=1 -timeout 5m` exited 0 while the app-state preflight prototype was being edited. These synthetic tests do not prove actual encrypted multi-child key-share delivery or resolve the missing-key retry-budget liveness issue.
- Fork `$env:CGO_ENABLED='0'; go test . -run '^TestManualMultiSignalFailedChildBeforeKeyShare$' -count=5 -timeout 2m` exited 0. The synthetic test uses real Signal encryption: with manual history and asynchronous ACKs, a malformed first encrypted child prevented a valid later key share from being processed and observed no outgoing acknowledgement, NACK or retry attempt. A single-child control decrypted and stored the key. A separate duplicate-plus-fresh case with synchronous ACKs processed the fresh child but observed no final outgoing node. The test invokes the decrypt handler directly and captures locally attempted sends; it does not prove server delivery, replay or real-account loss. The multi-child compatibility and repair-liveness issue remains open.
- After that fixture was added, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0 with four packages running tests. This is a mutable working-tree snapshot, not a release revision or proof that the issue is safe.

## App-State Event Retention Measurement

- Date: 2026-11-11 UTC, native Windows amd64; mutable local fork with uncommitted changes. Fork: `$env:CGO_ENABLED='0'; $env:WA_APPSTATE_HEAP_MEASURE='1'; go test . -run '^TestAppStateEventHeapMeasurement$' -count=1 -v -timeout 5m` exited 0, one test. Without the opt-in variable, the same test exited 0 and skipped.
- The test calls the actual event collector directly with eight synthetic pages of 10,000 decoded SET pin mutations each, retaining its event slice. After forced GC, process-wide Go `HeapAlloc` was 1,747,832 bytes at baseline, 13,212,560 after one page and 93,644,656 after eight pages (160,000 events); cumulative `TotalAlloc` increase after eight pages was 116,741,080 bytes. Values include other live process allocations; the fixture creates independent index strings and action objects. Forced GC alters execution and these samples are not peak heap, RSS, or an allocation attribution profile.
- The fetch loop's source accumulates events across `has_more` iterations, but this fixture does not exercise that loop, network responses, cryptographic validation, protobuf decoding or real traffic. It does not establish wire-size amplification, production compatibility or any memory ceiling. No real account was used.

## App-State Protobuf Preflight

- Date: 2026-11-11 UTC, native Windows, mutable local fork. A provisional schema-aware preflight now checks input length, field and message counts, nesting and cancellation before individual app-state protobuf decodes. An investigator reported `$env:CGO_ENABLED='0'; go test ./appstate -count=1` and `go vet ./appstate` exited 0; a later `$env:CGO_ENABLED='0'; go test ./appstate -count=1 -timeout 2m` also exited 0, one package.
- These are per-decode limits, not an aggregate bound across patches, external blobs, `has_more` pages or decoded events. They do not bound quadratic downstream mutation processing or interrupt `proto.Unmarshal`. The configured application bounds each HTTP response to 16 MiB; the fork's generic app-state downloader does not independently enforce that limit before reading. Synthetic tests are not representative-traffic compatibility evidence, and the limits are not owner-approved.
- Independent source review identified short mutation values that could panic in downstream hash/MAC processing. The fork now returns errors for insufficient MAC or IV bytes; an investigator reported `CGO_ENABLED=0 go test ./appstate -count=1 -timeout 5m` and `go vet ./appstate` passed, including malformed SET/REMOVE, nil and boundary cases and a valid authenticated mutation. Aggregate processing bounds are not established. No real account was used.
- The fork now indexes earlier mutation indices during patch validation instead of repeatedly scanning them backwards. An investigator reported `CGO_ENABLED=0 go test ./appstate -count=1 -timeout 5m` and `go vet ./appstate` passed; tests compare 1,000 seeded mixed sequences against the earlier lookup, including store errors and malformed input, plus 30,000 distinct indices. The index uses a Go map and additional memory proportional to distinct indices; unlike the earlier `hmac.Equal` scan, map lookup is not constant-time. Aggregate allocation and timing implications remain to be assessed.
- Project `$env:CGO_ENABLED='0'; go vet ./...` exited 0 while the fork's app-state validation changes were in progress; rerun on the completed snapshot is required.
- After the preflight, short-value checks and indexed lookup edits, fork `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 10m` exited 0 with four packages running tests, and project `$env:CGO_ENABLED='0'; go test ./internal/wa -count=1 -timeout 30m` exited 0 with one package. These passes do not prove cumulative app-state bounds or real-server compatibility.
- Project `$env:CGO_ENABLED='0'; go test ./... -count=1 -timeout 30m` exited 0 with nine packages passing on native Windows; this ran against the mutable local fork while other synthetic test work was in progress. It does not establish a fixed release snapshot, aggregate resource safety, or real-server behavior.

## Rebuilt Fork Branch

- Date: 2026-11-12, native Windows amd64, Go 1.27.3, `CGO_ENABLED=0`. Fork branch `receive-bounds` in `../whatsmeow-receive-bounds`, clean at commit `6cabab6` (six commits on `be96244`; local, not pushed). The earlier sections of this file describe the superseded prototype in `../whatsmeow`, not this branch.
- Fork: `go vet ./...` exited 0. `go test ./... -count=5` exited 0; three packages have tests (root, `appstate`, `binary`). `go build ./...` exited 0 for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64; cross-builds are not native runs.
- Fork: `go test ./binary -run '^$' -fuzz '^FuzzUnmarshal$' -fuzztime 20s -parallel 1` exited 0 with 158,097 executions. Fuzzing is not proof of a resource ceiling.
- Fork: with the request-waiter fix removed, `go test . -run 'Request|CancelResponse|ConcurrentResponse' -count=1` failed four tests, one with `close of closed channel`; with it, the same selection passed 10 repetitions.
- Project against that branch, without changing `go.mod`: a copy of `go.mod` and `go.sum` with the replacement pointing at `D:/projects-mou/whatsmeow-receive-bounds` was passed with `-modfile`. `go vet -modfile=<copy> ./...` exited 0. `go test -modfile=<copy> ./... -count=1 -timeout 30m` exited 0 with nine packages passing, including `internal/wa` (38 s), `internal/store` (225 s) and `cmd/whatsapp-mcp` (126 s). The working tree held the uncommitted `internal/wa/appstate_stop_test.go` change made during the prototype work.
- Synthetic fork tests cover: a full handler queue closing the connection once, starting no goroutines and leaving a record; a response delivered while the queue is full; a stream error handled at a full queue; an oversized frame and a frame over the node limit closing the connection; the record keeping the newest connection; waiter cleanup; short app-state values; a panicking event handler; the retry counter cap.
- Not established: the race detector was not run (it needs cgo, and no C compiler is installed); nothing ran natively on Linux or macOS; no limit was checked against real traffic; one passing project run does not explain the intermittent failures recorded above for the prototype. The application does not yet read the overload record. No real account was used. The dependency revision and its limits are not approved, and the constitution gate has not been re-evaluated.
- Later on 2026-11-12 a seventh commit, `a654373`, limits a history sync to 128 MiB after decompression, following a review finding that history contains messages written by other accounts. At `a654373`: fork `go vet ./...` exited 0, `go test ./... -count=3` exited 0 (three packages with tests), and `go build ./...` exited 0 for the five targets. Project `go test -modfile=<copy> ./internal/wa -count=1 -timeout 9m` exited 0, one package; the other eight project packages were not rerun at this commit. The new test expands about 130 KiB of compressed zeros past the limit and decodes a small valid blob; it does not exercise a download, and 128 MiB has not been compared with real history.
- An eighth commit, `9209108`, runs the push-name and business-name updates inside the message handler instead of in a goroutine per message. At `9209108`: fork `go vet .` exited 0; `go test . -run MessageName -count=10` exited 0 and the same test failed with the change removed ("handler returned while name update was blocked"); `go test ./... -count=3` exited 0 (three packages with tests); `go build ./...` exited 0 for the five targets. Project `go test -modfile=<copy> ./internal/wa ./cmd/... -count=1 -timeout 9m` exited 0, two packages; the other seven project packages were not rerun at this commit. A slow device store now delays the next message; that was not measured.
- `SynchronousAck` was not set or tested in the project. The plan lists it as a configuration the design must decide and test.

## Owner Decisions 2026-11-13 (planning)

Recorded during `/speckit-plan`, answered by the owner (`tiger-seo`):

- **Fork revision approved and pinned.** Branch `receive-bounds` at `92091085667b8cb97c0fb4fa5990c6ea54f54343`, with
  its limits: 16 MiB per frame after expansion, 64 nesting levels, 65,536 nodes per frame, 2,048 queued handler items,
  4,096 retry counters, 128 MiB per history sync after decompression. None was compared with real traffic. The branch
  was pushed to `github.com/tiger-seo/whatsmeow` (new branch, no other branch changed), and `go.mod` replaces
  `go.mau.fi/whatsmeow` with `github.com/tiger-seo/whatsmeow v0.0.0-20261112064615-92091085667b`; `go.sum` updated by
  `go mod tidy`.
- **Library maps accepted.** `tcTokenSenderTS`, `groupCache`, `userDevicesCache`, `sessionRecreateHistory` grow per
  distinct account or group; accepted, tracked in `docs/backlog/005-whatsmeow-per-account-maps.md`.
- **E003-RX-01 retired** by the pin; `docs/backlog/003-whatsmeow-receive-resource-bounds.md` closed.
- **Race-detector run in WSL2 with cgo allowed** for that run only. The owner installed gcc 15.2.0 in the Ubuntu VM
  (`apt-get install gcc libc6-dev`, which also upgraded libc6 from 2.43-2ubuntu2.4 to 2.43-2ubuntu2.7); the
  session's own attempt to install it as root was blocked by its permission classifier. Results below.

## Pinned Fork Gates

- Date: 2026-11-13, native Windows amd64, Go 1.27.4, `CGO_ENABLED=0`, project at `3df3cd6` plus the uncommitted pin,
  planning documents and the `internal/wa/appstate_stop_test.go` gate change. Each exit code read from the command.
- `gofmt -l .`: printed nothing, exit 0. `go vet ./...`: exit 0. `go vet ./internal/wa/testdata/cmd/session-check`:
  exit 0. `go mod verify`: `all modules verified`, exit 0.
- `go build -o /dev/null ./cmd/whatsapp-mcp` for windows/amd64, linux/amd64, linux/arm64, darwin/amd64,
  darwin/arm64: each exit 0.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`: `No vulnerabilities found.`, exit 0.
- `go test ./... -count=1 -timeout 30m`: exit 0, 9 packages ok (`cmd/whatsapp-mcp` 134.8 s, `internal/store` 253.8 s,
  `internal/wa` 44.8 s).
- Not run: the session-check helper's test and build, the pass-rate measurement with
  `SynchronousAck` (not yet implemented), native Linux or macOS. No real account was used.
- `TestQuietAllowListPin` compares with the `require` line, which still names upstream `f1b798561919`; the allow-list
  is empty, so the check passes without testing the replacement (research R15).

## Race Detector (WSL2)

- Date: 2026-11-13, WSL2 Ubuntu (not native Linux), gcc 15.2.0, `CGO_ENABLED=1` for these runs only,
  `TMPDIR=/var/tmp`, `MSYS_NO_PATHCONV=1` on the Windows side. A first attempt without `MSYS_NO_PATHCONV` never ran:
  Git Bash rewrote the `--cd /mnt/d/...` argument and WSL exited 127 with `ERROR_PATH_NOT_FOUND`.
- Fork at `9209108`, Go 1.27.3: `go test -race ./... -count=1 -timeout 20m` exited 0; `go.mau.fi/whatsmeow` (26.0 s),
  `appstate` (1.0 s) and `binary` (7.5 s) ok, no race reported.
- Project at `88a74e5`, Go 1.27.4: `go test -race -p 1 ./internal/wa ./cmd/... -count=1 -timeout 40m` exited 1.
  `internal/wa` ok (61.1 s), no race. `cmd/whatsapp-mcp` failed: `TestShellSavedSessionOutdatedHandoff` reported 4
  data races, all on the test's own `fs` variable, written by the fake session factory on the owner's goroutine and
  read by the polling test goroutine. A test defect from feature 003, not production code or the fork.
- Fix: the test shares the fake session through `atomic.Pointer`. Then `go test -race -p 1 ./cmd/... -count=1
  -timeout 40m` in WSL2 exited 0 (`cmd/whatsapp-mcp` ok, 158.5 s, no race), and on native Windows with
  `CGO_ENABLED=0` `go test ./cmd/whatsapp-mcp -run TestShellSavedSessionOutdatedHandoff -count=3` exited 0.
  `go vet ./cmd/...` exited 0 and `gofmt -l cmd/` printed nothing.
- Not run under the race detector: `internal/store` and the remaining packages (outside the plan's condition, which
  names the fork and the session packages).

## Fork Re-request Gate

- **Finding**: the first design review (`61a63f4`, BLOCK) found that with `SynchronousAck` the first retry receipt for
  an undecryptable message calls `immediateRequestMessageFromPhone` without checking
  `AutomaticMessageRerequestFromPhone`, while the asynchronous path checks it (`retry.go:441` and `:507-509` at
  `9209108`). Confirmed by reading the source on 2026-11-13.
- **Owner decision, 2026-11-13 (`tiger-seo`)**: gate it in the fork, as a new Fork Acceptance Contract item (8) and a
  new pin.
- **Fork commit** `d83e19ef5083f76ca86ffdddd4bcb6511c7e3981` on `receive-bounds` ("retry: respect
  AutomaticMessageRerequestFromPhone with synchronous acks"), pushed to `github.com/tiger-seo/whatsmeow`. It adds
  `phoneRerequestEnabled()`, used by the delayed and the immediate request, and
  `retry_phone_rerequest_test.go`.
- Fork, native Windows, Go 1.27.4, `CGO_ENABLED=0`, each exit code read directly:
  - without the `retry.go` change, `go test . -run TestRetryReceiptDoesNotRerequestFromPhoneUnlessEnabled -count=1`
    exited 1: `syncAck=true` attempted the peer send (nil-pointer panic in `preparePeerMessageNode`, because the test
    client has no session store);
  - with it, `go test . -run 'TestRetryReceiptDoesNotRerequestFromPhoneUnlessEnabled|TestPhoneRerequestEnabled'
    -count=10` exited 0;
  - `gofmt -l .` printed nothing; `go vet ./...` exited 0; `go test ./... -count=3 -timeout 20m` exited 0 (root,
    `appstate`, `binary`); `go build ./...` exited 0 for the five targets.
- **Pin**: `go.mod` replaces with `github.com/tiger-seo/whatsmeow v0.0.0-20261113120547-d83e19ef5083`; `go.sum`
  updated by `go mod tidy`.
- Project gates on that pin, native Windows, `CGO_ENABLED=0`: `gofmt -l .` printed nothing (exit 0); `go vet ./...`
  exit 0; `go mod verify` `all modules verified`, exit 0; the five `go build ./cmd/whatsapp-mcp` targets each exit 0;
  govulncheck v1.9.0 `No vulnerabilities found.`, exit 0; `go test ./... -count=1 -timeout 30m` exit 0, 9 packages ok
  (`cmd/whatsapp-mcp` 133.2 s, `internal/store` 313.6 s, `internal/wa` 44.6 s).
- **Inventory**: every site in the pin that reads `SynchronousAck` is listed in plan.md, "What `SynchronousAck` changes
  in the pin". One more account action found there, the phone request for a server `unavailable` placeholder
  (`message.go:334`), is not gated in either mode and is put to the owner at T004.
- Race detector on the new pin, WSL2 Ubuntu (not native Linux), gcc 15.2.0, `CGO_ENABLED=1` for these runs only,
  `MSYS_NO_PATHCONV=1`: fork at `d83e19e`, `go test -race ./... -count=1 -timeout 20m` exited 0 (root 32.2 s,
  `appstate` 1.1 s, `binary` 10.0 s); project, `go test -race -p 1 ./internal/wa ./cmd/... -count=1 -timeout 40m`
  exited 0 (`internal/wa` 64.5 s, `cmd/whatsapp-mcp` 166.5 s). No `DATA RACE` in either log.

## Fork Placeholder Gate

- **Finding**: the second design review (`1b1d91f`, CONCERNS) found that the `unavailable` placeholder request
  (`message.go:325-336`) is caused by any account, for example by sending a view-once message
  (`UnavailableTypeViewOnce = "view_once"`, `types/events/events.go:292`), and with `SynchronousAck` runs a full send
  on the handler. Confirmed by reading the source on 2026-11-13.
- **Owner decisions, 2026-11-13 (`tiger-seo`)**: gate it in the fork (contract item 9, a new pin); keep the roadmap's
  real-account history check and record the `pair` gap (T056); fix every low finding and run a third design review.
- **Fork commit** `bedcf018524509697b73299a224ee82b5fdeec1c` on `receive-bounds` ("message: respect
  AutomaticMessageRerequestFromPhone for unavailable placeholders"), pushed to `github.com/tiger-seo/whatsmeow`.
  `git rev-list --count f1b7985..bedcf01` is 12.
- Fork, native Windows, Go 1.27.4, `CGO_ENABLED=0`, each exit code read directly:
  - without the `message.go` change, `go test . -run TestUnavailablePlaceholderDoesNotRerequestFromPhoneUnlessEnabled
    -count=1 -v` exited 1: `syncAck=true` failed its assertion (request attempted) and `syncAck=false` panicked in
    `preparePeerMessageNode` from `immediateRequestMessageFromPhone`;
  - with it, the three phone-request tests passed `-count=10`; `gofmt -l .` printed nothing; `go vet ./...` exit 0;
    `go test ./... -count=3 -timeout 20m` exit 0 (root, `appstate`, `binary`); `go build ./...` exit 0 for the five
    targets.
- **Pin**: `go.mod` replaces with `github.com/tiger-seo/whatsmeow v0.0.0-20261113124132-bedcf0185245`; `go.sum`
  updated by `go mod tidy`.
- Project gates on that pin, native Windows, `CGO_ENABLED=0`: `gofmt -l .` printed nothing; `go vet ./...` exit 0;
  `go mod verify` `all modules verified`; the five `go build ./cmd/whatsapp-mcp` targets each exit 0; govulncheck
  v1.9.0 `No vulnerabilities found.`; `go test ./... -count=1 -timeout 30m` exit 0, 9 packages ok (`cmd/whatsapp-mcp`
  131.7 s, `internal/store` 326.9 s, `internal/wa` 43.3 s).
- Race detector, WSL2 Ubuntu (not native Linux), gcc 15.2.0, `CGO_ENABLED=1` for these runs only: fork
  `go test -race ./... -count=1 -timeout 20m` exit 0 (root 31.6 s, `appstate` 1.0 s, `binary` 7.8 s); project
  `go test -race -p 1 ./internal/wa ./cmd/... -count=1 -timeout 40m` exit 0 (`internal/wa` 60.7 s,
  `cmd/whatsapp-mcp` 166.0 s). No `DATA RACE` in the log.

## Owner Decisions 2026-11-13 (after the third design review)

- **Placeholder scope**: the fork gate (`bedcf01`) covers every `unavailable` placeholder type, including the
  ordinary one (type "") that upstream used to recover from the phone. The owner keeps the gate: placeholders are
  counted and reported, not recovered; recorded as a spec clarification; an opt-in request is in the backlog
  (`005-placeholder-phone-request.md`); T056 records the real `unavailable` count.
- **T004 answers** (`tiger-seo`): (1) the proposed application limits in `data-model.md` are accepted as proposed;
  (2) a stop during a history download may leave the marker unsafe, and T047's measured rate, including the
  `pair`-shaped case, returns to the owner before the code review; (3) history downloads keep the media client's
  16 MiB and 10 s per response. No number in `data-model.md` changes. The owner ticks T004.
- **Low findings of the third review** applied in the same batch: the placeholder decision in the spec, R9 no longer
  calls a failed merge terminal, T051 and invariant N6 name `lid_merge_failed` and the `unavailable` count.

## Specification Check (T003)

- Date: 2026-11-13. `docs/specification.md` is present locally. Found: §3.4 (line 131), §3.5 (169), §8.5 (542) with
  S3 (553), S4 (559) and S5 (562), §8.6 (567) with P2 (572) and P3 (573), §8.7 (584) with M7 (633).

## Foundational tests (T015)

- Date: 2026-11-13, Windows 10, `CGO_ENABLED=0`. Command per package: `go test -count=1 -run NONE -gcflags=-e
  ./internal/store/` (and `./internal/wa/`, `./cmd/whatsapp-mcp/`), each exit 1 before any production code.
- Every new test failed to compile for the expected reason: a missing symbol. `internal/store`: `ValidateMessage`,
  `MessageFieldBytes` (T005), `PhoneForLID`, `MappedLIDsWithData` (T006), `ContactNames` (T007),
  `DeviceBarrier.SetMappingObserver` (T008). `internal/wa`: `pinnedWhatsmeow` (T009); `ingest`, `IngestConfig`,
  `newIngest`, the state constants, `IngestCounters`, `ingestWorkerWait` (T010); `Session.ingest`,
  `droppedMessages`, `IngestFailed`, `handleEvent` with no result (T011); `QuietStopResult.DroppedMessages` (T012);
  `Terminal`, `Health.IngestFailed`, `Health.ReceiveOverload`, `SessionReceiveOverload` (T013). `cmd/whatsapp-mcp`:
  `Health.IngestFailed` (T014).
- After T016–T023 the same tests pass. Full suites on the result, 2026-11-13: `go test ./internal/wa/ -count=1
  -timeout 30m` exit 0 (34.3 s); `go test ./cmd/whatsapp-mcp/ -count=1 -timeout 30m` exit 0 (122.0 s); `go test
  ./internal/store/ -count=1 -timeout 30m` exit 0 (259.9 s). `go vet ./...` and `go vet
  ./internal/wa/testdata/cmd/session-check` exit 0; `gofmt -l .` prints nothing.
- Two defects found by the existing suite and fixed before it passed: messages dropped after the retire bit were
  counted twice (session and ingest); and the `ingest_stopped` log was written before the session's diagnostic
  consumer was stopped, so a stalled logger delayed retirement (`TestSessionCodecFailuresDoNotBlockCallback`). The
  log now comes last and only when a count is non-zero.

## App-State Stop Test with SynchronousAck (T024)

- With `SynchronousAck` the library runs each `server_sync` fetch on its event handler, and on `Disconnect` its
  handler loop drops the nodes still queued without handling them (`handlerQueueLoop` in `client.go` of
  `bedcf01`). The feature 004 shape (four fetches queued on `appStateSyncLock` in goroutines) cannot occur.
- New shape: the simulator holds the first app-state request, sends five more `server_sync` notifications, then
  answers the request with an error; the answer is read after the five, so they are in the handler queue. The next
  fetch stops inside its version read at the recording gate, on the handler, with four notifications queued behind
  it; a goroutine dump shows one fetch in progress and none waiting on the library's lock. The gate opens at
  `barrier_retired`, so that read is refused after the barrier. The refused-read, no-refused-write, passed-check and
  clean-marker assertions are unchanged.
- A first attempt held the second request in the simulator's read loop; the library's `Disconnect` then waited
  5 s for a websocket close the stalled simulator never answered. That was the simulator, not production code; the
  final shape holds no request.
- `WA_APPSTATE_STOP_RUNS=20 CGO_ENABLED=0 go test ./internal/wa/ -count=1 -run
  TestSimulatorStopDuringQueuedAppStateFetches -v -timeout 20m`, 2026-11-13 13:51 UTC: exit 0, outcomes
  `passed:20`.

## Pass rate with SynchronousAck (T025)

2026-11-13 13:59 UTC, Windows 10, Go 1.27.4, `CGO_ENABLED=0`, `TEMP` on `D:\tmp`, working tree after T005–T024 (not
yet committed), pin `bedcf01`. `WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopPassRate -count=1
-v -timeout 60m`: exit 0 (64.4 s).

| Kind | Runs | Passed | Rate | Failures | Passed but marker not clean | Feature 004 (`a3fb844`, 2026-11-09) |
| --- | --- | --- | --- | --- | --- | --- |
| pair: the pairing generation's check | 50 | 50 | 100.0% | none | 0 | 50 of 50 |
| pair: the stop check after the fresh link | 50 | 50 | 100.0% | none | 0 | 50 of 50 |
| stdio: stop check | 50 | 50 | 100.0% | none | 0 | 50 of 50 |
| serve: stop check | 50 | 50 | 100.0% | none | 0 | 50 of 50 |

No kind is lower than feature 004's recorded rate, so the gate before the user stories is met.

**Rerun on the code after US1–US3**, 2026-11-14, Windows 10, `CGO_ENABLED=0`, commit `14531fd`, pin `bedcf01`. Same
command (`WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopPassRate -count=1 -v -timeout 60m`),
exit 0 read from the command itself (62.5 s). Subtests: pair 50 of 50, stdio 50 of 50, serve 50 of 50 (150 `PASS`
lines, 0 `FAIL`); each pair run covers both of its checks. No kind is lower than feature 004's 50 of 50.

## US1 tests and implementation (T029–T032)

- **Expected failures (T029)**, 2026-11-13, Windows 10, `CGO_ENABLED=0`. `go test -count=1 -run
  'TestIngestExtract|TestIngestIgnores|TestIngestRejects|TestIngestLive|TestSimulatorIngest' ./internal/wa`: exit 1,
  first with missing symbols (`extractMessage`, `ingestStore`, `IngestConfig.store`, `SessionConfig.wrapIngestStore`).
  With the two test seams and a stub `extractMessage` that marks nothing eligible, the same run (exit 1, 138.5 s)
  failed on assertions: text, media and wrapped messages "not eligible"; every rejected case counted as ignored;
  nothing stored for the live senders, duplicates and the `Live` callback; a failed write returned `true`; on the
  simulator the text was acknowledged but not stored, the terminal signal never closed after a failed write, and the
  held handler never reached the store. `TestSimulatorIngestSendsNothingToThePhoneForOtherAccounts` passed against
  the stub, as expected: it checks the pin's configuration, not new code.
- **Simulator peer**: `internal/wasim/peer.go` sends end-to-end encrypted messages as another account. It starts a
  Signal session from the linked device's signed prekey (no one-time prekey), so the pinned library decrypts a real
  `pkmsg` and runs the session's handler. It also sends undecryptable stanzas and `unavailable` placeholders.
- **After T030–T032**, same date: `go test -count=1 -run TestIngest ./internal/wa` exit 0 (3.8 s); `go test -count=1
  -run 'TestSimulatorIngest|TestSession|TestOwnerTerminal|TestOwnerHealth' ./internal/wa` exit 0 (7.9 s); `go test
  -count=1 -timeout 30m ./internal/wa/ ./internal/wasim/ ./cmd/whatsapp-mcp/` exit 0 (88.8 s, 5.8 s, 146.9 s).
- **Simulator results**: a text and a captioned image are stored (caption apart from text) and receive delivery
  receipts; with the store failing, the message gets no receipt or ack, `Terminal()` closes, `Health().IngestFailed`
  is set and the stop leaves the marker clean; an undecryptable stanza gets one retry receipt and one ack, a
  `view_once` placeholder one ack and no receipt, the client sends no `message` node, and `unavailable` is 1; with the
  handler held, 2,100 queued receipts close the connection with one `receive_overload` (`item_limit`, "messages may
  be missing"), `Health().ReceiveOverload` is `item_limit` and there is no further dial.
- **Design note**: ingest escapes chat JIDs and message IDs in warnings with `strconv.QuoteToASCII`; the store has no
  exported escaping helper.

## US2 tests and implementation (T036–T037)

- **Expected failures (T036)**, 2026-11-13, Windows 10, `CGO_ENABLED=0`. `go test -count=1 -run
  'TestIngestHistory|TestHistoryWorker' ./internal/wa`: first exit 1 with missing symbols (`IngestConfig.afterHistory`,
  `ingest.liveWaiting`); with those added and no history worker, exit 1 (186.1 s): every history test timed out
  waiting for the worker or for the download to start. A first run hung because two waits had no deadline; every
  wait in these tests is now bounded.
- **After T037**, same date: `go test -count=1 -timeout 5m -run 'TestIngest|TestHistoryWorker|TestSession'
  ./internal/wa` exit 0 (72.7 s); `go test -count=5 -run TestIngestHistory ./internal/wa` exit 0 (24.5 s); `go test
  -count=1 -timeout 30m ./internal/wa/ ./internal/wasim/ ./cmd/whatsapp-mcp/` exit 0 (85.6 s, 3.6 s, 154.5 s).
- **Results**: own notifications only are queued (8), the ninth waiting one is reported `queue_full`; downloads are
  synchronous under the library context; history uses the library's own `ParseWebMessage` and the live sender rules;
  a bare edit never overwrites the original in either order; 600 messages flush as 250, 250, 100 with the
  conversation name offered once per flush as a real name; 200 KiB messages cut at 20 (under 4 MiB); a waiting live
  write goes between two flushes; too-large, download and parse failures are reported and not terminal; a store
  error on the second flush keeps the first 250 and reports `committed=250`; last activity never moves back; Stop is
  observed before downloads, between conversations and before flushes; a worker held inside the download is joined
  after the barrier and its device write is refused, so the check reports `storage_refused`.
- **Design note**: the ingest lock is a one-slot channel, not a `sync.Mutex`, which does not serve waiters in order.
  A live write registers itself as waiting and a worker gives way to it before each flush, so a waiting live write
  always goes first.

## US3 tests and implementation (T041–T043)

- **Expected failures (T041)**, 2026-11-13, Windows 10, `CGO_ENABLED=0`. `go test -count=1 -run
  'TestIngestLID|TestOwnerHands' ./internal/wa` and `go test -count=1 -run Reconcile ./cmd/whatsapp-mcp`: exit 1,
  missing symbols (`ingest.hintRetry`, `lidLookup`, `hintAttempts`, `ingestStore.PhoneForLID`, `ownerDeps.reconcileFrom`,
  `wa.ReconcileLIDs`, `wa.LIDMergeStore`, `startupReconcile`). The first run after T042–T043 failed in the tests'
  own setup ("device storage is not ready": the device was loaded but never saved); the tests now save it first.
- **After T042–T043**, same date: the same two runs exit 0 (7.8 s, 4.2 s); `go test -count=3 -run
  'TestIngestLID|TestOwnerHands' ./internal/wa` exit 0; `go test -count=1 -timeout 30m ./internal/wa/
  ./internal/wasim/ ./cmd/whatsapp-mcp/` exit 0 (55.7 s, 2.1 s, 125.1 s).
- **Results**: a mapped chat or sender LID is stored under the phone JID with its phone; an unmapped one stays a LID;
  a lookup error or deadline keeps the LID within the deadline; a mapping written during the run merges without a
  restart, a hint whose mapping is not visible yet is retried and merged, and dropped after 5 attempts; Stop during
  a retry wait ends the worker at once; a failed merge logs `lid_merge_failed` with escaped JIDs, is counted and is
  not terminal; an unchanged upsert produces no lookup; a remap logs `lid_reassigned` with old and new phones; the
  hint set stays at 1,024 and counts drops; a mapping merged between the first lookup and the write is seen by the
  re-lookup under the lock, live and in history; 250 unresolvable LIDs in one flush hold the lock under one
  deadline; the startup pass merges a LID chat (the phone chat's copy of a shared message wins) and a group-only
  sender before the owner starts, the second start merges nothing, an unmapped LID is untouched, a failed merge
  leaves both sides; an exhausted pass hands its cursor through `OwnerConfig` to the first generation, whose worker
  merges the rest.
- **Design notes**: `lid_reconcile` reports `status` (`completed`, `budget_exhausted`, or `incomplete` when a page
  read failed with budget left), `merged`, `failed` and `skipped` (a mapping the store refused as invalid). The store
  open and the pass share one 10-minute startup budget in `runShell`.

## Owner Reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-13 | 61a63f4 | BLOCK | |
| design | 2026-11-13 | 1b1d91f | CONCERNS | Fix all and review again (2026-11-13): placeholder gated in the fork (`bedcf01`), roadmap history check kept; fixed at 84dfa32 |
| design | 2026-11-13 | 84dfa32 | PASS | |
| design | 2026-11-13 | a040210 | PASS | |

## US4 tests and implementation (T044–T046)

- **Owner decision, 2026-11-14**: the name cache needs the stored name's quality and no store read returned it, so a
  fifth read-only helper, `Store.ChatName`, was added (plan, data model, `contracts/ingest.md` step 6, R12 and tasks
  T044/T046 updated). The alternative, offering once per chat per generation, would have looked up every group that
  already has a name again after each restart.
- **Expected failures (T045)**, 2026-11-14, Windows 10, `CGO_ENABLED=0`, before any implementation. `go test
  ./internal/store -run 'TestChatName|TestEveryMethodAfterClose' -count=1`: exit 1, `st.ChatName undefined`.
  `go vet ./internal/wa`: exit 1, `ingestStore has no method ChatName`, `ingest.now`, `newFailureCache`,
  `failureCacheSize`, `failureTTL`, `namePending`, `nameCacheLen` undefined.
- **Implementation (T046)**, 2026-11-14, Windows 10, `CGO_ENABLED=0`, working tree on `14531fd` (not committed):
  `Store.ChatName` (`internal/store/chats.go`), `internal/wa/ingest_names.go` (name cache of 4,096 chats, direct-chat
  order, group lookup set of 64 with one worker, failure cache of 1,024 groups for 10 minutes), step 6 in
  `HandleMessage`, and history flushes raising cached names. The contact table is read once per chat per
  generation, so a contact saved later is picked up at the next generation.
  - `go test ./internal/wa -run 'TestIngestName|TestIngestGroup|TestIngestFailed|TestFailureCache|TestIngestLookupSet'
    -count=1 -v`: exit 0, 11 tests passed. `go test ./internal/store -run 'TestChatName|TestEveryMethodAfterClose'
    -count=1`: exit 0.
  - `gofmt -l .` printed nothing, `go vet ./...` exit 0, `go test ./... -count=1 -timeout 30m` exit 0 (cmd 132.3 s,
    store 311.5 s, wa 67.0 s, wasim 3.6 s; the other packages passed). The suite ran twice in one command, the
    first time through a pipe, which also passed in every package.
  - Not run: the five cross-builds, `govulncheck`, the race detector in WSL2 and the quiet-stop pass-rate rerun
    after US4.

## Stops with ingest busy (T047)

2026-11-14, Windows 10, Go 1.27.4, `CGO_ENABLED=0`, `TEMP` on `D:\tmp`, working tree on `14531fd` with the US4 work
(not committed). `WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopWithIngestWorkers -count=1 -v
-timeout 60m`: exit 0 (501.5 s). 50 stops per case, each through the production owner and the simulator. The
simulator was extended for this: it answers the media-connection query, serves an encrypted history blob over HTTP
(`internal/wasim/history.go`), and a test-only `SessionConfig.MediaHTTP` points the download client at it, so a history
chunk is downloaded, decrypted and stored by the real library path. "In flight" counts runs where the work was still
running when the stop began.

| Case | Runs | Passed | Rate | In flight at the stop | Failures | `stop_bound_exceeded` | Marker not clean | Stop time p50 / max |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| history chunks (6 chunks of 1,000 messages; a third of the stops while the first download is held, the rest after 0–120 ms) | 50 | 47 | 94.0% | 50 | `storage_refused` 3 | 0 | 3 | 123 ms / 224 ms |
| group lookup (held at the simulator) and run-time merge (held in the store), both in progress | 50 | 50 | 100.0% | 50 | none | 0 | 0 | 7.106 s / 7.150 s |
| live message inside the handler (two slow LID lookups of 250 ms, a 400 ms write) | 50 | 50 | 100.0% | 50 | none | 0 | 0 | 1.080 s / 1.265 s |
| `pair`-shaped stop at the follow-up confirmation, first history download held | 50 | 50 | 100.0% | 50 | none | 0 | 0 | 159 ms / 169 ms |

- **History**: 3 of 50 stops during history ended `storage_refused` and left the unsafe marker, as research R11
  predicted for a worker still inside a library call at the barrier. The owner accepted this at T004 with the rate
  coming back; it comes back here. The marker's next-start handling is feature 003's repair, unchanged.
- **Group lookup and merge**: every stop passed, but each took about 7.1 s of the 10 s stop bound, because the
  simulator never answers the held group query and the worker is joined after the fence. A real server that stops
  answering would behave the same. No stop exceeded the bound in these runs; the margin is about 2.9 s.
- Feature 004's rate for a stop without ingest work is 50 of 50 (T025 above).

## Handler latency under history (T048)

2026-11-14, same machine and tree. `WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestIngestHandlerLatency
-count=1 -v -timeout 60m`: exit 0 (374.0 s). 20 runs. Each run: 20 history chunks (250 messages each) interleaved
with a burst of 2,000 stanzas from two other accounts, at full simulator speed: 1,000 texts by phone number, 900 by
LID with a phone `sender_pn` (so the library repeats mapping upserts) and 100 `view_once` placeholders.

| Measure | Result |
| --- | --- |
| Handler waits for the ingest lock (38,000 live writes) | p50 below the clock's resolution (0 s), p99 0 s, max 90.6 ms |
| Longest wait per run | p50 65.8 ms, max 90.6 ms |
| `receive_overload` events | 0 in 20 runs |
| Runs that did not finish storing | 0 |
| Messages stored | 138,000 of 138,000 (1,900 live and 5,000 history per run) |
| `unavailable` placeholders counted | 100 per run |
| Run time p50 / max | 20.3 s / 22.1 s |

Target met: no `receive_overload`. The wait is measured by a test-only hook in `lockForLive`. Not measured: a real
account's rate or message mix; the simulator sends as fast as the connection takes frames.

Owner review: these two tables go to the owner before the code review (T047, T048).

**Owner decision, 2026-11-14**: accepted both results above for the code review. (1) 3 of 50 stops during history
(6%) ending `storage_refused` with the unsafe marker, as research R11 predicted; the marker is handled by feature 003's
repair at the next start. (2) A stop of about 7.1 s of the 10 s bound while a group lookup never gets an answer, a
margin of about 2.9 s. Both go into the accepted risks of the T057 roll-up. No limit or code was changed for them.

## Abuse tests (T049)

2026-11-14, Windows 10, `CGO_ENABLED=0`, working tree on `14531fd` (not committed). `internal/wa/ingest_abuse_test.go`:
`go test ./internal/wa -run 'TestIngestAbuse|TestIngestClientHasNo' -count=1 -v`: exit 0, 5 tests passed on the first
run. They test behaviour already in place (store validation, the name rules of US4), so no expected-failure run
exists for them; a test that could not fail would be worth nothing, so each case asserts the rejection or the
bound it names.

- **Fields**: 15 cases. Text at exactly 256 KiB is stored; one byte more is rejected. Also rejected, with nothing
  written, `Live` not called and ingest not terminal: text and caption that are not UTF-8, a caption over 256 KiB,
  a media URL or direct path over 4 KiB, a MIME type over 255 bytes, a document name over 1 KiB, a file length over
  the signed range, a newline in the ID, a control character in the sender, a newsletter sender and a sender JID
  over 256 bytes. Each is acknowledged (`HandleMessage` returns true).
- **Names**: a push name over 1 KiB or not UTF-8 is not used (the chat gets the number); one of exactly 1 KiB is. An
  oversized group subject is not written and the group is not asked for again; an oversized history name is not
  offered while its messages are stored.
- **Instructions in text** (send, pair, post to a URL, "ignore your instructions"): stored verbatim as text; no
  client call, download or parse follows. `ingestClient` has exactly four methods (`OwnJID`, `ParseWebMessage`,
  `DownloadHistorySync`, `GetGroupInfo`) and a test fails when one is added: the interface has no send, pairing or
  connect method.
- **Pressure**: 8 goroutines of 250 events each (valid live messages in 125 direct and group chats, invalid events,
  ignored events and 2,000 merge hints) beside 3 history chunks of 700 messages: 3,300 stored (1,200 live and 2,100
  history), 400 rejected, 400 ignored, 1,012 hints dropped at the limit of 1,024, name cache 601 of 4,096, failure
  cache 600 of 1,024, 3 downloads, no terminal failure, every committed batch 1 to 250 messages, and the store holds
  exactly the stored count. The lookup set reached 1 of 64 here, since the failing fake answers at once; the bound
  of 64 is tested in `ingest_names_test.go`.
- **Not run**: the race detector (T054, WSL2). Disk exhaustion and callbacks racing retirement are covered by
  earlier tests (`TestIngestLiveStoreErrorIsTerminal`, the quiet-order and stop tests), not repeated here.

## Log secrecy, OPERATIONS.md and comments (T050–T052)

2026-11-14, Windows 10, `CGO_ENABLED=0`, tree on `8ba8933`.

- **T050**: `internal/wa/security_test.go` gains `TestIngestLogsKeepContentAtDebugAndNeverLogMediaMaterial` (debug and
  info) and `TestIngestLogsEscapeAttackerChosenIdentifiers`; `go test ./internal/wa -run TestIngestLogs -count=1
  -v`: exit 0, 3 test runs passed. One scenario (text, a captioned image, a group with a lookup, a name, a rejected
  oversized message, a failed history download, a failed write, a stop) runs through a real text handler at each
  level. Checked: stdout stays empty; media keys, hashes, URLs and direct paths appear in no form (raw, hex, base64
  standard, URL and raw, decimal list) at either level; text and captions appear at debug (the test fails if not,
  so it cannot pass blind) and not at info; push names and group subjects do not appear at info; the scenario reached
  `history_incomplete`, `ingest_failed` and `ingest_stopped`; a message ID containing a quote, a space and a forged
  `event=` is logged as its quoted ASCII form and produces no extra record. Names are also absent at debug today,
  which the test does not require. As for the abuse tests, these pass on code that already behaved this way.
- **T051**: `OPERATIONS.md` has a new "Message ingest (feature 005)" section: what is stored (text, captions, media
  metadata with the keys that decrypt media, names, LID links, history), what is not (and the `unavailable`
  count), unbounded growth, deletion in WhatsApp not deleting here, backups, what the program sends, the events
  (`ingest_failed`, `history_incomplete`, `receive_overload`, `lid_reconcile`, `lid_reassigned`, `lid_merge_failed`,
  `ingest_stopped`), the exit-1 storage message and what to do, the 6% unsafe stops during history, and a
  "What has been checked" list that keeps "not verified" apart, including that nothing was run on a real account.
- **T052**: a search of the listed files (and every Go file under `internal` and `cmd`) for requirement, success,
  decision and research IDs found two research references in the T047/T048 measurement comments; both were removed.
  No stale text about asynchronous acknowledgements remains (`SynchronousAck` is set in `session.go`). After the
  change `gofmt -l .` printed nothing, `go vet ./internal/wa` passed and `go test ./internal/wa ./cmd/...
  -count=1` exit 0 (wa 64.6 s, cmd 124.4 s).

## Full gates (T053) and line endings (T055)

2026-11-14, Windows 10 amd64, Go 1.27.4, `CGO_ENABLED=0`, `TEMP` and `TMP` on `D:\tmp`, commit `42fdbf1`. Each exit code
was written by the command itself into a file (no pipe), and the first line of each output was read.

| Gate | Exit code |
| --- | --- |
| `gofmt -l .` | 0, printed nothing |
| `go vet ./...` | 0 |
| `go mod verify` | 0 |
| `go test ./... -count=1 -timeout 30m` | 0 (cmd 161.9 s, store 355.0 s, wa 111.5 s, wasim 2.5 s; every package ok) |
| build `windows/amd64`, `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` | 0 each |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | 0, "No vulnerabilities found." |
| `go vet`, `go test` and `go build` of `./internal/wa/testdata/cmd/session-check` | 0 each |

- **Pin**: `go.mod` and `go.sum` were last changed by `84dfa32`, the commit that pinned the fork; `git diff 84dfa32 --
  go.mod go.sum` is empty. `replace` still names `github.com/tiger-seo/whatsmeow v0.0.0-20261113124132-bedcf0185245`
  (`bedcf01`).
- **Line endings (T055)**: `git ls-files --eol` lists 302 tracked files, all `w/lf`; none shows `w/crlf`.
- Not covered by this run: the race detector and native Linux or macOS test runs (T054, and CI on push).

## WSL2 race and suite runs (T054)

2026-11-14, WSL2 Ubuntu, Go 1.27.4 linux/amd64, gcc 15.2, `TMPDIR=/var/tmp`, results are WSL2, not native Linux.

- **Race run 1**, commit `0f4ebbf`, `CGO_ENABLED=1 go test -race ./internal/wa ./cmd/... -count=1 -timeout 40m`:
  exit 1. 0 data races, 1 failure: `TestIngestLIDRelookupOfAFlushHasOneDeadline` held the lock 1.097 s against a limit
  of 1 s plus the deadline. Cause: under the race detector the write of a 250-chat flush alone takes about 1.1 s, so
  the limit was slack, not the design under test. The limit is now 1.8 s (one deadline per LID would hold it at least
  2.5 s); the test passed 5 of 5 under `-race` and 3 of 3 natively. `cmd/whatsapp-mcp` passed (189.5 s).
- **Race run 2**, commit `533fb38`, same command: exit 1. One data race, in
  `TestHistoryWorkerInsideTheLibraryIsJoinedAfterTheBarrier`: the test replaced `sess.ingest.st` while the LID
  reconcile worker, started by a mapping the library stored at connect, read it in `merge`. It is the test's own
  setup (production never reassigns the field); the test now passes the wrapper through the session's
  `wrapIngestStore` option when the session is built. It passed 3 of 3 natively. Not yet rerun under `-race`.
- **Suite run**, same commit, `CGO_ENABLED=0 go test -p 1 ./...`: exit 1, not valid. Partway through the `store`
  tests the WSL root file system became read-only (`TempDir: mkdir /var/tmp/...: read-only file system`; later
  `df`: "Input/output error"), so the rest of `store` and the builds of `wa`, `walog` and `wasim` failed on the file
  system, not on the code. `cmd`, `buildinfo`, `config`, `lock` and `private` passed before it.
- **Final runs**, 2026-11-14, after the WSL VM was restarted (`/` writable, 951 GB available), commit `4d95554`:
  `CGO_ENABLED=1 go test -race ./internal/wa ./cmd/... -count=1 -timeout 40m`: exit 0 (wa 217.1 s, cmd 230.5 s),
  0 data races. `CGO_ENABLED=0 go test -p 1 ./... -count=1 -timeout 60m`: exit 0 (cmd 114.4 s, store 227.6 s, wa 55.2 s,
  wasim 3.2 s; every package ok). Both are WSL2 results, not native Linux. Exit codes were written by the commands
  themselves. The cause of the file system turning read-only was not established; the VM's virtual disk grows on a
  Windows drive with little free space, which is the likeliest reason and is not confirmed.
- No push to CI was made (only on the owner's request).

## Real-account journey (T056), in progress

Owner permission was given step by step in the session; the owner ran every command that touched the account. 2026-11-14,
Windows 10, binary built from commit `221a265` (`D:\tmp\whatsapp-mcp-221a265.exe`, reported as `-dirty` only because the
uncommitted `.claude/settings.json` plugin setting was in the tree; no Go file differed), new empty data directory
`D:\tmp\wa-005-real`, one account and one phone, the one used for the earlier real runs. Results are redacted: no
message text, name or JID is recorded.

- **Step 1, `pair --data-dir D:\tmp\wa-005-real` (run by the owner, QR scanned by the owner)**: exit message "linked.
  The saved session can be reused." The pairing generation's stop check and the fresh connection's stop check both
  passed (`quiet_stop_passed`, 262 ms and 460 ms, 0 dropped messages). The fresh connection lasted about 3.6 s after
  "Successfully authenticated". `ingest_stopped`: `stored=0 ignored=0 rejected=0 unavailable=0 dropped=0
  history_incomplete=0 lookups_dropped=0 hints_dropped=0 merged=1 merge_failed=0`. So during `pair` no history
  chunk was reported incomplete and none was stored: history, if WhatsApp sends it, did not arrive in that window.
- **Step 2, offline inspection (read-only, of a copy of `whatsapp.db`)**: `status` exit 0 (`logged_in: true`,
  `pairing_required: false`, marker clear). The copy holds 22 tables; `chats` 0, `messages` 0, `whatsmeow_contacts` 0,
  `whatsmeow_lid_map` 1, `mcp_version` 3. Database size 336 KB. Nothing else was opened or printed.
- **Observation**: the link produced one LID mapping and one run-time merge on a real server, and no ingest warning.
  The history a linked device receives after `pair` is the accepted risk named in the roll-up; with this run it is
  measured as zero stored and zero reported incomplete within a 3.6 s connection.
- **Not yet run**: `serve`, live messages (text, captioned image, a LID-addressed contact), the second inspection,
  and the cleanup (the owner unlinks the device on the phone, then the directory is deleted).
