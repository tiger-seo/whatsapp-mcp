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

Editorial note, 2026-11-08: item 4 originally read "a short soak run recorded in the roll-up (T046)". After the
design reviews the soak run became its own task T046 (4 hours, simulator) and the roll-up became T047, so the
reference was renumbered. The substance of the decision is unchanged.

This is a feature-level extension recorded by the owner. It does not close F09 and does not amend the constitution.

## Handoff

### Design review round 6 (PASS at ea9234b): notes for implementation

Low findings and observations the reviewer left open. They did not block, and the feature documents were not edited
after the PASS so that the verdict stays current. Take them into account when implementing:

1. Contract "Three contexts", the bullet on the work context still says `gen.work` covers "the abort of a Connect that
   has not returned". Follow step 4, the pairing paragraph and the data-model row instead: `gen.work` never reaches a
   Connect context. Fix the bullet when the contract is next touched.
2. Window (b): recognise "aborted through the recorded abort" with `context.WithCancelCause` and a sentinel cause
   (`errors.Is(context.Cause(ctx), errAttemptAborted)`), never with `ctx.Err() != nil` or a sticky flag, which would
   hide a Connect after step 9. Add a negative T013 (b) variant: a Connect after step 9 is counted.
3. On the post-pair path `accountWork` is set early in the owner's critical section, so a stop in window (b) on the
   fresh generation is still checked (conservative). State one rule for both paths or limit the "not checked"
   assertion to the startup and pairing Connect.
4. Window (a) is detectable (the owner finds the abort already called when it clears it). Consider failing the
   generation's check deterministically whenever it fires.
5. The QR emitter calls `cli.Disconnect()` itself when its context ends (`qrchan.go` near 125-131). T013's stop-in-open-
   pairing-window case and T014 should expect it.
6. T013's pairing-generation write case is synthetic. Add the realistic variant (`handlePair`'s `Store.Save(ctx)` and
   `sendNode(ctx)`, `pair.go` near 226 and 243) during an open window.
7. A third interleaving, the abort called before the retire bit is set when `Session.Connect` runs, fails closed (the
   generation is checked). Mention it next to (a) and (b).
8. Keep the lock order one way: the abort helper calls `MarkRetiring()` (barrier locks) while holding `o.mu`; no path
   may take `o.mu` while holding a barrier lock.
9. The 4bfd39e CONCERNS row has an empty decision column; later rows of the same stage supersede it.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-07 | 11ac064 | BLOCK | |
| design | 2026-11-07 | 14f648a | BLOCK | |
| design | 2026-11-07 | 8c32753 | CONCERNS | E003-RX-01 extended to feature 004 as proposed, 2026-11-07 (section "E003-RX-01 (implementation start)") |
| design | 2026-11-08 | 4bfd39e | CONCERNS | |
| design | 2026-11-08 | 39762a8 | BLOCK | |
| design | 2026-11-08 | ea9234b | PASS | |
