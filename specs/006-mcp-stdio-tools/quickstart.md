# Quickstart: Validate MCP Chat and Contact Tools

This guide is for the implementation phase. The commands below describe acceptance to build, not features already implemented by this planning commit. Use only synthetic temporary data and fake owners. Do not run stdio against the owner's saved-session data: it may connect automatically. No real-account permission is requested or assumed.

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

The data fixture contains 235 chats, name/activity ties, empty chats, repeated group participation, another sender's real latest message, captions, overlapping phone digits, mapped/unmapped LIDs and alias contact names. Assert stable pages, one result per stored chat/person, exact direct selection, all name fields, case folding and literal queries. Decode text once and compare with structured output; validate output schemas and null/omission rules. These tests must use `NewInMemoryTransports`, not only direct handler calls.

## 3. Store Correctness and Ingest Admission

Planned new test prefixes must be added by implementation; a command matching zero tests is not a pass. Use `-v` to verify actual named cases.

```bash
go test ./internal/store -run 'TestToolReader|TestMCPRead|TestListChats|TestGetChat|TestSearchContacts|TestContactChats|TestDirectChat' -count=1 -timeout 10m -v
go test ./internal/mcp ./cmd/whatsapp-mcp -run 'TestMCPFlood|TestMCPIngestPressure' -count=3 -timeout 10m -v
```

Expected: false latest inclusion skips the query; folded JID and literal contact searches follow the new contract; aliases deduplicate before paging; oversized legacy names fail before giant Go allocations. ToolReader has at most one active snapshot, no MCP admission waiters, immediate busy on saturation and proper cancellation/close accounting. Hold reads in acquisition/query/output stages while synthetic ingest and device activity continue. Observe zero MCP-admission-induced ingest stops, bounded WAL/heap and recovery after cancellation. Existing busy write policy remains terminal. Record actual measurements and workload counts; concurrency assertions alone do not prove all resource behavior.

## 4. Raw Framing and Response Budgets

```bash
go test ./internal/mcp -run 'TestStdio|TestResponseBudget|TestSafeText|TestMCPOverload' -count=3 -timeout 5m -v
```

Expected: exact 64 KiB frame boundary, depth/token limits, malformed/truncated/batched input, duplicate/oversized IDs, safe integer-ID boundaries and cancellation correlation, repeated initialize, unknown notifications, forwarding notification 1,024 and rejecting 1,025, and cancellation under full request admission. No more than four outstanding IDs and one tool response; overload produces a bounded explicit error without SDK/store admission. SDK-before-handler malformed params and early cancellation release credit after the response, or stop/drain if no response is possible; a subsequent valid call proves no live-session credit leak. Synthetic secrets in malformed params never appear in errors. Deliberate slow streams use cooperative tests or subprocesses, not leaked pumps in the test runner.

Use worst-case control/non-ASCII fields to prove preflight before marshaling, structured/text caps and actual 8 MiB outer cap. Oversized pages produce `resource_limit`, not partial success. Capture allocation/retained-frame high-water marks; audit any SDK intermediate copies that materialize before transport Write. A failed allocation/bounds proof blocks acceptance.

## 5. Command Lifetime and Native Pipes

```bash
go test ./cmd/whatsapp-mcp -run 'TestMCP|TestShellIngest|TestLifecycle' -count=3 -timeout 10m -v
go test ./internal/wa -run 'TestOwnerTerminal|TestQuietOrder|TestSimulatorStopDuringQueuedAppStateFetches' -count=1 -timeout 10m -v
```

Expected: stdio initializes while fake owner is disconnected; stdout parses exclusively as MCP. Real held-lock startup refuses before owner/MCP creation. EOF, cancellation and signals stop admission and drain actual tool/owner/store work before releasing the lock. Device fault before subscription, after subscription, during a call and during drain exits stdio 1; EOF/fault races retain fault status. Serve's device-only behavior remains unchanged; both shells retain ingest-failure exit.

Run subprocess cases on native Windows, Linux and macOS: idle blocked stdin, partial frame, permanently blocked stdout, closed stdout, blocked stderr during startup/active operation/shutdown, closed stderr/SIGPIPE, client cancellation and forced second signal. Assert at most three OS pumps including diagnostics, no replacement session/pumps, bounded output/diagnostic retention, drop-counter behavior and process exit after safe account/store drain. A stalled OS pump need not join inside the process; it may not hold store/owner references. Cross-building or WSL2 evidence does not verify native macOS/Windows pipe behavior.

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

Task generation must include every contract limit and abuse case, a review checkpoint for numeric proposals/notification-session compatibility, and native process tests. Commit tasks/design evidence and obtain the required owner-judge design review before implementation. After implementation, record actual gate results/platform gaps, update `OPERATIONS.md` without calling unverified behavior verified, then commit for owner code review. No PR/release or real-account run is authorized by this guide.
