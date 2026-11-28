package wa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestSendOutcomeClassification(t *testing.T) {
	wrap := func(text string, err error) error { return fmt.Errorf(text+": %w", err) }
	broken := errors.New("broken")
	disconnected := &whatsmeow.DisconnectedError{Action: "message send"}
	failed := []error{
		fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 463),
		fmt.Errorf("%w: no LID found for x from server", whatsmeow.ErrRecipientNotOnWhatsApp),
		whatsmeow.ErrNoPermissionInGroup,
		whatsmeow.ErrNotLoggedIn,
		whatsmeow.ErrRecipientADJID,
		fmt.Errorf("%w %s", whatsmeow.ErrUnknownServer, "x"),
		whatsmeow.ErrBroadcastListUnsupported,
		wrap("failed to get group members", broken),
		wrap("failed to get user info for 1@s.whatsapp.net to fill LID cache", broken),
		wrap("failed to get LID for PN 1@s.whatsapp.net", broken),
		wrap("failed to get device list", whatsmeow.ErrIQTimedOut),
		wrap("failed to get device list", whatsmeow.ErrIQDisconnected),
		wrap("failed to marshal message", broken),
		wrap("failed to marshal message (for own devices)", broken),
		wrap("failed to fetch LID mappings", broken),
		wrap("failed to prefetch sessions", broken),
		wrap("failed to save cached sessions", broken),
		wrap("failed to create sender key distribution message to send x to y", broken),
		wrap("failed to marshal sender key distribution message to send x to y", broken),
		wrap("failed to encrypt group message to send x to y", broken),
		wrap("failed to send message node: failed to marshal node", broken),
	}
	for _, err := range failed {
		got := classifySendError(err)
		if !errors.Is(got, ErrSendFailed) || errors.Is(got, ErrOutcomeUnknown) || !errors.Is(got, err) {
			t.Errorf("classify(%v) = %v, want a failed send", err, got)
		}
	}
	unknown := []error{
		broken,
		io.ErrUnexpectedEOF,
		whatsmeow.ErrMessageTimedOut,
		whatsmeow.ErrNotConnected,
		disconnected,
		wrap("failed to send message node", broken),
		wrap("failed to send message node", whatsmeow.ErrNotConnected),
		context.Canceled,
		context.DeadlineExceeded,
		wrap("something", context.Canceled),
		// A cancellation or a deadline leaves the outcome unknown even inside a listed step.
		wrap("failed to get device list", context.Canceled),
		wrap("failed to get group members", context.DeadlineExceeded),
		// A listed text counts only where the library puts it: at the start.
		wrap("later: failed to get device list", broken),
	}
	for _, err := range unknown {
		got := classifySendError(err)
		if !errors.Is(got, ErrOutcomeUnknown) || errors.Is(got, ErrSendFailed) || !errors.Is(got, err) {
			t.Errorf("classify(%v) = %v, want an unknown outcome", err, got)
		}
	}
}
