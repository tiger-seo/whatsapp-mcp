// Command bumpversion sets a schema version in a database file, so a start against a newer schema can be tried by
// hand (quickstart step 4):
//
//	bumpversion [-force] <whatsapp.db> messages <N>   sets mcp_version
//	bumpversion [-force] <whatsapp.db> device <N>     sets whatsmeow_version.compat
//
// It refuses any file outside the system temporary directory unless -force is given.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	force := flag.Bool("force", false, "allow a file outside the system temporary directory")
	flag.Parse()
	args := flag.Args()
	if len(args) != 3 || (args[1] != "messages" && args[1] != "device") {
		fmt.Fprintln(os.Stderr, "usage: bumpversion [-force] <whatsapp.db> messages|device <N>")
		os.Exit(2)
	}
	n, err := strconv.Atoi(args[2])
	if err != nil || n < 1 {
		fmt.Fprintln(os.Stderr, "bumpversion: N must be a positive number")
		os.Exit(2)
	}
	abs, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !*force && !strings.HasPrefix(abs, filepath.Clean(os.TempDir())) {
		fmt.Fprintf(os.Stderr, "bumpversion: %s is outside %s; use -force if this is the file you mean\n", abs, os.TempDir())
		os.Exit(1)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	query := "UPDATE mcp_version SET version = ?"
	if args[1] == "device" {
		query = "UPDATE whatsmeow_version SET compat = ?"
	}
	if _, err := db.Exec(query, n); err != nil {
		fmt.Fprintln(os.Stderr, "bumpversion:", err)
		os.Exit(1)
	}
	fmt.Println("done")
}
