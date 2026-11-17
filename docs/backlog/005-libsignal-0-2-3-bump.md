# Backlog: libsignal 0.2.3 bump (Dependabot #14) is held for a real-message check

- **Created:** 2026-11-14
- **Status:** Open. Dependabot pull request #14 (`go.mau.fi/libsignal` 0.2.2 → 0.2.3) is green on CI but not merged; no code was changed.
- **Affected area:** `go.mod` / `go.sum`; the Signal message decryption path used by the pinned whatsmeow fork; the quiet-stop check, which counts libsignal goroutines.
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** the first real-account run of the MCP-tools feature (or any earlier permitted real-account run), the next whatsmeow pin change, or before a release.
- **Source:** review of the open Dependabot pull requests, 2026-11-14.

## Evidence

- The diff between the two released versions is small. The relevant change is in `protocol/SignalMessage.go`: `VerifyMac` now uses the original serialized bytes kept in a new `MacInput` field and falls back to re-serializing when it is empty. The rest is the library's own `go.mod` (Go 1.26, `x/crypto` 0.58.0, `protobuf` 1.36.12). The Go requirement is not a problem: the project already builds with Go 1.27.4.
- CI on #14 passed on 2026-11-14 on Linux, Windows and macOS. That run uses `internal/wasim` only, so the new MAC path has not decrypted a real WhatsApp message.
- `AGENTS.md` ties the quiet-stop check and the whatsmeow pin to library behaviour; a library bump is treated as an owner decision.

## Follow-up

1. Before merging, run the quiet-stop pass-rate check on the PR branch (`WA_MEASURE=1`, `TestQuietStopPassRate`) and record it in the feature's `verification.md`.
2. Decrypt real messages with the bumped library in one permitted real-account run (new empty data directory under `D:\tmp`, binary from a named commit). Check that live and history messages are stored and that no MAC error is logged.
3. If both pass, merge #14 with a Conventional Commits message that gives the reason. If the owner accepts the risk without the real run, record that decision here.

## Closure

Close when #14 is merged with the evidence recorded, or when it is closed with the reason noted here.
