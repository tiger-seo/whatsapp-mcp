package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	alice    = "491511234567@s.whatsapp.net"
	bob      = "491511234568@s.whatsapp.net"
	groupG   = "120363000000000001@g.us"
	groupH   = "120363000000000002@g.us"
	lidAlice = "100000000042@lid"
)

type storedRow struct {
	senderJID, senderPhone, text, caption, mediaType string
	timeMS                                           int64
	fromMe                                           int
	url, directPath, mime, fileName                  *string
	key, sha, encSHA                                 []byte
	length                                           *int64
}

func readRow(t testing.TB, st *Store, chat, id string) storedRow {
	t.Helper()
	var r storedRow
	err := st.reader.QueryRow(`SELECT sender_jid, sender_phone, time, from_me, text, caption, media_type, media_url,
		media_direct_path, media_key, file_sha256, file_enc_sha256, file_length, mime_type, file_name
		FROM messages WHERE chat_jid = ? AND id = ?`, chat, id).Scan(&r.senderJID, &r.senderPhone, &r.timeMS, &r.fromMe, &r.text,
		&r.caption, &r.mediaType, &r.url, &r.directPath, &r.key, &r.sha, &r.encSHA, &r.length, &r.mime, &r.fileName)
	if err != nil {
		t.Fatalf("read %s/%s: %v", chat, id, err)
	}
	return r
}

func chatActivity(t testing.TB, st *Store, jid string) *int64 {
	t.Helper()
	var v *int64
	if err := st.reader.QueryRow("SELECT last_message_time FROM chats WHERE jid = ?", jid).Scan(&v); err != nil {
		t.Fatalf("chat %s: %v", jid, err)
	}
	return v
}

func TestRestoreReplacesAndKeepsOneRow(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(alice, "X", alice, at(0), withText("one")))
	mustStore(t, st, msg(alice, "X", alice, at(0), withText("two")))
	if n := countRows(t, st, "messages"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
	if got := readRow(t, st, alice, "X").text; got != "two" {
		t.Errorf("text = %q, want the new copy", got)
	}
	// Two different copies: the last one stored wins, in either order. This is the one order-dependent case.
	a, b := msg(alice, "Y", alice, at(1), withText("A")), msg(alice, "Y", alice, at(1), withText("B"))
	mustStore(t, st, a)
	mustStore(t, st, b)
	if got := readRow(t, st, alice, "Y").text; got != "B" {
		t.Errorf("A then B kept %q", got)
	}
	mustStore(t, st, a)
	if got := readRow(t, st, alice, "Y").text; got != "A" {
		t.Errorf("B then A kept %q", got)
	}
}

func TestSameIDInTwoChats(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(alice, "X", alice, at(0)), msg(groupG, "X", alice, at(1)))
	if n := countRows(t, st, "messages"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestLastActivityOnlyMovesForward(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(alice, "noon", alice, at(180)))
	mustStore(t, st, msg(alice, "nine", alice, at(0)))
	if got := chatActivity(t, st, alice); got == nil || *got != at(180).UnixMilli() {
		t.Errorf("after an older message: %v, want noon", got)
	}
	mustStore(t, st, msg(bob, "nine", bob, at(0)))
	mustStore(t, st, msg(bob, "noon", bob, at(180)))
	if got := chatActivity(t, st, bob); got == nil || *got != at(180).UnixMilli() {
		t.Errorf("after a newer message: %v, want noon", got)
	}
}

func TestMessageForUnknownChatCreatesItAtomically(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(groupG, "A", alice, at(5)))
	if got := chatActivity(t, st, groupG); got == nil || *got != at(5).UnixMilli() {
		t.Errorf("created chat activity = %v", got)
	}
	var phone string
	st.reader.QueryRow("SELECT phone FROM chats WHERE jid = ?", groupG).Scan(&phone)
	if phone != "" {
		t.Errorf("group phone = %q, want empty", phone)
	}
	mustStore(t, st, msg(alice, "A", alice, at(5)))
	st.reader.QueryRow("SELECT phone FROM chats WHERE jid = ?", alice).Scan(&phone)
	if phone != "491511234567" {
		t.Errorf("direct chat phone = %q", phone)
	}

	// A failure after the chat insert leaves neither the chat nor the message.
	boom := errors.New("injected")
	st.hooks.afterChatInsert = func() error { return boom }
	err := st.StoreMessage(context.Background(), msg(groupH, "B", alice, at(6)))
	st.hooks.afterChatInsert = nil
	if err == nil {
		t.Fatal("the injected failure was not reported")
	}
	var n int
	st.reader.QueryRow("SELECT count(*) FROM chats WHERE jid = ?", groupH).Scan(&n)
	if n != 0 || messageExists(t, st, groupH, "B") {
		t.Errorf("a failed write left %d chats and the message %v", n, messageExists(t, st, groupH, "B"))
	}
}

