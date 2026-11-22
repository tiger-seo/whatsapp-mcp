# whatsapp-mcp

A WhatsApp MCP server in a single Go binary. It connects to WhatsApp through
[whatsmeow](https://github.com/tulir/whatsmeow) and exposes it to LLM clients over MCP. It needs no Docker, no
PostgreSQL and no C compiler.

**Status:** in development. `whatsapp-mcp pair` links the binary to your WhatsApp account by QR code and
`whatsapp-mcp status` reports the local state offline. `whatsapp-mcp stdio` serves five read-only MCP tools:
`list_chats`, `get_chat`, `search_contacts`, `get_direct_chat_by_contact` and `get_contact_chats`.
`serve` does not expose MCP yet.

Launch `stdio` only from a trusted local client. It can read all stored chats and contacts without per-read
confirmation, and the client or its AI provider may retain results. The tools cannot pair, recover or send.

Running it, including where it keeps your data and how to back it up: [OPERATIONS.md](OPERATIONS.md).
