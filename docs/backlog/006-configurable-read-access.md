# Backlog: configurable access restrictions for MCP reads

- **Created:** 2026-11-17
- **Status:** Deferred for research by the owner; not part of feature 006.
- **Affected area:** MCP chat/contact reads, future message reads and configuration; policy consistency across future transports.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** Before proposing configurable read restrictions or expanding the local single-owner read policy to shared/remote use.
- **Source:** Feature 006 read-access clarification and `docs/specification.md` section 8.11, owner decision 2026-11-17; security review F01.

## Current Decision

For now, the owner-launched local stdio client may read all stored chats and contacts without per-read confirmation or per-chat restrictions. This does not authorize pairing, recovery or sending. Feature 006 must not add a restriction model merely to anticipate future use.

## Research Question

Investigate whether configuration-controlled access restrictions would usefully limit unintended disclosure, including when an otherwise trusted assistant is prompt-injected by retrieved content. Research is deferred; no allow-list model, configuration schema, default policy or implementation has been selected or approved.

## Topics to Investigate

- Restriction scope: chats, contacts, messages, latest-message previews, and whether login/status capabilities need separate treatment.
- Visibility rules for contact search, direct chats and group participation, including metadata that could disclose a hidden contact or chat.
- Identity normalization across phone numbers, phone JIDs, mapped/unmapped LIDs and subsequent merges or mapping changes.
- Allow-list versus deny-list semantics, default behavior, configuration validation and handling of policy changes during a session.
- Enforcement across every read path and future transports/proxying, so alternate tools cannot bypass a restriction.
- Owner-controlled configuration that tool arguments and retrieved content cannot expand; clear denial behavior and synthetic abuse tests.
- Usability and residual risk: restrictions can reduce disclosure scope but do not make an assistant immune to prompt injection.

## Expected Outcome

A researched recommendation with alternatives, trade-offs, affected user journeys and testable policy semantics for a separate owner decision. Implement only after that decision and the normal specification/review workflow. This backlog item does not close F01 or approve a security exception.
