package wa

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var extractTime = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// liveEvent is a live message from the peer in a direct chat carrying msg, unwrapped as the library does.
func liveEvent(id string, msg *waE2E.Message) *events.Message {
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: peerJID(), Sender: peerJID()},
			ID:            id, Timestamp: extractTime,
		},
		RawMessage: msg,
	}
	return evt.UnwrapRaw()
}

func key32(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func imageMessage(caption string) *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String(caption), URL: proto.String("https://mmg.example/i"), DirectPath: proto.String("/v/i"),
		MediaKey: key32(1), FileSHA256: key32(2), FileEncSHA256: key32(3), FileLength: proto.Uint64(1234),
		Mimetype: proto.String("image/jpeg"),
	}}
}

func TestIngestExtractText(t *testing.T) {
	for name, msg := range map[string]*waE2E.Message{
		"conversation":  {Conversation: proto.String("hello")},
		"extended text": {ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("hello")}},
	} {
		t.Run(name, func(t *testing.T) {
			m, ok := extractMessage(liveEvent("T1", msg), ownJID())
			if !ok {
				t.Fatal("a text message was not eligible")
			}
			if m.Text != "hello" || m.Caption != "" || m.MediaType != "" {
				t.Fatalf("text %q caption %q media %q", m.Text, m.Caption, m.MediaType)
			}
			if m.ChatJID != peerJID().String() || m.ID != "T1" || !m.Time.Equal(extractTime) || m.FromMe {
				t.Fatalf("identity fields: %+v", m)
			}
		})
	}
}

func TestIngestExtractMediaKeepsTheCaptionApart(t *testing.T) {
	cases := []struct {
		name, mediaType, caption, fileName string
		msg                                *waE2E.Message
	}{
		{"image", "image", "a caption", "", imageMessage("a caption")},
		{"video", "video", "clip", "", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption: proto.String("clip"), URL: proto.String("https://mmg.example/v"), DirectPath: proto.String("/v/v"),
			MediaKey: key32(1), FileSHA256: key32(2), FileEncSHA256: key32(3), FileLength: proto.Uint64(1234),
			Mimetype: proto.String("video/mp4"),
		}}},
		{"document", "document", "the report", "report.pdf", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Caption: proto.String("the report"), URL: proto.String("https://mmg.example/d"), DirectPath: proto.String("/v/d"),
			MediaKey: key32(1), FileSHA256: key32(2), FileEncSHA256: key32(3), FileLength: proto.Uint64(1234),
			Mimetype: proto.String("application/pdf"), FileName: proto.String("report.pdf"),
		}}},
		{"audio", "audio", "", "", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: proto.String("https://mmg.example/a"), DirectPath: proto.String("/v/a"),
			MediaKey: key32(1), FileSHA256: key32(2), FileEncSHA256: key32(3), FileLength: proto.Uint64(1234),
			Mimetype: proto.String("audio/ogg"),
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ok := extractMessage(liveEvent("M1", c.msg), ownJID())
			if !ok {
				t.Fatal("not eligible")
			}
			if m.MediaType != c.mediaType || m.Caption != c.caption || m.Text != "" {
				t.Fatalf("media %q caption %q text %q", m.MediaType, m.Caption, m.Text)
			}
			md := m.Media
			if md.URL == "" || md.DirectPath == "" || !bytes.Equal(md.MediaKey, key32(1)) ||
				!bytes.Equal(md.FileSHA256, key32(2)) || !bytes.Equal(md.FileEncSHA256, key32(3)) ||
				md.FileLength == nil || *md.FileLength != 1234 || md.MIMEType == "" || md.FileName != c.fileName {
				t.Fatalf("media metadata not copied: %+v", md)
			}
			if err := store.ValidateMessage(m); err != nil {
				t.Fatalf("extracted message invalid: %v", err)
			}
		})
	}
}

func TestIngestExtractUnwrapsWrappers(t *testing.T) {
	inner := imageMessage("wrapped")
	cases := map[string]*waE2E.Message{
		"view once":  {ViewOnceMessage: &waE2E.FutureProofMessage{Message: inner}},
		"view once2": {ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: inner}},
		"ephemeral":  {EphemeralMessage: &waE2E.FutureProofMessage{Message: inner}},
		"document with caption": {DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("wrapped"), FileName: proto.String("f.txt"), Mimetype: proto.String("text/plain")},
		}}},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			m, ok := extractMessage(liveEvent("W1", msg), ownJID())
			if !ok {
				t.Fatal("a wrapped message was not eligible")
			}
			if m.Caption != "wrapped" || m.MediaType == "" {
				t.Fatalf("not stored as its inner type: %+v", m)
			}
		})
	}
}

