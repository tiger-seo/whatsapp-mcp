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
- `.github/workflows/ci.yml`: one `check` job (gofmt, go vet, five cross-builds) and `go test` on Linux, plus
  Windows and macOS for pull requests and `main`.

Spike results are in `docs/spikes.md`.
S1 (§8.5) is done: `sqlstore` works on `modernc.org/sqlite` with the dialect name `"sqlite"`. Nothing is
imported from an earlier deployment (D16), so the I3 spike is closed and there is no `import` command.

The work is split into Spec Kit features in `docs/roadmap.md`, to be implemented one at a time in its order.

## Conventions

- One Go module at the root; the binary is `whatsapp-mcp` (`cmd/whatsapp-mcp`); code goes in `internal/...`
  (§8.3 A4).
- Always `CGO_ENABLED=0`. Never add a dependency that needs CGO.
- In stdio mode, stdout carries the MCP protocol, so logs go to stderr.
- Requirement IDs (G1, M2, D3, …) are for planning documents. Don't cite them in code comments.
- Never act on a real WhatsApp account (pairing, sending) without the user's permission for that step.
