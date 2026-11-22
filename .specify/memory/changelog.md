# Changelog

## Merged Features Log

### Reusable Session After a Proven Quiet Stop — archived 2026-11-22
**Branch:** 004-shutdown-drain
**Spec:** [specs/004-shutdown-drain/spec.md](../archive/004-shutdown-drain/spec.md)

**What was added:**
- Reuse a saved session after a proven quiet stop, with fail-closed recovery when the check or another clear prerequisite fails.
- Run the single post-pair reconnect on a fresh client and generation; remove the switch without enabling other retry paths.
- Report the quiet-stop outcome and safe failure categories on stderr without exposing raw observations.

**New Components:**
- Per-generation network fence and bounded quiet-stop observation in `internal/wa`, in-memory device-barrier refusal accounting in `internal/store`, and command outcome wiring.

**Superseded:**
- RETIRED: SC-019 (from .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-001) → replaced by SC-028. Reason: Proved quiet attempts can be reused; SC-029 exercises unsafe retention in fault cases.
- RETIRED: SC-024 (from .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-007) → replaced by SC-034. Reason: The separately authorized journey now exercises a restart without recovery.
- RETIRED: "Temporary restriction" (from .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Unsafe-Run Marker") → replaced by FR-110. Reason: A passed quiet-stop check and all other prerequisites permit orderly clear after a production attempt.

**Tasks Completed:** 47/47 tasks

### WhatsApp Connection, Pairing and Status — archived 2026-11-22
**Branch:** 003-whatsapp-pairing-status
**Spec:** [specs/003-whatsapp-pairing-status/spec.md](../archive/003-whatsapp-pairing-status/spec.md)

**What was added:**
- A connection owner with one terminal QR window, a local offline status snapshot and bounded version discovery.
- Guarded device-store work, durable app-state repair metadata, an unsafe-run barrier with explicitly authorized offline recovery, and shutdown that keeps ownership until writes drain.
- Default-deny reconnect with a narrow post-pair exception; the recorded upstream receive-resource exception was specific to this feature baseline, not a verified bound or inherited permission.

**New Components:**
- `internal/wa`, `internal/walog`, guarded device-store and committed-view status wiring, and application migrations 2 and 3.

**Superseded:**
- RETIRED: "Commands not yet implemented" (from .specify/archive/001-binary-skeleton/spec.md -> "Commands not yet implemented") → replaced by FR-084. Reason: Pair and status now have command-specific behavior (offline status: FR-090); pair still respects the owner lock.
- RETIRED: AS-006 (from .specify/archive/001-binary-skeleton/spec.md -> "`status` will not take the lock") → replaced by AS-019. Reason: Status inspects existing committed storage offline and respects the owner lock rather than querying a running daemon.

**Tasks Completed:** 77/77 tasks

### Storage: one SQLite file and migrations — archived 2026-11-22
**Branch:** 002-sqlite-storage
**Spec:** [specs/002-sqlite-storage/spec.md](../archive/002-sqlite-storage/spec.md)

**What was added:**
- One private SQLite database for the device store and message history, with independent schema histories and read-only compatibility preflight.
- Atomic chat and message writes, deterministic bounded queries, contact identity resolution, and LID merge.
- A storage recovery, backup and restore contract with synthetic platform evidence and documented limits.

**New Components:**
- `internal/store`, embedded SQL migrations, storage CLI lifecycle wiring and the storage section of `OPERATIONS.md`.

**Tasks Completed:** 73/73 tasks

### Binary skeleton (CLI, configuration, logging, lock, CI) — archived 2026-11-22
**Branch:** 001-binary-skeleton
**Spec:** [specs/001-binary-skeleton/spec.md](../archive/001-binary-skeleton/spec.md)

**What was added:**
- Build identification and help, layered configuration with startup validation, and a predictable per-user data directory.
- Exclusive ownership with PID diagnostics, graceful and forced stops, and protocol-clean stdout for the skeleton.
- CGO-free checks on three operating systems and builds for five targets; the security follow-up added private-path checks and supply-chain gates.

**New Components:**
- `cmd/whatsapp-mcp`, `internal/config`, `internal/lock`, `internal/buildinfo`, `internal/private` and `.github/workflows/ci.yml`.

**Tasks Completed:** 44/44 tasks
