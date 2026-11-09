# whatsapp-mcp

A WhatsApp MCP server in a single Go binary. It needs no Docker, no PostgreSQL and no C compiler. The
WhatsApp client is whatsmeow.

## Start here

`docs/` (the specification, the spike results and the roadmap) is kept locally and is not in the repository.
Without it, ask the user for the specification before planning; don't guess the behaviour from the code.

- `docs/specification.md` is the source of truth. Read §8 before planning or writing code.
  - §1–§7 describe existing behaviour. Use them as the reference for behaviour that must be kept.
  - §6 lists known defects in that behaviour. This project avoids them by design.
  - §8.10 records decisions D1–D18. They are settled, so don't reopen them unless the user asks.

## Current state

Features 001 (binary skeleton, pull request #1), 002 (SQLite storage, pull request #5) and 003 (WhatsApp
connection, pairing and status, pull request #9) are implemented, verified and merged into `main`. The code:

- `cmd/whatsapp-mcp`: the commands, flag parsing, the logger and the shutdown lifecycle. `version`, `status` (offline
  local snapshot) and `pair` (QR window, offline recovery) work; `stdio` and `serve` open the store and the owner but
  have no MCP tools yet.
- `internal/config`: layered settings and the data directory. Every setting is one descriptor in the
  registry in `internal/config/setting.go`; a new setting is one descriptor plus one `Config` field.
- `internal/lock`: the single-instance lock on the data directory.
- `internal/buildinfo`: version, commit and build date.
- `internal/store` (feature 002): the one SQLite file `whatsapp.db` with the whatsmeow device schema and the message
  schema (embedded migrations), the approved limits, the write side (`StoreMessages`, `UpdateChats`, `MergeLID`), the
  read side (the chat, contact and message queries in `queries.go` and `contacts.go`) and the lifecycle (`Open`,
  `Close`, `Device`). `stdio`, `serve` and `pair` open it after the lock. `OPERATIONS.md` is the operator guide. A
  LID sender whose name is only on the phone-number chat has no sender name until `MergeLID` runs (owner decision in
  `specs/002-sqlite-storage/verification.md`). The store tests write several GB of temporary databases, so `TEMP`
  needs room; two heavy tests (a 500 MiB listing and a 400,000-message memory check) run only with
  `WA_HEAVY_TESTS=1`.
- `internal/private`: also `CheckFile` and file identities, for the database and its sidecars.
- `.github/workflows/ci.yml`: one `check` job (gofmt, go vet, five cross-builds) and `go test` on Linux, Windows and
  macOS for every push and pull request (the Windows job keeps its temporary directory on the `D:` drive, which is
  about 12 times faster than the runner's default). A nightly `heavy` job on Linux runs the heavy store tests when
  `main` has changed; it can also be started by hand.

Feature 003 added (owner reviews: design PASS at 8edc293, code PASS at 71edddf and ecd18b1; accepted risks in
`specs/003-whatsapp-pairing-status/verification.md`):

- `internal/wa`: the connection owner (state, epochs, one attempt at a time, watchdog, pairing windows), the pinned
  whatsmeow session adapter, the version provider, the app-state repair controller (`recovery.go`) and the bounded
  HTTP client. Every automatic reconnect is denied by default; the one exception is the connection WhatsApp requests
  right after pairing, which `pair` makes on a fresh client after the pairing client was retired and checked (feature
  004; the `PostPairReconnect` switch is gone). The 405 budget, startup retries and ordinary reconnect are deferred
  to the roadmap follow-up "logout and shutdown drain".
- `internal/wasim`: a simulated WhatsApp server and phone for tests only.
- `internal/store`: the unsafe-run marker (schema 3), repair markers, the read-only `Inspect` view and offline
  recovery. The marker stays unsafe after a failed quiet-stop check, an unresolved pairing, a storage fault or a kill,
  until a separately authorized offline recovery (`pair`, interactive terminal).
- `internal/lock`: also `AcquireExisting`, for `status`.
- Evidence is Windows, WSL2, Linux/Windows/macOS CI runners and one real account on Windows. Not verified: another
  account or phone, native Linux or macOS runs, native console/PTY recovery, macOS `F_FULLFSYNC`, power loss.
  `OPERATIONS.md` separates implemented from verified.

Feature 004 (shutdown drain, branch `004-shutdown-drain`, not merged) is implemented and was run on one real account.
Its code review is **BLOCK** (4d65199) and T047 is open; the cause is a defect in the client library, with a fix in a
fork (see the feature's "Current state"). It adds the network fence and the quiet-stop check (`internal/wa/fence.go`,
`quiet.go`), the fresh-client post-pair link in `internal/wa/owner.go`, and the stop lines of `stdio`, `serve` and
`pair`. The deadlines (1 s, 2 s) and the allow-list (empty) are in `quiet.go`. Evidence is in
`specs/004-shutdown-drain/verification.md`. E003-RX-01 expires at the start of 2026-12-02 UTC.

The current state of the feature being worked on can be recorded in a "## Current state" section of that feature's
`specs/NNN-*/tasks.md` (the resume point for a fresh session: what is done, what is open, what waits for the owner).
Keep this file's summary short and put the detail there.

Spike results are in `docs/spikes.md`.
S1 (§8.5) is done: `sqlstore` works on `modernc.org/sqlite` with the dialect name `"sqlite"`. Nothing is
imported from an earlier deployment (D16), so the I3 spike is closed and there is no `import` command.

The work is split into Spec Kit features in `docs/roadmap.md`, to be implemented one at a time in its order.

## Owner reviews

Every feature passes two reviews by the `owner-judge` agent (`.claude/agents/owner-judge.md`); the
constitution's Development Workflow sets the rule.

1. After `/speckit-tasks`, commit the feature directory and run owner-judge in **design** mode.
2. After implementation, commit the work and its `verification.md` evidence and run it in **code** mode.

Add the row the agent ends with to the "Owner reviews" table in `specs/NNN-*/verification.md` and commit it:

```
## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-10-28 | 8c54a83 | PASS | |
```

The last row of a stage counts. `BLOCK` means fix and review again; `CONCERNS` needs the user's decision in
the last column.

After 3 `BLOCK` rounds of one stage the user may park the last round's findings (constitution, Development
Workflow). Only the user decides this. List each parked finding in the feature's `tasks.md` under
`## Parked findings`, with the review, its commit, the finding and the task or phase it blocks, and mark those
tasks blocked. Commit that change on its own, then put `Parked at <that commit>: <finding titles>` in the
review's last column. A parked finding is resolved and reviewed before the task it blocks starts, and before the
code review at the latest. High-severity findings about data loss, security or a real WhatsApp account cannot be
parked. `.claude/hooks/owner-review-gate.sh` (wired in `.claude/settings.json`) refuses
`/speckit-implement` without a passing design review and `gh pr create` / `gh pr merge` without a passing
code review, and treats a review as stale once anything it covered changed after the recorded commit (after
the parking commit, for a parked `BLOCK`).
Changing `verification.md` never makes a review stale, and neither does ticking tasks `[X]` in `tasks.md` when
nothing else in it changed. On a branch without a feature directory the hook
asks for confirmation instead; put the verdict in the pull request. The hook needs `bash` and `jq`.

## Conventions

- Ubuntu WSL2 is available for Linux tests from this Windows workspace. From PowerShell, run for example
  `wsl -d Ubuntu --cd /mnt/d/projects-mou/whatsapp-mcp-next --exec env CGO_ENABLED=0 go test ./... -count=1 -timeout 10m`.
  Use a Linux Go toolchain compatible with `go.mod` and Linux temporary storage with enough free space for the
  store tests. Record WSL2 results as such; they do not establish native Linux hardware or CI results.
- One Go module at the root; the binary is `whatsapp-mcp` (`cmd/whatsapp-mcp`); code goes in `internal/...`
  (§8.3 A4).
- Always `CGO_ENABLED=0`. Never add a dependency that needs CGO.
- In stdio mode, stdout carries the MCP protocol, so logs go to stderr.
- Requirement IDs (G1, M2, D3, …) are for planning documents. Don't cite them in code comments.
- Never act on a real WhatsApp account (pairing, sending) without the user's permission for that step.
