# Backlog: recover "unavailable" placeholder messages from the phone

- **Created:** 2026-11-13
- **Status:** Deferred by the owner on 2026-11-13; feature 005 counts placeholders and does not recover them.
- **Affected area:** the pinned fork (`tiger-seo/whatsmeow@bedcf01`, contract item 9), feature 005 ingest.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** the send-approval decision (security review F01), a real run where the `unavailable` count in
  `ingest_stopped` is high, or before the first release.
- **Source:** `specs/005-message-ingest/spec.md` (clarification 2026-11-13), `research.md` R5, plan contract item 9.

## Problem

WhatsApp sends a linked device an `unavailable` placeholder instead of a message when the message is view-once, or
when the sender's device did not encrypt it for this device (type ""). Upstream asks the user's phone for the message
and stores the resend. Because any account can cause a placeholder, the pinned fork asks only when
`AutomaticMessageRerequestFromPhone` is set, which this project leaves off. Ordinary placeholder messages are
therefore acknowledged, counted and lost.

## Options

1. An opt-in setting that turns the request back on, with a rate limit and a bound on outstanding requests, run off
   the handler goroutine so a burst cannot stall live ingest.
2. Request only for chats or senders the owner has allow-listed.

Either is an account action on another account's trigger, so it needs the owner's send-approval policy first.
