# whatsapp-mcp Agent Knowledge

## Architecture Overview

A WhatsApp MCP server in one Go binary, `whatsapp-mcp` (`cmd/whatsapp-mcp`). No Docker, no PostgreSQL, no C
compiler. The WhatsApp client is whatsmeow, pinned in `go.mod`. One Go module; code lives in `internal/...`.

**Packages:** `cmd/whatsapp-mcp` (commands, flags, logger, shutdown lifecycle), `internal/config` (layered settings;
a new setting is one descriptor in `setting.go` plus one `Config` field), `internal/store` (the one SQLite file
`whatsapp.db`: whatsmeow device schema, message schema, the unsafe-run marker, repair markers, offline recovery),
`internal/wa` (the connection owner, the pinned session adapter, the network fence and the quiet-stop check),
`internal/wasim` (a simulated WhatsApp server and phone, tests only), `internal/lock`, `internal/private`,
`internal/buildinfo`, `internal/walog` (the whatsmeow log bridge).

**Commands:** `version`; `status` (offline snapshot, never connects); `pair` (QR window, offline recovery); `stdio` and
`serve` (connect a saved session; no MCP tools yet). `OPERATIONS.md` is the operator guide and keeps implemented and
verified apart.

## Specification Sources (Speckit)

### `docs/specification.md` (local, not in the repository): source of truth
`docs/` except `docs/backlog/` is kept locally. Without it, ask the user for the specification before planning; do not
infer behaviour from the code. §1–§7 describe the earlier implementation (behaviour to keep), §6 its defects (avoided
by design), §8 this project; §8.10 holds decisions D1–D18 (settled: do not reopen unless the user asks) and §8.11 the
feature security decisions. `docs/roadmap.md` orders the features. The storage spikes S1 and S2 are in
`specs/002-sqlite-storage/research.md` (appendix "Spike results").

