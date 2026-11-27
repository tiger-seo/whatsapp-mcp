# Search Feasibility Gate

**Status**: Verified

T001-T003 use synthetic scratch data outside `/speckit-implement`. This is neither product implementation nor owner approval. Implementation requires a fresh design `PASS` on the commit with verified evidence and the corresponding design changes; `CONCERNS` does not clear this gate.

| Required fixture or check | Acceptance evidence |
| --- | --- |
| 400,000 short messages (100,000 for the timed fixtures on macOS since 2026-11-27) | Default filtered searches return all 12 expected sparse matches or a proven empty result, within the inherited cancellation deadlines. Spread matches throughout history, including beyond the former 100,200-visit window. |
| Larger source-field volume | Retrieve a match older than 128 nonmatching rows with 256 KiB each of text and caption, without treating skipped or unverified rows as nonmatches. Record actual original and folded lengths. |
| Verified wide reply and hard work cap | Default context on the 50-match maximum-size and escape-heavy fixtures succeeds, and unchanged continuations recover all identities; an unfinished search instead returns no data or continuation. |
| Context row cap independent of reply bytes | With non-default counts and small fields, exhaust available context row credits before any byte ceiling binds. Record positive probes, point projections (including rejected ones if any), and the final ledger. Verify closest-neighbour order and both renderings' flags against the seeded history: a known omitted key marks a side reduced; an empty, exhausted or fully requested side does not. No read may exceed the 2,000-row ceiling just to decide a flag. |
| Bounded query and memory | Record seek plans, visited candidates, batch size and selected limits for plain, tied, deep, alias, exact, group and malformed-field cases. Prove prechecks bound values before materialization, transient heap fits the existing envelope, and candidate/text ceilings are enforced on consumed work. |
| Lifecycle | Show driver cancellation through actual connection return, writer progress and WAL fence ordering. Record all tested platforms and gaps. |

Set `**Status**: Verified` only when the required evidence is complete. Record commands, toolchain/pins, datasets, elapsed times, examined rows/bytes and exit statuses here. Set fixed candidate, batch and byte ceilings in the reviewed contract based on the measurements. If the fixtures cannot fit within existing limits, revise and re-review; changing accepted behavior requires an owner decision.

## Preliminary Windows Probe (2026-11-23)

An isolated `go run D:/tmp/kilo/search_probe_007.go` used Go 1.27.4, `modernc.org/sqlite` v1.62.0, `CGO_ENABLED=0`, `GOPROXY=off` and `-mod=readonly`. The process printed all expected cases; its wrapper did not expose a numeric exit status. The scratch source and database were removed. The simplified index seek reported `SEARCH m USING COVERING INDEX m_order (t<?)`. Separate length and body reads followed key enumeration for each candidate.

| Fixture | Trial | Result |
| --- | --- | --- |
| 400,000 short rows, 12 sparse matches or no matches | 100,200 visits; 64 or 256 keys per batch | Neither search proved its result; 1,903,803 original and folded bytes examined in 15.76-25.13 s. |
| Same short-row fixture | 400,001-visit ceiling; same batches | Exact 12 and zero results, checked against independent SQL on the ASCII fixture; 400,000 rows and 7,600,012 original and folded bytes in 70.73-80.02 s. |
| 128 maximum-field nonmatches before an older hit | 64 MiB original-field trial cap, then 80 MiB | The first stopped before reading the hit (67,108,864 bytes, 1.30 s). The second found it and proved the end (67,108,876 bytes, 1.52 s). Both visited 129 keys. |

The per-candidate field-read approach does not meet either search deadline for short histories at scale. An unrestricted SQL reference answered in 0.51-0.53 s, but is not a bounded-work solution. The simplified probe did not verify the production index shapes, cancellation and actual return, memory, aliases, concurrency, Unicode expansion or SDK reply shaping.

## Bounded Batch Probe (2026-11-23)

