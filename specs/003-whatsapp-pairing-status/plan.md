# Implementation Plan: WhatsApp Connection, Pairing and Status

**Branch**: `003-whatsapp-pairing-status` | **Date**: 2026-11-02 | **Spec**: [spec.md](spec.md)

**Input**: `specs/003-whatsapp-pairing-status/spec.md`, including the three owner decisions recorded on 2026-11-02.

**Status**: BLOCKED after Phase 0 research. Phase 1 has not started. This document records findings and candidate integration decisions, not an implementation-ready design or owner approval.

## Summary

Feature 003 adds a testable WhatsApp connection owner, terminal QR pairing, saved-session reuse, recoverable pairing state, offline local status, bounded version discovery and outdated-client recovery. The existing private shared database and lock remain the only storage/ownership foundation. Application message ingest, MCP tools, HTTP and media remain outside this feature.

Offline research established feasible approaches for device-write quiescence, QR rotation and logout recovery, status inspection and version discovery. It also established an unresolved High availability risk in the pinned protocol client: frame decompression and overflow handling are unbounded before application callbacks. Constitution Principles V and X do not permit proceeding on the assumption that those risks are accepted. See the exact evidence and decision paths in [research.md](research.md#r12---receive-path-security-gate-fails-before-phase-1).

## Technical Context

**Language/Version**: Go 1.27.0 module, toolchain Go 1.27.3; always `CGO_ENABLED=0`.

**Primary Dependencies**: Existing `go.mau.fi/whatsmeow v0.0.0-20261027104441-1eec1136a50b`, `go.mau.fi/util v0.11.0`, `modernc.org/sqlite v1.61.0`, `github.com/gofrs/flock v0.13.1` and the standard library. Candidate QR dependency: encoder package `rsc.io/qr v0.2.0`, zero module dependencies, plus a small deterministic terminal renderer; do not import its C-backed `libqrencode` subpackage. No dependency was added or updated during planning.

**Storage**: Existing private `<data dir>/whatsapp.db`, device schema 16 and message schema 1. No new application tables/migrations. Add a read-only existence inspection using the existing committed SQLite/WAL view. Wrap device persistence interfaces with a closeable admission barrier because direct device calls bypass feature 002's application-store gate.

**Testing**: Standard `testing`, `testing/synctest` for lifecycle timers with an in-memory fake, local controlled HTTP/TLS fixtures, synthetic SQLite devices and command/subprocess tests. Keep SQLite/real network I/O outside fake-time bubbles. No automated real-account access; a separately invoked interactive developer harness can validate same-process logout recovery only with separate owner permission for each action.

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.

**Project Type**: Single-module CLI/server; new internal connection adapter and existing command-shell wiring.

**Performance Goals**: Preserve the approved profile: one owned client/attempt; one application-initiated connect with a 30-second deadline; discovery within 15 seconds and 1 MiB consumed; one current QR up to 4 KiB and 60 seconds; three five-second delayed 405 retries; six-hour version refresh; shutdown failure notification after ten seconds while retaining ownership until writes stop. These are acceptance bounds, not claims of measured implementation performance.

**Constraints**: No C toolchain, protocol stdout contamination, credential/QR logs, implicit pairing, second device database, new listener, application message ingest, or real-account operation without separate permission. The 30-second connection deadline must not become the lifetime deadline of a healthy connection. Existing ten-second shutdown must not return and release the lock while device operations are admitted.

**Scale/Scope**: One account/process per private local data directory. The app-owned limits are fixed constants, not settings. The library's compressed-frame and queue capacities are not total receive-memory ceilings; see the blocking gate below.

## Constitution Check

*Initial gate: scope and accepted decisions were checked before source research. The protocol lifetime, persistence and receive-resource questions required evidence rather than an assumed pass. Phase 0 established the following results; the discovered failure stops Phase 1. Post-design re-evaluation is not performed because no Phase 1 design was approved.*

| Principle | Evidence or candidate control | Result after Phase 0 |
| --- | --- | --- |
| I. Specification is the source of truth | Retain D1-D18, offline status, approved constants and approved-attempt QR rotation. No specification decisions are changed to conceal incompatibilities. | Pass for proposed scope |
| II. One self-contained binary | One module, existing pure-Go stack, five CGO-free targets; justify QR encoder and exclude its C subpackage. | Pass for proposed scope |
| III. One process owns session/data | Lock before store/client; a DeviceContainer + session-store + LID-store barrier can reject/drain all pinned persistence calls before store close. | Feasible; not implemented or verified |
| IV. Clean protocol channel | Reuse one stderr logger; upstream bridge drops format arguments, including QR secrets; display QR only in `pair`. | Feasible; not implemented or verified |
| V. Secure by default | App-owned limits and private storage controls are specified, but unbounded receive decompression and overflow goroutines remain before those controls. | FAIL: F09 receive-path gap |
| VI. Testable by design | Fake narrow capabilities and lifecycle events; synthetic storage; fake-time retry/refresh tests; planned side-effect-negative assertions. | Feasible; not implemented or verified |
| VII. Correct MCP tools | No tools delivered here; preserve four status fields and future pairing state boundary. | Pass for proposed scope |
| VIII. Real accounts handled carefully | Research used local source and public module metadata only. All live checks remain manual and separately permissioned. | Pass |
| IX. Simplicity | No speculative settings/IPC/REST/import; reuse committed-view inspection, command lifecycle and logger. An unapproved protocol fork is not silently introduced. | Pass for proposed scope |
| X. Security planned per feature | The enabled receive capability has applicable High F09 exposure with neither a complete control nor an owner-approved, named, time-bounded exception. | FAIL: owner decision required |

### Blocking Decision

The pinned client decrypts authenticated server frames and then calls unbounded zlib `io.ReadAll` before node dispatch (`binary/unpack.go:21-30`, `client.go:871-883`). Its 2048-node queue spawns an extra goroutine retaining each overflowed node instead of applying a total concurrency bound (`client.go:892-904`). `FrameMaxSize = 1 << 24` caps the encoded websocket input, not decompressed bytes; 2048 caps only the channel, not all pending nodes. No supported pre-allocation/backpressure hook was found.

This is a source-level availability design risk, not a demonstrated exploit or a claim that arbitrary contacts can directly inject Noise frames. The genuine WhatsApp server is authenticated by TLS/Noise; that reduces the threat to trusted-upstream behaviour/compromise but does not enforce resource bounds. No live traffic or exploit was exercised.

To resume, the owner must select one of these paths:

1. Authorize investigation/design of a narrowly pinned protocol hardening patch or an upstream revision with proven decompression, queue/concurrency and relevant device-growth limits. New thresholds require explicit acceptance; preserve the already approved app-owned constants. Dependency integrity, license/provenance, CGO-free builds and negative tests must be reviewed before enabling it.
2. Explicitly approve a constitution-compliant temporary risk exception, recorded in `verification.md`, with a named owner, compensating controls, scope and expiry/review date for each residual High exposure. An exception is not granted by this document or by the spec's disclaimer about library internals. A soft Go memory limit alone is not a hard bound or sufficient evidence of mitigation.

Already-live app-state/contact/LID writes also need an accepted volume/disk policy or coverage by the same explicit risk decision. They cannot simply be assigned to feature 004 while 003 connects and persists them. Disabling automatic history downloads reduces one surface, not all of it. Prekey batches are already bounded by the library; do not mislabel them as unbounded.

Until the decision is recorded and the gates rechecked, do not generate an implementation-ready Phase 1 design, tasks or production client changes. No security exception or new resource ceiling has been invented here.

## Project Structure

### Documentation (this feature)

```text
specs/003-whatsapp-pairing-status/
  spec.md                     # accepted feature/clarification baseline, unchanged
  plan.md                     # this blocked planning record
  research.md                 # Phase 0 evidence and candidate decisions
  checklists/requirements.md  # existing local, gitignored requirements-quality checklist
```

Not generated because the gate failed: `data-model.md`, `contracts/` and `quickstart.md`. `tasks.md` belongs to `/speckit.tasks`, not this command. `verification.md` is required if the owner elects an exception and later for implementation/security evidence and owner-review records.

### Source Code (repository root)

Candidate integration locations only; no code changed:

```text
cmd/whatsapp-mcp/           # commands, offline status, pair input and lifecycle stop ordering
internal/wa/               # proposed owner, protocol adapter, persistence barrier and fake
internal/store/            # add read-only Inspect; retain one shared database
internal/walog/            # candidate shared home for the existing argument-dropping bridge
internal/private/          # reuse private-file and handle checks
.github/workflows/ci.yml   # replace stub-status privacy fixture with an offline helper
OPERATIONS.md              # later pairing/status/stop/recovery operating guidance
```

**Structure Decision**: Preserve one module and existing package responsibilities. A direct device-store barrier is necessary even though the database already has an application gate. Any protocol hardening may change this candidate layout; finalize it only after the blocked decision.

## Phase 0 Findings

- `Disconnect` is not a device-write join. A complete persistence chokepoint exists at DeviceContainer, all 12 session-specific store interfaces and the global LID store. Cover reads, write-looking reads, transaction callbacks and `Save`/`Delete`; reassert wrappers after first Save initializes the device.
- `ConnectContext` supplies the connection's lifetime context. Use a cancellable owner lifetime plus a separate 30-second attempt watchdog, not a 30-second context that kills healthy connections later.
- QR expiry/rotation follows the channel's per-code validity. Final timeout requires Enter for a new attempt. After confirmed logout the old device is permanently deleted; create a fresh device/client only after retiring the old generation, on the same open container.
- `status` cannot call current `startup` unchanged or `store.Open`: both can create/mutate storage. Reuse the committed-view preflight plus an existence-only identity query under the lock.
- Discovery can be bounded through the supplied HTTP client or a small bounded fetcher. Reject redirects, enforce TLS and the consumed-byte budget, classify errors without raw response bodies, retain the last usable version. Do not silently accept a truncated prefix containing a plausible revision.
- `SetWAVersion` writes unsynchronized globals. A mutex around application connects alone does not synchronize internal auto-reconnects; an accepted design must serialize all handshakes or use per-handshake payload snapshots without runtime global mutation.
- Keep raw upstream format arguments out of every log level. The library logs actual QR material at debug. Shared bridge relocation must preserve the existing storage log contract.
- CI and tests currently depend on stub status creating storage/failing and not being lock-blocked. Deliberately update these fixtures rather than weakening the accepted status behaviour.
- Library receive resource exposures remain a gate failure even with manual history downloads and a persistence barrier. Neither feature 004 deferral nor a soft heap limit resolves them.

## Phase 1 and Validation Handoff

Phase 1 is intentionally not started. After the blocking decision, complete the data model, lifecycle/fake/CLI contracts and runnable validation guide; then re-evaluate all constitution gates. Task generation follows that completed design, and the required owner-judge design review follows the feature-directory commit after tasks. This planning research is not an owner-judge verdict.

Required later verification includes deterministic six-hour/five-second timer scenarios, bounded HTTP/TLS failures, QR secrecy/rotation/EOF, stale generations, direct device writes held through shutdown and wrapper reattachment, unchanged-file offline status tests, CI privacy-fixture replacement, five CGO-free builds and native Windows/Linux/macOS tests. Explicitly compile the manual `testdata` harness because `./...` excludes it. Record live acceptance separately and do not mark unperformed checks passed.

Planning checks performed on 2026-11-02: `go version` reports Go 1.27.3 windows/amd64; `CGO_ENABLED=0 go mod verify` reports `all modules verified`. These do not validate new code, a new QR dependency or a protocol patch. No implementation tests, live account operations or vulnerability exploit tests were run.

## Complexity Tracking

| Violation | Why it cannot be accepted automatically | Required resolution |
| --- | --- | --- |
| Principles V/X: newly enabled receive work lacks hard decompression/pending-node bounds and has no approved High-risk exception | Limits in application callbacks and store wrappers occur after upstream decompression/queue allocation. The roadmap's 004 boundary does not disable those 003 paths. | Owner-approved hardening direction or a named, scoped, time-bounded exception with compensating controls; rerun planning gates. |

The inherited feature 002 sidecar-creation deviation remains documented in `specs/002-sqlite-storage/plan.md`; it does not excuse the separate receive-path risk. No new deviation is approved by this plan.
