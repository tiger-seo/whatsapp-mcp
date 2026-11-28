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
| design | 2026-11-28 | 25c6bb8 | PASS | |
| code | 2026-11-28 | 862034b | CONCERNS | |

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

## Implementation Evidence (2026-11-27 UTC)

All runs are on branch `008-send-messages`, the uncommitted working tree on top of `eed038c`, with `CGO_ENABLED=0`.
Windows is Windows 10 amd64 with Go 1.27.4; WSL2 is Ubuntu amd64 under WSL2 with Go 1.27.4, not native Linux. All
data is synthetic; accounts are the fake send port or `internal/wasim`. No real account was used. Exit codes were
read from the command itself.

### Initial gates (T036, T035): local full test run failed

| Time | Command | Result |
| --- | --- | --- |
| 14:48 | `gofmt -l .` | printed nothing; exit 0 |
| 14:48 | `go vet ./...` (Windows, and with `GOOS`/`GOARCH` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) | exit 0 each |
| 14:48 | `go mod verify` | "all modules verified"; exit 0 |
| 14:48 | the five `go build` targets of `AGENTS.md` | exit 0 each |
| 14:48 | `go vet`, `go build`, `go test` of `./internal/wa/testdata/cmd/session-check` | exit 0 each |
| 14:48 | `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | "No vulnerabilities found."; exit 0 |
| 14:48 | `go.mod`, `go.sum` diff; `w/crlf` count in `git ls-files --eol` | no change; 0 |
| 14:50-14:57 | `go test ./... -count=1 -timeout 30m` (Windows) | **exit 1**. `store` and `mcp` failed; `cmd/whatsapp-mcp`, `buildinfo`, `config`, `lock`, `media`, `private`, `wa`, `walog`, `wasim` passed |
| 14:57-14:58 | `go test ./internal/wa -run TestSimulatorStopDuringEachSendStep -count=20 -v` | exit 0; 200 passed (9 held steps and their parent, 20 times), 0 failed, 0 skipped |

The failures of the full run are four cases of two feature 007 timed searches over 400,000 messages, each stopped
by the 5-second store deadline: `TestToolMessageSearchScale/{sparse_needle,not-present}` in `internal/store` and
`TestMCPMessageAcceptanceScale/{sparse_needle,not-present}` in `internal/mcp`. Observations, not a diagnosis:

- Both tests passed in the 14:31-14:39 run below, whose only failure was another test. The store code did not change
  between the two runs.
- This feature changes no search query, schema or index.
- The cause is **not established**. Load from packages running in parallel is a guess, and no comparison with the
  base commit was made. A first rerun of the two tests alone, bundled with a machine-load check, was refused by the
  session's permission check.

Later reruns, on the same code, after the owner asked to try again:

| Time | Command | Result |
| --- | --- | --- |
| 15:31-15:32 | `go test -p 1 ./internal/store ./internal/mcp -run '^(TestToolMessageSearchScale\|TestMCPMessageAcceptanceScale)$' -count=1 -timeout 9m -v` | exit 0; both tests passed. `sparse_needle` and `not-present` took 2.18 s and 1.96 s in `store`, 2.82 s and 2.56 s in `mcp`, against the 5 s deadline |
| 15:32 | `go test ./... -count=1 -timeout 30m` | **not completed**: Claude Code stopped it because the system was critically low on memory. Its partial output holds only `link.exe: exit status 1` build failures (`private`, `media`, `cmd/whatsapp-mcp`, `config`) and no test result; low memory is the likely reason, not confirmed |

The passing run of the two tests alone shows that they hold when run by themselves. It does not replace a passing
full run.

At the time of this run, native Linux and macOS evidence was not yet available. See the committed-revision follow-up below.

### Foundation and User Story 1 (T018)

| Time | Command | Result |
| --- | --- | --- |
| 14:23 | `go test ./internal/media ./internal/mcp ./internal/wa ./internal/config ./internal/store -run 'Send\|Recipient\|Policy\|Limit\|LIDsForPhone' -count=1 -v` (Windows) | exit 0; 269 passed, 0 failed, 4 skipped |
| 14:31-14:39 | `go test ./internal/media/... ./internal/mcp ./internal/wa ./internal/config ./internal/store ./internal/wasim -count=1 -timeout 30m` | exit 1: `TestAbuseSDKRedactionAndRecovery` (feature 006) failed, because a session without send tools recorded the unknown name `send_message` as a send tool. All other packages passed |
| ~14:42 | after the fix (send handling only once the send tools are registered): `go test ./internal/mcp -count=1` | exit 0 (177.6 s) |

Repetitions during development, all exit 0 at the last run: `TestSendOwner*` `-count=200`; the MCP send, guard and
audit tests `-count=100`. Two test races found by repetition were fixed in the tests (an owner event sent before the
startup attempt; a client call sent before the guard released the tool credit).

The simulator runs cover a lookup, a text, an image upload and send with a caption, a group send, each local record,
and a stop held at `lookup`, `devices`, `prekeys`, `groupinfo`, `upload` and `ack`, also for a group send. Every
stop ended the call before `barrier_retired` and passed the quiet-stop check. **No quiet-stop allow-list change was
needed**, including for the group steps that feasibility did not cover.

### User Story 2, files (T024) - Windows and WSL2 only

| Time | Platform and command | Result |
| --- | --- | --- |
| 14:58 | Windows: `go test ./internal/media ./internal/mcp ./internal/config -run '^(TestSendPath\|TestSendFile\|TestSendDirs)' -count=1 -v` | exit 0; 49 passed, 0 failed, 4 skipped (`media` 2, `config` 2, `mcp` 0) |
| 14:59-15:01 | WSL2: `go test -p 1 ./internal/media/... ./internal/mcp ./internal/wa ./internal/wasim ./internal/config ./cmd/whatsapp-mcp -run 'Send\|Ogg\|SyntacticPath\|Recipient\|Policy\|Limit\|HTTP\|RealFFmpeg' -count=1 -v` | exit 0; 349 passed, 0 failed, 3 skipped (real ffmpeg: not installed; device node: needs root; case alias: case-sensitive volume) |
| ~14:44 | WSL2 as root: the `internal/media` test binary, `-test.run SendPath` | all path cases passed, including the device node. (The same invocation also selected the converter tests, which failed because `go` is not on root's `PATH` to build the stand-in; they passed in the user run) |

Windows skips: both symbolic-link tests (creating a link needs a privilege this account lacks) and, in `config`, the
unprivate and linked outbox cases. Symbolic-link controls are therefore **not verified on Windows**; junctions,
junction swap, alternate data streams, UNC, `\\?\`, `\\.\`, reserved names and hard links passed. 8.3 names are off on
the volume (not verified). A trailing dot or space opens the same file inside the outbox. On Windows an allowed
directory **cannot be renamed or deleted** while its root is open; on WSL2 it can, and opens keep using the old
directory. The case-alias identity check ran on Windows (case-insensitive NTFS) and passed; the macOS run that T003
asks for is **not verified**.

CI had not run at the time of these local checks. See the committed-revision follow-up below for the native results.

### User Story 3, voice notes (T031)

| Time | Command | Result |
| --- | --- | --- |
| 14:58 | Windows: `WA_FFMPEG_TESTS=1 go test ./internal/media ./internal/mcp -run '^(TestOgg\|TestSendConvert\|TestSendAudio\|TestRealFFmpeg\|FuzzOggOpusDuration)' -count=1 -v` | exit 0; 64 passed, 0 failed, 0 skipped. ffmpeg 9.1 full build (gyan.dev, scoop, whose shim is a launcher): mp3, wav, m4a, aac, flac, opus, vorbis, amr converted to 12 s within 1 s; disguised HLS and concat files refused |
| during development | `go test ./internal/media -run '^$' -fuzz FuzzOggOpusDuration -fuzztime 45s` | exit 0; 188,793 executions |
| during development | `go test ./internal/media -run '^$' -fuzz FuzzSyntacticPath -fuzztime 30s` | exit 0; 175,661 executions |

WSL2 has no ffmpeg, so the real converter is **not verified** there; the stand-in tests passed. See the follow-up below
for native Linux, macOS and Chocolatey results.

### Decisions and deviations for the code review

| Item | What was done |
| --- | --- |
| 64 KiB session limit against 65,536-byte text | Found during implementation. Owner decision 2026-11-27: 512 KiB for send calls only. The change made `129e4ae` stale; `owner-judge` renewed the design `PASS` at `25c6bb8`. |
| Import cycle (`wa` test imports `mcp`, `mcp` would import `media`, which imports `wa`) | The send types are in a leaf package, `internal/media/sendapi` |
| Cancel and budget before the hand-over | Codes `cancelled` and `timeout` (the design review left the names to implementation) |
| Phone forms | The tools contract is followed: digits, optional `+`, spaces and dashes. The read tools also accept dots and parentheses, which FR-002's "as for the read tools" could be read to require |
| Failures the contract has no code for | An unreadable local LID map or chat is `lookup_failed`; a private working file that cannot be written is `upload_failed` |
| Known failures before the write | Matched by the pinned library's error values and message starts; a pin change must recheck them. A cancellation or deadline inside such a step stays `outcome_unknown` |
| Lookup skip | A phone whose stored chat is under its mapped LID counts as having a stored chat |
| Late low finding of the `129e4ae` review | For send tools the guard writes a success reply to a cancelled request and ignores a cancel that arrives during the write |

## Committed-Revision Follow-up (2026-11-28 UTC)

Tested revision `25c6bb8` with `CGO_ENABLED=0`. The [CI run](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/46091450701)
passed. The separate nightly heavy-store job was skipped as expected for a push. No real account was used.

| Where | Command or check | Result |
| --- | --- | --- |
| Local Windows/amd64, Go 1.27.4 | `gofmt -l .`; `go vet ./...`; `go mod verify` | Exit 0 each; no formatting changes; all modules verified |
| Local Windows/amd64 | `go vet ./internal/wa/testdata/cmd/session-check`; `go build -o NUL ./internal/wa/testdata/cmd/session-check`; `go test ./internal/wa/testdata/cmd/session-check` | Exit 0 each |
| Local Windows/amd64 | `go test -p 1 ./... -count=1 -timeout 30m` | Failed (`FAIL`): 10 packages passed, `internal/store` failed, and one package had no test files. `internal/mcp` passed. |
| Local Windows/amd64 | `go test -p 1 ./internal/store -run '^(TestToolMessageSearchScale\|TestToolMessageContextRowCap)$' -count=1 -timeout 9m -v` | Exit 0; 4 passed including subtests, 0 failed, 0 skipped. The timed search cases took 2.34 s and 2.35 s; the row-cap test took 1.96 s. |
| Local Windows/amd64 | `go test ./internal/wa -run '^TestSimulatorStopDuringEachSendStep$' -count=20 -json` | Exit 0; 200 passed including parents, 0 failed, 0 skipped |
| CI check job | `gofmt`, `go vet`, `go mod verify`, govulncheck v1.9.0, and builds for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 | All steps succeeded |
| CI Windows/amd64, Linux/amd64, macOS/arm64 | `go test -timeout 30m ./...` | Successful on each runner: 11 packages reported `ok`, one had no test files; no failures. Some packages used cached results on Windows and macOS. |

In the local serial suite, `TestToolMessageSearchScale` hit the 5-second deadline in `sparse_needle` and
`not-present`, and `TestToolMessageContextRowCap` also returned `context deadline exceeded`. Running packages
serially did not eliminate the timeouts. The isolated rerun passed; the root cause is still unconfirmed. No query,
deadline or test was changed to obtain the passing result.

### Native file and converter checks (T024, T031)

Counts include parent tests and subtests. Each CI command completed successfully (exit 0).

| Runner | `go test -timeout 10m -count=1 -v -run 'SendDirs\|SendPath\|SendConvert' ./internal/config ./internal/media` | `WA_FFMPEG_TESTS=1 go test -timeout 10m -count=1 -v -run RealFFmpeg ./internal/media` |
| --- | --- | --- |
| Windows/amd64 | 45 passed, 0 failed, 1 skipped (unprivate outbox); symlinks, linked outbox and case alias passed. 8.3 names were not exercised. | 13 passed, 0 failed, 0 skipped; Chocolatey launcher, FFmpeg 9.1 essentials |
| Linux/amd64 | 42 passed, 0 failed, 2 skipped (case alias on case-sensitive volume; device node needs root) | 12 passed, 0 failed, 1 skipped (AMR); apt FFmpeg 8.0.1-3ubuntu2 |
| macOS/arm64 | 43 passed, 0 failed, 1 skipped (device node needs root); case alias passed. The socket fixture could not be created and was reported as not verified in the log. | 11 passed, 0 failed, 2 skipped (Vorbis and AMR); Homebrew FFmpeg 9.0.2 |

The renewed design review covered the send-only frame and parameter limits, including worst-case JSON escaping,
bounded parsing, and retaining one tool credit. It noted that the test trace for the new bound and consolidation
into the project baseline could be clearer. This is a design verdict, not a code review.

### Code Review of `862034b` (CONCERNS)

The review confirmed that the passing native CI covered the same implementation, but the local Windows store
timeouts remain unexplained. No same-host baseline run against `main` was available at review time. The reviewer's permissions
prevented independent Go checks and access to the pinned fork in the module cache. No code `PASS` was issued.

The review also noted that `os.MkdirTemp` creates the per-send directory under the private work root and that the
directory is checked before use, but its creation does not go through `internal/private`. Native device-node
refusal on Linux and macOS, socket refusal on macOS, Windows 8.3 names, and Intel and physical Macs remain not verified.

### Same-Host Store Comparison (2026-11-28 UTC)

On Windows/amd64 with Go 1.27.4 and `CGO_ENABLED=0`, ran
`go test -p 1 ./internal/store -count=1 -timeout 30m -json` once on each revision, sequentially. The baseline
was a temporary export of `origin/main`; the feature ran in the existing checkout. Counts include subtests.

| Revision | UTC time | Exit | Pass / fail / skip | `sparse_needle` | `not-present` | `TestToolMessageContextRowCap` |
| --- | --- | --- | --- | --- | --- | --- |
| `origin/main` (`8c07f92`) | 01:47–01:53 | 0 | 738 / 0 / 12 | 2.45 s | 2.58 s | 2.43 s |
| Feature (`8dc4ad0`, same code as `862034b`) | 01:53–01:58 | 0 | 741 / 0 / 12 | 2.57 s | 2.85 s | 1.99 s |

Neither revision reproduced the timeouts in the store-only suite. This comparison establishes neither the cause
of the earlier failures nor a passing local full-repository run. No test or deadline was changed. The recorded code
verdict remains `CONCERNS`; no PR was opened.

### Local Windows Full Suite With Load Sampling (2026-11-28 UTC)

At `e543473` (same code as `25c6bb8`), with Windows/amd64, Go 1.27.4 and `CGO_ENABLED=0`, ran
`go test ./... -count=1 -timeout 30m -json` from 02:02:59 to 02:10:14 UTC. The Go command exited **0**:
11 packages passed, one had no test files, and 2,469 test events passed, 0 failed and 37 skipped (counts include
subtests). Default package parallelism and the existing deadlines were unchanged. No real account was used.

| Previously failing case | Time |
| --- | --- |
| Store search: `sparse_needle` / `not-present` | 2.34 s / 2.40 s |
| MCP search: `sparse_needle` / `not-present` | 3.84 s / 4.32 s |
| `TestToolMessageContextRowCap` | 2.05 s |

Agent Manager listed no managed sessions before the run; activity outside Agent Manager was not monitored.
Across 86 Windows counter samples taken five seconds apart, CPU averaged 50.96% (peak 100%), available memory
never fell below 2,895 MiB, and the test temp volume (`D:`) averaged 79.06% idle with a disk queue averaging 0.46
(peak 2.32). These are machine-wide measurements during the passing run only. The earlier failures were not
instrumented, so this does not establish whether other agents or resource contention caused them.

### Not done

| Task | State |
| --- | --- |
| T037 | documentation written; the `docs/roadmap.md` "Done" line waits for the merge |
| T038 | real account: **not verified**; needs the owner's permission for each step, including the client time-limit measurement |
| T039 | Implementation and evidence committed; code review returned `CONCERNS` at `862034b`. A current code `PASS` is still required before opening a PR. |
