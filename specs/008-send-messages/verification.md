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
| design | 2026-11-27 | 0c9a90f | CONCERNS | |
| design | 2026-11-27 | 129e4ae | PASS | |

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

## Design review of `2596af3` (BLOCK): handling

| Finding | Severity | Handling |
| --- | --- | --- |
| The local record's `lockForLive` waits with no context; a history flush or merge can hold the lock about 30 s, so the reply misses the guard deadline or retirement passes its drain bound | medium | R9, pipeline contract: a cancellable `lockForLiveCtx` (select on the lock and `ctx.Done()`, `liveWaiting` balanced). A lock not obtained in time is "record missing". T009 tests a worker holding the lock against the budget and against retirement. |
| Only the copy's reads can be abandoned; resolving, opening and checking still run in the handler, and shutdown waits for the handler | medium | R3, R4, spec edge case, pipeline contract: the whole source stage (syntax, resolve, open, checks, disk check, working directory, copy) runs in one abandonable goroutine. The send slot is held until it actually returns, so later calls get `busy` and stuck work cannot pile up. T011 tests a held `Lstat`, open and read during a stop. |
| The copy is capped at 100 MiB, not at the opened size | low | R3, tools contract, spec edge case: copy limited to the opened size plus one byte; a grown file is refused (T011 test). |
| A failed free-space measurement is not defined | low | R12, T004, tools contract: it refuses with `insufficient_space`. |
| The budget test would take minutes of real time, and the guard and service clocks are separate | low | R7, T012: an injectable guard clock and timers; the guard passes its admission time to the handler, and the service derives its deadlines from it. |
| The tasks header cites R1–R11 | low | Now R1–R12. |
| Observation: client request timeouts (believed 60 s in the TypeScript MCP SDK) | — | T038 times a large send through the owner's client; `OPERATIONS.md` documents the effective limit (T037). |
| Observation: kept roots on Windows may block renaming the outbox | — | T019 records the behaviour; T037 documents it. |

## Design review of `0c9a90f` (CONCERNS): handling

A CONCERNS verdict does not clear the feasibility gate, so the findings were fixed and the design sent for another review rather than accepted.

| Finding | Severity | Handling |
| --- | --- | --- |
| No rule for a client cancellation after WhatsApp accepted the send; the guard closes the session on a cancelled success, and the record may be skipped | medium | R7, spec FR-013, both contracts: once the send call started, send and record continue on contexts detached from the request's cancellation (still bounded by the budget, 5 s and the work context). A cancelled call returns the tool error `sent_cancelled` (with chat, ID, timestamp) or `outcome_unknown`, which the guard accepts. Tests in T011 and T012. |
| `send_audio_message` with a `.ogg` has no defined disk worst case | low | R12, FR-011, contracts, data model: `send_audio_message` always uses the conversion case. T011 test. |
| Only the record's lock wait is bounded; relookup and the store write run under the library context | low | R9, T009: relookup and `StoreMessage` take the record's context; test with the store writer held; `store.io_error` noise expected. |
| The admission time cannot ride in the request context with go-sdk v1.8.0; the clocks must be shared | low | R7, T012: per-session guard state read through a session-to-guard map, never from client parameters; one injected clock for guard and service. |
| Unclear who resolves the directories and creates the outbox; `status` could create artifacts | low | R10, T003: only tool-serving commands do it, after the lock; config checks syntax only; test that `status` with recipients set creates nothing. |
| Observation A: a hung network path leaves sending `busy` until restart | — | Documented in `OPERATIONS.md` (T037). |
| Observation B: R3 said "before any port call" | — | Reworded to "outside any port call". |
| Observation C: a stage finishing after its handler was cancelled | — | It removes its working directory, then frees the slot (pipeline contract, T011 test). |
| Observation D: FR-011 omitted the +1 MiB | — | Aligned in FR-011, the contracts and the data model. |

## Design review of `129e4ae` (PASS): low findings for implementation

The review confirmed every round-1 to round-3 fix against the code and the pinned go-sdk, and found that the round-3 changes need no new feasibility evidence. Its low findings are settled during implementation without editing the reviewed documents. The code review checks them.

| Finding | Settled in |
| --- | --- |
| A client cancel racing the end of the handler or the response write can still close the session (narrow window, message sent and recorded) | T012: for the three send tools, the guard writes rather than rejects a success reply to a cancelled request, with a test that injects the cancel after the handler returns. |
| T014 does not repeat R10's conditions | T014 follows R10: resolve, check by identity, open and create only when `send_allowed_recipients` is not empty, after the lock and before `attachOwner`. The sending off/on and `status` cases go in `cmd/whatsapp-mcp/send_wiring_test.go`. |
| The `sent_cancelled` result shape is not defined | T013 defines it within the contract's error shape (`chat_jid`, `id`, `timestamp`, `local_record`) and names the code for a cancel before the hand-over. |
| Observations | A missing guard state makes the handler refuse, never fall back to "now" (T012). `OPERATIONS.md` tells operators to check `list_messages` before retrying a cancelled send (T037). |
