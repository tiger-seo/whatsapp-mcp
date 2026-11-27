# Contract: Send Tools

Three tools are added to the existing local MCP session. Argument handling follows the 006/007 conventions:

- snake_case names; the schema lists only real defaults;
- null, duplicate and unknown fields are refused;
- every failure is a tool error (`isError: true`) with structured content `{ "error": { "code", "message", "field"? } }` and a text rendering of it;
- errors never echo message text, captions, file contents or full paths.

Annotations: `readOnlyHint: false`, `destructiveHint: false`, `idempotentHint: false`, `openWorldHint: true`. Each description says that the call sends a WhatsApp message in the owner's name, and that sending is off until `send_allowed_recipients` is configured.

## Arguments

| Tool | Argument | Required | Rules |
| --- | --- | --- | --- |
| all three | `recipient` | yes | 1–256 UTF-8 bytes: a phone number (digits, optional leading `+`, spaces and dashes ignored), a phone JID, a LID or a group JID. |
| `send_message` | `message` | yes | 1–65,536 bytes of valid UTF-8. |
| `send_file` | `media_path` | yes | An absolute path, 1–4,096 bytes (see "Paths"). |
| `send_file` | `caption` | no (default `""`) | 0–65,536 bytes of valid UTF-8. A non-empty caption on a voice note (`.ogg`) is refused. |
| `send_audio_message` | `media_path` | yes | As for `send_file`. The extension must be one of `ogg`, `opus`, `mp3`, `wav`, `m4a`, `aac`, `flac`, `amr`. |

## Message type (`send_file`, case-insensitive extension)

| Extension | Sent as | MIME |
| --- | --- | --- |
| jpg, jpeg | image | image/jpeg |
| png | image | image/png |
| gif | image | image/gif |
| webp | image | image/webp |
| ogg | voice note (Ogg Opus required) | audio/ogg; codecs=opus |
| mp4 | video | video/mp4 |
| avi | video | video/avi |
| mov | video | video/quicktime |
| anything else | document, titled with the base name | application/octet-stream |

`send_audio_message` sends an Ogg Opus file directly; any other allowed extension, including non-Opus `.ogg`, is converted first.

## Paths

- The path is resolved with its links followed and must land inside one of the allowed directories. The directories are resolved, checked against the data directory by identity (never by path text) and opened once at startup when sending is enabled, and those handles are kept. The resolved file is then opened confined to that directory, so a link or junction swapped in after resolution is refused rather than followed. A link from one allowed directory into another is accepted.
- On Windows, a path through a junction is refused, because it does not resolve. So are reserved device names (`CON`, `NUL.txt`).
- A file that grows after it is opened is refused (the copy is limited to the opened size plus one byte).
- These are refused: relative paths; UNC, device and `\\?\` paths; alternate data streams; anything that is not a regular file; files with more than one link; empty files; files over 100 MiB (also enforced on the bytes read).
- A refusal because of the allow-list names `send_allowed_dirs`.

## Success result

```json
{ "sent": true, "chat_jid": "<normalized chat>", "id": "<message id>", "timestamp": "<RFC 3339>",
  "media_type": "" | "image" | "video" | "audio" | "document", "voice_note": true | false,
  "duration_seconds": <voice notes only>, "local_record": "stored" | "missing" }
```

The text rendering states the same facts in one sentence.

## Error codes

| Code | When | Side effects before it |
| --- | --- | --- |
| `invalid_input` | Malformed or out-of-range argument, refused address kind, bad extension, caption on a voice note | none |
| `not_permitted` | Recipient not in `send_allowed_recipients` (message names the setting) | none |
| `rate_limited` | Sends: 10 in the last minute or 200 in the last 24 hours. Registration lookups: 10 in the last minute or 100 in the last 24 hours (cached results excluded). Includes `retry_after_seconds` and which limit applied. | none |
| `busy` | Another tool call or send is in progress, or an earlier send's abandoned file read has not ended yet | none |
| `not_connected` | The session is not connected | none |
| `not_on_whatsapp` | Phone number without stored chat is not registered | one lookup |
| `lookup_failed` | The registration lookup failed or timed out | one lookup |
| `file_refused` | Path, link, file kind, size or allow-list refusal (names `send_allowed_dirs` where it applies) | none |
| `insufficient_space` | Free space on the data directory's volume is below 512 MiB plus the send's worst-case working files (two times the file size plus 1 MiB for `send_file`; the file size plus 200 MiB for `send_audio_message`, always), or it cannot be measured | none |
| `invalid_audio` | `.ogg` that is not Ogg Opus, or malformed Ogg data | file read, private working copy |
| `ffmpeg_missing` | Conversion needed and no converter configured or on `PATH` (names `ffmpeg`) | file read |
| `conversion_failed` | Converter error, 60 s limit or output limit | file read, conversion |
| `upload_failed` | Upload refused or failed before the send | upload |
| `send_failed` | WhatsApp refused the message (error in its acknowledgement), or an error from the fixed list known to happen before the hand-over (not on WhatsApp, device or prekey fetch failed, encryption failed); counted in the rate window | upload if any |
| `sent_cancelled` | The client cancelled the request after WhatsApp acknowledged the message. The error carries `chat_jid`, `id` and `timestamp`; the message was sent and recorded locally if the record succeeded. Counted in the rate window; not retried | message sent |
| `outcome_unknown` | Cancelled, timed out, disconnected, or any unrecognised error once the send call had started; may have been delivered; counted in the rate window; not retried | send possibly delivered |
