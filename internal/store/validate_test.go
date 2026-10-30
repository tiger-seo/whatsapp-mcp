package store

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want a *ValidationError", err)
		return
	}
	if field != "" && ve.Field != field {
		t.Errorf("field = %q, want %q", ve.Field, field)
	}
}

func TestParseJIDRules(t *testing.T) {
	long := strings.Repeat("1", maxJIDBytes-len("@lid")) + "@lid"
	if _, err := parseChatJID("chat_jid", long); err != nil {
		t.Errorf("a JID of exactly 256 bytes: %v", err)
	}
	_, err := parseChatJID("chat_jid", "1"+long)
	wantInvalid(t, err, "chat_jid")

	for _, bad := range []string{"", "@g.us", "x@evil.example", "\x00@lid", "no-at-sign", "1@s.whatsapp.net\n", "1:2@s.whatsapp.net"} {
		_, err := parseChatJID("chat_jid", bad)
		wantInvalid(t, err, "chat_jid")
	}
	for _, good := range []string{"491511234567@s.whatsapp.net", "123-456@g.us", "100000000001@lid"} {
		if _, err := parseChatJID("chat_jid", good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}

	j, err := parseSenderJID("sender_jid", "491511234567:12@s.whatsapp.net")
	if err != nil || j.String() != "491511234567@s.whatsapp.net" || phoneOf(j) != "491511234567" {
		t.Errorf("device part: %v, %v; want it stripped", j, err)
	}
	if _, err := parseSenderJID("sender_jid", "123-456@g.us"); err == nil {
		t.Error("a group is not a sender")
	}
	if j, err := parseSenderJID("sender_jid", "100000000001:3@lid"); err != nil || j.String() != "100000000001@lid" || phoneOf(j) != "" {
		t.Errorf("LID sender = %v, %v", j, err)
	}
}

func TestValidateIDAndTime(t *testing.T) {
	if err := validateID("id", strings.Repeat("a", maxIDBytes)); err != nil {
		t.Errorf("256 bytes: %v", err)
	}
	for _, bad := range []string{"", strings.Repeat("a", maxIDBytes+1), "a\x00b", "a\nb", "a\xffb"} {
		wantInvalid(t, validateID("id", bad), "id")
	}
	if err := validateTime("time", minTime); err != nil {
		t.Errorf("minTime: %v", err)
	}
	wantInvalid(t, validateTime("time", minTime.Add(-time.Millisecond)), "time")
	wantInvalid(t, validateTime("time", maxTime), "time")
	if err := validateTime("time", maxTime.Add(-time.Millisecond)); err != nil {
		t.Errorf("just under maxTime: %v", err)
	}
}

func TestValidateTextLimits(t *testing.T) {
	for _, c := range []struct {
		field string
		max   int
	}{{"text", maxTextBytes}, {"name", maxNameBytes}, {"search", maxSearchBytes}, {"media_url", maxURLBytes}, {"mime_type", maxMIMEBytes}} {
		if err := validText(c.field, strings.Repeat("a", c.max), c.max); err != nil {
			t.Errorf("%s at the limit: %v", c.field, err)
		}
		wantInvalid(t, validText(c.field, strings.Repeat("a", c.max+1), c.max), c.field)
	}
	wantInvalid(t, validText("text", "a\xffb", 100), "text")
	// Control characters are allowed in text.
	if err := validText("text", "a\x1b[31m\x07", 100); err != nil {
		t.Errorf("control characters in text: %v", err)
	}
}

func TestValidateMedia(t *testing.T) {
	ok := MediaMeta{MediaKey: make([]byte, 32), FileSHA256: make([]byte, 32), FileEncSHA256: make([]byte, 32)}
	n := int64(maxFileLength)
	ok.FileLength = &n
	if err := validateMedia("document", ok); err != nil {
		t.Errorf("valid media: %v", err)
	}
	wantInvalid(t, validateMedia("sticker", MediaMeta{}), "media_type")
	for field, m := range map[string]MediaMeta{
		"media_key":       {MediaKey: make([]byte, 31)},
		"file_sha256":     {FileSHA256: make([]byte, 33)},
		"file_enc_sha256": {FileEncSHA256: make([]byte, 1)},
		"media_url":       {URL: strings.Repeat("u", maxURLBytes+1)},
		"file_name":       {FileName: strings.Repeat("f", maxFileNameBytes+1)},
	} {
		wantInvalid(t, validateMedia("image", m), field)
	}
	for _, bad := range []int64{-1, maxFileLength + 1} {
		bad := bad
		wantInvalid(t, validateMedia("image", MediaMeta{FileLength: &bad}), "file_length")
	}
	zero := int64(0)
	if err := validateMedia("image", MediaMeta{FileLength: &zero}); err != nil {
		t.Errorf("file length 0: %v", err)
	}
}

func TestNormalizePhone(t *testing.T) {
	for _, in := range []string{"+49 151 1234567", "49-151-1234567", "491511234567@s.whatsapp.net", "(49) 151.1234567", "491511234567"} {
		if got, err := NormalizePhone(in); err != nil || got != "491511234567" {
			t.Errorf("NormalizePhone(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "+", "abc", "49x151", "123@lid", "123@g.us", strings.Repeat("1", 21), "1@s.whatsapp.net@x"} {
		if got, err := NormalizePhone(bad); err == nil {
			t.Errorf("NormalizePhone(%q) = %q, want an error", bad, got)
		}
	}
	if got, err := NormalizePhone(strings.Repeat("1", 20)); err != nil || len(got) != 20 {
		t.Errorf("20 digits: %q, %v", got, err)
	}
}

func TestParseContact(t *testing.T) {
	for _, c := range []struct{ in, phone, lid string }{
		{"+49 151 1234567", "491511234567", ""},
		{"491511234567@s.whatsapp.net", "491511234567", ""},
		{"491511234567:7@s.whatsapp.net", "491511234567", ""},
		{"100000000001@lid", "", "100000000001"},
		{"100000000001:9@lid", "", "100000000001"},
	} {
		phone, lid, err := parseContact(c.in)
		if err != nil || phone != c.phone || lid != c.lid {
			t.Errorf("parseContact(%q) = %q, %q, %v; want %q, %q", c.in, phone, lid, err, c.phone, c.lid)
		}
	}
	for _, bad := range []string{"", "Alice", "123@g.us", "@lid", "x@evil.example"} {
		_, _, err := parseContact(bad)
		wantInvalid(t, err, "contact")
	}
}

func FuzzNormalizePhone(f *testing.F) {
	for _, s := range []string{"+49 151 1234567", "491511234567@s.whatsapp.net", "", "(1)", "1@lid", "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := NormalizePhone(s)
		if err != nil {
			wantInvalid(t, err, "phone")
			return
		}
		if n := len(got); n < 1 || n > 20 || strings.Trim(got, "0123456789") != "" {
			t.Fatalf("accepted %q as %q", s, got)
		}
		if again, err := NormalizePhone(got); err != nil || again != got {
			t.Fatalf("%q does not round-trip: %q, %v", got, again, err)
		}
	})
}

func FuzzValidateMessage(f *testing.F) {
	f.Add("491511234567@s.whatsapp.net", "ID1", "491511234567:3@s.whatsapp.net", "hello", int64(1_800_000_000_000))
	f.Add("123-456@g.us", "", "x@lid", "", int64(0))
	f.Fuzz(func(t *testing.T, chat, id, sender, text string, ms int64) {
		m := Message{ChatJID: chat, ID: id, SenderJID: sender, Text: text, Time: time.UnixMilli(ms)}
		sm, _, err := m.validate()
		if err != nil {
			wantInvalid(t, err, "")
			return
		}
		if !utf8.ValidString(sm.id) || sm.chatJID == "" || sm.senderJID == "" || len(sm.chatJID) > maxJIDBytes {
			t.Fatalf("accepted an unusable message: %+v", sm)
		}
		if _, err := parseChatJID("chat_jid", sm.chatJID); err != nil {
			t.Fatalf("canonical chat JID %q does not round-trip: %v", sm.chatJID, err)
		}
	})
}
