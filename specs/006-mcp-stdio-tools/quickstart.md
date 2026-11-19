# Quickstart: Validate MCP Chat and Contact Tools

This guide is for the implementation phase. The commands below describe acceptance to build, not features already implemented by this planning commit. Use only synthetic temporary data and fake owners. Do not run stdio against the owner's saved-session data: it may connect automatically. No real-account permission is requested or assumed.

**Readiness**: T001's isolation policy is specified in contracts/store-and-lifecycle.md (spike evidence in verification.md); implementation waits for the owner-judge design review of the committed design. A concurrency cap, context timer, artifact check or finite flood run is not a substitute for that review. Do not start dependency/runtime implementation or run the future acceptance commands before it clears.

Behavior is defined in [contracts/tools.md](contracts/tools.md), [contracts/stdio.md](contracts/stdio.md) and [contracts/store-and-lifecycle.md](contracts/store-and-lifecycle.md); fields are in [data-model.md](data-model.md). Record command, date, platform, counts/skips and the command's own exit code in [verification.md](verification.md). These commands use Git Bash on Windows; use a large local temporary volume for full store tests.

## 1. Dependency and Format Gates

```bash
export CGO_ENABLED=0
go mod verify
go list -m github.com/modelcontextprotocol/go-sdk
gofmt -l .
go vet ./...
```

Expected after implementation: exact SDK v1.8.0, module integrity passes, no gofmt output, no vet findings. Confirm whatsmeow remains replaced by `github.com/tiger-seo/whatsmeow v0.0.0-20261113124132-bedcf0185245`, libsignal remains at its existing pin and no CGO/toolchain upgrade was added. Review all transitive dependency changes; SDK compatibility is not a vulnerability scan result.

## 2. Five Tools Through the Official In-Memory Transport

```bash
go test ./internal/mcp -count=1 -timeout 5m -v
```

Expected: all five tools initialize/discover/call through the SDK paired in-memory transport with seeded temporary stores, no real network. Both deferred login tools are absent and calls fail without account work. Test every parameter and omitted default, valid explicit false/page zero, invalid limit zero/null, unknown and duplicate argument fields, invalid types/identities, and offset overflow. Tool listChanged/subscription capabilities are explicitly disabled; `subscriptions/listen` cannot retain state.

The data fixture contains 235 chats, name/activity ties, empty chats, repeated group participation, another sender's real latest message, captions, overlapping phone digits, mapped/unmapped LIDs and alias contact names. Traverse all 235 chats with omitted limit (default 30), explicit 1 and 200, asserting completeness/uniqueness. A known mapping with only a LID chat returns that exact row for equivalent phone/JID/LID inputs; phone wins when both rows exist. A formatted phone search does not match digits-only storage unless a separate name literally matches; formatted identity inputs still normalize. Decode text once and compare with structured output and schemas. US1 independently discovers its two tools; exactly five-tool discovery is cross-story acceptance after both stories. Use `NewInMemoryTransports`, not only direct handlers.

## 3. Store Correctness and Ingest Admission

Planned new test prefixes must be added by implementation; a command matching zero tests is not a pass. Use `-v` to verify actual named cases.

```bash
go test ./internal/store -run 'TestToolReader|TestMCPRead|TestListChats|TestGetChat|TestSearchContacts|TestContactChats|TestDirectChat' -count=1 -timeout 10m -v
go test ./internal/mcp ./cmd/whatsapp-mcp -run 'TestMCPFlood|TestMCPIngestPressure' -count=3 -timeout 10m -v
```

Expected after T001 and implementation: false latest inclusion skips the query; literal searches/aliases and bounded source fields match the contract. Prove the isolation policy in contracts/store-and-lifecycle.md (first-step reads, C = 100,200 walk, contact ceilings, coordinated admission with the 65-waiter cap, WAL guard) under ordinary/device arrivals before/after tool entry, pending work and repeated readmission; no extra active slot/reader, queued tool or bypass of ordinary capacity. Exercise initial SQL, expensive later Next, folding and transaction close, not only initial QueryContext. Measure 5 s/10 s cancellation requests separately from actual termination/resource-return bounds established by T001. Delayed return retains accounting and forbids replacement snapshots; control failure produces unsuccessful normal cleanup, no premature unlock. Flood evidence must accompany the supported mechanism argument and show no MCP-induced ingest stop, bounded heap/temp/WAL and actual return. Ingest ErrBusy remains terminal.

For the transaction fence, hold a synthetic snapshot after cancellation and prove the next ordinary transaction cannot get the writer until the reader connection actually returns. Repeat across device lease and autocommit boundaries and repeated tool admission, and verify timely resumption, existing control-failure timeout and recovery to 64 MiB. Record transaction ordering separately from physical peak WAL size and verify free-space assumptions separately; a passing stress peak cannot establish the fence or disk bound.

## 4. Raw Framing and Response Budgets

```bash
go test ./internal/mcp -run 'TestStdio|TestResponseBudget|TestSafeText|TestMCPOverload' -count=3 -timeout 5m -v
```

Expected: exact 64 KiB frame boundary, depth/token limits, malformed/truncated/batched input, duplicate/oversized IDs, safe integer-ID boundaries and cancellation correlation, repeated initialize, unknown notifications, forwarding notification 1,024 and rejecting 1,025, and cancellation under full request admission. No more than four outstanding IDs and one tool response; overload produces a bounded explicit error without SDK/store admission. SDK-before-handler malformed params and early cancellation release credit after the response, or stop/drain if no response is possible; a subsequent valid call proves no live-session credit leak. Synthetic secrets in malformed params never appear in errors. Deliberate slow streams use cooperative tests or subprocesses, not leaked pumps in the test runner.

