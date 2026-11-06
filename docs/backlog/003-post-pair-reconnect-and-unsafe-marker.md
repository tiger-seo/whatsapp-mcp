# Backlog: post-pair reconnect and the unsafe-run marker (feature 003 debt)

- **Created:** 2026-11-06
- **Status:** Open. The owner approved enabling `OwnerConfig.PostPairReconnect` for the `pair` command on 2026-11-06. Still to do: a permissioned real-account re-check and a fresh code review of the enabled path.
- **Affected features:** `specs/003-whatsapp-pairing-status` now; feature 004 (history sync) is blocked by the second item below.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** before feature 004's real-account test, before any release, and on any relevant upstream whatsmeow fix. Keep it next to the E003-RX-01 expiry (start of 2026-12-02 UTC) and review both together.
- **Source:** `specs/003-whatsapp-pairing-status/verification.md` ("Real-Account Observation", "T077 Candidate Post-Pair Reconnect Against the Simulator", "T077 Decision (approved)"), roadmap "Follow-up: WhatsApp logout and shutdown drain".

## Debt

1. **Unjoined upstream work.** `go sendUnifiedSession`, goroutines started when the handler queue overflows, and any node handler that outlives 10 x 30 s are not joined. After a post-pair reconnect on the same generation, a late send from them can still arrive. Not proven: a held pinned node handler or `LoggedOut` dispatch across Disconnect and retry, and the full cancel, retire and stop matrix with the real client.
2. **No reusable session.** Any production Connect leaves the run marker unsafe, so every pairing needs a separately authorized offline recovery before the next run. Until this is solved there is no connected session `stdio` or `serve` can reuse, and feature 004's real-account history sync cannot run.
3. **Thin evidence.** Simulator and Windows only for the candidate; one real-account pair run (515 about 0.6 s after `PairSuccess`; the link failed with the follow-up denied). No Linux or macOS run of the enabled path.
4. **A risky path behind a code-level switch.** `PostPairReconnect` must not become permanent by default or by being forgotten.

## Follow-up

1. Find and verify a way to drain or account for all relevant upstream work after a Connect (the roadmap's "logout and shutdown drain" item), so the marker can be cleared after an orderly stop without offline recovery.
2. Replace the code-level switch and the unsafe-marker policy with that mechanism, or close this item by recording a reviewed decision to keep them.
3. Re-run the enabled path on Linux and macOS and with the real client's cancel, retire and stop matrix.
4. Recheck upstream whatsmeow for a fix to the unjoined goroutines.

## Closure

Close when the marker clears safely after an orderly stop and the switch is gone, or when the owner records a reviewed decision to accept the current policy permanently. Do not close it by silence.
