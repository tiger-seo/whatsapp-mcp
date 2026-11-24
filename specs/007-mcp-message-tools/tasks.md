---
description: "Implementation tasks for MCP message tools"
---

# Tasks: MCP Message Tools

**Input**: `specs/007-mcp-message-tools/` design artifacts and `.specify/memory/constitution.md`

**Organization**: Tasks are grouped by independently testable user story. `[P]` marks work on separate files with no unmet task dependencies; coordinate shared files rather than editing them concurrently. Tests use synthetic offline fixtures only.

## Phase 1: Setup and Feasibility

- [X] T001 Review `specs/007-mcp-message-tools/contracts/store-and-lifecycle.md`, existing `internal/store/migrations/0001_init.sql` indexes, and `internal/mcp/results.go`; identify all global, chat, sender, chat/sender, exact-ID, direct and group seek shapes and their proposed bounds before the feasibility check.
- [X] T002 Run the isolated synthetic spike in `specs/007-mcp-message-tools/feasibility.md` outside `/speckit-implement`: prove exact sparse/empty results at 400,000 short rows and an older hit after 128 maximum-size nonmatches. Record candidate visits, examined bytes, timing and plans, including deep-offset, tied, alias and group seeks. Check that costly work is performed only on bounded candidates in cancellable first steps.
- [X] T003 Complete the remaining measurements in `specs/007-mcp-message-tools/feasibility.md`: field guards, folded expansion, temporary SQLite/Go/SDK allocations, 32 MiB SQLite tool share within the 256 MiB process cap, actual cancellation through connection return, writer progress and fence ordering, and the 50-match default-context fixture within 10 seconds. Record commands, dates, counts, exit codes and gaps. Align fixed measured production visit, batch and byte ceilings in `specs/007-mcp-message-tools/contracts/store-and-lifecycle.md`; mark feasibility Verified only when all criteria pass.
  Include the small-field, non-default context-row-cap fixture and record its separate probe and projection charges before marking feasibility Verified.
- [X] T004 Before `/speckit-implement`, commit the completed `specs/007-mcp-message-tools/feasibility.md` and aligned design, then record a fresh design PASS covering that commit in `specs/007-mcp-message-tools/verification.md`. Use `.claude/hooks/owner-review-gate.sh` to confirm that missing, pending or stale evidence and CONCERNS do not clear this feature. Do not wire scratch work into production.

## Phase 2: Foundation

**Prerequisite**: T001-T004 completed and current design PASS. No production implementation or story work before this gate.

- [X] T005 Add shared synthetic message fixtures and narrow reader test scaffolding in `internal/store/tool_message_test.go` for private SQLite snapshots, local aliases, field validation and absence of account/network capabilities.
- [X] T006 Implement the shared message read facade and bounded snapshot lifecycle in `internal/store/tool_reader.go`: one admitted read-only snapshot, validated selectors, synchronous non-reentrant sizing policy, cancellable first-step statements, and release statements/transaction/connection before encoding; keep store accounting until actual return and tool credit until transport retirement. Add the shared same-chat neighbour selection in `internal/store/tool_message.go`, bounding each side to 20 neighbours and counting at most 2,000 touched context rows, including probes and rejected materializations. Keep closest neighbours, return chronological sides, and mark only omissions of available neighbours.
  Follow the store contract's one-key lookahead: reserve initial probes for every requested side, then reserve the projection and any needed successor probe before advancing a side. Charge probes and projections separately.
- [X] T007 Implement shared validated public message projections in `internal/mcp/message_results.go` and `internal/store/tool_message.go`: `chat_jid` and `id` at most 256 UTF-8 bytes; UTC RFC 3339 timestamp; Boolean `is_from_me`; full sender address; empty missing strings; media type empty or `image`, `video`, `audio`, `document`; text and caption each at most 256 KiB and names at most 1 KiB; reject malformed stored fields without disclosure. Preserve local name precedence and separate text/caption.
- [X] T008 Add shared strict argument and output sizing support in `internal/mcp/message_tools.go` and `internal/mcp/message_results.go`: reject null, duplicates, unknown fields, invalid types and out-of-range values without echoing input; reserve envelope and markers, precharge both escaped representations, verify actual encoded bytes, and preserve existing tools and guarded transport.
- [X] T009 Verify shared lifecycle and capacity in `internal/store/tool_message_test.go` and `internal/mcp/message_tools_test.go`: four protocol requests, one tool, 16 active store operations, four reader connections, 64 ordinary waiters (65 while tool holds a slot), zero tool waiters, 5-second store cancellation, 10-second tool cancellation and at most 5-second output wait; cover actual return versus cancellation, failed output and redacted errors.

