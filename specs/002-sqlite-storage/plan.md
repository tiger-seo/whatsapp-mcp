# Implementation Plan: Storage: one SQLite file and migrations

**Branch**: `002-sqlite-storage` | **Date**: 2026-10-28 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/002-sqlite-storage/spec.md`

## Summary

All persistent state moves into one private SQLite file, `<data dir>/whatsapp.db`, on the pure-Go
`modernc.org/sqlite` driver. It holds whatsmeow's device store at its own schema version and our `chats`/`messages`
tables at a separate version, driven by embedded numbered migrations. A new package, `internal/store`:

- opens the file safely: private artifacts, a read-only preflight that changes nothing, then device upgrade and
  message migrations, each step atomic;
- exposes the write, query and merge operations features 003–008 need, with the approved limits enforced inside
  the store.

The design follows [research.md](research.md):

- two connection pools, 1 writer and 4 readers (R2);
- whatsmeow's per-step transactional upgrades, verified for the pinned version (R3);
- a `mode=ro&readonly_shm=1` preflight (R4);
- pre-folded columns searched with `instr` for Unicode, case-insensitive, literal search (R7);
- a semaphore admission gate with deadlines (R9);
- a free-space reserve (R10).

`stdio`, `serve` and `pair` open the store after the lock. The operator guide starts as `OPERATIONS.md`.

## Technical Context

**Language/Version**: Go 1.27 (toolchain 1.27.3), `CGO_ENABLED=0`.

**Primary Dependencies**:

- `modernc.org/sqlite v1.61.0`;
- `go.mau.fi/whatsmeow` (pinned pseudo-version `v0.0.0-20261027104441-1eec1136a50b`; `store/sqlstore` and
  `types` only in this feature);
- `golang.org/x/text` (case folding, NFC);
- existing: `golang.org/x/sys`, `internal/private`.

**Storage**: one SQLite file in WAL mode. Data model in [data-model.md](data-model.md).

**Testing**:

- `go test` against SQLite in `t.TempDir()`, with Go fuzzing for validators and benchmarks for SC-004;
- CI on Linux, plus Windows and macOS for pull requests and `main`;
- manual, recorded cross-identity checks (quickstart step 6).

**Target Platform**: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64; local filesystems only.

**Project Type**: single-binary CLI/server; this feature is an internal library plus CLI wiring.

**Performance Goals**: every FR-021–FR-033 query < 200 ms on 100,000 messages / 1,000 chats (SC-004, local
benchmark); 16×100 concurrent writes with queries and no "database is locked" (SC-005).

**Constraints**: the approved storage acceptance profile (spec) as constants. Also:

- no file changed by a refused open;
- no secrets or message bodies in logs;
- store close within the 10 s shutdown limit;
- startup upgrades ≤ 10 min.

**Scale/Scope**: one account; history grows without bound (disclosed, not limited). About 2,500 lines of Go plus
tests, in one package and small changes to `cmd/whatsapp-mcp` and `internal/private`.

## Constitution Check

*GATE: checked before Phase 0 and again after Phase 1 (constitution v1.1.0).*

| Principle | How this plan complies | Status |
| --- | --- | --- |
| I. Spec is the source of truth | S1–S4, S6, D2, D16 and D18 are implemented as specified; the clarifications of 2026-10-28 are in the spec; nothing in D1–D18 is reopened | Pass |
| II. One self-contained binary | modernc, whatsmeow and x/text are pure Go; no `dbutil/litestream`; five cross-builds in CI | Pass |
| III. One process owns the data | one file, opened only after the data-directory lock; embedded migrations; no working-directory paths | Pass |
| IV. Clean protocol channel | the store logs only through the stderr `slog` logger. It never logs message bodies at any level, stricter than "debug only" | Pass |
| V. Secure by default | input validated and bounded before use; limits on bytes, rows, time, concurrency and disk; private files through `internal/private`; links refused, handles checked; no silent permission changes; govulncheck on the new graph | Pass |
| VI. Testable, and tested | store tested against SQLite in temp dirs with no network; whatsmeow's *client* is not used here; gofmt, vet and test gate the feature | Pass |
| VII. Correct MCP tools | the store gives `(chat_jid, id)` lookups and ambiguous-ID errors, a 200 cap and distinct not-found/invalid errors that 005/006 map to tool errors | Pass |
| VIII. Real accounts handled with care | synthetic devices only; no pairing or connection; restored copies never connect | Pass |
| IX. Simplicity | three justified pure-Go dependencies (R14); own 60-line migration runner instead of a framework; no settings added (limits are constants) | Pass |
| X. Security planned per feature | the spec's Security section takes up F03, F05, F09–F13 and F16 and defers the others to named features; abuse cases are tests; evidence goes to `verification.md`. High findings for capabilities this feature enables: F03, F05 and F09 are addressed, and F15 is covered by existing CI. None open, no exceptions | Pass |

Post-design re-check (after data model and contracts): unchanged, all Pass. The design adds no settings and no
services. The only new package outside `internal/store` is a function added to `internal/private`.

## Project Structure

### Documentation (this feature)

```text
specs/002-sqlite-storage/
├── spec.md
├── plan.md              # this file
├── research.md          # Phase 0: decisions R1–R14
├── data-model.md        # Phase 1: tables, rules, transitions
├── quickstart.md        # Phase 1: validation guide
├── contracts/
│   ├── store-api.md            # Go API of internal/store, errors, concurrency
│   └── startup-and-events.md   # CLI behaviour, messages, log events
├── verification.md      # created during implementation: results and evidence
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source Code (repository root)

