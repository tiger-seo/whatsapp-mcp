# Backlog: let `pair` receive the initial history

- **Created:** 2026-11-13
- **Status:** Deferred by the owner on 2026-11-13; the gap is accepted for feature 005.
- **Affected area:** `pair` (`cmd/whatsapp-mcp`, `internal/wa` owner), feature 005 history ingest.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** a real-account `pair` whose initial history is mostly reported as incomplete, or before the first
  release.
- **Source:** `specs/005-message-ingest/spec.md` (clarification 2026-11-13), `research.md` R4.

## Problem

WhatsApp sends the initial history to the newly linked device, which is `pair`'s post-pair connection. `pair` stops
soon after it confirms the link. The library acknowledges each history notification when it arrives, so a
notification `pair` has not stored when it stops is reported as `history_incomplete` and is not sent again.

## Options

1. A bounded wait in `pair` after the link is confirmed, until the history queue is idle or a time limit passes. This
   changes feature 004's `pair` contract and its stop timing.
2. `DisableManualHistorySyncReceipt` while `pair` runs, so WhatsApp may resend to the next connection. Whether it does
   is not established.
