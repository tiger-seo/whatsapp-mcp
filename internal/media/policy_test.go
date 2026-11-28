package media

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mapAliases is an in-memory LID map: LID JID to phone JID.
type mapAliases struct {
	lids  map[string]string
	err   error
	reads int
}

func (m *mapAliases) PhoneForLID(_ context.Context, lid string) (string, bool, error) {
	m.reads++
	if m.err != nil {
		return "", false, m.err
	}
	phone, ok := m.lids[lid]
	return phone, ok, nil
}

func (m *mapAliases) LIDsForPhone(_ context.Context, phone string) ([]string, error) {
	m.reads++
	if m.err != nil {
		return nil, m.err
	}
	var out []string
	for lid, p := range m.lids {
		if p == phone {
			out = append(out, lid)
		}
	}
	return out, nil
}

const (
	peerPhone  = "491511000001@s.whatsapp.net"
	peerLID    = "100000000001@lid"
	otherPhone = "491511000002@s.whatsapp.net"
	otherLID   = "100000000002@lid"
	testGroup  = "120363000000000001@g.us"
)

func TestSendPolicy(t *testing.T) {
	mapped := map[string]string{peerLID: peerPhone}
	for _, tt := range []struct {
		name      string
		allow     []string
		recipient string
		want      string // "" = not permitted
	}{
		{"empty list", nil, peerPhone, ""},
		{"empty list refuses a mapped LID", nil, peerLID, ""},
		{"phone listed", []string{peerPhone}, peerPhone, peerPhone},
		{"phone listed in another form", []string{"+49 151 100-0001"}, peerPhone, peerPhone},
		{"other phone", []string{peerPhone}, otherPhone, ""},
		{"LID of a listed phone goes to the phone", []string{peerPhone}, peerLID, peerPhone},
		{"phone of a listed LID", []string{peerLID}, peerPhone, peerPhone},
		{"listed LID goes to its phone", []string{peerLID}, peerLID, peerPhone},
		{"unmapped listed LID stays a LID", []string{otherLID}, otherLID, otherLID},
		{"unmapped LID is not guessed", []string{otherPhone}, otherLID, ""},
		{"phone is not guessed from an unmapped LID", []string{otherLID}, otherPhone, ""},
		{"group listed", []string{testGroup}, testGroup, testGroup},
		{"group not listed", []string{peerPhone}, testGroup, ""},
		{"any permits a phone", []string{"*"}, otherPhone, otherPhone},
		{"any permits a group", []string{"*"}, testGroup, testGroup},
		{"any sends a mapped LID to the phone", []string{"*"}, peerLID, peerPhone},
		{"any keeps an unmapped LID", []string{"*"}, otherLID, otherLID},
		{"a group number is not a phone", []string{"491511000001@g.us"}, peerPhone, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewPolicy(tt.allow)
			if err != nil {
				t.Fatal(err)
			}
			r, err := ParseRecipient(tt.recipient)
			if err != nil {
				t.Fatal(err)
			}
			aliases := &mapAliases{lids: mapped}
			got, err := p.Resolve(context.Background(), aliases, r)
			if tt.want == "" {
				if !errors.Is(err, ErrNotPermitted) {
					t.Fatalf("Resolve = %q, %v; want ErrNotPermitted", got.String(), err)
				}
			} else if err != nil || got.String() != tt.want {
				t.Fatalf("Resolve = %q, %v; want %q", got.String(), err, tt.want)
			}
			if len(tt.allow) == 0 && aliases.reads != 0 {
				t.Errorf("sending is off, but the LID map was read %d times", aliases.reads)
			}
		})
	}
}

func TestSendPolicyAliasFailureRefuses(t *testing.T) {
	p, err := NewPolicy([]string{peerLID})
	if err != nil {
		t.Fatal(err)
	}
	broken := &mapAliases{err: errors.New("broken")}
	for _, recipient := range []string{peerLID, otherPhone} {
		r, _ := ParseRecipient(recipient)
		if got, err := p.Resolve(context.Background(), broken, r); !errors.Is(err, ErrAliases) {
			t.Errorf("%s: Resolve = %q, %v; want ErrAliases", recipient, got.String(), err)
		}
	}
}

func TestSendAllowList(t *testing.T) {
	got, err := NormalizeAllowList([]string{"+49 151 100-0001", peerPhone, "100000000001:2@lid", testGroup})
	if err != nil || strings.Join(got, ",") != peerPhone+","+peerLID+","+testGroup {
		t.Fatalf("NormalizeAllowList = %v, %v", got, err)
	}
	if got, err := NormalizeAllowList(nil); err != nil || len(got) != 0 {
		t.Errorf("empty list = %v, %v", got, err)
	}
	for _, bad := range [][]string{
		{"*", peerPhone}, {peerPhone, "*"}, {"*", "*"}, {"status@broadcast"}, {"1@newsletter"}, {"s.whatsapp.net"},
		{"1@broadcast"}, {""}, {peerPhone, "not-a-number"}, {"**"},
	} {
		got, err := NormalizeAllowList(bad)
		if err == nil {
			t.Errorf("NormalizeAllowList(%q) = %v, want an error", bad, got)
			continue
		}
		for _, entry := range bad {
			if len(entry) > 3 && strings.Contains(err.Error(), entry) {
				t.Errorf("error repeats the entry %q: %v", entry, err)
			}
		}
	}
}
