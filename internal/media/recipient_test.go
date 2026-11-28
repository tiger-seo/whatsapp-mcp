package media

import (
	"errors"
	"strings"
	"testing"
)

func TestSendRecipientForms(t *testing.T) {
	for in, want := range map[string]struct {
		jid  string
		kind RecipientKind
	}{
		"491511000001":                    {"491511000001@s.whatsapp.net", KindPhone},
		"+491511000001":                   {"491511000001@s.whatsapp.net", KindPhone},
		"+49 151 100-00-01":               {"491511000001@s.whatsapp.net", KindPhone},
		"  +491511000001 ":                {"491511000001@s.whatsapp.net", KindPhone},
		"491511000001@s.whatsapp.net":     {"491511000001@s.whatsapp.net", KindPhone},
		"491511000001:7@s.whatsapp.net":   {"491511000001@s.whatsapp.net", KindPhone},
		"100000000042@lid":                {"100000000042@lid", KindLID},
		"100000000042:3@lid":              {"100000000042@lid", KindLID},
		"120363000000000001@g.us":         {"120363000000000001@g.us", KindGroup},
		"491511000001-1500000000@g.us":    {"491511000001-1500000000@g.us", KindGroup},
		strings.Repeat("1", 20):           {strings.Repeat("1", 20) + "@s.whatsapp.net", KindPhone},
		"1" + strings.Repeat(" ", 200):    {"1@s.whatsapp.net", KindPhone},
		"1" + strings.Repeat("-", 255):    {"1@s.whatsapp.net", KindPhone},
		strings.Repeat("9", 20) + "@lid":  {strings.Repeat("9", 20) + "@lid", KindLID},
		strings.Repeat("9", 20) + "@g.us": {strings.Repeat("9", 20) + "@g.us", KindGroup},
		"00491511000001":                  {"00491511000001@s.whatsapp.net", KindPhone},
		"491511000001.0:2@s.whatsapp.net": {"491511000001@s.whatsapp.net", KindPhone},
	} {
		r, err := ParseRecipient(in)
		if err != nil {
			t.Errorf("ParseRecipient(%q): %v", in, err)
			continue
		}
		if r.String() != want.jid || r.Kind() != want.kind {
			t.Errorf("ParseRecipient(%q) = %s (%s), want %s (%s)", in, r, r.Kind(), want.jid, want.kind)
		}
	}
}

func TestSendRecipientRefused(t *testing.T) {
	for _, in := range []string{
		"", " ", "+", "-", "++1", "1+1", "abc", "(49) 151", "49.151", "49\n151", "49\x00151", "\xff",
		strings.Repeat("1", 21), "1" + strings.Repeat(" ", 256), "0", "000", "0@s.whatsapp.net",
		"status@broadcast", "491511000001@broadcast", "123@newsletter", "123456@newsletter",
		"s.whatsapp.net", "@s.whatsapp.net", "@g.us", "@lid", "g.us", "491511000001@c.us",
		"491511000001@hosted", "491511000001@hosted.lid", "491511000001@bot", "867051314767696@msgr",
		"abc@s.whatsapp.net", "+491511000001@s.whatsapp.net", "a1@lid", "0@lid", "x@g.us", "-@g.us",
		strings.Repeat("1", 65) + "@g.us", "123:1@g.us", "123.1@g.us", "1@s.whatsapp.net@g.us",
		"491511000001@s.whatsapp.net ", "491511000001@S.WHATSAPP.NET",
	} {
		if r, err := ParseRecipient(in); !errors.Is(err, ErrRecipient) {
			t.Errorf("ParseRecipient(%q) = %q, %v; want a refusal", in, r.String(), err)
		}
	}
	// The refusal never repeats the value.
	_, err := ParseRecipient("secret-value@newsletter")
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Errorf("refusal = %v", err)
	}
}
