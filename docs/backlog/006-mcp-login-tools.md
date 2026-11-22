# Backlog: MCP login-status and pairing-QR tools

- **Created:** 2026-11-17
- **Status:** Deferred by the owner; not part of feature 006.
- **Affected area:** `get_login_status`, `get_pairing_qr`, stdio pairing authorization and future transport parity.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** Before proposing MCP login/pairing support, or before first-release acceptance to decide whether these tools remain deferred.
- **Source:** Owner decision during feature 006 specification, 2026-11-17; `docs/specification.md` P1/P4 and section 8.11.

## Current Scope

Feature 006 exposes five chat/contact tools without `get_login_status` or `get_pairing_qr`. Initial linking uses the existing terminal `whatsapp-mcp pair`; local offline inspection uses `whatsapp-mcp status`. Their established behavior and safety gates remain unchanged. No new stdio pairing opt-in is approved by this deferral.

## Deferred Capabilities

- `get_login_status`: expose a coherent snapshot of the running owner's `connected`, `logged_in`, `pairing_required` and `wa_version`, without confusing saved identity with verified online authentication.
- `get_pairing_qr`: deliver the current QR image through MCP only during an owner-approved eligible attempt, with structured information and explanatory text, without logging or persisting pairing material.

## Design Questions Before Implementation

- Decide how the owner authorizes a pairing window independently of tool arguments. Stdio currently opens no attempt, while terminal `pair` cannot run beside it for the same data directory because of the exclusive lock.
- Determine whether a local startup opt-in is appropriate and whether safely completing the post-pair connection in stdio requires a separately approved lifecycle change. The existing post-pair reconnect permission is for terminal `pair` only; this item does not extend it.
- Preserve unsafe-run recovery, one-attempt admission, QR expiry/replacement, generation isolation and quiet-stop gates. Do not introduce same-process re-pair or additional reconnects without a separate owner decision.
- Define QR disclosure/retention limits, honest unavailable responses, client image support and future transport policy parity; already delivered images cannot be recalled from client transcripts.

## Completion and Evidence

Use a separate specification/review cycle with in-memory MCP tests, a fake owner, seeded private storage and negative authorization/expiry/race tests. Any real-account connection or pairing still requires separate permission for that step. Reassess release scope explicitly; until delivered and verified, neither tool may be advertised as implemented. This item does not close F01 or approve an exception.
