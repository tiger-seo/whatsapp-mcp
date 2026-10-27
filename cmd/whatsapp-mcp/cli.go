package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tiger-seo/whatsapp-mcp/internal/config"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// streams are the process's standard streams and environment, passed in so tests can call run in process.
type streams struct {
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	environ []string
}

type command struct {
	name string
	help string
	// flags is false for commands that take no configuration.
	flags bool
	run   func(cmd *command, args []string, s *streams) int
}

func commands() []*command {
	return []*command{
		{name: "stdio", help: "MCP over stdio, for Claude Desktop and Cursor", flags: true, run: runStdio},
		{name: "serve", help: "Daemon: MCP over HTTP at /mcp", flags: true, run: runServe},
		{name: "pair", help: "Pair this device with a terminal QR code", flags: true, run: runPair},
		{name: "status", help: "Print connection, login and version state", flags: true, run: runStatus},
		{name: "version", help: "Print build information", run: runVersion},
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, environ []string) int {
	s := &streams{stdin: stdin, stdout: stdout, stderr: stderr, environ: environ}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "whatsapp-mcp: missing command")
		writeUsage(stderr)
		return exitUsage
	}
	name := args[0]
	switch {
	case name == "-h" || name == "-help" || name == "--help":
		writeUsage(stdout)
		return exitOK
	case strings.HasPrefix(name, "-"):
		fmt.Fprintln(stderr, `whatsapp-mcp: flags go after the command, e.g. "whatsapp-mcp stdio --log-level debug"`)
		return exitUsage
	}
	for _, cmd := range commands() {
		if cmd.name == name {
			return cmd.run(cmd, args[1:], s)
		}
	}
	fmt.Fprintf(stderr, "whatsapp-mcp: unknown command %q\n", name)
	writeUsage(stderr)
	return exitUsage
}

func writeUsage(w io.Writer) {
	fmt.Fprint(w, "whatsapp-mcp: WhatsApp MCP server\n\nUsage:\n  whatsapp-mcp <command> [flags]\n\nCommands:\n")
	for _, cmd := range commands() {
		fmt.Fprintf(w, "  %-9s %s\n", cmd.name, cmd.help)
	}
	fmt.Fprint(w, "\nRun \"whatsapp-mcp <command> --help\" for the command's flags.\n")
}

// parseFlags parses a command's flags. It returns the flags that were set, keyed by flag name. When ok is
// false the command must return code at once: help was shown or the arguments were wrong.
func parseFlags(cmd *command, args []string, s *streams) (flags map[string]string, code int, ok bool) {
	fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.flags {
		for _, st := range config.Settings() {
			fs.String(st.Flag, "", st.Usage)
		}
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeCommandHelp(s.stdout, cmd)
			return nil, exitOK, false
		}
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s: %v\n", cmd.name, err)
		writeCommandHelp(s.stderr, cmd)
		return nil, exitUsage, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(s.stderr, "whatsapp-mcp: %s: unexpected argument %q\n", cmd.name, fs.Arg(0))
		return nil, exitUsage, false
	}
	flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) { flags[f.Name] = f.Value.String() })
	return flags, exitOK, true
}

func writeCommandHelp(w io.Writer, cmd *command) {
	if !cmd.flags {
		fmt.Fprintf(w, "Usage:\n  whatsapp-mcp %s\n\n%s\n", cmd.name, cmd.help)
		return
	}
	fmt.Fprintf(w, "Usage:\n  whatsapp-mcp %s [flags]\n\n%s\n\nFlags:\n", cmd.name, cmd.help)
	settings := config.Settings()
	width := 0
	for _, st := range settings {
		width = max(width, len(st.Flag+" string"))
	}
	in := config.Inputs{UserConfigDir: os.UserConfigDir}
	for _, st := range settings {
		def, err := st.Default(in)
		if err != nil {
			def = "<user config dir>/whatsapp-mcp"
		}
		fmt.Fprintf(w, "  --%-*s   %s (env %s, default %s)\n", width, st.Flag+" string", st.Usage, st.Env, def)
	}
}