## Phase 3: User Story 1 - Find Messages (P1, MVP)

**Goal**: Search local messages with stable paging, optional context and bounded, explicit reply shaping.

**Independent test**: On disconnected synthetic SQLite history, compare combined filters, alias spellings, tied pages, captions and same-chat context against expected results; distinguish incomplete search errors from verified oversized replies and continue an unchanged maximum-size fixture without omissions or duplicates.

### Tests

- [X] T010 [P] [US1] Add store search cases in `internal/store/tool_message_search_test.go` for AND filters, exclusive dates including equal/reversed bounds, exact chat, author aliases and unmapped LIDs, Unicode case folding and canonical equivalence, literal punctuation/wildcards, caption-only matches, newest-first `(time, chat_jid, id)` ties, offsets and empty/end pages. Include the sparse, empty and large-field fixtures in `specs/007-mcp-message-tools/feasibility.md`.
- [X] T011 [P] [US1] Add SDK listing cases in `internal/mcp/message_list_test.go` for every schema argument/default, invalid/null/duplicate/unknown fields, types and byte/range bounds; validate context counts even when off, no neighbours when off, structured/text agreement and control-safe output.
- [X] T012 [P] [US1] Add synthetic budget cases in `internal/mcp/message_budget_test.go` for 50 maximum-size anchors with five maximum-size neighbours per side, plus maximum-escape content/captions; continue through all 50 identities exactly once on unchanged data, including one-match continuations, marked first-anchor previews, reduced sides and the unsupported-next-offset narrow-search result. At page 100,000 with limit 1, verify that a complete nonempty context page advises narrowing in both representations without marking the page reduced, supplying an invalid next page, or claiming that another match exists; an empty page carries no advice.
  Add the small-field, non-default context-row-cap fixture from `feasibility.md` against the guarded SDK and real test store. Assert that row credits, not bytes, stop expansion; check the touched-row ledger and both renderings' flags for known omissions, empty sides, proven ends and sides that met the requested count.
- [X] T013 [US1] Add incomplete-search versus reply-shaping cases in `internal/store/tool_message_search_test.go` and `internal/mcp/message_list_test.go`: work exhaustion with and without context returns search-specific `resource_limit`, narrower-filter advice and no messages, page metadata or continuation; a verified oversized context page alone may be shortened, whereas no-context listing returns the complete page or reply-size error.

### Implementation

- [X] T014 [US1] Implement indexed candidate search in `internal/store/tool_message_search.go` within the reviewed visit and batch ceilings from T003, including offset, aliases, duplicates and terminal probes. Keep at most two buffered streams and 200 page keys; seek by date/chat/sender, deduplicate composite keys and prove a full page or exhaustion before context.
- [X] T015 [US1] Implement bounded literal Unicode matching in `internal/store/tool_message_search.go`: evaluate existing folded fields only after source length and batch budget checks. Apply T003's cumulative original/folded-byte ceilings and per-field expansion bound; return work-limit error rather than treating exhaustion as no match.
- [X] T016 [US1] Implement context-list assembly and reply shaping in `internal/store/tool_message_search.go` and `internal/mcp/message_results.go`, reusing shared neighbour selection and reduction flags. Enforce 32 MiB cumulative result-field materialization, 2 MiB per representation and 8 MiB frame; admit full contiguous anchors before closest alternating neighbours.
- [X] T017 [US1] Implement context-list continuation and first-anchor fallback in `internal/mcp/message_results.go`: reserve metadata, use UTF-8-safe marked `content`/`caption` previews only when the first verified anchor cannot fit, fixed fallback at most 32 KiB per field, and resume at absolute first omitted offset with `limit: 1` or `narrow_search` above 100,000. Also emit `narrow_search` without page reduction on a complete nonempty context page at `page: 100000`, `limit: 1`. Never preview neighbours.
- [X] T018 [US1] Register and implement `list_messages` in `internal/mcp/message_tools.go`: default limit 50 (1-200), page 0 (0-100,000, checked product at most 100,000), query at most 1,024 UTF-8 bytes, timestamp range 2000 inclusive to 2100 exclusive, context default false and counts 5 each (0-20); retain existing local read boundary.
- [X] T019 [US1] Run the US1 synthetic store and SDK tests in `internal/store/tool_message_search_test.go`, `internal/mcp/message_list_test.go` and `internal/mcp/message_budget_test.go`, including the search-scale and default-context fixtures. Record named counts, date, exit status and fixture timings in `specs/007-mcp-message-tools/verification.md`.

