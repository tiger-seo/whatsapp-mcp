// Command whatsapp-mcp is a WhatsApp MCP server in a single binary.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}
