# Backlog: upstream receive-resource bounds for feature 003

- **Created:** 2026-11-02
- **Status:** Open; temporary approved exception E003-RX-01, not resolved or parked.
- **Affected feature:** `specs/003-whatsapp-pairing-status` and later features expanding its receive workload.
- **Owner:** Maintainer `tiger-seo`, explicitly confirmed by the user.
- **Expiry:** 2026-12-02 at the start of the UTC date; no automatic renewal.
- **Earlier review:** Before first public release/broader deployment; promptly on a relevant upstream fix or resource incident.
- **Source:** `docs/security-review.md` F09; feature research R12-R17; canonical scope/controls/evidence in `specs/003-whatsapp-pairing-status/verification.md`.

## Evidence

Pinned whatsmeow has no expanded-frame limit and starts a sender per queue overflow. Synthetic tests confirmed 65,770 bytes expanding to 32 MiB and 2,048 overflow nodes adding 2,048 goroutines. Senders exited on connection-context cancellation; no ordinary-contact exploit or permanent post-cancellation leak was demonstrated. Related decoded/slow-handler retention prevents claiming a full upstream receive-memory ceiling; not every path was reproduced.

Inspected HEAD `54038813c47f3f351eb6fcaafab0e1b99d676850` (2026-11-02) is seven commits newer but fixes neither primary path. Recheck upstream at review time rather than treating this as permanent.

## Follow-up

1. Reassess before expiry/trigger and record a new decision/evidence. Pause the triggering release/deployment or continued operation pending review; never silently renew.
2. Prefer a small upstream-compatible mitigation for decompression/decoder allocation and nonblocking finite admission, preserving IQ responses and budget ownership across reconnects/detached work.
3. Explicitly approve compatibility limits; proposed 16-MiB/64-MiB research values are not accepted thresholds.
4. Obtain separate approval for any fork/provenance/license/rebase maintenance, publication or new exception. This entry authorizes none.
5. Verify normal/negative input, cleanup, resource accounting and native platforms. Keep private storage, bounded device/application work, credential secrecy and account permissions mandatory throughout.

## Closure

Close after a reviewed mitigation enforces the accepted policy and retires the exception, or a separately explicit product/constitution process changes the requirement. Renewal or an operator restart is not closure. Preserve historical approvals/evidence and the original threat-model review.
