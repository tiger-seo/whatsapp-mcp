# Backlog index

Proposals and deferred work, one file per item. The file name starts with the feature that raised the item. Keep
this table in step with the files: add a row for each new item, and update its status when the item's status line
changes.

| Item | Created | Status | Review trigger |
| --- | --- | --- | --- |
| [Upstream receive-resource bounds](003-whatsmeow-receive-resource-bounds.md) | 2026-11-02 | Closed 2026-11-13; fork `tiger-seo/whatsmeow@9209108` pinned, E003-RX-01 retired | – |
| [Heavy store test wall-clock limit](003-heavy-test-wall-clock-limit.md) | 2026-11-07 | Open; cause not identified | Next nightly `heavy` failure, or before a release |
| [Offline recovery prompt usability](003-offline-recovery-prompt-usability.md) | 2026-11-07 | Open; not started | Before the first release |
| [The pinned whatsmeow can drop the post-pair restart request](004-whatsmeow-lost-restart-request.md) | 2026-11-10 | Open; known limitation of feature 004, fix proposed upstream (tulir/whatsmeow#1337) and carried by the pinned fork since 2026-11-13 | When #1337 is merged or rejected, on the next pin change, or a real `pair` that ends incomplete after the phone confirmed |
| [`pair` can wait without a bound after a refused early 515](004-pair-wait-after-refused-515.md) | 2026-11-10 | Open; found by code reading, not reproduced | Before the first release, or when `pair` or `waitSavedConnection` changes |
| [Library maps that grow per account or group](005-whatsmeow-per-account-maps.md) | 2026-11-13 | Accepted by the owner; not bounded | On a pin change, a memory incident, or before a release for accounts with very many contacts or groups |
| [Let `pair` receive the initial history](005-pair-initial-history-wait.md) | 2026-11-13 | Deferred by the owner; gap accepted for feature 005 | A real `pair` whose initial history is mostly incomplete, or before the first release |
| [Store admission pressure can stop ingest](005-store-busy-stops-ingest.md) | 2026-11-13 | Open; planning input for feature 006 | Planning of feature 006, or an `ingest_failed` with class `busy` |
| [`serve` and `stdio` keep waiting after a device storage fault](005-device-fault-command-wait.md) | 2026-11-13 | Open; moved out of feature 005 | The next feature that changes the connected commands' lifecycle, or a real run that stays up after a fault |
| [Recover "unavailable" placeholder messages from the phone](005-placeholder-phone-request.md) | 2026-11-13 | Deferred by the owner; placeholders counted, not recovered | The send-approval decision (F01), a high `unavailable` count in a real run, or before the first release |
