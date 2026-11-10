# Backlog: `pair` can wait without a bound after a refused early 515

- **Created:** 2026-11-10
- **Status:** Open. Found by code reading in the code review of feature 004 at `7893044`; not reproduced. The outcome is safe (the marker stays unsafe, nothing is sent), but the command hangs.
- **Affected area:** `cmd/whatsapp-mcp` (`waitSavedConnection`, `pairingWindowFlow`), `internal/wa` (owner phase publication after PairSuccess).
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** before the first release, or when `pair` or `waitSavedConnection` changes.
- **Source:** `specs/004-shutdown-drain/verification.md`, section "Lost restart request on Linux CI" (review of `7893044`).

## Problem

A 515 that arrives before PairSuccess waits for it for at most 30 s and is then refused. If PairSuccess comes later
(a slow save), its update already reports the phase as disconnected, and the pairing window's end publishes the same
phase again a moment later. If both updates land before `pairingWindowFlow` has drained its event queue, the loop
reads the pairing result and calls `waitSavedConnection`, which only waits for a new event and never reads the
current phase (the status snapshot has no phase). Nothing publishes again, so `pair` waits until it is interrupted,
against `contracts/cli.md` ("stalled past 30 s ... exit 1, linking could not be completed safely").

## Proposal

Make `waitSavedConnection` start from the owner's current phase before it waits: expose the phase in the status
snapshot, or have `Subscribe` replay the newest event. Add a command-level test in which the disconnected event was
already consumed before the wait begins.

## Open questions

- Whether a real server ever sends the 515 more than 30 s before PairSuccess; the four real pairings saw it about
  0.6 to 0.8 s after.