### `specs/NNN-*/`: feature specs
Every feature so far: 001 binary skeleton, 002 SQLite storage, 003 connection, pairing and status (all merged), 004
shutdown drain (pull request #11). Each has `spec.md`, `plan.md`, `tasks.md`, contracts and a `verification.md` with
the evidence and the Owner reviews table. Nothing is archived yet (`.specify/archive/` does not exist).

### `.specify/memory/constitution.md`: principles
Read it before planning or implementing. It sets the review workflow below.

> **Lookup order:** `specs/` → `.specify/memory/` → `.specify/archive/` → `docs/specification.md`. Never assume
> behaviour — read the spec.

## Backlog (`docs/backlog/`)
Deferred work and proposals, one file per item, named after the feature that raised it. `docs/backlog/INDEX.md` lists
every item with its status and review trigger; keep it in sync.

## Owner Reviews

Every feature passes two reviews by the `owner-judge` agent (`.claude/agents/owner-judge.md`):

1. After `/speckit-tasks`, commit the feature directory and run it in **design** mode.
2. After implementation, commit the work and its `verification.md` evidence and run it in **code** mode.

Add the row it ends with to the "Owner reviews" table in `specs/NNN-*/verification.md` and commit it:

```
| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-10-28 | 8c54a83 | PASS | |
```

- The last row of a stage counts. `BLOCK`: fix and review again. `CONCERNS`: the user decides in the last column.
- After 3 `BLOCK` rounds of one stage only the user may park findings: list them in the feature's `tasks.md` under
  `## Parked findings` (review, commit, finding, blocked task), commit that alone, and write `Parked at <commit>:
  <titles>` in the last column. High-severity findings about data loss, security or a real account cannot be parked.
- `.claude/hooks/owner-review-gate.sh` refuses `/speckit-implement` without a current design PASS and `gh pr create`
  / `gh pr merge` without a current code PASS. A review is stale once a file it covered changes after its commit.
  Exempt: `verification.md`, ticking `[X]` in `tasks.md`, and the `## Current state` sections of `AGENTS.md`,
  `CLAUDE.md` and the feature's `tasks.md` (heading may carry a suffix; the section runs to the next `##`). So
  documentation fixes on a branch under review belong in those places or wait for a new review. The hook needs
  `bash` and `jq`.

## Current state

Features 001–004 are merged (004, shutdown drain: pull request #11, 2026-11-10). Feature 005, message ingest, is in
planning on branch `005-message-ingest`: plan, design and tasks are done; the design review passed at `a040210` after four
rounds; the owner ticks T004, then implementation starts. The resume point of a feature in progress is
the `## Current state` section of its `tasks.md`. Since 2026-11-13 `go.mod` replaces whatsmeow with the owner's fork
`github.com/tiger-seo/whatsmeow` at `bedcf01` (branch `receive-bounds`), which enforces the receive bounds, sends
nothing to the user's phone on another account's behalf, and carries
the lost-515 fix proposed upstream as tulir/whatsmeow#1337; E003-RX-01 is retired.

## Commands

Always `CGO_ENABLED=0`. Record every result in the feature's `verification.md` with the command, date, counts and the
exit code read from the command itself (a pipe through `tail` or `grep` hides a failure).

```
gofmt -l .                                              # must print nothing
go vet ./...
go mod verify
go test ./... -count=1 -timeout 30m                     # store tests write several GB to TEMP
for t in windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null ./cmd/whatsapp-mcp; done
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go vet ./internal/wa/testdata/cmd/session-check         # the helper is under testdata, so ./... skips it; also test and build it
```

On demand only (each guarded by an environment variable):

| Variable | What it runs |
| --- | --- |
| `WA_HEAVY_TESTS=1` | the 500 MiB listing and the 400,000-message memory check in `internal/store` (nightly CI job) |
| `WA_MEASURE=1` | `TestQuietStopMeasurement` and `TestQuietStopPassRate` (50 stops per kind) |
| `WA_SOAK=1` | `TestQuietStopSoak`, four hours on the simulator (`WA_SOAK_OUT` for the CSV) |
| `WA_APPSTATE_STOP_RUNS=N` | repeats `TestSimulatorStopDuringQueuedAppStateFetches` |

CI (`.github/workflows/ci.yml`) runs gofmt, vet, the five builds and `go test` on Linux, Windows and macOS for every
push and pull request.

## File Operations

The repository stores LF. A script that edits files on Windows must read with `newline=''` and write with
`newline='\n'`; afterwards `git ls-files --eol` must show no `w/crlf`. Do not cite requirement IDs (FR-, SC-, D3, …) in
code comments.

## Real WhatsApp Account

Never pair, connect or send on a real account without the user's permission for that step. A permitted run uses a new
empty data directory under `D:\tmp` and the binary built from a named commit; the user scans the QR code; the cleanup is
the user unlinking the device on the phone, then deleting the directory. Record redacted results in
`verification.md`; a step not run is "not verified". Automated tests use `internal/wasim` only.

## Known Issues & Gotchas

### ⚠️ The Quiet-Stop Check Sees Every Goroutine in the Process
**Symptom:** a simulator test fails with `work_running` although its own client stopped cleanly.
**Cause:** the check counts whatsmeow, libsignal and websocket goroutines process-wide. A goroutine leaked by an
earlier test in the same package fails every later check.
**Rule:** find the leaking test, not the failing one. Test rigs that open several clients ignore goroutines that
existed before the rig started. The old `readPump` leak was a library defect, fixed by the pin `f1b7985`.

### ⚠️ The whatsmeow Log Bridge Logs Format Strings, Not Arguments
**Symptom:** a debug log shows `Failed to do initial fetch of app state %s: %v` with no name and no error.
**Cause:** `internal/walog` drops the library's arguments so no key, JID or message text reaches the log.
**Rule:** do not expect the cause of a library error in a real-account log. Reproduce it on `internal/wasim` with a
recording wrapper (see `internal/wa/appstate_stop_test.go`), then decide.

### ⚠️ The Stop Order Is a Safety Invariant
**Symptom:** a change to retirement looks harmless and the order test fails, or worse, a write is lost.
**Cause:** the quiet-stop rules depend on the order work cancel → `Disconnect` → network fence → storage barrier →
library context (`contracts/quiet-stop.md` in `specs/004-shutdown-drain/`). A write after the fence closed sends
nothing, and a read refused after the barrier changes nothing and cannot reach the network, so neither fails the
check; both arguments hold only in that order.
**Rule:** never swap the fence and the barrier or cancel the library context earlier. A new store method must set the
guard's `write` flag correctly: a write marked as a read escapes the check.

### ⚠️ A whatsmeow Pin Change Is an Owner Decision
**Symptom:** a library bump looks like routine maintenance.
**Cause:** the fork's receive bounds and their approved limits are tied to the exact revision; the quiet check's
allow-list entries record the version they were reviewed for (`quiet_test.go` compares them with `go.mod`); new upstream reconnect paths must stay behind
`EnableAutoReconnect = false`; `appstate_stop_test.go` matches function names in a goroutine dump.
**Rule:** ask the owner first, check the Fork Acceptance Contract in `specs/005-message-ingest/plan.md`, read the diff for reconnect and storage changes, and
rerun the full gates and the pass-rate run.

### ⚠️ WSL2 Runs Need Three Workarounds
**Symptom:** `env: 'go': No such file or directory`, or `error obtaining VCS status: exit status 128` in
`cmd/whatsapp-mcp` and `internal/wasim`, or store tests filling `/tmp`.
**Cause:** the Linux toolchain is in `~/sdk/go1.27.3/bin`, not on the `--exec` PATH; git in WSL refuses a repository
on `/mnt/d` owned by another user; `/tmp` is a small tmpfs.
**Rule:** `wsl -d Ubuntu --cd /mnt/d/projects-mou/whatsapp-mcp-next --exec bash -lc 'export
PATH=$HOME/sdk/go1.27.3/bin:$PATH TMPDIR=/var/tmp CGO_ENABLED=0; go test -p 1 ./... -count=1 -timeout 20m'`, with
the repository in git's `safe.directory` inside WSL. The VM can take up to half the RAM; with little free memory the
run is killed. Record results as WSL2, not native Linux.

### ⚠️ stdout Is the MCP Protocol in `stdio` Mode
**Rule:** logs and human messages go to stderr; a test asserts stdout stays empty after a stop.
