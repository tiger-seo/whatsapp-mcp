# Data Model: Sending Text, Files and Voice Notes

No schema migration. Sent messages use the existing `messages` and `chats` tables.

## Persistent: sent message (existing `messages` row)

| Field | Value for a sent message |
| --- | --- |
| `chat_jid` | The normalized recipient: phone JID, group JID, or the LID as given when no mapping is known |
| `id` | The message ID generated before the send |
| `sender_jid` | The account's own phone JID, without a device part |
| `time` | The server timestamp from the acknowledgement |
| `from_me` | true |
| `text` | `message` (text sends), otherwise empty |
| `caption` | `caption` (`send_file`), otherwise empty |
| `media_type` | `image`, `video`, `audio` or `document`; empty for text |
| media metadata | From the upload: URL, direct path, media key, both SHA-256 hashes, length, MIME type, and for documents the file name |

Written once after the acknowledgement, through the existing single-message upsert on `(chat_jid, id)`. The chat's last activity moves forward as for any message. A later history sync of the same message updates the same row.

## Configuration (new settings)

| Setting | Type | Default | Validation at startup |
| --- | --- | --- | --- |
| `send_allowed_dirs` | list of directories | `<data dir>/outbox` (created private if missing) | Absolute, existing directories, resolved with links. Each must not be the data directory, an ancestor of it, or inside it, except the default outbox and its subdirectories. |
| `send_allowed_recipients` | list of addresses | empty (sending off) | Each entry is a phone number, phone JID, LID or group JID, normalized. Status, broadcast, newsletter, server and malformed addresses are refused. `*` is allowed only as the sole entry. |
| `ffmpeg` | path | unset (PATH lookup at call time) | Absolute path to an existing executable regular file. |

## In-memory state

- **Send slot**: Process-wide; at most one send in progress. Released when the call returns, including the local record.
- **Rate window**: The timestamps of sends handed to WhatsApp in the last 24 hours, at most 200. A send is allowed when fewer than 10 fall in the last minute and fewer than 200 in the last 24 hours. Lost on restart.
- **Owner send admissions**: The number of admitted send calls on the current generation. Retirement waits for it to reach zero.
- **Per-send working directory**: A private directory under the data directory, for the upload's encrypted temporary file and conversion input and output. It exists only during one send and is removed on every exit path. A leftover from a crash is removed at the next start.

## Send call lifecycle

```text
received → arguments valid → recipient normalized → policy allowed → rate allowed → connected
  → [phone without stored chat: registered] → file validated → [converted] → [uploaded]
  → handed to WhatsApp ─┬→ acknowledged → recorded (or record missing) → success
                        └→ cancelled / deadline / disconnected → outcome unknown
Any refusal before "handed to WhatsApp" → failure with no side effect; not counted in the rate window.
```
