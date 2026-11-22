# Backlog: libsignal 0.2.3 bump (Dependabot #14) is held for a real-message check

- **Created:** 2026-11-14
- **Status:** Open. [Dependabot #14](https://github.com/tiger-seo/whatsapp-mcp/pull/14) (`go.mau.fi/libsignal` 0.2.2 → 0.2.3) passed CI, local simulator checks and a real live-message check at `34f7c8a`; real history storage is not yet verified. No dependency change has been merged.
- **Affected area:** `go.mod` / `go.sum`; the Signal message decryption path used by the pinned whatsmeow fork; the quiet-stop check, which counts libsignal goroutines.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** the first real-account run of the MCP-tools feature (or any earlier permitted real-account run), the next whatsmeow pin change, or before a release.
- **Source:** review of the open Dependabot pull requests, 2026-11-14.

## Evidence

- The diff between the two released versions is small. The relevant change is in `protocol/SignalMessage.go`: `VerifyMac` now uses the original serialized bytes kept in a new `MacInput` field and falls back to re-serializing when it is empty. The rest is the library's own `go.mod` (Go 1.26, `x/crypto` 0.58.0, `protobuf` 1.36.12). The Go requirement is not a problem: the project already builds with Go 1.27.4.
- [PR CI](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/44975882193) passed on 2026-11-22 on Linux, Windows and macOS. The local quiet-stop recorder passed 50/50 in each of its four checks, and both focused ingest tests passed. See the [PR verification comment](https://github.com/tiger-seo/whatsapp-mcp/pull/14#issuecomment-7085082805) for commands, build provenance and limits. None of these checks used a real WhatsApp account.
- In the maintainer's real-account run on 2026-11-22, pairing and session reuse succeeded and the incoming test message was present in the database. History sync activity was logged, but history message storage was not confirmed.
- `AGENTS.md` ties the quiet-stop check and the whatsmeow pin to library behaviour; a library bump is treated as an owner decision.

## Follow-up

1. Quiet-stop evidence is recorded for `34f7c8a`. If the PR changes, check that the evidence still applies and rerun on the new candidate as needed.
2. Finish the smoke test by checking the test database for a known pre-pairing message and checking the full `serve` log for decrypt failures, incomplete history and shutdown outcome. The log bridge suppresses library error arguments, so absence of the string `MAC` alone is not evidence. Record redacted results and have the owner unlink the test device before deleting the data directory. Any repeat run needs explicit permission, a binary from a named commit and a new, empty private data directory under `D:\tmp`.
3. Once the gates pass and the owner review is current, merge #14 with a Conventional Commits message that gives the reason. If the owner explicitly accepts the risk without the real run, record that decision here; it does not replace the review gate.

## Closure

Close when #14 is merged with the evidence recorded, or when it is closed with the reason noted here.
