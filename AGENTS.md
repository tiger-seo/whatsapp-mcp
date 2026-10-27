# whatsapp-mcp-next

A WhatsApp MCP server in a single Go binary. It needs no Docker, no PostgreSQL and no C compiler. The
WhatsApp client is whatsmeow.

## Start here

- `docs/specification.md` is the source of truth. Read §8 before planning or writing code.
  - §1–§7 describe existing behaviour. Use them as the reference for behaviour that must be kept.
  - §6 lists known defects in that behaviour. This project avoids them by design.
  - §8.10 records decisions D1–D18. They are settled, so don't reopen them unless the user asks.

## Current state

The repository holds only the specification; there is no code yet. Spike results are in `docs/spikes.md`.
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
