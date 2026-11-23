# Search Feasibility Gate

**Status**: Pending

Run T001-T003 on synthetic scratch data outside `/speckit-implement`. This is neither product implementation nor owner approval. Commit the complete evidence and the corresponding design changes before requesting a new design review. A review of this pending record, even an accepted `CONCERNS`, does not clear implementation.

| Required fixture or check | Acceptance evidence |
| --- | --- |
| 400,000 short messages | Default filtered searches return all 12 expected sparse matches or a proven empty result, within the inherited cancellation deadlines. Spread matches throughout history, including beyond the former 100,200-visit window. |
| Larger source-field volume | Retrieve a match older than 128 nonmatching rows with 256 KiB each of text and caption, without treating skipped or unverified rows as nonmatches. Record actual original and folded lengths. |
| Verified wide reply and hard work cap | Default context on the 50-match maximum-size and escape-heavy fixtures succeeds, and unchanged continuations recover all identities; an unfinished search instead returns no data or continuation. |
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
