# Changelog

## Merged Features Log

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
