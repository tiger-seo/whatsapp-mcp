# Backlog: a heavy store test fails on a wall-clock limit under load

- **Created:** 2026-11-07
- **Status:** Open. One failure in three runs of unchanged code; cause not identified; no test or code was changed.
- **Affected area:** `internal/store` heavy opt-in tests (`WA_HEAVY_TESTS=1`), the nightly `heavy` CI job.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** the next failure of the nightly `heavy` job, or before a release.
- **Source:** `specs/003-whatsapp-pairing-status/verification.md`, section "T061 Verification Gates".

## Evidence

On 2026-11-06 (Windows 10, `CGO_ENABLED=0`, 38 GB free on `D:`), the first heavy run failed in `TestPerContactMemory/phone`: `LastInteraction` took 107 ms against a 50 ms limit, while memory use was within bounds. The same case then took 2.0 ms alone and 2.9 ms in a full rerun, and the whole heavy set passed. The machine was busy with other work (parallel test runs) during the first run; that is a suspicion, not a measurement.

## Follow-up

1. Run the heavy set several times on an idle machine and on the nightly runner, and record the `LastInteraction` timings to see the spread.
2. If the spread is wide, replace the fixed 50 ms limit with a relative bound (for example against a baseline measured in the same run) or a retry-once policy that still fails a real regression. Do not simply raise the number.
3. Keep the memory assertions unchanged; they are the safety property.

## Closure

Close when the limit no longer fails on a loaded machine and still catches a real slowdown, or when repeated idle runs show the failure was a one-off and the owner records that decision.
