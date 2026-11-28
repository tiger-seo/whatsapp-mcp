// Package media is the send service: it checks who may be sent to, reads files from the allowed directories,
// analyses and converts audio, and sends through the connection owner's send port.
package media

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/media/sendapi"
	"go.mau.fi/whatsmeow/types"
)

const (
	maxPhoneDigits    = 20
	maxGroupUserBytes = 64
)

// ErrRecipient refuses an address that cannot receive a send. Its text never repeats the value.
var ErrRecipient = errors.New("must be a phone number, a phone JID, a LID or a group JID of at most 256 bytes; status, broadcast, newsletter and server addresses are not accepted")

// Recipient is a normalized send address.
type Recipient struct {
	JID types.JID
}

// String is the normalized address.
func (r Recipient) String() string { return r.JID.String() }

// Kind is the address kind; note to self is decided by the caller, which knows the account's own addresses.
func (r Recipient) Kind() RecipientKind {
	switch r.JID.Server {
	case types.DefaultUserServer:
		return KindPhone
	case types.HiddenUserServer:
		return KindLID
	case types.GroupServer:
		return KindGroup
	}
	return KindUnknown
}

func digitsOnly(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// phoneRecipient accepts the digits of a phone number. A number of zeros alone is a server account.
func phoneRecipient(digits string) (Recipient, error) {
	if !digitsOnly(digits) || len(digits) > maxPhoneDigits || strings.Trim(digits, "0") == "" {
		return Recipient{}, ErrRecipient
	}
	return Recipient{JID: types.NewJID(digits, types.DefaultUserServer)}, nil
}

// ParseRecipient normalizes a recipient: a phone number (digits, an optional leading +, spaces and dashes
// ignored), a phone JID, a LID or a group JID. The device part of a phone JID or LID is dropped.
func ParseRecipient(s string) (Recipient, error) {
	if s == "" || len(s) > sendapi.MaxRecipientBytes || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return Recipient{}, ErrRecipient
	}
	user, server, isJID := strings.Cut(s, "@")
	if !isJID {
		rest := strings.TrimPrefix(strings.TrimSpace(s), "+")
		return phoneRecipient(strings.Map(func(r rune) rune {
			if r == ' ' || r == '-' {
				return -1
			}
			return r
		}, rest))
	}
	switch server {
	case types.GroupServer:
		if user == "" || len(user) > maxGroupUserBytes || strings.Trim(user, "0123456789-") != "" ||
			strings.Trim(user, "-") == "" {
			return Recipient{}, ErrRecipient
		}
		return Recipient{JID: types.NewJID(user, types.GroupServer)}, nil
	case types.DefaultUserServer, types.HiddenUserServer:
	default:
		return Recipient{}, ErrRecipient
	}
	jid, err := types.ParseJID(s)
	if err != nil || jid.Server != server || !digitsOnly(jid.User) || len(jid.User) > maxPhoneDigits {
		return Recipient{}, ErrRecipient
	}
	if server == types.DefaultUserServer {
		return phoneRecipient(jid.User)
	}
	if strings.Trim(jid.User, "0") == "" {
		return Recipient{}, ErrRecipient
	}
	return Recipient{JID: types.NewJID(jid.User, types.HiddenUserServer)}, nil
}
