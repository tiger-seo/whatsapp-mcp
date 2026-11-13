package store

import (
	"errors"
	"fmt"
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

// messageFieldCases holds, for each field of Message, one value StoreMessages accepts and one it rejects.
func messageFieldCases() []struct {
	field     string
	good, bad func(*Message)
} {
	hash := func(n int) []byte { return make([]byte, n) }
	length := func(n int64) *int64 { return &n }
	return []struct {
		field     string
		good, bad func(*Message)
	}{
		{"chat_jid", func(m *Message) { m.ChatJID = "100000000002@lid" }, func(m *Message) { m.ChatJID = "1:2@s.whatsapp.net" }},
		{"id", func(m *Message) { m.ID = strings.Repeat("i", maxIDBytes) }, func(m *Message) { m.ID = "" }},
		{"sender_jid", func(m *Message) { m.SenderJID = "491511234567:4@s.whatsapp.net" }, func(m *Message) { m.SenderJID = groupG }},
		{"time", func(m *Message) { m.Time = minTime }, func(m *Message) { m.Time = time.Time{} }},
		{"text", func(m *Message) { m.Text = strings.Repeat("t", maxTextBytes) }, func(m *Message) { m.Text = strings.Repeat("t", maxTextBytes+1) }},
		{"caption", func(m *Message) { m.Caption = strings.Repeat("c", maxCaptionBytes) }, func(m *Message) { m.Caption = "a\xffb" }},
		{"media_type", func(m *Message) { m.MediaType = "audio" }, func(m *Message) { m.MediaType = "sticker" }},
		{"media_url", func(m *Message) { m.Media.URL = strings.Repeat("u", maxURLBytes) }, func(m *Message) { m.Media.URL = strings.Repeat("u", maxURLBytes+1) }},
		{"media_direct_path", func(m *Message) { m.Media.DirectPath = "/p" }, func(m *Message) { m.Media.DirectPath = strings.Repeat("p", maxPathBytes+1) }},
		{"media_key", func(m *Message) { m.Media.MediaKey = hash(32) }, func(m *Message) { m.Media.MediaKey = hash(31) }},
		{"file_sha256", func(m *Message) { m.Media.FileSHA256 = hash(32) }, func(m *Message) { m.Media.FileSHA256 = hash(33) }},
		{"file_enc_sha256", func(m *Message) { m.Media.FileEncSHA256 = hash(32) }, func(m *Message) { m.Media.FileEncSHA256 = hash(1) }},
		{"file_length", func(m *Message) { m.Media.FileLength = length(maxFileLength) }, func(m *Message) { m.Media.FileLength = length(-1) }},
		{"mime_type", func(m *Message) { m.Media.MIMEType = "image/jpeg" }, func(m *Message) { m.Media.MIMEType = strings.Repeat("m", maxMIMEBytes+1) }},
		{"file_name", func(m *Message) { m.Media.FileName = "a.pdf" }, func(m *Message) { m.Media.FileName = strings.Repeat("f", maxFileNameBytes+1) }},
	}
}

// ValidateMessage gives the same verdict, with the same field, as StoreMessages for every field of Message, and
// its error never repeats the value.
func TestValidateMessage(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := t.Context()
	for i, c := range messageFieldCases() {
		good := msg(groupG, fmt.Sprintf("good%d", i), alice, at(i))
		c.good(&good)
		if err := ValidateMessage(good); err != nil {
			t.Errorf("%s: ValidateMessage rejected an accepted value: %v", c.field, err)
		}
		if err := st.StoreMessages(ctx, []Message{good}); err != nil {
			t.Errorf("%s: StoreMessages rejected the good value: %v", c.field, err)
		}
		bad := msg(groupG, fmt.Sprintf("bad%d", i), alice, at(i))
		c.bad(&bad)
		verr := ValidateMessage(bad)
		wantInvalid(t, verr, c.field)
		serr := st.StoreMessages(ctx, []Message{bad})
		wantInvalid(t, serr, c.field)
		if verr != nil && serr != nil && verr.Error() != serr.Error() {
			t.Errorf("%s: ValidateMessage says %q, StoreMessages %q", c.field, verr, serr)
		}
		if verr != nil && bad.Text != "" && strings.Contains(verr.Error(), bad.Text) {
			t.Errorf("%s: error repeats the text", c.field)
		}
	}
	secret := Message{ChatJID: "secret-value", ID: "x", SenderJID: alice, Time: at(0)}
	if err := ValidateMessage(secret); err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Errorf("error = %v; want a rejection that does not repeat the value", err)
	}
}

// MessageFieldBytes is exactly what StoreMessages charges: messages summing to 16 MiB are accepted and one byte
// more is a resource limit.
func TestMessageFieldBytes(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := t.Context()
	m := msg(groupG, "id", alice, at(0), withCaption("cap"))
	m.MediaType, m.Media.URL, m.Media.MediaKey = "image", "https://u", make([]byte, 32)
	_, charged, err := m.validate()
	if err != nil {
		t.Fatal(err)
	}
	if got := MessageFieldBytes(m); got != charged {
		t.Errorf("MessageFieldBytes = %d, StoreMessages charges %d", got, charged)
	}
	batch := func(extra int) []Message {
		var out []Message
		total := 0
		for i := 0; total < maxBatchBytes; i++ {
			m := msg(groupG, fmt.Sprintf("fb%d", i), alice, at(i), withText(strings.Repeat("x", maxTextBytes)))
			if n := MessageFieldBytes(m); total+n > maxBatchBytes {
				m.Text = strings.Repeat("x", maxTextBytes-(total+n-maxBatchBytes))
			}
			total += MessageFieldBytes(m)
			out = append(out, m)
		}
		out[len(out)-1].Text += strings.Repeat("y", extra)
		return out
	}
	if err := st.StoreMessages(ctx, batch(0)); err != nil {
		t.Errorf("exactly 16 MiB by MessageFieldBytes: %v", err)
	}
	if err := st.StoreMessages(ctx, batch(1)); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("one byte more: %v, want ErrResourceLimit", err)
	}
}
