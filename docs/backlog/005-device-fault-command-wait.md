# Backlog: `serve` and `stdio` keep waiting after a device storage fault

- **Created:** 2026-11-13
- **Status:** Fixed for `stdio` in feature 006; open for `serve`.
- **Affected area:** `cmd/whatsapp-mcp` (`serve`, `stdio`), `internal/wa` owner, feature 003's storage-fault path.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** the next feature that changes the connected commands' lifecycle, or a real run that stays up
  disconnected after a storage fault.
- **Source:** `specs/005-message-ingest/research.md` R9 and the appendix ("Lifecycle And Failure").

## Problem

Before feature 006, when the owner retired a generation for a device storage fault, `serve` and `stdio` kept
waiting for lifecycle cancellation instead of stopping. The operator saw a process that was up but could do
nothing. Feature 005 added a
terminal signal for ingest failures; using it for device faults too would change feature 003's behaviour, which the
005 spec does not cover, so it was left out.

## Feature 006 resolution and remaining work

`stdio` observes the latched device-storage fault, stops through normal drain, and exits 1 even if the fault races
EOF or occurs during owner drain. Marker ownership stays unchanged. See the
[command and native evidence](../../specs/006-mcp-stdio-tools/verification.md#t037t038-unix-pipe-follow-up-2026-11-20-utc).
The owner's ingest-terminal signal and `serve` behavior are unchanged. Changing `serve` needs separate scope and tests.