Windows/amd64, Go 1.27.4, pinned SQLite v1.62.0. From the repository root, `go run D:/tmp/kilo/wa007_batch_probe.go` with `CGO_ENABLED=0`, `GOPROXY=off`, and `GOFLAGS=-mod=readonly` exited 0 after nine search trials and one cancellation trial. The scratch program uses migration `0001_init.sql`, no live account, SQLite memory statistics enabled before opening, one connection and a read transaction per search. The probe remains outside the repository and production code.

| Check | Result |
| --- | --- |
| 400,000 short rows with tied timestamps | Six trials at 64, 256 and 512 keys per batch returned the exact 12 IDs in order or an empty result in 1.301-1.705 s. Each examined 400,000 keys and 6,399,964 bytes each of original and folded text. A 100,200-visit trial returned a limit error with no results. |
| Maximum field volume | At 64 keys per batch, 64 MiB correctly returned a limit error before the older match. The 80 MiB trial returned the exact older hit after 129 visits and 67,108,870 bytes each of original and folded text (330 ms). |
| Query shape and heap | `EXPLAIN QUERY PLAN` showed a covering tuple seek on `messages_time`, bounded materialized key/length batches and rowid point reads. The temporary sort is over matched keys in the admitted batch only. SQLite process high-water marks were 8,830,664-9,355,168 bytes in isolation; Go counters measured cumulative allocations, not peak live memory. |
| Standalone cancellation and return | A 15 ms deadline on a large-field first-step query returned `context deadline exceeded` in 16 ms. The driver still showed one leased connection at query return, then zero after explicit close; the pool served the next query. This does not verify store admission, snapshot retirement under writer pressure, or transport credit. |

The global batch query replaces the slow per-candidate field reads as the candidate design. The numeric trial ceilings are not production defaults. The record stays Pending until the remaining indexed shapes, per-field fold expansion, shared memory and writer/WAL lifecycle, and SDK context assembly are verified and a current design review covers the committed evidence.

## Seek and Unicode Expansion Probe (2026-11-23)

On Windows/amd64 with Go 1.27.4, the pinned SQLite and Unicode tables, and `CGO_ENABLED=0`, `GOPROXY=off`, `GOFLAGS=-mod=readonly`, `go run D:/tmp/kilo/wa007_batch_probe.go D:/tmp/kilo/wa007_seek_fold_probe.go` exited 0. An earlier harness run exited 1 because its fixed-chat expectation included the exclusive cursor row; the corrected fixture uses an in-chat upper cursor that includes that row. Both programs are scratch only and remove the synthetic database on exit.

| Check | Result |
| --- | --- |
| 400,000 short rows | All six sparse/empty trials returned exactly 12/0 matches, visiting 400,000 candidates and admitting 6,399,964 bytes each of source and folded fields per trial in 1.441-2.288 s. The separate 100,200-visit trial returned no partial result. |
| Indexed batch shapes | SQLite selected covering seeks on `messages_time`, `messages_chat_time`, `messages_sender_time`, `messages_sender_jid`, `messages_chat_sender_phone`, and `messages_chat_sender_jid`. The individual tied continuation and filtered batch checks returned expected IDs, each with 2-4 candidates and 12-34 admitted bytes each of source and fold. The deep keyset, direct-chat latest, pair, and 101-key ID-sentinel queries showed the expected seek plans; those extra four checks inspected plans only. |
| Maximum source volume, ASCII | The older match succeeded after 129 visits and 67,108,870 bytes each of source and fold with the 80 MiB source trial cap (379 ms). The 64 MiB source cap failed closed. |
| Maximum source volume, Unicode | Each 262,144-byte source field of repeated U+0390 folded to 786,432 bytes. The older match succeeded after 129 visits and 67,108,870 source / 201,326,598 folded bytes in 753 ms with trial caps of 80 MiB source / 320 MiB fold. At 192 MiB folded, the older row exceeds the remaining allowance and the probe returned no result. |
| Oversized stored fold | An injected 1,048,578-byte folded field rejected the first 64-key batch with no results or admitted text bytes (4 ms); this exercises the prototype's length rejection, not the production field validator. |
| Isolated heap and cancellation | SQLite process high-water marks did not exceed 9,355,224 bytes during search trials. This was a one-connection probe, not shared-heap evidence; Go measurements were cumulative allocations only. A 15 ms deadline was observed at 17 ms, with the lease still in use until explicit close and a subsequent pool query succeeding. |

