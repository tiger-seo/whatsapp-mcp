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
