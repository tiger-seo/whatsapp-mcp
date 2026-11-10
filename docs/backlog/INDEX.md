# Backlog index

Proposals and deferred work, one file per item. The file name starts with the feature that raised the item. Keep
this table in step with the files: add a row for each new item, and update its status when the item's status line
changes.

| Item | Created | Status | Review trigger |
| --- | --- | --- | --- |
| [Security hardening of feature 001](001-binary-skeleton-security-hardening.md) | 2026-10-27 | Implemented on branch `001-security-hardening`; not yet committed, pushed or run in CI | — |
| [Upstream receive-resource bounds](003-whatsmeow-receive-resource-bounds.md) | 2026-11-02 | Open; temporary exception E003-RX-01, extended to feature 004 and the pin `f1b7985` | Expires at the start of 2026-12-02 UTC; earlier on a release, an upstream fix or a resource incident |
| [Heavy store test wall-clock limit](003-heavy-test-wall-clock-limit.md) | 2026-11-07 | Open; cause not identified | Next nightly `heavy` failure, or before a release |
| [Offline recovery prompt usability](003-offline-recovery-prompt-usability.md) | 2026-11-07 | Open; not started | Before the full-branch code review of feature 003 |
| [The pinned whatsmeow can drop the post-pair restart request](004-whatsmeow-lost-restart-request.md) | 2026-11-10 | Open; known limitation of feature 004, fix proposed upstream (tulir/whatsmeow#1337) | When #1337 is merged or rejected, on the next pin change, or a real `pair` that ends incomplete after the phone confirmed |
