# Backlog: the pinned whatsmeow can drop the post-pair restart request

- **Created:** 2026-11-10
- **Status:** Open. Recorded as a known limitation of feature 004 (owner decision 2026-11-10, `specs/004-shutdown-drain/spec.md` clarification); fix proposed upstream, not adopted.
- **Affected area:** `pair` (the post-pair link), `internal/wa`; the pinned `go.mau.fi/whatsmeow` (`f1b7985`).
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** when tulir/whatsmeow#1337 is merged or rejected, on the next pin change, or if a real `pair` ends with "linking could not be completed safely" after the phone confirmed the link.
- **Source:** `specs/004-shutdown-drain/verification.md`, section "Lost restart request on Linux CI".

## Problem

Right after pairing, the server sends a restart request (stream error 515) and closes the pairing socket. If the
close arrives in the same instant, the frame consumer decodes the 515 after the socket context is already
cancelled, and the library can drop it in two places:

- `handleFrame` chooses at random between queueing the node and the cancelled context;
- `handlerQueueLoop` takes the node from the queue, sees the cancelled context and returns without handling it.

The library treats its own disconnect as expected, so no event arrives either. The owner never starts the fresh
connection and the link ends after the 30 s watchdog as incomplete: safe (no fresh client, the pairing stays
unresolved, the marker stays unsafe), but the operator has to recover and pair again.

## Evidence

- Linux CI on pull request #11: `TestOwnerPostPairServerCloseAfterThe515StillLinks` failed in both runs; under WSL2
  2 of 10 runs on the current pin and 5 of 15 on the previous pin failed; native Windows 20 of 20 passed.
- Every real-account pairing (four, 2026-11-08 to 2026-11-10) received and handled its 515.
- The test now closes after the 515 was handled; `TestPostPairLostRestartRequestLeavesThePairingUnresolved` pins the
  safe outcome of a lost 515.

## Proposal

Adopt the upstream fix once merged (a pin change, with its own E003-RX-01 reassessment and code review), then drop
the wait in the simulator test or add a same-instant variant. The fix is tulir/whatsmeow#1337, from the fork
`tiger-seo/whatsmeow`, branch `fix-handleframe-stream-error`, commit `ce09515`: stream errors are queued in
preference to the cancelled context, handled directly when the socket is already closed, and handled by the queue
loop even when it is about to exit, all with the context passed to `ConnectContext`. With it, the original
same-instant test passed 200 of 200 runs under WSL2.

## Open questions

- Two more places can in theory drop a frame in the same instant (the socket's read loop and the frame consumer);
  none showed up in the failing runs.
