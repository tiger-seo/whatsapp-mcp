// Command seed writes N synthetic messages through the store, for the benchmark and the manual checks in the
// quickstart. The data directory must already exist and be private. It refuses any directory outside the system
// temporary directory unless -force is given. It never touches a WhatsApp account.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

func main() {
	dir := flag.String("dir", "", "data directory")
	n := flag.Int("n", 1000, "number of messages")
	force := flag.Bool("force", false, "allow a directory outside the system temporary directory")
	flag.Parse()
	if *dir == "" || *n < 1 {
		fmt.Fprintln(os.Stderr, "usage: seed -dir <data dir> [-n N] [-force]")
		os.Exit(2)
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !*force && !strings.HasPrefix(abs, filepath.Clean(os.TempDir())) {
		fmt.Fprintf(os.Stderr, "seed: %s is outside %s; use -force if this is the directory you mean\n", abs, os.TempDir())
		os.Exit(1)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, abs, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	defer st.Close(ctx)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for written := 0; written < *n; {
		size := min(1000, *n-written)
		batch := make([]store.Message, size)
		for i := range batch {
			k := written + i
			chat := fmt.Sprintf("49151100%05d@s.whatsapp.net", k%1000)
			batch[i] = store.Message{
				ChatJID: chat, ID: fmt.Sprintf("seed-%d", k), SenderJID: chat,
				Time: base.Add(time.Duration(k) * time.Second), Text: fmt.Sprintf("synthetic message %d", k),
			}
		}
		if err := st.StoreMessages(ctx, batch); err != nil {
			fmt.Fprintln(os.Stderr, "seed:", err)
			os.Exit(1)
		}
		written += size
	}
	fmt.Printf("wrote %d messages\n", *n)
}
