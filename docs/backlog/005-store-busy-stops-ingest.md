# Backlog: store admission pressure can stop ingest

- **Created:** 2026-11-13
- **Status:** Open; a planning input for feature 006.
- **Affected area:** `internal/store` admission (16 active, 64 waiting, 5 s wait), feature 005 ingest, features
  006–007 MCP read tools.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** planning of feature 006, or an `ingest_failed` with error class `busy` in any run.
- **Source:** `specs/005-message-ingest/research.md` R9.

## Problem

Feature 005 treats every message-store error on a write, including `ErrBusy`, as terminal: ingest stops and the
connected command exits 1 (spec clarification: storage temporarily unavailable stops ingest). In feature 005 the only
store users are ingest's own goroutines, so admission is not under pressure. Features 006–007 add MCP reads to the
same store; a burst of reads could fill the waiting queue, a message write could then get `ErrBusy`, and the whole
command would stop.

## Options

1. Reserve admission for writes, so reads cannot starve them.
2. Bound MCP read concurrency below the store's limit in the tool layer.
3. Treat `ErrBusy` on a write as retryable once within the handler's time, which changes the spec's clarification and
   needs the owner.
