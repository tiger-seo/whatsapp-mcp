# Backlog: library maps that grow per account or group

- **Created:** 2026-11-13
- **Status:** Accepted by the owner on 2026-11-13; not bounded.
- **Affected area:** the pinned `go.mau.fi/whatsmeow` fork (`tiger-seo/whatsmeow@9209108`), feature 005 ingest.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** on a pin change, a memory incident, or before a release meant for accounts with very many
  contacts or groups.
- **Source:** `specs/005-message-ingest/plan.md` (Constitution Check), `research.md` R2.

## Problem

Four maps in the client library gain one entry for each distinct account or group they see and never shrink:
`tcTokenSenderTS`, `groupCache`, `userDevicesCache` and `sessionRecreateHistory`. Another account can add entries by
messaging from new accounts or adding the account to new groups. Growth is per distinct account or group, not per
message, and lasts until the process stops; each generation builds a new client.

## Follow-up

1. Measure the size per entry and the count after a long run, on the simulator first.
2. If needed, add a cap or an eviction to the fork, which is a pin change and goes to the owner.