The trial values are not production ceilings. Still required: integrated alias merge/deduplication, deep offset and latest group outcomes; a justified upper bound for folding rather than one 3x example; memory high-water under the existing shared heap and writer pressure; and message assembly through the guarded SDK for maximum-size, escape-heavy, continuation and context-row-cap fixtures. The current guarded encoder and WAL regressions are baseline evidence only. This record remains Pending.

## Alias Paging and Guarded Reply Probes (2026-11-23)

Both probes ran on Windows/amd64 with Go 1.27.4, `CGO_ENABLED=0`, `GOPROXY=off`, and `-mod=readonly`. Neither probe registers a production tool or touches an account.

| Check | Result |
| --- | --- |
| SQLite alias merge at offset 100,000 | The scratch merge used 256-key batches over separately ordered phone and LID streams with overlapping rows and tied timestamps. It matched an independent SQL reference for all 20 page keys after deduplication: 100,020 unique matches, 80,017 and 80,016 branch visits, 440 ms for merge and page selection. The newest group candidate matched the group reference, and a newer outgoing direct message won. This exercised keys only (no text read or folded matching), hardcoded aliases, and no production admission or shared visit-cap enforcement. An earlier fixture had the direct message tied with the group message but expected it to win; raising the direct timestamp corrected the fixture. |
| Guarded SDK, ordinary maximum fields | Scratch assembly using the existing size charger, safe text renderer and guarded in-memory SDK transport returned all 50 distinct identities in order over 48 calls and reported reduced context. The initial request took 168 ms; the slowest call took 421 ms. The largest serialized SDK result was 3,147,378 bytes before its JSON-RPC envelope. Both representations matched semantically and were individually below 2 MiB. |
| Guarded SDK, escape-heavy maximum fields | The same path returned all 50 identities in order over 50 calls, with a marked first-anchor preview and reduced context. Initial request: 80 ms; slowest: 122 ms; largest serialized SDK result: 852,698 bytes before its envelope. An earlier run incorrectly applied the per-request deadline to the entire continuation sequence; the rerun measured each SDK call with a real clock outside the test's virtual clock. |

These guarded fixtures used synthetic in-memory projections and a scratch response schema. They do not establish end-to-end timing with SQLite projection, exact public response shape, native framing, actual context row accounting, or peak live allocations. The gate remains Pending until those checks, the justified folding bound, shared-heap and writer-pressure/cancellation evidence, and fixed production ceilings are complete.

## Admitted Query Under WAL Pressure (2026-11-23)

An external Go test overlay exercised the scratch candidate query through the existing `toolRead` and `Store.read` admission and lease path on Windows/amd64 (Go 1.27.4, pinned SQLite, `CGO_ENABLED=0`, `GOPROXY=off`, `-mod=readonly`). Four runs passed. A read-only snapshot processed 3-5 completed 64-key batches; each completed batch checked 16 MiB of source fields and 16 MiB of folded fields and returned zero hits. Synthetic writes committed eight 16 MiB transactions, moving the WAL from 118,301,712 to 135,201,952 bytes on the crossing transaction. The candidate read returned cancellation while the fixture still held the snapshot and reader lease. A replacement tool was refused and a follow-up writer timed out at the fence. After allowing rollback and connection return, the reader usage was zero and a subsequent write succeeded.

The process-wide SQLite high-water mark was 6,117,392-6,270,256 bytes in these runs. This measures one candidate reader and one synthetic writer for this workload, not an isolated 32 MiB per-tool accounting proof, worst-case process saturation, or Go/SDK live heap. The pressure harness deliberately repeats its batch and delays rollback after cancellation to observe the fence. It does not exercise the production search ledger or the integrated SQLite-to-SDK reply path. Status remains Pending.

