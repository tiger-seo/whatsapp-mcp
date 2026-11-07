# Verification: Reusable Session After a Proven Quiet Stop

Evidence for feature 004. Task T003 adds the other sections (handoff, E003-RX-01 decision, measurement and deadline
approval, simulator results, pass-rate run, platform evidence, real-account journey, not verified).

## E003-RX-01 (implementation start)

Facts collected 2026-11-07T21:53Z from `specs/003-whatsapp-pairing-status/verification.md`:

- Exception: E003-RX-01, F09 limited to upstream receive expansion; approved by `tiger-seo` on 2026-11-02 for
  feature 003's private, local, single-owner deployment.
- Expiry: the start of 2026-12-02 UTC, no automatic renewal. Not expired on 2026-11-07.
- Pin: `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b` in `go.mod`; it matches the exception.
- Incidents: none recorded in the available project evidence.
- This feature makes long connected `stdio` and `serve` runs possible for the first time (today every connect leaves
  the marker unsafe). That is the exception's own trigger ("before another feature materially expands the receive
  workload"), and constitution X allows no inheritance by another feature.

**Owner decision, 2026-11-07 (`tiger-seo`):** E003-RX-01 is extended to feature 004 as proposed:

1. Scope: feature 004 only, in the same private, local, single-owner deployment. Message ingest (005), a listener or
   any shared deployment needs a new decision.
2. Same pin and same expiry (start of 2026-12-02 UTC, no renewal). Feature 004's code review must happen before then.
3. Earlier trigger: a resource incident (memory or goroutine growth in a long run) or a pin change stops continued
   operation until the owner decides again.
4. Compensating control: the exception's long-run monitoring is exercised during this feature's verification, with a
   short soak run run as T046 and recorded in the roll-up (T047). The simulator cannot show real receive volume; that stays "not
   verified".
5. Not covered: stop and drain ordering. It is what this feature implements, so it is its own acceptance gate.

This is a feature-level extension recorded by the owner. It does not close F09 and does not amend the constitution.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-07 | 11ac064 | BLOCK | |
| design | 2026-11-07 | 14f648a | BLOCK | |
| design | 2026-11-07 | 8c32753 | CONCERNS | E003-RX-01 extended to feature 004 as proposed, 2026-11-07 (section "E003-RX-01 (implementation start)") |
| design | 2026-11-08 | 4bfd39e | CONCERNS | |
| design | 2026-11-08 | 39762a8 | BLOCK | |
