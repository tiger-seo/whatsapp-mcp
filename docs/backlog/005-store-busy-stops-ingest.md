# Backlog: store admission pressure can stop ingest

- **Created:** 2026-11-13
- **Status:** Addressed for feature 006's chat/contact reads; reassess for future readers and real `busy` incidents.
- **Affected area:** `internal/store` admission (16 active, 64 waiting, 5 s wait), feature 005 ingest, features
  006–007 MCP read tools.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** a new store reader or admission-policy change, or an `ingest_failed` with error class `busy`.
- **Source:** `.specify/archive/005-message-ingest/research.md` R9.

## Problem

Feature 005 treats every message-store error on a write, including `ErrBusy`, as terminal: ingest stops and the
connected command exits 1 (spec clarification: storage temporarily unavailable stops ingest). In feature 005 the only
store users are ingest's own goroutines, so admission is not under pressure. Features 006–007 add MCP reads to the
same store; a burst of reads could fill the waiting queue, a message write could then get `ErrBusy`, and the whole
command would stop.

## Feature 006 resolution

One tool can take an immediately available store slot and reader; tools never queue or bypass waiting ordinary
work. The gate preserves ordinary capacity, cancels for ordinary contention, and retains accounting until the
read connection returns. A WAL drain fence protects the next writer at the transaction boundary. The synthetic
[pressure and native evidence](../../specs/006-mcp-stdio-tools/verification.md#t035-resource-measurements-2026-11-20-utc)
covers this policy, not every storage failure. Ordinary write `ErrBusy` remains terminal, without a retry;
a failed drain can still fail ingest and close stdio unsuccessfully. This is not approval for new reader workloads.
