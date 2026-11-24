# whatsapp-mcp

A WhatsApp MCP server in a single Go binary. It connects to WhatsApp through
[whatsmeow](https://github.com/tulir/whatsmeow) and exposes it to LLM clients over MCP. It needs no Docker, no
PostgreSQL and no C compiler.

**Status:** in development. `whatsapp-mcp pair` links the binary to your WhatsApp account by QR code and
`whatsapp-mcp status` reports the local state offline. `whatsapp-mcp stdio` serves read-only MCP tools for
chats, contacts and stored messages. Use `list_messages` to search, `get_message_context` to read around a message, and
`get_last_interaction` to find the latest stored exchange with a contact. A verified context listing can include a
continuation when it exceeds the reply budget; an unfinished search never returns partial results. See the
[verification record](specs/007-mcp-message-tools/verification.md) for completed checks and pending review.
`serve` does not expose MCP yet.

Launch `stdio` only from a trusted local client. It can read all stored chats, contacts and messages without per-read
confirmation, and the client or its AI provider may retain results. The tools cannot pair, recover or send.

Running it, including where it keeps your data and how to back it up: [OPERATIONS.md](OPERATIONS.md).