func fullMedia() MediaMeta {
	n := int64(1234)
	return MediaMeta{
		URL: "https://example.invalid/a", DirectPath: "/v/a", FileLength: &n, MIMEType: "image/jpeg", FileName: "a.jpg",
		MediaKey: bytes.Repeat([]byte{1}, 32), FileSHA256: bytes.Repeat([]byte{2}, 32), FileEncSHA256: bytes.Repeat([]byte{3}, 32),
	}
}

func assertMedia(t *testing.T, r storedRow, want MediaMeta) {
	t.Helper()
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	if str(r.url) != want.URL || str(r.directPath) != want.DirectPath || str(r.mime) != want.MIMEType || str(r.fileName) != want.FileName {
		t.Errorf("text media fields = %q %q %q %q, want %+v", str(r.url), str(r.directPath), str(r.mime), str(r.fileName), want)
	}
	if !bytes.Equal(r.key, want.MediaKey) || !bytes.Equal(r.sha, want.FileSHA256) || !bytes.Equal(r.encSHA, want.FileEncSHA256) {
		t.Error("blob media fields differ")
	}
	if (r.length == nil) != (want.FileLength == nil) || (r.length != nil && *r.length != *want.FileLength) {
		t.Errorf("file length = %v, want %v", r.length, want.FileLength)
	}
}

func TestRestoreKeepsEveryMediaField(t *testing.T) {
	setters := map[string]func(*MediaMeta){
		"url":         func(m *MediaMeta) { m.URL = "https://example.invalid/b" },
		"direct path": func(m *MediaMeta) { m.DirectPath = "/v/b" },
		"media key":   func(m *MediaMeta) { m.MediaKey = bytes.Repeat([]byte{9}, 32) },
		"sha256":      func(m *MediaMeta) { m.FileSHA256 = bytes.Repeat([]byte{8}, 32) },
		"enc sha256":  func(m *MediaMeta) { m.FileEncSHA256 = bytes.Repeat([]byte{7}, 32) },
		"length":      func(m *MediaMeta) { n := int64(99); m.FileLength = &n },
		"mime":        func(m *MediaMeta) { m.MIMEType = "image/png" },
		"file name":   func(m *MediaMeta) { m.FileName = "b.png" },
	}
	for name, set := range setters {
		t.Run(name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			withMedia := func(m MediaMeta, text string) Message {
				return msg(alice, "M", alice, at(0), withText(text), func(x *Message) { x.MediaType = "image"; x.Media = m })
			}
			mustStore(t, st, withMedia(fullMedia(), "first"))
			// A copy without any metadata keeps all eight stored fields and takes the other fields from itself.
			mustStore(t, st, withMedia(MediaMeta{}, "second"))
			r := readRow(t, st, alice, "M")
			assertMedia(t, r, fullMedia())
			if r.text != "second" {
				t.Errorf("text = %q, want the new copy's", r.text)
			}
			// A copy that offers just this field replaces it only.
			want := fullMedia()
			offer := MediaMeta{}
			set(&offer)
			set(&want)
			mustStore(t, st, withMedia(offer, "third"))
			assertMedia(t, readRow(t, st, alice, "M"), want)
		})
	}
}

func TestCaptionAndSenderForms(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st,
		msg(alice, "C", alice, at(0), withText(""), withCaption("a caption")),
		msg(groupG, "D", "491511234567:12@s.whatsapp.net", at(1)),
		msg(groupG, "E", lidAlice, at(2)),
		msg(groupG, "F", "100000000042:5@lid", at(3), fromMe()),
	)
	if r := readRow(t, st, alice, "C"); r.caption != "a caption" || r.text != "" {
		t.Errorf("caption row = %+v", r)
	}
	if r := readRow(t, st, groupG, "D"); r.senderJID != alice || r.senderPhone != "491511234567" {
		t.Errorf("device-part sender = %q / %q", r.senderJID, r.senderPhone)
	}
	if r := readRow(t, st, groupG, "E"); r.senderJID != lidAlice || r.senderPhone != "" {
		t.Errorf("LID sender = %q / %q", r.senderJID, r.senderPhone)
	}
	if r := readRow(t, st, groupG, "F"); r.senderJID != lidAlice || r.fromMe != 1 {
		t.Errorf("own LID sender = %q, from_me %d", r.senderJID, r.fromMe)
	}
}

