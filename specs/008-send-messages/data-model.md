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

Written once after the acknowledgement, through ingest's serialized live-write path (`lockForLive`, LID `relookup`, then the existing single-message upsert on `(chat_jid, id)`), so a chat merged away during the send is not recreated. The chat's last activity moves forward as for any message. A later history sync of the same message updates the same row.

## Configuration (new settings)

| Setting | Type | Default | Validation at startup |
| --- | --- | --- | --- |
| `send_allowed_dirs` | list of directories | `<data dir>/outbox` (created private if missing, only when sending is enabled) | Syntax always. When `send_allowed_recipients` is not empty: absolute, existing directories, resolved with links, compared with the data directory by identity (`os.SameFile` on the entry, its ancestors and the data directory's ancestors). Each must not be the data directory, an ancestor of it, or inside it, except the default outbox and its subdirectories. Each is opened as an `os.Root` and kept. |
| `send_allowed_recipients` | list of addresses | empty (sending off) | Each entry is a phone number, phone JID, LID or group JID, normalized. Status, broadcast, newsletter, server and malformed addresses are refused. `*` is allowed only as the sole entry. |
| `ffmpeg` | path | unset (PATH lookup at call time) | Absolute path to an existing executable regular file. |

## In-memory state

- **Send slot**: Process-wide; at most one send in progress. Released when the call returns, including the local record, and not before an abandoned source-stage goroutine (R3) has actually returned.
- **Rate window**: The timestamps of sends handed to WhatsApp in the last 24 hours, at most 200. A send is allowed when fewer than 10 fall in the last minute and fewer than 200 in the last 24 hours. Lost on restart.
- **Lookup window**: The timestamps of registration lookups sent in the last 24 hours, at most 100. A lookup is allowed when fewer than 10 fall in the last minute and fewer than 100 in the last 24 hours. Lost on restart.
- **Lookup cache**: Per normalized phone number, the last lookup result (registered with its LID, or unregistered) and its time; used for 24 hours; at most 1,000 entries, oldest evicted first. Failed lookups are not cached. Lost on restart.
- **Allowed roots**: One kept `os.Root` handle per allowed directory, opened at startup when sending is enabled and closed at shutdown.
- **Owner send admissions**: The number of admitted send calls on the current generation. Retirement waits for it to reach zero.
- **Per-send working directory**: A private directory under the data directory, holding the plaintext working copy (or conversion input), the converter's `output.ogg`, and the upload's encrypted temporary file. It is created only after the free-space check, which requires at least "512 MiB plus two times the file size for a file send, or plus the file size and 200 MiB for a conversion". It exists only during one send and is removed on every exit path. A leftover from a crash or an abandoned stuck read is removed at the next start.

## Send call lifecycle

```text
received → arguments valid → recipient normalized → policy allowed → rate allowed + slot → connected
  → [phone without stored chat: cached result, or lookup within its window → registered]
  → file validated → disk space ok → working copy → [converted] → [uploaded]
  → handed to WhatsApp ─┬→ acknowledged → recorded (or record missing) → success
                        ├→ WhatsApp refusal / known pre-hand-over error → send failed (counted)
                        └→ cancelled / deadline / disconnected / unrecognised error → outcome unknown (counted)
Any refusal before "handed to WhatsApp" → failure with no side effect; not counted in the rate window.
```
