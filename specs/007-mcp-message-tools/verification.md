# Verification: MCP Message Tools

## Planning Evidence

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-22 | `.specify/scripts/bash/setup-plan.sh --json` (Git Bash on Windows) | Exit 0; resolved branch `007-mcp-message-tools` and generated the plan template in this directory. |
| 2026-11-22 | Read-only audit of `internal/store`, `internal/mcp`, the feature spec and the 002/006 baselines | Two independent research passes identified the indexed seek paths and the existing reply/lifecycle bounds. Research decisions are recorded in [research.md](research.md). No SQLite measurements, runtime tests or operator-account actions were run. |
| 2026-11-22 | Task checklist validation with `git grep --no-index` and PowerShell | Exit 0; 36 sequential unchecked tasks with correct story labels and file paths. `git show --check 0d4c7fa` and `git ls-files --eol -- specs/007-mcp-message-tools/tasks.md` passed. Documentation checks only. |
| 2026-11-22 | Git Bash `-n .claude/hooks/owner-review-gate.sh` and isolated `/d/tmp/kilo/whatsapp-007-gate-check.sh` | Exit 0 for both. Seven gate fixtures passed: existing design PASS unchanged, missing/pending/uncommitted evidence denied, accepted CONCERNS denied, committed Verified evidence plus a current PASS allowed, and edited evidence made the PASS stale. No production or account actions. |
| 2026-11-22 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 90s -run '^TestResult(HTMLMetacharacterCharges\|SDKEncodingEscapedBudget\|ActualTwoMiBBoundary\|NearLimitPassesFramePreflight)$'` | Exit 0, four existing MCP reply-size regressions, Windows/amd64 (package 1.115 s). This verifies the encoder baseline, not feature 007 context assembly. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 90s -run '^(TestToolReadDoesNotSucceedAfterCancellation\|TestToolWALWriteCancelsWithoutReleasing\|TestWriterFenceDuringExistingDeviceLease)$'` | Exit 0, three existing reader and writer-fence regressions, Windows/amd64 (package 10.873 s). Not a test of the proposed message query path. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 90s -run '^(TestServerSDKExplicitSchemasAndDispatch\|TestGuardOfficialInMemory\|TestCommonSchemaDefaults)$'` | Exit 0, three existing MCP registration, transport and default regressions, Windows/amd64 (package 0.365 s). |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/store -count=1 -timeout 120s -run '^TestMCPIngestPressureWALFence$' -v` | Exit 0, one existing native Windows/amd64 WAL-fence test (4.65 s). Not evidence for the proposed message statements. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./internal/mcp -count=1 -timeout 120s` | Exit 0, existing MCP package suite, Windows/amd64 (96.461 s). Feature 007 tools are not wired in. |
| 2026-11-23 | `CGO_ENABLED=0 go vet ./internal/mcp ./internal/store` | Exit 0, Windows/amd64. |
| 2026-11-23 | `CGO_ENABLED=0 go test ./cmd/whatsapp-mcp -count=1 -timeout 90s -run '^Test(MCPPipeNative\|MCPStdioIntegratedDrain)$'` | Exit 0, existing native pipe and integrated drain tests, Windows/amd64 (package 91.963 s). |
| 2026-11-23 | `CGO_ENABLED=0 GOPROXY=off GOFLAGS=-mod=readonly go run D:/tmp/kilo/wa007_batch_probe.go` | Exit 0 on Windows/amd64; nine synthetic SQLite search trials and one connection-return trial passed. [Measured results and limitations](feasibility.md#bounded-batch-probe-2026-11-23). |
| 2026-11-23 | `git diff --check -- specs/007-mcp-message-tools`; `git ls-files --eol -- specs/007-mcp-message-tools`; `git grep -E -o '^[-] \[[ xX]\] T[0-9]{3}' -- specs/007-mcp-message-tools/tasks.md` with sequential ID validation | Exit 0 for all three checks; no whitespace errors, 10 LF files, and 36 sequential IDs. Documentation checks only, before the S6 decision below; no new runtime evidence was collected. |

The owner approved the S6 duplicate-ID overflow rule on 2026-11-23: list all matching chats for 2–100; at 101 or more, require `chat_jid` without returning a chat list or selecting a message. See the [S6 amendment](../../docs/specification.md#85-storage). This resolves the documentation conflict, not the feasibility or design-review gate.

## Runtime and Security Evidence

Not verified. The indexed query and driver-return feasibility checks, actual heap and reply bounds, SDK acceptance and native stdio tests are required as described in the [store contract](contracts/store-and-lifecycle.md) and [quickstart](quickstart.md). No security exception or real-account verification is in scope.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-22 | 0d4c7fa | CONCERNS | |
| design | 2026-11-23 | 7d150f9 | PASS | |

- **Scope of the 7d150f9 PASS:** Design only. Feasibility is still Pending, so this verdict does not clear `/speckit-implement`. A fresh PASS must cover committed Verified evidence and the fixed production ceilings. Non-blocking follow-ups: verify reduced-side reporting at non-default sizes above the context-row cap, and include an expansion-heavy Unicode variant in the feasibility review.

- **Search coverage:** The [preliminary probe](feasibility.md#preliminary-windows-probe-2026-11-23) confirmed the original limits reject representative searches. The [bounded batch probe](feasibility.md#bounded-batch-probe-2026-11-23) met those fixtures' timing on the global query alone. Full verification is still required before selecting production ceilings. The no-continuation policy for unfinished search remains unchanged.
- **Two-stage gate:** The read-only review does not clear production implementation. Remediation requires a fresh design PASS covering the committed [feasibility record](feasibility.md); the hook refuses even an accepted CONCERNS for this feature. The current record is pending. SQLite plans, latency, heap, connection return and native behavior were not verified in the initial review.
