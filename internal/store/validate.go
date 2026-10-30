package store

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/types"
)

const (
	serverUser  = types.DefaultUserServer // s.whatsapp.net
	serverGroup = types.GroupServer       // g.us
	serverLID   = types.HiddenUserServer  // lid
)

// validText checks that s is valid UTF-8 and at most max bytes. It never repeats s.
func validText(field, s string, max int) error {
	if len(s) > max {
		return invalid(field, "too long")
	}
	if !utf8.ValidString(s) {
		return invalid(field, "not valid UTF-8")
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// parseJID parses s with whatsmeow's parser and applies the store's rules: a server from allowed, a non-empty
// user, at most 256 bytes, valid UTF-8 and no control characters. A device part is dropped when stripDevice is
// set, and refused otherwise. The returned string is the canonical form.
func parseJID(field, s string, allowed []string, stripDevice bool) (types.JID, error) {
	if s == "" {
		return types.JID{}, invalid(field, "empty")
	}
	if len(s) > maxJIDBytes {
		return types.JID{}, invalid(field, "too long")
	}
	if !utf8.ValidString(s) || hasControl(s) {
		return types.JID{}, invalid(field, "contains invalid characters")
	}
	jid, err := types.ParseJID(s)
	if err != nil || jid.User == "" {
		return types.JID{}, invalid(field, "malformed JID")
	}
	ok := false
	for _, a := range allowed {
		if jid.Server == a {
			ok = true
		}
	}
	if !ok {
		return types.JID{}, invalid(field, "server not allowed")
	}
	if jid.Device != 0 || jid.RawAgent != 0 {
		if !stripDevice {
			return types.JID{}, invalid(field, "has a device part")
		}
		jid.Device, jid.RawAgent = 0, 0
	}
	if len(jid.String()) > maxJIDBytes {
		return types.JID{}, invalid(field, "too long")
	}
	return jid, nil
}

// parseChatJID accepts a chat: a phone-number, group or LID JID. A device part is not allowed.
func parseChatJID(field, s string) (types.JID, error) {
	return parseJID(field, s, []string{serverUser, serverGroup, serverLID}, false)
}

// parseSenderJID accepts a sender and removes its device part.
func parseSenderJID(field, s string) (types.JID, error) {
	return parseJID(field, s, []string{serverUser, serverLID}, true)
}

func validateID(field, s string) error {
	if s == "" {
		return invalid(field, "empty")
	}
	if len(s) > maxIDBytes {
		return invalid(field, "too long")
	}
	if !utf8.ValidString(s) || hasControl(s) {
		return invalid(field, "contains invalid characters")
	}
	return nil
}

func validateTime(field string, t time.Time) error {
	if t.Before(minTime) || !t.Before(maxTime) {
		return invalid(field, "outside the supported range")
	}
	return nil
}

func validateMedia(mediaType string, m MediaMeta) error {
	switch mediaType {
	case "", "image", "video", "audio", "document":
	default:
		return invalid("media_type", "not one of image, video, audio, document")
	}
	for _, f := range []struct {
		name string
		v    string
		max  int
	}{
		{"media_url", m.URL, maxURLBytes}, {"media_direct_path", m.DirectPath, maxPathBytes},
		{"mime_type", m.MIMEType, maxMIMEBytes}, {"file_name", m.FileName, maxFileNameBytes},
	} {
		if err := validText(f.name, f.v, f.max); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		name string
		v    []byte
	}{{"media_key", m.MediaKey}, {"file_sha256", m.FileSHA256}, {"file_enc_sha256", m.FileEncSHA256}} {
		if len(f.v) != 0 && len(f.v) != mediaHashBytes {
			return invalid(f.name, "must be exactly 32 bytes")
		}
	}
	if m.FileLength != nil && (*m.FileLength < 0 || *m.FileLength > maxFileLength) {
		return invalid("file_length", "outside 0 to 4 GiB")
	}
	return nil
}

// NormalizePhone returns the digits of a phone number written with an optional +, spaces, dashes, dots and
// parentheses around 1 to 20 digits, or of an @s.whatsapp.net JID. Anything else is a validation error.
func NormalizePhone(s string) (string, error) {
	return normalizePhone("phone", s)
}

func normalizePhone(field, s string) (string, error) {
	if len(s) > maxJIDBytes {
		return "", invalid(field, "too long")
	}
	if i := strings.IndexByte(s, '@'); i >= 0 {
		if s[i+1:] != serverUser {
			return "", invalid(field, "not a phone number")
		}
		s = s[:i]
		if s == "" || strings.Trim(s, "0123456789") != "" || len(s) > 20 {
			return "", invalid(field, "not a phone number")
		}
		return s, nil
	}
	rest := s
	if strings.HasPrefix(rest, "+") {
		rest = rest[1:]
	}
	var digits strings.Builder
	for _, r := range rest {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
		default:
			return "", invalid(field, "not a phone number")
		}
	}
	if n := digits.Len(); n < 1 || n > 20 {
		return "", invalid(field, "not a phone number")
	}
	return digits.String(), nil
}

// parseContact reads a contact argument: a phone number in any NormalizePhone form gives the phone digits, and a
// @lid JID, with its device part stripped, gives the LID user part. Anything else is a validation error.
func parseContact(s string) (phone, lid string, err error) {
	if strings.HasSuffix(s, "@"+serverLID) {
		jid, err := parseJID("contact", s, []string{serverLID}, true)
		if err != nil {
			return "", "", err
		}
		return "", jid.User, nil
	}
	if i := strings.IndexByte(s, '@'); i >= 0 && s[i+1:] == serverUser {
		// A phone JID may carry a device part, which is dropped before the digits are read.
		jid, err := parseJID("contact", s, []string{serverUser}, true)
		if err != nil {
			return "", "", err
		}
		s = jid.User
	}
	phone, err = normalizePhone("contact", s)
	return phone, "", err
}

// phoneOf returns the digits of a JID's user part for the phone-number server and "" for any other.
func phoneOf(j types.JID) string {
	if j.Server != serverUser {
		return ""
	}
	return j.User
}
