# Verification: Sending Text, Files and Voice Notes

## Planning Evidence

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-27 | `.specify/scripts/bash/setup-plan.sh --json` (Git Bash on Windows) | Exit 0; resolved branch `008-send-messages` and generated the plan template. |
| 2026-11-27 | Read-only audit of `internal/mcp` (guard, registration), `internal/wa` (owner retirement, session, fence, HTTP clients, fake), `internal/wasim`, `internal/store` (message upsert, LID helpers), `internal/config`, `internal/private` and the pinned whatsmeow fork's send, upload and usync code | Decisions in [research.md](research.md) R1–R11. No runtime tests at planning time. |
| 2026-11-27 | Feasibility items 1–4 (scratch only; local Windows, WSL2, CI runs 45997516073 and 45998881113) | All passed after two design changes; commands, counts, exit codes and limitations in [feasibility.md](feasibility.md). Committed at `5f20340`. |
| 2026-11-27 | Task checklist validation (`grep -E '^- \[[ X]\] T[0-9]{3} '`, label and file-path checks); `git ls-files --eol -- specs/008-send-messages/tasks.md` | 39 sequential tasks, story labels on every story-phase task, LF. Committed at `c5801b2`. Documentation checks only. |

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-27 | 3254097 | BLOCK | |
| design | 2026-11-27 | 2596af3 | BLOCK | |

## Design review of `3254097` (BLOCK): handling

| Finding | Severity | Handling |
| --- | --- | --- |
| Working files have no disk bound; a send can push the data volume under the store's 256 MiB reserve and force offline recovery | high | FR-011 and R12: before the working directory exists, fresh free space must cover 512 MiB plus the send's worst case (2 × file; file + 200 MiB for a conversion), else `insufficient_space` with no side effect. Tasks T004 (`FreshFreeBytes`), T011 (check and negative test), T032. |
| `UploadReader` encrypts without checking the context, so a slow source drive can hold an admitted call past the drain bound | medium | R1 and R3: the plaintext is copied into the private working directory, with context checks between chunks, before any port call; uploads read only that copy; a stuck read is abandoned and swept at startup. Tasks T008, T011 (held-reader stop test), T022. |
| The local record bypasses ingest's lock and LID relookup and can recreate a merged LID chat | medium | FR-014 and R9: the record goes through ingest's live-write path (`lockForLive`, `relookup`, write). Task T009 with an interleaved-merge test. |
| The 5 s guard margin cannot hold the 5 s record plus the reply; a late reply closes the connection and invites a resend | medium | FR-012 and R7: one budget (acceptance by 2:00, record ≤ 5 s, guard deadline 2:15, ≥ 10 s for the reply). Task T012 with an ack-at-1:59.9 test. |
| Registration lookups are uncapped and uncached | medium, owner | Owner decision 2026-11-27: lookups limited to 10 per minute and 100 per 24 hours, results cached 24 hours (at most 1,000). Recorded in §8.11 and the spec's Clarifications; FR-002, R8, tasks T005 and T011. |
| A case-alias of the data directory on macOS passes a path-text check | medium | FR-007 and R10: comparison by identity (`os.SameFile` on the entry, its ancestors and the data directory's ancestors). Task T003 with a native macOS test. |
| `ChatName` ok = false treats unnamed chats as unknown | low | New `ChatExists` read (T004), used by T011. |
| Allowed roots may be reopened by path | low | R4: roots opened once at startup and kept (T003, T019, T021). |
| `-protocol_whitelist file` vs `file,pipe` | low | Aligned to `file,pipe`, as verified in feasibility (R6, pipeline contract). |
| A junction above the default outbox would stop `stdio` even with sending off | low | R10: directories are resolved, opened and created only when sending is enabled (T003 test). |
| Unrecognised errors after the hand-over could read as `send_failed` | low | R9 and the tools contract: a fixed list of pre-hand-over errors is `send_failed`; anything else is `outcome_unknown` (T008 test). |
| Group sends not exercised in feasibility | low | T010 tests them on the simulator; leftover library work there goes to the owner as an allow-list decision, not widened in the task. |
| No executable-substitution test | low | T026: an `ffmpeg` in the current directory or a relative `PATH` entry is never used. |
