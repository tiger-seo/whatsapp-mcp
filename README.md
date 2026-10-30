# whatsapp-mcp

A WhatsApp MCP server in a single Go binary. It connects to WhatsApp through
[whatsmeow](https://github.com/tulir/whatsmeow) and exposes it to LLM clients over MCP. It needs no Docker, no
PostgreSQL and no C compiler.

**Status:** in development. The binary skeleton exists; only `whatsapp-mcp version` works so far.

Running it, including where it keeps your data and how to back it up: [OPERATIONS.md](OPERATIONS.md).
