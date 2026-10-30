package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestGetMessage(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	alice := phoneJID("491511000001")
	nameChat(t, st, alice, "Alice", NameSaved)
	mustStore(t, st, msg(alice, "X1", alice, at(1), withText("hello"), withCaption("cap")))

	got, err := st.GetMessage(ctx, alice, "X1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "X1" || got.ChatJID != alice || got.ChatName != "Alice" || got.Text != "hello" || got.Caption != "cap" ||
		got.SenderJID != alice || got.SenderPhone != "491511000001" || !got.Time.Equal(at(1)) {
		t.Errorf("GetMessage = %+v", got)
	}

	_, err = st.GetMessage(ctx, alice, "missing")
	if nf := wantNotFound(t, "missing ID", err); nf != nil && (nf.ChatJID != alice || nf.ID != "missing") {
		t.Errorf("not found names %q, %q", nf.ChatJID, nf.ID)
	}
	_, err = st.GetMessage(ctx, groupOne, "X1")
	if nf := wantNotFound(t, "wrong chat", err); nf != nil && nf.ChatJID != groupOne {
		t.Errorf("not found names chat %q", nf.ChatJID)
	}
	_, err = st.GetMessage(ctx, "not a jid", "X1")
	wantInvalidFor(t, "malformed chat", err, "chat_jid")
	_, err = st.GetMessage(ctx, alice, "")
	wantInvalidFor(t, "empty ID", err, "id")
}

func TestGetMessageByID(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	alice := phoneJID("491511000001")
	mustStore(t, st,
		msg(alice, "ONLY", alice, at(1)),
		msg(alice, "TWICE", alice, at(2)),
		msg(groupOne, "TWICE", alice, at(3)),
	)

	got, err := st.GetMessageByID(ctx, "ONLY")
	if err != nil || got.ChatJID != alice {
		t.Errorf("ID in one chat: %+v, %v", got, err)
	}
	_, err = st.GetMessageByID(ctx, "NONE")
	if nf := wantNotFound(t, "unknown ID", err); nf != nil && (nf.ChatJID != "" || nf.ID != "NONE") {
		t.Errorf("not found names %q, %q", nf.ChatJID, nf.ID)
	}

	_, err = st.GetMessageByID(ctx, "TWICE")
	var amb *AmbiguousIDError
	if !errors.As(err, &amb) {
		t.Fatalf("ID in two chats: %v, want *AmbiguousIDError", err)
	}
	wantIDs(t, "ambiguous chats", amb.ChatJIDs, []string{groupOne, alice}) // sorted as strings
	if amb.ID != "TWICE" {
		t.Errorf("ambiguous ID %q", amb.ID)
	}
	_, err = st.GetMessageByID(ctx, "")
	wantInvalidFor(t, "empty ID", err, "id")
}

func TestGetMessageByIDLimit(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	chats := func(n int) {
		t.Helper()
		var msgs []Message
		for i := 0; i < n; i++ {
			msgs = append(msgs, msg(fmt.Sprintf("1203630000%08d@g.us", i), "SHARED", phoneJID("491511000001"), at(i)))
		}
		mustStore(t, st, msgs...)
	}
	chats(maxAmbiguousIDs)
	_, err := st.GetMessageByID(ctx, "SHARED")
	var amb *AmbiguousIDError
	if !errors.As(err, &amb) || len(amb.ChatJIDs) != maxAmbiguousIDs {
		t.Fatalf("100 chats: %v, want an AmbiguousIDError listing 100", err)
	}
	chats(maxAmbiguousIDs + 1)
	_, err = st.GetMessageByID(ctx, "SHARED")
	if !errors.Is(err, ErrResourceLimit) {
		t.Errorf("101 chats: %v, want ErrResourceLimit", err)
	}
}

func TestMediaInfo(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	alice := phoneJID("491511000001")
	length := int64(12345)
	media := MediaMeta{
		URL: "https://mmg.example/file", DirectPath: "/v/t62/file",
		MediaKey: bytes.Repeat([]byte{1}, 32), FileSHA256: bytes.Repeat([]byte{2}, 32), FileEncSHA256: bytes.Repeat([]byte{3}, 32),
		FileLength: &length, MIMEType: "image/jpeg", FileName: "photo.jpg",
	}
	mustStore(t, st,
		msg(alice, "IMG", alice, at(1), withCaption("a photo"), func(m *Message) { m.MediaType, m.Media = "image", media }),
		msg(alice, "BARE", alice, at(2), func(m *Message) { m.MediaType = "audio" }),
		msg(alice, "TXT", alice, at(3)),
	)

	view, got, err := st.MediaInfo(ctx, alice, "IMG")
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != "IMG" || view.MediaType != "image" || view.Caption != "a photo" {
		t.Errorf("view = %+v", view)
	}
	if got.URL != media.URL || got.DirectPath != media.DirectPath || got.MIMEType != media.MIMEType || got.FileName != media.FileName ||
		!bytes.Equal(got.MediaKey, media.MediaKey) || !bytes.Equal(got.FileSHA256, media.FileSHA256) ||
		!bytes.Equal(got.FileEncSHA256, media.FileEncSHA256) || got.FileLength == nil || *got.FileLength != length {
		t.Errorf("media = %+v, want %+v", got, media)
	}

	// A media message with no stored metadata has a nil length, not zero.
	_, bare, err := st.MediaInfo(ctx, alice, "BARE")
	if err != nil {
		t.Fatal(err)
	}
	if bare.FileLength != nil || bare.URL != "" || len(bare.MediaKey) != 0 {
		t.Errorf("bare media = %+v", bare)
	}

	_, _, err = st.MediaInfo(ctx, alice, "TXT")
	if !errors.Is(err, ErrNotMedia) {
		t.Errorf("text message: %v, want ErrNotMedia", err)
	}
	_, _, err = st.MediaInfo(ctx, alice, "missing")
	wantNotFound(t, "missing message", err)
}