```text
cmd/whatsapp-mcp/
├── commands.go          # open the store in runUntilStopped and pair; UTC log time
└── *_test.go            # e2e: first start, refusal exit codes, close on shutdown

internal/private/
├── private.go           # + CheckFile (strict 0600 rule on Unix for database artifacts)
└── private_*_test.go

internal/store/
├── doc.go
├── store.go             # Store, Open, Close, Device
├── files.go             # path forms, artifact checks, creation (R6)
├── preflight.go         # header check and read-only version preflight (R4)
├── migrate.go           # migration runner (R5)
├── migrations/
│   └── 0001_init.sql    # mcp_version, chats, messages, indexes
├── limits.go            # approved profile constants
├── admit.go             # admission gate, deadlines, outcomes (R9)
├── disk.go, disk_unix.go, disk_windows.go   # free-space reserve (R10)
├── fold.go              # fold() and the mcp_fold SQL function (R7)
├── validate.go          # JIDs, IDs, fields, NormalizePhone (R11)
├── chats.go             # UpdateChats, name and activity rules
├── messages.go          # StoreMessages, re-store rule
├── queries.go           # list/get chats and messages, context, last interaction
├── contacts.go          # SearchContacts over the device store
├── merge.go             # MergeLIDChat
├── errors.go            # error types (contract)
├── events.go            # typed, secret-free event logging (R12)
├── *_test.go            # unit, integration, fuzz, benchmark
└── testdata/cmd/{bumpversion,seed}/          # test helpers used by quickstart

OPERATIONS.md            # operator guide, "Storage" section (FR-047, FR-049, FR-050)
```

**Structure Decision**: one Go module (Principle II). All database access lives in `internal/store` (FR-038).
The device-store container is handed out through `Store.Device()` for 003. `cmd/whatsapp-mcp` only calls
`store.Open` and `Close`.

## Implementation notes for task generation

1. **Gate first:** the SC-006 byte-for-byte preflight test and the `readonly_shm` behaviour on Windows, Linux and
   macOS (R4), plus `TestDeviceUpgradeStepsAreTransactional` (R3). If `readonly_shm` fails anywhere, switch to the
   in-memory fallback before building on it.
2. Then files/private → preflight → migrations → `Open`/`Close` → CLI wiring (US1, US2).
3. Writes and rules (US3), then queries (US4), single-message lookups (US5), then the merge (US6).
4. Limits, admission, disk, events and the abuse cases (US7) run across all of the above. Each approved limit gets a
   boundary test and an excess test.
5. `OPERATIONS.md`, the benchmark, the cross-identity evidence and the restore rehearsal close the feature,
   recorded in `verification.md`.

## Complexity Tracking

No constitution violations to justify.
