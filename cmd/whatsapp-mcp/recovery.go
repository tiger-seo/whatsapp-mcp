package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// recoverOffline never constructs an owner or initiates account work.
func recoverOffline(ctx context.Context, st *store.Store, s *streams) bool {
	fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: unsafe run; offline recovery is required before connection or pairing")
	if !recoveryInteractive(s) {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: recovery requires interactive input and output handles; on Windows use a native console, not Git Bash/mintty")
		return false
	}
	present, err := st.RecoveryIdentityPresent(ctx)
	if err != nil {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: cannot inspect recovery identity; marker remains blocked")
		return false
	}
	input := newRecoveryInput(ctx, s.stdin)
	if input == nil {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: a previous recovery input read is still pending; restart after it finishes")
		return false
	}
	if !promptRecovery(ctx, input, s.stdout, present) || ctx.Err() != nil {
		return false
	}
	if err := st.RecoverRunGuard(ctx, present); err != nil {
		fmt.Fprintln(s.stderr, "whatsapp-mcp: pair: recovery could not be committed; reopen to inspect the marker. Restore disk space (512 MiB when latched) before retrying offline recovery")
		return false
	}
	_, _ = io.WriteString(s.stdout, "Offline recovery completed. A fresh pair requires a separate invocation and approval.\n")
	return true
}

type recoveryInput struct {
	request chan struct{}
	result  chan string
	done    chan struct{}
	cancel  context.CancelFunc
}

var recoveryReaderSlot = make(chan struct{}, 1)

// Only the reader goroutine touches the input. A blocked OS read may outlive
// cancellation, but it cannot print, start another read, or authorize recovery.
func newRecoveryInput(ctx context.Context, source io.Reader) *recoveryInput {
	select {
	case recoveryReaderSlot <- struct{}{}:
	default:
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	in := &recoveryInput{request: make(chan struct{}), result: make(chan string), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(in.done)
		defer func() { <-recoveryReaderSlot }()
		r := bufio.NewReaderSize(source, 1026)
		for {
			select {
			case <-ctx.Done():
				return
			case <-in.request:
			}
			if ctx.Err() != nil {
				return
			}
			line, err := r.ReadSlice('\n')
			if ctx.Err() != nil {
				return
			}
			value := ""
			if err == nil && len(line) <= 1025 {
				value = strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
			}
			select {
			case <-ctx.Done():
				return
			case in.result <- value:
			}
		}
	}()
	return in
}

// Prompting stays in the command goroutine, so no output can follow its return.
func promptRecovery(ctx context.Context, in *recoveryInput, out io.Writer, present bool) bool {
	defer func() {
		in.cancel()
		if ctx.Err() == nil {
			<-in.done
		}
	}()
	write := func(text string) bool {
		if ctx.Err() != nil {
			return false
		}
		_, err := io.WriteString(out, text)
		return err == nil && ctx.Err() == nil
	}
	read := func(prompt string) string {
		if !write(prompt) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case in.request <- struct{}{}:
		}
		select {
		case <-ctx.Done():
			return ""
		case line := <-in.result:
			if ctx.Err() == nil {
				return line
			}
			return ""
		}
	}
	ask := func(prompt, expected string) bool { return read(prompt) == expected }
	if !ask("Offline recovery only. Type recover to select recovery: ", "recover") {
		return false
	}
	if !write("Identify the exact account and linked device using your own evidence. A shared Linux/Chrome label, timing, or missing local identity cannot identify it. An orphaned phone link may exist.\n") {
		return false
	}
	if !write("If entries cannot be distinguished, you may separately authorize and manually unlink every potentially matching entry; this can disconnect unrelated devices. This program never unlinks them.\n") {
		return false
	}
	choice := read("After identifying the account, type absent if the relevant entries are absent, or unlink if manual removal is needed: ")
	if choice == "unlink" {
		if !ask("Type authorize-unlink to separately authorize manual phone-side removal (potentially including unrelated devices): ", "authorize-unlink") {
			return false
		}
		if !write("Manually unlink the relevant phone entries now. This application does not perform the unlink.\n") {
			return false
		}
	} else if choice != "absent" {
		return false
	}
	if !write("Now verify on the phone that the relevant entries are absent.\n") {
		return false
	}
	if !ask("Type verified only after identifying the account and verifying the relevant phone entries are absent: ", "verified") {
		return false
	}
	if present && !ask("A local identity exists. Type remove to separately authorize its local removal: ", "remove") {
		return false
	}
	return ctx.Err() == nil
}
