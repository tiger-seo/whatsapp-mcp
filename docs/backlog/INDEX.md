# Backlog index

Proposals and deferred work, one file per item. The file name starts with the feature that raised the item. Keep
this table in step with the files: add a row for each new item, and update its status when the item's status line
changes.

| Item | Created | Status | Review trigger |
| --- | --- | --- | --- |
| [Security hardening of feature 001](001-binary-skeleton-security-hardening.md) | 2026-10-27 | Implemented on branch `001-security-hardening`; not yet committed, pushed or run in CI | — |
| [Upstream receive-resource bounds](003-whatsmeow-receive-resource-bounds.md) | 2026-11-02 | Open; temporary exception E003-RX-01, extended to feature 004 and the pin `f1b7985` | Expires at the start of 2026-12-02 UTC; earlier on a release, an upstream fix or a resource incident |
| [Post-pair reconnect and the unsafe-run marker](003-post-pair-reconnect-and-unsafe-marker.md) | 2026-11-06 | Open; debts 1, 2 and 4 delivered on branch `004-shutdown-drain` and run on one real account (code review pending), debt 3 and stage 2 open | Before feature 005's (message ingest) real-account test, before any release, on an upstream fix; together with E003-RX-01 |
| [Heavy store test wall-clock limit](003-heavy-test-wall-clock-limit.md) | 2026-11-07 | Open; cause not identified | Next nightly `heavy` failure, or before a release |
| [Offline recovery prompt usability](003-offline-recovery-prompt-usability.md) | 2026-11-07 | Open; not started | Before the full-branch code review of feature 003 |