## SQLite-to-Guarded-MCP Continuation (2026-11-23)

A scratch overlay seeded the existing SQLite schema with 50 anchors and 500 same-chat neighbours per fixture. Both content and caption were 256 KiB per row. Each request selected fixture anchors and projected anchors and neighbours in one read-only snapshot, returned the snapshot before final encoding, and sent the result through the existing guarded in-memory MCP transport. The client began with the default page size and then used the returned `next_page` and ordinary one-match pagination. It verified all 50 identities, in order and exactly once, followed by an empty page; it also checked same-chat neighbour identity and order, reduction markers, preview metadata presence, and semantic agreement between both renderings.

| Fixture | Initial reply | Slowest call | Calls including empty page | Largest serialized SDK result (before envelope) |
| --- | ---: | ---: | ---: | ---: |
| Ordinary maximum-size fields | 202 ms | 215 ms | 49 | 3,147,790 bytes |
| Escape-heavy maximum-size fields | 69 ms | 82 ms | 51 | 852,942 bytes |

This demonstrates that the default oversized context reply and its continuation can work with real SQLite projection, not just generated in-memory DTOs. It uses a fixture-specific anchor selector and a scratch tool handler rather than the production search ledger and store admission. It does not exercise the non-default 2,000-context-row boundary, establish worst-case shared SQLite or live Go/SDK heap, prove a folded-field bound, or measure native framing. No production ceilings were selected; the feasibility gate remains Pending.

## Context Row Cap and Probe Reruns (2026-11-23)

Windows/amd64, Go 1.27.4, pinned SQLite v1.62.0 and `golang.org/x/text` v0.43.0, `CGO_ENABLED=0`, `GOPROXY=off`, `-mod=readonly`. Commands and exit codes are recorded in [verification.md](verification.md). All probes used synthetic data and scratch programs outside the tracked source tree.

| Probe | Measured result | Limitation |
| --- | --- | --- |
| Independent context row cap | Three runs with 60 anchors, 20 requested neighbours per side, tied timestamps and one-byte text. Each charged 1,035 positive probes plus 965 point projections, exhausting exactly 2,000 credits. Two empty probes returned their reservations. Both renderings agreed on neighbour identity and order, 70 reduced sides, 48 fully requested sides, one empty side and one proven short side. No page reduction. Structured and text were each 207,477 bytes; serialized SDK result was 454,552 bytes before the envelope. Request and in-memory session setup took 127-139 ms, excluding fixture seeding. | Real SQLite schema and guarded SDK, but fixture-supplied anchors and a scratch allocator; all projections fit in this fixture. Does not verify native framing, store admission or combined row-and-byte rejection. |
| Search and byte caps | Six 400,000-row trials confirmed exact 12/0 results in 1.349-2.777 s. The 100,200-visit trial returned no partial result. The older match followed 128 maximum-field nonmatches and was found after 129 visits: 67,108,870 source bytes for both variants, 67,108,870 folded bytes for ASCII and 201,326,598 for expansion-heavy Unicode. Narrower byte caps and a malformed oversized folded field failed closed. The standalone first-step cancellation returned in 16 ms; the pool served a new query after lease close. | Search-only timing, not one admitted SDK request. The 9,355,224-byte maximum isolated SQLite high-water mark and cumulative Go allocation counters do not prove shared or peak live memory bounds. |
| Unicode expansion | The exact pinned NFC-then-fold pipeline checked all 1,112,064 Unicode scalars individually; the largest singleton expansion was 6 bytes from 2 (U+0390). Of eight long-pattern trials, repeated U+0390 reached 786,432 output bytes from 262,144 input; repeated U+0344 inserted 8,738 stream-safe combining grapheme joiners. | The 3x singleton result is not an upper bound for arbitrary strings. Normalization across scalar boundaries and stream-safe insertion must be included in a justified bound. |
| Oversized context and WAL | Both guarded continuation fixtures recovered all 50 identities. Initial replies took 203/80 ms; slowest individual requests took 219/106 ms (ordinary/escape-heavy). Three admitted-reader pressure runs crossed from 118,301,712 to 135,201,952 WAL bytes, retained the fence until reader return and then allowed the writer to proceed. Process-wide SQLite high-water was 6,117,376-6,244,064 bytes in this workload. | These remain separate scratch probes, not integrated admission-to-wire measurements or worst-case saturation evidence. |

