# whatsapp-mcp

A WhatsApp MCP server in a single Go binary. It connects to WhatsApp through
[whatsmeow](https://github.com/tulir/whatsmeow) and exposes it to LLM clients over MCP. It needs no Docker, no
PostgreSQL and no C compiler.

**Status:** in development. `whatsapp-mcp pair` links the binary to your WhatsApp account by QR code and
`whatsapp-mcp status` reports the local state offline. There are no MCP tools yet: `stdio` and `serve` start but
expose nothing.

Running it, including where it keeps your data and how to back it up: [OPERATIONS.md](OPERATIONS.md).
