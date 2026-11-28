# whatsapp-mcp

A WhatsApp MCP server in a single Go binary. It connects to WhatsApp through
[whatsmeow](https://github.com/tulir/whatsmeow) and exposes it to LLM clients over MCP. It needs no Docker, no
PostgreSQL and no C compiler.

**Status:** in development. `whatsapp-mcp pair` links the binary to your WhatsApp account by QR code and
`whatsapp-mcp status` reports the local state offline. `whatsapp-mcp stdio` serves read-only MCP tools for
chats, contacts and stored messages. Use `list_messages` to search, `get_message_context` to read around a message, and
`get_last_interaction` to find the latest stored exchange with a contact. A verified context listing can include a
continuation when it exceeds the reply budget; an unfinished search, including one that runs out of time, never
returns partial results. See the
[verification record](specs/007-mcp-message-tools/verification.md) for completed checks and pending review.
`serve` does not expose MCP yet.

`stdio` can also send: `send_message` (text), `send_file` (an image, video or document from an allowed directory) and
`send_audio_message` (a voice note; formats other than Ogg Opus need ffmpeg). **Sending is off until you list the
recipients** in `config.toml` in the data directory:

```toml
send_allowed_recipients = ["+49 151 00000000"]
# send_allowed_dirs = ['C:\Users\me\whatsapp-outbox']   # default: the outbox directory in the data directory
# ffmpeg = 'C:\tools\ffmpeg\bin\ffmpeg.exe'              # default: ffmpeg on PATH
```

Sending has been tested offline only, not yet on a real account.

Launch `stdio` only from a trusted local client. It can read all stored chats, contacts and messages without per-read
confirmation, and the client or its AI provider may retain results. It can send, without asking you, to every listed
recipient. The tools cannot pair or recover.

## Connect to Claude Code

Use a local `whatsapp-mcp` executable. If you are building from source, run `go build ./cmd/whatsapp-mcp`
from the repository root with `CGO_ENABLED=0`.

Replace the example path with the absolute path to your executable. Both commands use the default data directory.
If it does not already have a linked session, run `pair` and scan the QR code from WhatsApp's **Linked devices**
screen. Run `claude mcp add` from the project directory where you use Claude Code.

**Windows (PowerShell)**

```powershell
C:\tools\whatsapp-mcp.exe pair
claude mcp add --transport stdio --scope local whatsapp -- C:\tools\whatsapp-mcp.exe stdio
```

The example uses a path without spaces. In `cmd.exe`, do not wrap the path in single quotes: they become part of the executable name.

**macOS and Linux**

```sh
"$HOME/bin/whatsapp-mcp" pair
claude mcp add --transport stdio --scope local whatsapp -- "$HOME/bin/whatsapp-mcp" stdio
```

Start Claude Code in that project and run `/mcp` to check that `whatsapp` is connected. To verify that the tools
work, ask Claude to call `list_chats` with a small limit. `--scope local` registers the server only for you in
the current project. Claude Code starts `stdio` itself, so do not run it separately or reuse the same data
directory from another running client: only one process can hold its lock.

Running it, including where it keeps your data and how to back it up: [OPERATIONS.md](OPERATIONS.md).