## Integrated Admission and Fold Envelope (2026-11-23)

An external Go overlay used the real store admission (`toolRead` and `Store.read`), one read-only SQLite snapshot, and the guarded SDK in-memory transport. It checked that the reader lease was returned before encoding and waited for the guard's response write to finish before making the next call. Windows/amd64 only; no production tools or account actions. Commands are in [verification.md](verification.md).

| Fixture | Result |
| --- | --- |
| 400,000 short rows | Exact 12 sparse matches in 3.294 s and proven empty in 2.327 s; both visited 400,000 rows and charged 6,399,964 bytes each of source and fold. The sparse call overlapped eight 2 MiB commits while holding the snapshot. The 100,200-visit trial returned a guarded limit error without results in 516 ms. |
| Expanded older hit | Found the older message in 870 ms after 129 visits, 67,108,870 source bytes and 201,326,598 folded bytes. |
| Conservative field envelope | A synthetic 63-row fixture placed both folded fields at 2,621,440 bytes per row field, not claiming that the fold pipeline produces those values. It proved empty in 1.633 s while eight 2 MiB writes ran within the snapshot's lifetime. It charged 33,030,144 source and 330,301,440 folded bytes. An over-bound field failed closed. SQLite process high-water was 9,809,720 bytes; sampled Go heap grew by 2,648,816 bytes above its baseline. |
| Oversized context continuation | Both modes returned all 50 IDs once (49 ordinary and 51 escape-heavy calls including terminal empties), with semantic parity. Slowest requests: 251 ms ordinary, 337 ms escape-heavy. The ordinary sampled Go heap peaked at 57,688,640 bytes on this run and 65,346,840 bytes on the earlier passing run. SQLite high-water was about 5 MB for these fixtures. |

**Pinned Unicode output-length bound.** For valid UTF-8 of `N` bytes, exhaustive scalar measurements on `golang.org/x/text` v0.43.0 with Unicode 17.0 on Go 1.27.4 give `bytes(NFD(r)) <= 3 * bytes(r)` and `bytes(Fold(r)) <= 3 * bytes(NFD(r))`. NFC preserves canonical decomposition up to stream-safe joiner insertion, and the pinned full default fold is context-independent. The pinned normalizer can insert at most one U+034F per source scalar that triggers stream-safe overflow; those scalars are non-ASCII and cost at least two source bytes. Each inserted joiner survives folding as two bytes. Thus the result is bounded by `9N + 2 floor(N/2) <= 10N`, or 2,621,440 bytes for a 256 KiB source field. This is an output-length bound for the pinned tables, not an allocation bound or a cumulative work ceiling. Recheck on toolchain/table changes.

## Fixed-Ceiling Verification (2026-11-23)

Use 400,001 visits, 256 keys per batch, 80 MiB original and 320 MiB folded bytes per call. The extra visit credit lets the cursor prove end after the 400,000-row fixture. The byte ceilings cover the default large-field fixture including its expansion-heavy variant; they do not promise an exact answer for an arbitrarily large matching range. The existing deadlines, frame cap and context ledger are unchanged.

