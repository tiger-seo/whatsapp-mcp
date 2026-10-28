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
- a preflight that reads through a read-only committed view, which replays the WAL in Go and is served to SQLite
  by `modernc.org/sqlite/vfs`, so no file is opened for writing (R4, spike S2);
- pre-folded columns searched with `instr` for Unicode, case-insensitive, literal search (R7);
- a semaphore admission gate with deadlines (R9);
- a free-space reserve (R10);
- contacts found under a phone number or a LID, resolved through the device store's map, with the LID merge
  rewriting senders in every chat (R15);
- a 256 MiB hard heap limit on SQLite's own memory, which Go cannot see, with SQLite's memory statistics switched
  on so that the limit is enforced, and at most 2 deep sender-filtered listings at once (R16).

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

*GATE: checked before Phase 0 and again after Phase 1, and re-checked against constitution v1.2.0 after the
design review of 2026-10-28.*

| Principle | How this plan complies | Status |
| --- | --- | --- |
| I. Spec is the source of truth | S1–S4, S6, D2, D16 and D18 are implemented as specified; the clarifications of 2026-10-28 are in the spec; nothing in D1–D18 is reopened | Pass |
| II. One self-contained binary | modernc, whatsmeow and x/text are pure Go; no `dbutil/litestream`; five cross-builds in CI | Pass |
| III. One process owns the data | one file, opened only after the data-directory lock; embedded migrations; no working-directory paths | Pass |
| IV. Clean protocol channel | the store logs only through the stderr `slog` logger. It never logs message bodies at any level, stricter than "debug only". whatsmeow's device-store logs are bridged into the same logger by `walog.go`, which forwards the level, module and format string but not the arguments (R3) | Pass |
| V. Secure by default | input validated and bounded before use; limits on bytes, rows, time, concurrency and disk; the database file is created through `internal/private`; links refused, handles checked; no silent permission changes; govulncheck on the new graph. SQLite creates `-wal`/`-shm` itself: justified deviation, see Complexity Tracking | Pass with justified deviation |
| VI. Testable, and tested | store tested against SQLite in temp dirs with no network; whatsmeow's *client* is not used here; gofmt, vet and test gate the feature | Pass |
| VII. Correct MCP tools | the store gives `(chat_jid, id)` lookups and ambiguous-ID errors, a 200 cap and distinct not-found/invalid errors that 005/006 map to tool errors | Pass |
| VIII. Real accounts handled with care | synthetic devices only; no pairing or connection; restored copies never connect | Pass |
| IX. Simplicity | three justified pure-Go dependencies (R14); own 60-line migration runner instead of a framework; no settings added (limits are constants). The preflight's WAL view (about 150 lines) uses `modernc.org/sqlite/vfs` from the same module, so it adds no dependency; spike S2 showed that no SQLite open mode avoids it (R4) | Pass |
| X. Security planned per feature | the spec's Security section takes up F03, F05, F09–F13 and F16 and defers the others to named features; abuse cases are tests; evidence goes to `verification.md`. High findings for capabilities this feature enables: F03, F05 and F09 are addressed, and F15 is covered by existing CI. None open, no exceptions | Pass |
| Development Workflow (v1.2.0): owner reviews | the design review ran on 2026-10-28 and returned BLOCK; its findings are resolved in the spec, research, data model, contract and tasks, and a new design review must pass before `/speckit-implement`. The code review follows implementation, before the pull request. Both verdicts go to `verification.md` | Pending re-review |

Post-design re-check (after data model and contracts): unchanged. All Pass, with the one justified deviation under
Principle V recorded in Complexity Tracking. The design adds no settings and no services. The only new package outside `internal/store` is a function added to `internal/private`.

## Project Structure

### Documentation (this feature)

```text
specs/002-sqlite-storage/
├── spec.md
├── plan.md              # this file
├── research.md          # Phase 0: decisions R1–R16
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
├── private.go           # + CheckFile (strict 0600 rule on Unix, macOS ACLs, file identity)
└── private_*_test.go

internal/store/
├── doc.go
├── store.go             # Store, Open, Close, Device
├── files.go             # path forms (resolved on Windows), artifact checks and identities, creation (R6)
├── preflight.go         # header check and read-only version preflight (R4)
├── walview.go           # committed view: WAL replay served read-only through modernc's fs.FS VFS (R4)
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
├── contacts.go          # SearchContacts and resolveContact over the device store (R15)
├── merge.go             # MergeLID: sender rewrite in every chat, then the @lid chat
├── errors.go            # error types (contract)
├── events.go            # typed, secret-free event logging (R12)
├── walog.go             # whatsmeow waLog → slog bridge: level, module, format string only (R3)
├── *_test.go            # unit, integration, fuzz, benchmark
└── testdata/cmd/{bumpversion,seed}/          # test helpers used by quickstart

OPERATIONS.md            # operator guide, "Storage" section (FR-047, FR-049, FR-050)
```

**Structure Decision**: one Go module (Principle II). All database access lives in `internal/store` (FR-038).
The device-store container is handed out through `Store.Device()` for 003. `cmd/whatsapp-mcp` only calls
`store.Open` and `Close`.

## Implementation notes for task generation

1. **Gate first:** the committed view (R4) with its byte-for-byte and differential tests on Windows, Linux and
   macOS, plus `TestDeviceUpgradeStepsAreTransactional` (R3). Nothing is built on the preflight until the view
   passes on all three.
2. Then files/private → preflight → migrations → `Open`/`Close` → CLI wiring (US1, US2).
3. Writes and rules (US3), then queries (US4), single-message lookups (US5), then the merge (US6).
4. Limits, admission, disk, events and the abuse cases (US7) run across all of the above. Each approved limit gets a
   boundary test and an excess test.
5. `OPERATIONS.md`, the benchmark, the cross-identity evidence and the restore rehearsal close the feature,
   recorded in `verification.md`.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Principle V: "New sensitive files are created through `internal/private`". SQLite's own VFS creates `whatsapp.db-wal` and `whatsapp.db-shm`, and briefly `whatsapp.db-journal` while it switches a new database to WAL, not `internal/private` | SQLite opens and creates its sidecars inside the driver; the store cannot intercept those calls without replacing the VFS | A custom SQLite VFS routing every open through `internal/private` duplicates the driver's file layer and its locking for no added protection. The sidecars are protected anyway: the data directory is private, so no other user can create or swap entries in it; on Unix SQLite gives sidecars the 0600 main file's mode and owner, and on Windows they inherit the directory's protected access list; every sidecar is checked through `private.CheckFile` before the open and again right after it, and an unsafe one stops startup. A `-journal` that outlives the switch (a crash) is refused at the next start rather than rolled back (research R6, tasks T017, T018, T020, T024) |
