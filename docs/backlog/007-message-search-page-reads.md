# Message search over large histories exceeds the store deadline on macOS

**Created**: 2026-11-26

**Status**: Open; deferred. The owner set the macOS guarantee at 100,000 messages on 2026-11-27 and it passed native
acceptance. Faster search over larger macOS histories, and Intel Macs, remain.

## Problem

On the 400,000-row acceptance fixture, a default `list_messages` search on the macOS/arm64 CI runner often reaches the
5-second store deadline. Windows and Linux pass. Measurements are in the feature 007
[verification record](../../specs/007-mcp-message-tools/verification.md#smaller-macos-sizes-results-2026-11-26-utc)
and the sections above it.

- An unfiltered search walks `messages_time` and looks up each row in `messages`. It reads about 25,700 pages of
  4 KiB (105 MB), which is both B-trees once.
- On macOS, exact requests spent 1.5-2.8 s inside `pread` and 2.2-2.4 s elsewhere. In slow runs the time per read
  reached 200 µs, which extrapolates to about 5.1 s for the full read set. Faster query code alone would not cover
  that case.
- `messages_time` is declared newest-first while messages mostly arrive in ascending time, so in the fixture its
  pages are about half empty (21.6 MB of content in 46.8 MB).

## Decision (2026-11-27)

The owner has no Apple Silicon Mac and chose to keep the 5-second deadline and set the macOS timing guarantee at
100,000 messages; Windows and Linux keep 400,000. At 400,000 none of the four standard GitHub macOS runner types
passed every time. Measured on `macos-26` and `macos-15` (arm64), three machines for each size and runner:

| Size | Result | Slowest reply |
| --- | --- | --- |
| 100,000 | Qualified on both | 2.14 s |
| 200,000 | Failed on both | Timeout (`macos-26`), 4.33 s (`macos-15`) |
| 400,000 (control, same machines) | 1 of 12 runs passed | Timeout |

Sizes between 100,000 and 200,000 were not measured. These are shared virtual machines and do not show how a physical
Mac behaves.

## Still open

- **Larger macOS histories.** A search beyond the guaranteed size can return `timeout`. Real accounts can hold more.
- **The timeout gives no remedy.** The message says only that the request timed out; advice to narrow by chat, sender
  or date would help an LLM client avoid repeating the same search. It changes the tool contract.
- **Intel Macs.** At 100,000 the named acceptance passed on both Intel runner types, but
  `TestMessageBudgetContextRows` (a `list_messages` call expanding 2,000 context rows) exceeded the deadline on 3 of 6
  machines in one run and 1 of 6 in the next. Windows, Linux and Apple Silicon passed it.

## Rejected: a covering search index

Add, by a versioned migration, an ascending index on `(time, chat_jid, id, text, caption, text_fold, caption_fold)`
and use it for the unfiltered search. SQLite walks it backwards for newest-first order, reads one B-tree, and keeps
the same field guards, UTF-8 validation, literal matching, work ceilings and deadlines.

| Fixture measurement (scratch, Windows) | Pages | Size |
| --- | ---: | ---: |
| Read today: `messages` + `messages_time` | 25,738 | 105.4 MB |
| Covering index, newest-first key order | 18,971 | 77.7 MB |
| Covering index, ascending key order | 10,512 | 43.1 MB |

At the per-read times seen on macOS (58-200 µs), 10,512 reads would take about 0.6-2.1 s. If the other 2.2-2.4 s did
not change, a request would take roughly 2.8-4.5 s. **This is arithmetic, not a measurement**, and the slow end leaves
little margin. Removing the row lookups should also save some CPU time; the amount is unmeasured.

### Result under the acceptance load (2026-11-26)

With the other acceptance packages running, today's schema timed out on all six first searches over short messages.
With the index, four of six met the 4.0 s bar, one took 4.56 s and one timed out. **It is not adopted.** The sections
below are the proposal and the earlier measurements as they stood.

### Prototype results on an idle runner (2026-11-26)

Measured in the opt-in diagnostic with the index in place of `messages_time`; details are in the verification record.

- On a fast macOS runner, first searches over short messages took 1.1-1.7 s with the index and 1.4-3.4 s without.
  Today's schema also met the agreed 4.0 s bar there, so this does not show that the index gives headroom.
- **The page saving depends on arrival order.** Oldest-first arrival, as in the acceptance fixture, saves 59% of the
  pages read. Newest-first arrival saves only 8% (18,971 against 20,674 pages), because the index then fills about
  half of each page while today's index fills them fully. The mix in real databases is unmeasured.
- **Long messages get worse.** With 256 KiB fields the database doubles, ingest takes 1.5-2.1 times as long, a search
  reads 38-51% more bytes and the first request is somewhat slower.

### What it costs

- **Storage and writes.** The index repeats text, caption and both folded forms. Added to today's schema it would
  grow the short fixture by about 10% (43 MB on 433 MB); in place of `messages_time` the fixture is slightly smaller.
  Long messages grow it much more: see above. Every insert and edit writes the larger entries.
- **Migration.** Existing databases build the index at the next start, and older binaries then refuse the database.
- **Coverage.** Only the unfiltered search changes. A chat-filtered or sender-filtered search over a very large chat
  or author still reads two B-trees, and no acceptance fixture measures it.
- **Design work.** The feature 007 plan assumes no migration. The revision changes the plan, research, store contract,
  feasibility evidence and tasks, and needs a new design review before implementation.

### Gate before adopting (applied: failed)

Every first request with the index exact in 4.0 s or less over at least three rounds, in a run where today's schema
misses that bar.

### Open question

Whether the new index should replace `messages_time` instead of being added to it. The prototype replaced it; every
other newest-first read would then walk the wider entries, which is not measured.

## Other options

| Option | State |
| --- | --- |
| Map the database for reads (`mmap_size`) | Measured and rejected: first requests did not improve, resident memory rose by more than 300 MB, and a read error would fault the process. |
| Query changes without a schema change | Not prototyped; optimisation stopped. Could reduce time outside `pread`, not the read set. |
| Larger database page size | Not measured. Fewer, larger reads, but applies only to new or rebuilt databases and to the device store too. |

## Review trigger

A report of search or context timeouts, before the first release, or before claiming Intel macOS support.
