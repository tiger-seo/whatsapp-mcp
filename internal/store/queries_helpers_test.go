package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// Synthetic identities shared by the query tests. Phone numbers are made up; LIDs are opaque digits.
const (
	ownJID   = testOwnPhone + "@s.whatsapp.net"
	ownLID   = testOwnLID + "@lid"
	groupOne = "120363000000000001@g.us"
	groupTwo = "120363000000000002@g.us"
)

func phoneJID(digits string) string { return digits + "@s.whatsapp.net" }
func lidJID(user string) string     { return user + "@lid" }

// seedDeviceAs saves a synthetic device under the given own JID (which may carry a device part) and LID (the
// zero JID for none), and returns it loaded back.
func seedDeviceAs(t testing.TB, st *Store, own, lid types.JID) *wastore.Device {
	t.Helper()
	ctx := context.Background()
	dev := st.Device().NewDevice()
	dev.ID = &own
	dev.LID = lid
	dev.PushName = "Test Account"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(ctx); err != nil {
		t.Fatalf("Save device: %v", err)
	}
	loaded, err := st.Device().GetFirstDevice(ctx)
	if err != nil {
		t.Fatalf("GetFirstDevice: %v", err)
	}
	return loaded
}

// nameChat gives a chat a name of the given quality, creating the chat if it is missing.
func nameChat(t testing.TB, st *Store, jid, name string, q NameQuality) {
	t.Helper()
	if err := st.UpdateChat(context.Background(), ChatUpdate{JID: jid, Name: name, NameQuality: q, NameTime: baseTime}); err != nil {
		t.Fatalf("UpdateChat %s: %v", jid, err)
	}
}

func page(limit, offset int) Page { return Page{Limit: limit, Offset: offset} }

// all is a page large enough for every test dataset.
var all = page(200, 0)

func chatJIDs(chats []ChatView) []string {
	out := make([]string, len(chats))
	for i, c := range chats {
		out[i] = c.JID
	}
	return out
}

func hitIDs(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Message.ID
	}
	return out
}

func messageIDs(views []MessageView) []string {
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = v.ID
	}
	return out
}

func wantIDs(t testing.TB, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// wantInvalidFor checks that err is a *ValidationError naming field ("" accepts any field).
func wantInvalidFor(t testing.TB, what string, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("%s: %v, want *ValidationError", what, err)
		return
	}
	if field != "" && ve.Field != field {
		t.Errorf("%s: field %q, want %q", what, ve.Field, field)
	}
}

func wantNotFound(t testing.TB, what string, err error) *NotFoundError {
	t.Helper()
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("%s: %v, want *NotFoundError", what, err)
		return nil
	}
	return nf
}

// badPages are the pages outside the approved limits.
var badPages = map[string]Page{
	"limit 0":        page(0, 0),
	"limit 201":      page(maxListSize+1, 0),
	"offset -1":      page(10, -1),
	"offset 100,001": page(10, maxOffset+1),
}