Use worst-case control/non-ASCII fields to prove preflight before marshaling, structured/text caps and actual 8 MiB outer cap. Oversized pages produce `resource_limit`, not partial success. Capture allocation/retained-frame high-water marks; audit any SDK intermediate copies that materialize before transport Write. A failed allocation/bounds proof blocks acceptance.

## 5. Command Lifetime and Native Pipes

**Before Phase 3 (T045/T046):** After the renewed design gate clears, record the intended red command tests locally before wiring, then rerun them after wiring. Use stdin-discard and no SDK session. Check the resolved ordinary log level, timely mandatory acknowledgment during an otherwise clean drain, and exit 1 after ordered owner/store/lock cleanup on reporting failure. After a separately authorized push of the locally verified slice, collect the [focused T046 CI results](tasks.md#diagnostic-command-tests-and-native-evidence-t045t046) from native Linux and macOS. Both runners must show every required named stderr/SIGPIPE/signal case passing and the asserted child exit outcomes. Windows tests, WSL2, cross-builds, zero matches or required-case skips cannot establish this gate. Use only synthetic temporary data and test-only helpers.

Check refusal content, not just the code: healthy stderr must name invalid settings and their valid choices, or give the platform permission remedy. Hold a mandatory write through cleanup and acknowledge it before its original deadline; expect exit 0 when otherwise clean. For the separate second-signal case, require a test-only observation of the forced callback during pending finalization; exit 1 alone can be diagnostic expiry. Re-run existing stdio CLI and process regressions with out-of-band readiness and observable cleanup, preserving empty stdout, offline behavior and lock refusal.

**Integrated acceptance (T024/T026, T036-T038):** Revalidate both diagnostic and store-control shutdown against actual MCP drainage, then run the full native process matrix below. Foundation runs do not replace integrated acceptance.

```bash
go test ./cmd/whatsapp-mcp -run 'TestMCP|TestShellIngest|TestLifecycle' -count=3 -timeout 10m -v
go test ./internal/wa -run 'TestOwnerTerminal|TestQuietOrder|TestSimulatorStopDuringQueuedAppStateFetches' -count=1 -timeout 10m -v
```

Expected: stdio initializes while fake owner is disconnected; stdout parses exclusively as MCP. Real held-lock startup refuses before owner/MCP creation. EOF, cancellation and signals stop admission and drain actual tool/owner/store work before releasing the lock. Device fault before subscription, after subscription, during a call and during drain exits stdio 1; EOF/fault races retain fault status. Serve's device-only behavior remains unchanged; both shells retain ingest-failure exit.

Run native Windows/Linux/macOS subprocesses: blocked/partial stdin, slow/blocked/closed stdout, blocked stderr during startup/operation/drain, closed stderr/SIGPIPE, client cancellation and forced second signal. Assert three fixed OS pumps, no replacements, ordinary 16 x 4 KiB drop queue and independent mandatory 16 x 1 KiB queue, at most 128 fixed enum cells and one earliest-deadline timer. With ordinary logs saturated or stderr blocked, submit incomplete-history/receive-loss and denied/fault records: mandatory overflow/failed or missing 5 s full-write acknowledgment must latch normal exit 1, preserve categories and beat EOF success, never block drain or claim delivery. Acknowledged records may proceed without this failure; ordinary-only drops are permitted. Record actual OS/architecture/control combinations, not inferred runtime coverage from cross-builds or WSL2. Fixed blocked pumps hold no owner/store references and may end at safe process exit.

At every supported log level, trigger the real incomplete-history/receive-loss producers and typed denied/failed reporting. At error level, WARN mandatory events must still reach the reporting path before severity filtering; unrelated info/debug and message bodies must remain filtered. Test the outer Handler.Enabled behavior, not only writes to a sink after a filter has already discarded the event.

## 6. Full Delivery Gates

```bash
go mod verify
go vet ./...
go test ./... -count=1 -timeout 30m
for t in windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null ./cmd/whatsapp-mcp || exit $?
done
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go vet ./internal/wa/testdata/cmd/session-check
go test ./internal/wa/testdata/cmd/session-check -count=1
go build -o /dev/null ./internal/wa/testdata/cmd/session-check
git diff --check
git ls-files --eol
```

Every command must exit 0; gofmt from step 1 must print nothing; no tracked file may show `w/crlf`. Capture the program's own exit code before any log-filtering command. CI's native Windows/Linux/macOS tests and five builds remain mandatory before delivery claims. Do not enable heavy/soak/measurement environment variables or CGO race tests without their normal on-demand authorization.

## 7. Review and Operator Evidence

T001 first resolves the known technical prerequisite, records supported APIs/query plans/resource bounds/actual completion evidence, and stops for an explicit owner decision if the existing pin/architecture cannot support it. Only then commit the revised design and obtain owner-judge design review before implementation. Numeric proposals, mandatory-delivery fallback and finite notification compatibility remain review inputs, not inferred approval. Later record actual acceptance/platform gaps and update `OPERATIONS.md` truthfully before the authorized code-review checkpoint. No PR/release, pin/budget change, extra process/database or real-account run is authorized by this guide.
