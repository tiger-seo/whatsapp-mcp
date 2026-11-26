# Message search misses the store deadline on native macOS

**Created**: 2026-11-26

**Status**: Open; design revision proposed, awaiting the owner's decision. Blocks tasks T033 and T036 of feature 007.

## Problem

On the 400,000-row acceptance fixture, a default `list_messages` search on the macOS/arm64 CI runner often reaches the
5-second store deadline. Windows and Linux pass. Measurements are in the feature 007
[verification record](../../specs/007-mcp-message-tools/verification.md#mapped-read-candidate-2026-11-26-utc).

- An unfiltered search walks `messages_time` and looks up each row in `messages`. It reads about 25,700 pages of
  4 KiB (105 MB), which is both B-trees once.
- On macOS, exact requests spent 1.5-2.8 s inside `pread` and 2.2-2.4 s elsewhere. In slow runs the time per read
  reached 200 µs, which extrapolates to about 5.1 s for the full read set. Faster query code alone would not cover
  that case.
- `messages_time` is declared newest-first while messages mostly arrive in ascending time, so in the fixture its
  pages are about half empty (21.6 MB of content in 46.8 MB).

## Proposed revision: a covering search index

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

### What it costs

- **Storage and writes.** The index repeats text, caption and both folded forms. The fixture grows by about 10%
  (43 MB on 433 MB). Long messages grow it much more: the message text is stored twice, and large entries spill into
  overflow pages. Every insert and edit writes one more index.
- **Migration.** Existing databases build the index at the next start, and older binaries then refuse the database.
- **Coverage.** Only the unfiltered search changes. A chat-filtered or sender-filtered search over a very large chat
  or author still reads two B-trees, and no acceptance fixture measures it.
- **Design work.** The feature 007 plan assumes no migration. The revision changes the plan, research, store contract,
  feasibility evidence and tasks, and needs a new design review before implementation.

### Gate before adopting

Prototype the index and its query in an opt-in diagnostic and compare it with the current search on native macOS:
first and repeat requests, reads, time, memory, database size and ingest time. Include the large-field fixtures.
Adopt it only if first requests pass repeatably with margin.

### Open question

Whether the new index should replace `messages_time` instead of being added to it. In the fixture that would make the
database slightly smaller than today, but every other newest-first read would walk the wider entries. Not measured.

## Other options

| Option | State |
| --- | --- |
| Map the database for reads (`mmap_size`) | Measured and rejected: first requests did not improve, resident memory rose by more than 300 MB, and a read error would fault the process. |
| Query changes without a schema change | Not prototyped. Could reduce the 2.2-2.4 s outside `pread`, not the read set. May be needed together with the index. |
| Larger database page size | Not measured. Fewer, larger reads, but applies only to new or rebuilt databases and to the device store too. |
| Change the deadline, fixture or success criterion | Not proposed. Only the owner can change them. |

## Review trigger

The owner's decision on this revision; feature 007 cannot complete native acceptance without one.