// ignoredEvents are every event ingest must skip without a write.
func ignoredEvents() map[string]*events.Message {
	out := map[string]*events.Message{}
	edit := liveEvent("E1", &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			EditedMessage: &waE2E.Message{Conversation: proto.String("edited")},
		},
	}}})
	out["edited (IsEdit)"] = edit
	flagged := liveEvent("E2", &waE2E.Message{Conversation: proto.String("edited text")})
	flagged.IsEdit = true
	out["IsEdit flag"] = flagged
	out["bare edit"] = liveEvent("E3", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: &waE2E.Message{Conversation: proto.String("x")},
	}})
	out["revoke"] = liveEvent("E4", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum()}})
	out["sticker"] = liveEvent("S1", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{URL: proto.String("u")}})
	out["reaction"] = liveEvent("R1", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}})
	out["location"] = liveEvent("L1", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(1)}})
	out["poll"] = liveEvent("P1", &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("q")}})
	out["empty"] = liveEvent("X1", &waE2E.Message{})
	out["empty text"] = liveEvent("X2", &waE2E.Message{Conversation: proto.String("")})
	out["nil message"] = &events.Message{Info: types.MessageInfo{ID: "X3", Timestamp: extractTime,
		MessageSource: types.MessageSource{Chat: peerJID(), Sender: peerJID()}}}
	status := liveEvent("B1", &waE2E.Message{Conversation: proto.String("status")})
	status.Info.Chat = types.StatusBroadcastJID
	out["status broadcast"] = status
	news := liveEvent("N1", &waE2E.Message{Conversation: proto.String("news")})
	news.Info.Chat = types.NewJID("120363000000000009", types.NewsletterServer)
	news.Info.Sender = news.Info.Chat
	out["newsletter"] = news
	return out
}

func TestIngestIgnoresUnsupportedMessages(t *testing.T) {
	for name, evt := range ignoredEvents() {
		t.Run(name, func(t *testing.T) {
			if _, ok := extractMessage(evt, ownJID()); ok {
				t.Fatal("eligible; want ignored")
			}
			r := newIngestRig(t)
			if !r.in.HandleMessage(evt) {
				t.Fatal("an ignored message returned false; it would never be acknowledged")
			}
			c := r.in.Counters()
			if c.Ignored != 1 || c.Stored != 0 || c.Rejected != 0 {
				t.Fatalf("counters %+v, want one ignored", c)
			}
			assertNoMessages(t, r.st)
		})
	}
}

// rejectedEvents are eligible messages whose fields the store refuses; none is repaired.
func rejectedEvents() map[string]*events.Message {
	out := map[string]*events.Message{}
	text := func(id string) *events.Message {
		return liveEvent(id, &waE2E.Message{Conversation: proto.String("text")})
	}
	zero := text("Z1")
	zero.Info.Timestamp = time.Time{}
	out["zero time"] = zero
	early := text("Z2")
	early.Info.Timestamp = time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC)
	out["before 2000"] = early
	late := text("Z3")
	late.Info.Timestamp = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	out["from 2100"] = late
	out["empty ID"] = text("")
	out["ID over 256 bytes"] = text(strings.Repeat("i", 257))
	noSender := text("Z4")
	noSender.Info.Sender = types.JID{}
	out["absent sender"] = noSender
	groupSender := liveEvent("Z5", &waE2E.Message{Conversation: proto.String("text")})
	groupSender.Info.Chat = groupJID()
	groupSender.Info.IsGroup = true
	groupSender.Info.Sender = groupJID()
	out["sender not a user"] = groupSender
	shortKey := imageMessage("c")
	shortKey.ImageMessage.MediaKey = key32(1)[:31]
	out["31-byte media key"] = liveEvent("Z6", shortKey)
	longHash := imageMessage("c")
	longHash.ImageMessage.FileSHA256 = append(key32(2), 0)
	out["33-byte hash"] = liveEvent("Z7", longHash)
	out["text over 256 KiB"] = liveEvent("Z8", &waE2E.Message{Conversation: proto.String(strings.Repeat("x", 256<<10+1))})
	return out
}

func TestIngestRejectsInvalidMessagesWithoutRepair(t *testing.T) {
	for name, evt := range rejectedEvents() {
		t.Run(name, func(t *testing.T) {
			if m, ok := extractMessage(evt, ownJID()); ok && store.ValidateMessage(m) == nil {
				t.Fatalf("a message with an invalid field was valid after extraction: %+v", m)
			}
			r := newIngestRig(t)
			if !r.in.HandleMessage(evt) {
				t.Fatal("a rejected message returned false")
			}
			c := r.in.Counters()
			if c.Rejected != 1 || c.Stored != 0 || c.Ignored != 0 {
				t.Fatalf("counters %+v, want one rejected", c)
			}
			assertNoMessages(t, r.st)
			if n := len(r.liveMessages()); n != 0 {
				t.Fatalf("Live called %d times for a rejected message", n)
			}
		})
	}
}

// assertNoMessages fails when the store holds any message.
func assertNoMessages(t *testing.T, st *store.Store) {
	t.Helper()
	hits, err := st.ListMessages(context.Background(), store.MessageFilter{}, store.Context{}, store.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("%d messages written, want none", len(hits))
	}
}