## Phase 4: User Story 2 - Read Around a Message (P2)

**Goal**: Resolve a unique or exact-chat message and return its closest context without guessing.

**Independent test**: Seed identical IDs in multiple chats and neighbours at boundaries; test exact pair, unique ID, missing pair, and ambiguity at 2, 100 and 101 chats, including exact/no-context and oversize cases.

### Tests

- [X] T020 [P] [US2] Add exact lookup and neighbour tests in `internal/store/tool_message_context_test.go` for exact-chat no fallback, unique/missing IDs, ambiguity at 2/100/101 chats, timestamp/ID ties, unequal counts, explicit zero, sparse edges and chronological closest neighbours.
- [X] T021 [P] [US2] Add SDK context cases in `internal/mcp/message_context_test.go` for defaults 6 per side, 0-20 validation, and structured/text parity. Assert ordered, complete `matching_chats` at 2 and 100 matches; at 101, assert the limit error asks for `chat_jid` and neither representation contains a partial chat list or a selected message. Cover exact lookup with `before: 0` and `after: 0`, complete target or actionable size error, and neighbour-only reductions without previewing the target.

### Implementation

- [X] T022 [US2] Implement exact-pair and ID-only lookups in `internal/store/tool_message_context.go` using indexed equality and a 101-key sentinel; report all ascending chat addresses only for 2-100 matches, too-many without a partial list above 100, and reuse bounded same-snapshot context selection.
- [X] T023 [US2] Register and implement `get_message_context` in `internal/mcp/message_tools.go`, requiring `message_id`, accepting optional exact `chat_jid` and default 6 neighbours per side (0-20); map not-found, ambiguity and reply-size errors without private input or a misleading smaller-context remedy for an oversized target.
- [X] T024 [US2] Run the independent context tests in `internal/store/tool_message_context_test.go` and `internal/mcp/message_context_test.go`; record named counts, command, date and exit status in `specs/007-mcp-message-tools/verification.md`.

## Phase 5: User Story 3 - Latest Contact Interaction (P3)

**Goal**: Find the newest qualifying local direct or authored group message for a contact or its known alias.

**Independent test**: Seed phone/LID aliases, direct messages in both directions, group authors, owner self-chat and newer unrelated activity; each contact spelling selects the same newest qualifying message or the specified not-found error.

### Tests

- [X] T025 [P] [US3] Add store identity and ordering cases in `internal/store/tool_message_interaction_test.go` for mapped and unmapped identities, outgoing direct messages, group authors, owner self-chat only, unrelated newer activity, tied timestamps, no history and an unfinished group stream that could beat a direct candidate.
- [X] T026 [P] [US3] Add SDK interaction cases in `internal/mcp/message_interaction_test.go` for required `jid`, identity forms and validation, disconnected operation, one-message structured/text parity, and not-found. Distinguish an unverified outcome from an oversized verified message: both are `resource_limit` errors with distinct explanations, no partial message or preview, and no advice to use unsupported filters.

### Implementation

- [X] T027 [US3] Implement latest-interaction selection in `internal/store/tool_message_interaction.go` using same-snapshot local alias resolution, at most two direct seeks and two ordered sender streams, the shared visit ceiling and descending `(time, chat_jid, id)` comparator; check residual group/author rules after enumeration, skip group scans for own identity and refuse an unverified winner.
- [X] T028 [US3] Register and implement `get_last_interaction` in `internal/mcp/message_tools.go`, accepting required locally resolved `jid`, with no network lookup or account capability. Return exactly one complete message or distinguish verified no history, inability to verify the outcome within the work cap, and inability to fit the verified message within the reply cap. Never preview the selected message or suggest unsupported filters.
- [X] T029 [US3] Run the independent interaction tests in `internal/store/tool_message_interaction_test.go` and `internal/mcp/message_interaction_test.go`; record named counts, command, date and exit status in `specs/007-mcp-message-tools/verification.md`.

## Phase 6: Cross-Cutting Verification