| Focused probe | Result |
| --- | --- |
| Key-only exact, alias and group seeks | The composite index returned the exact pair without fallback. An unordered indexed probe touched at most 101 keys for 2, 100, 101 and 102 matching chats; ordered SQL before the limit was rejected because its plan sorted the matching range. The deep alias merge agreed with the independent 20-key page at offset 100,000: 160,033 branch visits, 60,012 duplicates and 626 first-step aggregate reads in 748 ms. The latest group key matched the reference and the newer outgoing direct message won. The unfinished-stream error remains a delivery regression case. |
| Fixed batch and heap guard | Under a scratch-only 32 MiB hard SQLite **process** cap, both 400,000-row calls succeeded (3.668 s sparse, 2.540 s empty), and the expanded older match returned in 1.099 s. Both 2.5 MiB folded fields on 63 synthetic rows consumed 330,301,440 folded bytes while eight 2 MiB writes overlapped the snapshot; process SQLite peak was 9,831,880 bytes. A 256-key batch used maximum-width chat addresses and IDs; the over-width key and over-bound fold were rejected. Production's 256 MiB process cap is unchanged. |
| Reply and lifecycle | With the same scratch heap guard the 100 continuation requests recovered both sets of 50 IDs; the slowest ordinary and escape-heavy calls took 379 and 163 ms. The separate row-cap and cancellation/fence probes above cover their respective boundaries without requiring all three tools in the scratch build. |

**Allocation basis.** Keep bodies out of key, length, cursor, match and sort intermediates. With 256-byte ID and chat fields, at most 712 retained keys (two 256-key streams and 200 page keys) hold at most 364,544 raw key-string bytes; a 256-key aggregate with sixfold byte escaping, fixed fields and framing is below 1 MiB. Decode into the fixed key shape and discard each aggregate after its refill. SQLite only inspects one admitted row's bodies at a time, not all the bytes debited to the cumulative ledger. The projection path holds the precharged reply and one bounded scratch projection, not every projected or rejected row. The reply's raw strings cannot outgrow their charged representations; marshal once after lease return, keep each representation within 2 MiB and keep the entire encoded frame within 8 MiB. Only one reply-producing tool is admitted through transport retirement. These storage and buffer bounds, plus the reduced-hard-cap run with writer overlap, are the design evidence; sampled Go heap (55,202,736 bytes peak for ordinary replies on the last run) is an observation, not a runtime heap quota. Verify these shapes and actual allocations in the implementation checks. No production path, native framing, Linux or macOS was verified by these probes. A current design `PASS` is still required.

## Native macOS Measurement and Platform Sizes (2026-11-27)

The evidence above is from Windows. The delivered search was then measured on hosted native runners; commands, revisions, counts and logs are in the [verification record](verification.md#smaller-macos-sizes-results-2026-11-26-utc).

| Check | Result |
| --- | --- |
| 400,000 short rows, Windows/amd64 and Linux/amd64 | Exact sparse and empty replies in every recorded CI acceptance run. Some earlier local Windows runs under concurrent load timed out, on earlier search code. |
| 400,000 short rows, macOS | No standard runner type (`macos-26`, `macos-15`, `macos-26-intel`, `macos-15-intel`) passed all runs within a 4.0 s bar on three machines each; many searches reached the 5-second store trigger. |
| 100,000 short rows, macOS/arm64 | `macos-26` and `macos-15`: all full test runs and all 18 named runs passed with exact results; slowest guarded reply 2.14 s. Store-level searches took 0.26-0.88 s. |
| 200,000 short rows, macOS/arm64 | Failed on both runner types. |
| Other fixtures on macOS/arm64 | The large-field search, 50-anchor continuation, context row-cap and lifecycle fixtures passed in the runs that recorded them; the failures were the 400,000- and 200,000-row searches and one native stdio cancellation run, analysed in research R5. |

By the owner's decision the short-row criterion is 100,000 on macOS; the other criteria are unchanged. Intel macOS at 100,000, sizes between 100,000 and 200,000, and physical Macs were not measured. The corrections in [research R5](research.md) change no ceiling. With growing refills a one-match first page may read up to one batch past its match, as other page sizes already do; all visits and bytes stay charged to the same ledger.
