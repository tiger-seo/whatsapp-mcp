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

Feature 001 (binary skeleton) is implemented and verified, including CI; it is done once pull request #1 is
merged into `main`. The code:

- `cmd/whatsapp-mcp`: the commands, flag parsing, the logger and the shutdown lifecycle. Only `version`
  does real work yet.
- `internal/config`: layered settings and the data directory. Every setting is one descriptor in the
  registry in `internal/config/setting.go`; a new setting is one descriptor plus one `Config` field.
- `internal/lock`: the single-instance lock on the data directory.
- `internal/buildinfo`: version, commit and build date.
- `internal/store` (feature 002, in progress on branch `002-sqlite-storage`): the one SQLite file `whatsapp.db`
  with the whatsmeow device schema and the message schema (embedded migrations), the approved limits, the write
  side (`StoreMessages`, `UpdateChats`, `MergeLID`) and the lifecycle (`Open`, `Close`, `Device`). `stdio`,
  `serve` and `pair` open it after the lock. The chat, contact and message queries (tasks T039-T046, T057-T058)
  wait for parked findings P1-P6 in `specs/002-sqlite-storage/tasks.md`. `OPERATIONS.md` is the operator guide.
- `internal/private`: also `CheckFile` and file identities, for the database and its sidecars.
- `.github/workflows/ci.yml`: one `check` job (gofmt, go vet, five cross-builds) and `go test` on Linux, plus
  Windows and macOS for pull requests and `main`.

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

- One Go module at the root; the binary is `whatsapp-mcp` (`cmd/whatsapp-mcp`); code goes in `internal/...`
  (§8.3 A4).
- Always `CGO_ENABLED=0`. Never add a dependency that needs CGO.
- In stdio mode, stdout carries the MCP protocol, so logs go to stderr.
- Requirement IDs (G1, M2, D3, …) are for planning documents. Don't cite them in code comments.
- Never act on a real WhatsApp account (pairing, sending) without the user's permission for that step.