- [X] T030 [P] Add guarded SDK offline acceptance in `internal/mcp/message_acceptance_test.go` for discovery of exactly three new tools alongside the five existing tools, every argument/default, all three journeys, no-result and duplicate cases, and both representations; use only synthetic fake account state. Against the delivered store and guarded SDK path, assert correct successful replies within 10 seconds for both variants of the 50-match default-context fixture and both 400,000-row default searches (12 sparse matches and proven empty). Time from request through reply receipt, excluding fixture setup; timing out or returning an error fails the fixture.
  For each new tool, induce a closed-store refusal through the guarded SDK. Assert an `unavailable` tool error in both renderings with safe local-session guidance and no result data, continuation, recovery or writes; distinguish it from an empty result, `not_found` and `resource_limit`.
- [X] T031 [P] Add native stdio regression and stalled-output tests in `cmd/whatsapp-mcp/message_stdio_test.go`: protocol-only stdout, redacted stderr, cancellation and actual credit retirement, no connection/pairing, no tool-originated writes, files or recovery; stored instructions, controls and fake approvals remain inert. Seed distinct synthetic credential, query, message-text and caption markers and assert that failure diagnostics and tool error text contain none of them.
- [X] T032 Add mixed ingest/read, WAL and drain regressions in `internal/store/tool_message_lifecycle_test.go`: refuse new tool reads if ordinary waiters are present, WAL is above 64 MiB, fresh free space is below 640 MiB, or measurement fails; check stable writer observation. Separately drive an already admitted reader above 128 MiB WAL and check cancellation/drain, existing checkpoint and fence order, writer progress and no early release.
- [ ] T033 Run `specs/007-mcp-message-tools/quickstart.md` feature acceptance with named tests confirmed in each package, `CGO_ENABLED=0`, synthetic fixtures and native Windows/Linux/macOS controls; record commands, dates, platform, commit, test counts, exit codes and unverified platforms in `specs/007-mcp-message-tools/verification.md`.
- [X] T034 Run gofmt, vet, module verification, full tests, vulnerability check and all five CGO-free build targets per `AGENTS.md` and `specs/007-mcp-message-tools/quickstart.md`; record actual command exit codes, counts and limitations in `specs/007-mcp-message-tools/verification.md`; do not treat cross-builds as native runtime evidence.
- [X] T035 Document the delivered local tools, budget and continuation behavior, and verification limits in `README.md`, `OPERATIONS.md`, and the current state in `AGENTS.md`; update `docs/backlog/INDEX.md` only if a tracked backlog item changes.
- [ ] T036 Obtain current owner code review of implementation and `specs/007-mcp-message-tools/verification.md` before PR creation or merge; record the dated reviewed commit and verdict, resolve BLOCK or record the user's decision for CONCERNS, and repeat if covered work changes.

## Dependencies and Execution Order

- T001 → T002 → T003 → T004; feasibility failure blocks and returns to design. T004 is the owner review gate, not an approval granted by this task list.
- T004 → T005-T009. T006 → T007 → T008 → T009; T005 supplies fixtures. All stories depend on the foundation.
- US1: T010-T012 may proceed in parallel on separate files; T013 follows those fixtures. T014 → T015 → T016 → T017: T016 consumes the verified base page from T014/T015 before assembling context. T018 follows store and shaping work; T019 verifies the story. Coordinate shared `internal/store/tool_message.go` and `internal/mcp/message_results.go` edits.
- US2: T020 and T021 may proceed in parallel; T022 reuses shared context selection T006, T023 follows T022, and T024 verifies. US2 has no dependency on US1; coordinate edits in the shared tool registry.
- US3: T025 and T026 may proceed in parallel; T027 follows the foundation and T028 follows T027; T029 verifies. Coordinate registration in `internal/mcp/message_tools.go` with US1/US2.
- T030 and T031 can run in parallel after relevant story implementations; T032 follows lifecycle integration. T033-T034 follow completed tests and implementations. T035 documents the verified behavior, and T036 follows recorded evidence and docs. Native platform evidence may be gathered independently without claiming unrun platforms passed.

## Implementation Strategy

Complete feasibility and the owner design gate first, then foundation and US1 as the MVP. Verify each story independently before delivering all three together. Run cross-cutting synthetic, concurrency, native and regression gates before the owner code review. No real account, production wiring before design PASS, schema migration, new dependency, or new account/network capability is authorized by these tasks.
