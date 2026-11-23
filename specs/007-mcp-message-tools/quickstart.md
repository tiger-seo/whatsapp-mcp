# Quickstart: Offline Message Tool Validation

Run from the repository root after the feature tests are implemented. Use the pinned toolchain in `go.mod`, `CGO_ENABLED=0`, and a temporary directory with space for seeded SQLite files. All feature tests must create their own private temporary directory and use the offline account fake. Do not point `stdio` at your regular data directory or run `pair`.

## Feature Acceptance

In PowerShell:

```powershell
$env:CGO_ENABLED = '0'
go test -count=1 -timeout 5m -v ./internal/store ./internal/mcp ./cmd/whatsapp-mcp -run '^Test(MCPMessage|ToolMessage)'
```

Use the prefixes above for the new test groups. Confirm named tests actually ran in each package; `no tests to run` is not evidence. Run the same command on native Windows, Linux and macOS CI with `CGO_ENABLED=0` set in the native shell, not by cross-running binaries.

| Fixture | Expected observation |
| --- | --- |
| Mixed chats, literal text and caption, aliases and tied instants | Discover exactly the three new tools in addition to the existing ones; verify every documented argument/default, newest-first pages, and case-insensitive literal matches over original text and caption. |
| Exact, missing and repeated IDs | Exact chat never falls back. ID alone is unique or returns the expected not-found/ambiguity. Verify 2, 100 and 101 matching-chat boundaries and no leakage outside the reply. |
| Direct, group, self-chat and no-history contact | All mapped spellings agree; unrelated direct/group messages are excluded; only self-chat qualifies for the owner identity. |
| Context on and off, limits and edges | Explicit zeros are honoured. Same-chat neighbours are closest first for selection and chronological in output. Flag a reduced side only when an available neighbour was omitted. |
| 50 maximum-field-size base matches and five neighbours on each side | Default context returns a nonempty success under the existing budget, and continuation through unchanged data visits all 50 identities exactly once. Repeat with escape-heavy text/captions that cannot fit whole. Check that any preview is marked and exact lookup never previews. |
| A partial search at its work limit, contrasted with a verified oversized reply | The former returns no messages or continuation and suggests narrower filters. The latter trims or resumes only as permitted by the [tool contract](contracts/tools.md). |
| Search coverage fixtures from [feasibility.md](feasibility.md) | Verify the exact sparse and empty results across 400,000 short rows and the older match after 128 maximum-size nonmatching rows. Check the fixed work ceilings separately; the caller receives no partial search if a ceiling prevents verification. |
| Concurrent ingest, cancelled reader and stalled output | Verify query plan/byte ceilings, actual reader return, writer progress, fence ordering, and tool credit through acknowledged or failed writes. Preserve redacted stderr and protocol-only stdout. No account call, result file or conversation write from the tool. |

## Regression Gates

```powershell
gofmt -l .
go vet ./...
go mod verify
go test ./... -count=1 -timeout 30m
```

Expect no formatting output or failing commands. Use [CI](../../.github/workflows/ci.yml) for the five CGO-free cross-builds, vulnerability check and native matrix. Record the date, commit, platform, commands, named results, bounds and exit codes in `verification.md` when checks are run. Mark skipped or unrun coverage not verified; a successful document check is not runtime evidence.