func TestSearchFoldsAreStored(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(alice, "S", alice, at(0), withText("ÉTÉ Привіт"), withCaption("STRASSE")))
	var tf, cf string
	if err := st.reader.QueryRow("SELECT text_fold, caption_fold FROM messages").Scan(&tf, &cf); err != nil {
		t.Fatal(err)
	}
	if tf != fold("ÉTÉ Привіт") || cf != "strasse" {
		t.Errorf("folds = %q / %q", tf, cf)
	}
}

func TestBatchLimits(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	many := func(n int) []Message {
		out := make([]Message, n)
		for i := range out {
			out[i] = msg(alice, fmt.Sprintf("id%04d", i), alice, at(i))
		}
		return out
	}
	if err := st.StoreMessages(ctx, many(maxBatchMessages)); err != nil {
		t.Fatalf("1,000 messages: %v", err)
	}
	if n := countRows(t, st, "messages"); n != maxBatchMessages {
		t.Fatalf("rows = %d", n)
	}
	other := many(maxBatchMessages + 1)
	for i := range other {
		other[i].ChatJID = bob
	}
	if err := st.StoreMessages(ctx, other); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("1,001 messages: %v, want ErrResourceLimit", err)
	}
	if err := st.StoreMessages(ctx, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty batch: %v, want ErrInvalid", err)
	}
	var chats int
	st.reader.QueryRow("SELECT count(*) FROM chats WHERE jid = ?", bob).Scan(&chats)
	if chats != 0 {
		t.Error("a rejected batch left a chat behind")
	}

	// Exactly 16 MiB of field bytes is accepted and one byte more is not.
	big := func(extra int) []Message {
		var out []Message
		total := 0
		for i := 0; total < maxBatchBytes; i++ {
			m := msg(groupG, fmt.Sprintf("big%d", i), alice, at(i), withText(strings.Repeat("x", maxTextBytes)))
			_, size, _ := m.validate()
			if total+size > maxBatchBytes {
				m.Text = strings.Repeat("x", maxTextBytes-(total+size-maxBatchBytes))
				_, size, _ = m.validate()
			}
			total += size
			out = append(out, m)
		}
		out[len(out)-1].Text += strings.Repeat("y", extra)
		return out
	}
	if err := st.StoreMessages(ctx, big(0)); err != nil {
		t.Errorf("exactly 16 MiB: %v", err)
	}
	before := countRows(t, st, "messages")
	if err := st.StoreMessages(ctx, big(1)); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("16 MiB + 1 byte: %v, want ErrResourceLimit", err)
	}
	if countRows(t, st, "messages") != before {
		t.Error("a rejected batch stored messages")
	}
}

func TestInvalidMessageRejectsTheWholeBatch(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	bad := msg(alice, "bad", alice, at(0))
	bad.Media.MediaKey = []byte("short")
	err := st.StoreMessages(context.Background(), []Message{
		msg(alice, "1", alice, at(0)), msg(bob, "2", bob, at(1)), bad, msg(groupG, "4", alice, at(2)),
	})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "media_key" {
		t.Fatalf("error = %v, want a validation error on media_key", err)
	}
	if countRows(t, st, "messages") != 0 || countRows(t, st, "chats") != 0 {
		t.Error("a rejected batch stored rows")
	}
}

func TestStoreAfterClose(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	st.Close(context.Background())
	if err := st.StoreMessage(context.Background(), msg(alice, "A", alice, at(0))); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
	if err := st.UpdateChat(context.Background(), ChatUpdate{JID: alice}); !errors.Is(err, ErrClosed) {
		t.Errorf("UpdateChat after Close: %v, want ErrClosed", err)
	}
}

func TestMessageTimeIsStoredInMilliseconds(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	when := time.Date(2026, 5, 6, 7, 8, 9, 123_000_000, time.UTC)
	mustStore(t, st, msg(alice, "T", alice, when))
	if got := readRow(t, st, alice, "T").timeMS; got != when.UnixMilli() {
		t.Errorf("time = %d, want %d", got, when.UnixMilli())
	}
}
