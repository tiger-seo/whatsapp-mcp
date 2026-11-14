# Backlog: `serve` and `stdio` keep waiting after a device storage fault

- **Created:** 2026-11-13
- **Status:** Open; moved out of feature 005 after its first design review.
- **Affected area:** `cmd/whatsapp-mcp` (`serve`, `stdio`), `internal/wa` owner, feature 003's storage-fault path.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** the next feature that changes the connected commands' lifecycle, or a real run that stays up
  disconnected after a storage fault.
- **Source:** `specs/005-message-ingest/research.md` R9 and the appendix ("Lifecycle And Failure").

## Problem

When the owner retires a generation for a device storage fault, `serve` and `stdio` keep waiting for the lifecycle
to be cancelled instead of stopping. The operator sees a process that is up but can do nothing. Feature 005 adds a
terminal signal for ingest failures; using it for device faults too would change feature 003's behaviour, which the
005 spec does not cover, so it was left out.

## Proposal

Close the owner's terminal signal on a device storage fault as well, stop the command through the normal sequence,
keep the existing storage-fault message and exit 1. Needs a spec entry and tests in the feature that takes it.
