# Contract: Five MCP Chat and Contact Tools

**Scope**: Feature 006, official go-sdk v1.8.0, stdio only. Names and purposes are stable; exact corrected parameter/output contracts below apply. JSON Schema uses object roots, explicit property types, defaults, enum/minimum/maximum constraints, required arrays and `additionalProperties: false`. Do not advertise a property default only in a description.

## Discovery and Validation

Exactly these tools are registered. Describe returned names/text as untrusted locally stored data. Where SDK annotations are supported, mark tools read-only, non-destructive and closed-world; annotations are descriptive, not authorization enforcement. No prompts/resources, subscription handlers, sampling or elicitation callbacks are exposed. Explicitly disable the SDK's tools listChanged/subscription defaults and reject `subscriptions/listen`, rather than assuming absence of application callbacks disables them. Discovery and adversarial calls must prove no retained subscriptions. Server identity is `whatsapp-mcp` with the actual build-info version.

| Tool | Required inputs | Optional inputs and schema defaults | Success object |
| --- | --- | --- | --- |
| `list_chats` | none | `query: string = ""`; `limit: integer = 30`; `page: integer = 0`; `include_last_message: boolean = true`; `sort_by: string = "last_active"` | Chat page |
| `get_chat` | `chat_jid: string` | `include_last_message: boolean = true` | Singular chat |
| `search_contacts` | `query: string` | `limit: integer = 50`; `page: integer = 0` | Contact page |
| `get_direct_chat_by_contact` | `sender_phone_number: string` | none | Singular chat |
| `get_contact_chats` | `jid: string` | `limit: integer = 20`; `page: integer = 0` | Chat page |

All list limits are 1-200. Page schema range is 0-100,000; the handler additionally validates the computed offset. Queries/identities follow [data-model.md](../data-model.md), including byte ceilings the schema's character-oriented `maxLength` cannot establish by itself. Put matching maxLength constraints in schemas where appropriate, then validate UTF-8 bytes in handlers. Required strings are nonempty/nonblank as specified. Null is not omission. Unknown/duplicate argument keys fail, no number coercion. Handler defaults/validation run independently of schema defaults; low-level SDK registration is not an automatic validator.

## Query Semantics

### list_chats

Match query against folded stored name or folded JID using literal substrings. Empty query means no filter. `last_active` sorts last activity descending, missing activity last, then JID ascending. `name` sorts named/name-quality-nonzero chats before unnamed chats, folded name ascending, then JID ascending. `include_last_message: false` must avoid reading latest-message fields, not merely discard them after a costly query.

### get_chat

Validate a full chat JID, normalize its supported canonical representation, then select that exact stored chat. Do not apply substring contact matching or infer another chat through phone/LID aliasing here. Missing chat is a tool error. False latest inclusion must not fail because an unrequested message body is too large.

### search_contacts

Match full, first, push and business names across local aliases and stored phone digits with folded literal substrings. A name containing `%`, `_`, `+`, `&` or quotes is searched as data. A phone search containing formatting characters is literal, not a hidden call to phone normalization; callers may use digit substrings. Normalize/group person identities before filtering and paging; match any alias name, return one canonical person. Exclude groups and unsupported person domains. Sort folded chosen name ascending then canonical person JID ascending.

### get_direct_chat_by_contact

The historical input name remains `sender_phone_number`, but its schema description explicitly allows formatted phone, phone JID or LID. Resolve locally; match phone exactly, then exact mapped/unresolved LID if no phone chat exists. No substring, partial-number, group or network match. Latest detail is included. A known contact with no stored direct chat still yields `not_found`.

### get_contact_chats

Resolve `jid` as a contact, not a group. Include each actual direct phone/LID chat and every stored chat containing a message from either known alias, using existence membership rather than one result per message. Preserve distinct stored chat JIDs before a merge; repeated participation never duplicates a chat. Sort activity descending, null last, JID ascending. Include the actual latest message even when another participant sent it. No matches is a successful empty page.

## Structured and Text Output

Object shapes and exact fields are defined once in [data-model.md](../data-model.md). Publish explicit output schemas for each tool, including the success root and the common structured error root as alternatives, both object-shaped. Validate generated structured success/error roots against those schemas in tests; all runtime DTO construction passes explicit field/size checks.

Every success/error tool result has structured content plus one safe JSON text rendering of the same object. The text is JSON, not a JSON-encoded string containing another JSON-encoded result. Parse text once to obtain the structured value. ASCII escaping makes control/directional characters inert in text while preserving their decoded value. Do not use a compact `format` input. No image, QR, media URL or secret is returned.

Example successful `get_chat` with latest detail disabled:

```json
{
  "source": "local_store",
  "untrusted_data": true,
  "chat": {
    "jid": "15550000001@s.whatsapp.net",
    "name": "Example Contact",
    "is_group": false,
    "last_message_time": "2026-11-17T00:00:00Z"
  }
}
```

When latest is requested but absent, include `latest_message: null`. When activity is unknown include `last_message_time: null`. Never replace a missing value with an empty timestamp or fake message. `phone_number` is null for unmapped LID contacts. Count is returned length, not a database-wide total.

## Error Contract

| Code | Meaning / required behavior |
| --- | --- |
| `invalid_input` | Name the invalid field and a fixed actionable reason; do not echo rejected content. |
| `not_found` | Requested singular chat/direct chat does not exist; no internal-error substitution. |
| `busy` | Tool credit or immediate store admission unavailable; retry later. No read queue or automatic retry. |
| `resource_limit` | Fields, store result, preflight encoding or response budget exceeded; suggest a smaller page or disabling latest detail where applicable. No partial success. |
| `timeout` | Cancellation deadline reached; request cancellation and retain actual work/lease accounting until return under T001's validated completion policy. The error is not proof the snapshot drained. |
| `cancelled` | Client/connection cancellation; response is best-effort if transport is still writable. |
| `unavailable` | Store is closed or cannot serve the read; no empty-success substitute. |
| `storage_error` | Sanitized genuine storage failure; no SQL/driver detail/keys/local paths. |

Tool errors set `CallToolResult.IsError = true`, with the common structured `error` object and matching text. Protocol-level malformed envelopes, unknown methods/tools and transport framing failures use the pinned SDK/MCP protocol errors or connection failure as applicable; they are not misreported as a tool's successful result. Sanitize SDK-generated error text/data, including errors before middleware/handler entry, so malformed params are never echoed. Calls to the two deferred tools are unknown-tool failures with zero account activity.

At the pre-dispatch boundary, a valid excess `tools/call` gets the same bounded `busy` tool-error result directly, without entering the SDK or store. Other excess requests use a bounded JSON-RPC overload error with implementation code `-32000` and fixed message `server busy`. Never accumulate overload replies; if the response cannot complete within its bound, fail the session and drain normally.

## Acceptance Matrix

- Every schema default equals the independent handler default; explicit false and `page: 0` are preserved; `limit: 0`, null, unknown/duplicate/type-invalid fields fail.
- All five tools have success, empty/not-found, validation and mapped storage-error tests through `NewInMemoryTransports` with a seeded store. The fake owner makes zero contact/network/pair/send calls.
- Stable paging over 235 chats; limits 1/200 valid and 0/201 invalid; checked offset boundary 100,000 and overflow.
- Case folding/NFC, literal wildcard-like characters, all contact name fields, exact/overlapping numbers, mapped/unmapped aliases, distinct pre-merge rows and repeated participation.
- Real latest-message ties, caption separation, no latest query when false, null/omission rules, no internal media/credential fields.
- Decode text once and compare with structured data, including controls and non-ASCII. Validate every output root/schema and actual byte budgets.
