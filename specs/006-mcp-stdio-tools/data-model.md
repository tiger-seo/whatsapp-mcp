# Data Model: MCP Chat and Contact Reads

**Date**: 2026-11-17. Public DTOs and transient protocol state only. No migration, persisted model or retention change.

## Public Results

All success roots are objects with `source: "local_store"` and `untrusted_data: true`. These constants label provenance, not trustworthiness or instruction authority. Unknown output properties are not emitted. Optional fields have the exact absence rules below.

### Chat

| Field | Type | Meaning |
| --- | --- | --- |
| `jid` | string | Full canonical stored chat identity, phone/group/LID. |
| `name` | string | Stored display name; empty is allowed when unknown. |
| `is_group` | boolean | Whether the identity is a group. |
| `last_message_time` | string or null | Last activity as UTC RFC 3339; null if unknown. |
| `latest_message` | Message or null, optional | Omitted when latest detail was not requested; null when requested but no message exists. |

The optional latest detail does not control last-activity metadata. Direct/contact-chat tools always request latest detail. The singular direct tool prefers the exact phone chat over its mapped LID chat; the plural contact tool may return both distinct actual chat JIDs before merging. Empty names are not instructions or inferred phone numbers.

### Message

Only the latest message of a chat is exposed here; message list/context tools are not part of 006.

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Message ID, meaningful together with `chat_jid`. |
| `chat_jid` | string | Owning stored conversation. |
| `chat_name` | string | Display name associated with that chat. |
| `sender` | string | Full normalized sender JID, not an assumed phone. |
| `sender_name` | string | Locally resolved sender display name/fallback. |
| `timestamp` | string | UTC RFC 3339, with fractional precision retained when present. |
| `is_from_me` | boolean | Own-message indicator. |
| `content` | string | Message text; may be empty. |
| `caption` | string | Separate image/video/document caption; may be empty. |
| `media_type` | string | Empty, `image`, `video`, `audio` or `document`. |

`(chat_jid, id)` is the identity. Latest selection orders timestamp descending, then message ID descending; it must be a real message from this chat. Never expose `MediaMeta`, download URLs, keys, hashes, device state or raw internal structs. DTO strings preserve authoritative values; safe text escaping does not change them.

### Contact

| Field | Type | Meaning |
| --- | --- | --- |
| `jid` | string | Canonical person key: mapped phone JID, otherwise unresolved LID JID. |
| `phone_number` | string or null | Digits of known normalized phone; null if unknown. |
| `name` | string | Chosen local name/fallback. |

Group and unsupported-domain rows are excluded. Group aliases before matching/paging. Within one entry use full name, first name, push name, business name, then identity fallback; prefer the phone entry's chosen name over a mapped LID entry's name. Every name field on either alias can match even if it is not the chosen display name. Local mappings do not authorize any action.

### Result Envelopes

| Result | Fields in addition to provenance |
| --- | --- |
| Chat page | `chats: Chat[]`, `count: integer`, `limit: integer`, `page: integer` |
| Contact page | `contacts: Contact[]`, `count: integer`, `limit: integer`, `page: integer` |
| Singular chat | `chat: Chat` |
| Tool error | `error: {code: string, message: string, field?: string}` |

`count` is returned item count, never total matches. Page roots always use arrays, including `[]`; count must equal length. Error roots use `isError: true` in the enclosing SDK result; they contain no partial success object. Input/output contracts are in [contracts/tools.md](contracts/tools.md).

## Input Value Rules

- All arguments are JSON objects. Omission applies defaults; explicit null is invalid for every field. Unknown properties, duplicate argument keys, type coercion and fractional/exponential values that are not safely representable integers are rejected. No bool/string-to-number conversion.
- `query`: at most 1,024 UTF-8 bytes. Required contact queries must contain a non-whitespace character, but matching preserves the supplied nonempty string rather than silently trimming it. Optional chat queries default to empty. Use Unicode NFC/case folding and literal substring matching, not wildcard or regular-expression semantics. Phone search is over stored digits; formatting normalization belongs to contact identity inputs, not arbitrary search text.
- Contact identity inputs accept 1-20 phone digits with the existing supported leading plus/space/dash/dot/parenthesis forms, phone JIDs or LID JIDs; maximum 256 UTF-8 bytes. Normalize using local mapping only; strip a supported contact device suffix. Unsupported domains or malformed values fail. Group JIDs are not contact inputs.
- `chat_jid`: at most 256 UTF-8 bytes; exact phone, group or LID chat JID, validated by the store. Reject device-specific chat addresses rather than broadening an exact lookup.
- `limit`: integer 1-200. `page`: integer 0-100,000; compute `page * limit` with checked arithmetic and reject offsets over 100,000 before querying. `sort_by`: `last_active` or `name` only. Explicit false for latest inclusion remains false.
- Store field ceilings remain binding: message IDs/JIDs 256 bytes, names 1 KiB, content and caption 256 KiB each, plus the store's aggregate 32 MiB result budget. Check device-origin names before unbounded Go scanning/folding. An invalid/oversized legacy field is a clear bounded failure, not silently truncated data.

## Transient State and Ownership

### Session

`new -> initializing -> active -> stopping -> stopped`. Initialization occurs once. No MCP reattachment or pump replacement in the process. Unsafe storage/run-marker startup never reaches initializing. A disconnect without account logout does not erase stored data; already-running permitted local reads do not depend on connectivity.

### Request

`frame_checked -> credit_admitted -> SDK_dispatch -> handler -> response_pending -> written`. Response completion releases its request credit. A tool credit spans dispatch through response completion, so the next tool cannot construct a second large result behind a slow writer. Overflow work gets a bounded busy error without SDK dispatch/store admission. Cancellation or transport failure moves admitted work to cancelled/stopping, but accounting is released only after actual handler completion and disposal of retained output.

### Store Snapshot

`not_admitted -> active -> cancellation_requested? -> committed_read/rolled_back -> connection_returned`. There is no tool-store admission wait state. Request cancellation at 5 s or earlier under the isolation policy in contracts/store-and-lifecycle.md (waiting ordinary work, reader contention, WAL guard, caller). Admission additionally requires no ordinary waiter, a free slot and reader, -wal ≤ 64 MiB and ≥ 640 MiB free disk. Retain accounting until real transaction/connection return, admit no replacement snapshot while work remains, and latch unsuccessful shutdown if a cancelled read has not returned within the 5 s admission wait.

### Pump Lifetime

One reader and one writer for protocol, plus one diagnostic pump. One current input frame/output frame; ordinary diagnostics use 16 x 4 KiB queue/drop accounting. Mandatory diagnostics use a separate 16 x 1 KiB queue, 5 s complete-write acknowledgment deadline and a fixed enum table of at most 128 count/time cells. No raw query/name/body/client key enters retained diagnostic state. Mandatory overflow/failed or overdue acknowledgment latches diagnostic_delivery_failed and unsuccessful normal shutdown, never silent successful continuation. EOF or ordinary queue drainage cannot clear that latch; it does not itself change the owner's marker proof.

Pumps own bytes/completion channels only, no owner/store/callback references. Lifecycle state tracks mandatory submission/acknowledgment and one shared earliest-deadline timer; it does not require a fourth OS pump. A pump blocked in an OS call may survive connection stop, never restarts, and ends at process exit after actual request/account/store cleanup. Cooperative test streams can join; production inherited-stream pump joins are not promised. Exact reporting/failure rules are in [contracts/stdio.md](contracts/stdio.md).
