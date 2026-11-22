# Design Model: MCP Message Tools

## Stored and projected types

| Type | Fields and relations | Rules |
| --- | --- | --- |
| Message key | `chat_jid`, `id`; ordered with stored `time` | Composite identity; at most 256 UTF-8 bytes per identifier. Descending `(time, chat_jid, id)` on base pages; ascending `(time, id)` within a chat. |
| Contact identity | Phone digits and corresponding phone JID, LID JID (if known) | Resolve through the local map in the current read snapshot. An unmapped LID is not a phone. The account's own identity is obtained from local state only. |
| Message | `id`, `chat_jid`, `chat_name`, `sender`, `sender_name`, `timestamp`, `is_from_me`, `content`, `caption`, `media_type` | `sender` is the full stored address; `timestamp` is RFC 3339 in UTC. `is_from_me` is Boolean. Other fields are strings; missing names, text, caption and media type use empty strings. Nonempty `media_type` is `image`, `video`, `audio` or `document`. Text and caption stay separate and have a 256 KiB limit each; names have a 1 KiB limit. Malformed stored public fields fail without exposing raw data. |
| Context | `before`, `after`, `before_reduced`, `after_reduced` | Neighbours are plain messages, not recursive hits. Both arrays oldest first. Keep only closest neighbours; a reduced flag means an available requested neighbour was omitted, not that the chat ended or the caller requested zero. |
| Context hit | Message plus context and optional `preview_fields` | Only the first verified anchor of a context-enabled listing can be previewed. `preview_fields` lists the shortened `content` and/or `caption`, in that order, without changing identity or stored text. |
| Verified page | Ordered base keys, up to requested `limit`; internal only | Full page found or end proven. An unverified page is never rendered. Context, names and projections are resolved in the same snapshot. |
| Listing continuation | `page`, `limit`; per-reply suggestion only | Absolute offset of first omitted base match expressed as `page` with `limit: 1`. No cursor, persisted state or snapshot across calls. |

## Lifecycle

`validated -> admitted -> snapshot -> verified -> budgeted -> snapshot returned -> encoded -> output acknowledged`. Invalid input stops before admission. Not found, ambiguity, work limit, cancellation and storage faults skip the success path. Cancellation is not resource return: keep the store lease until statements, snapshot and connection have returned; keep the independent tool credit until the existing transport lifecycle retires it. Reply-budget reductions happen only after the base result is verified.

No schema changes, writes, network lookups, media metadata projection, or account transitions. See the [tool contract](contracts/tools.md) for argument and result shapes.
