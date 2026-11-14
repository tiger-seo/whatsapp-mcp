package wa

import (
	"math"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// extractMessage turns an eligible message event, live or parsed from history, into its stored form. ok is false
// when the message is not eligible: its chat is not a user, group or LID chat, it is an edit or a bare protocol
// message, or it has neither text nor a recognised media type. The result is not validated: the caller checks it
// with store.ValidateMessage and never repairs it. own is the account's phone JID; the sender of an own message
// is always that JID.
func extractMessage(evt *events.Message, own types.JID) (store.Message, bool) {
	if evt == nil || evt.Message == nil || evt.IsEdit {
		return store.Message{}, false
	}
	switch evt.Info.Chat.Server {
	case types.DefaultUserServer, types.GroupServer, types.HiddenUserServer:
	default:
		return store.Message{}, false
	}
	if isProtocolMessage(evt.Message) || isProtocolMessage(evt.RawMessage) {
		return store.Message{}, false
	}
	m := store.Message{
		ChatJID: evt.Info.Chat.ToNonAD().String(),
		ID:      evt.Info.ID,
		Time:    evt.Info.Timestamp,
		FromMe:  evt.Info.IsFromMe,
	}
	if !extractContent(evt.Message, &m) {
		return store.Message{}, false
	}
	switch {
	case evt.Info.IsFromMe:
		if !own.IsEmpty() {
			m.SenderJID = own.ToNonAD().String()
		}
	case !evt.Info.Sender.IsEmpty():
		m.SenderJID = evt.Info.Sender.ToNonAD().String()
	}
	return m, true
}

// isProtocolMessage reports content that is a protocol message, including one the phone forwarded from another
// of the account's devices: edits, revocations, history notifications and the like are never stored as messages.
func isProtocolMessage(msg *waE2E.Message) bool {
	return msg.GetProtocolMessage() != nil || msg.GetDeviceSentMessage().GetMessage().GetProtocolMessage() != nil
}

// extractContent copies text, or a recognised media type with its caption and metadata, into m. It reports whether
// there was any.
func extractContent(msg *waE2E.Message, m *store.Message) bool {
	if t := msg.GetConversation(); t != "" {
		m.Text = t
		return true
	}
	if t := msg.GetExtendedTextMessage().GetText(); t != "" {
		m.Text = t
		return true
	}
	switch {
	case msg.GetImageMessage() != nil:
		x := msg.GetImageMessage()
		m.MediaType, m.Caption = "image", x.GetCaption()
		m.Media = mediaMeta(x.GetURL(), x.GetDirectPath(), x.GetMediaKey(), x.GetFileSHA256(), x.GetFileEncSHA256(), x.FileLength, x.GetMimetype(), "")
	case msg.GetVideoMessage() != nil:
		x := msg.GetVideoMessage()
		m.MediaType, m.Caption = "video", x.GetCaption()
		m.Media = mediaMeta(x.GetURL(), x.GetDirectPath(), x.GetMediaKey(), x.GetFileSHA256(), x.GetFileEncSHA256(), x.FileLength, x.GetMimetype(), "")
	case msg.GetAudioMessage() != nil:
		x := msg.GetAudioMessage()
		m.MediaType = "audio"
		m.Media = mediaMeta(x.GetURL(), x.GetDirectPath(), x.GetMediaKey(), x.GetFileSHA256(), x.GetFileEncSHA256(), x.FileLength, x.GetMimetype(), "")
	case msg.GetDocumentMessage() != nil:
		x := msg.GetDocumentMessage()
		m.MediaType, m.Caption = "document", x.GetCaption()
		m.Media = mediaMeta(x.GetURL(), x.GetDirectPath(), x.GetMediaKey(), x.GetFileSHA256(), x.GetFileEncSHA256(), x.FileLength, x.GetMimetype(), x.GetFileName())
	default:
		return false
	}
	return true
}

func mediaMeta(url, directPath string, key, sha, encSHA []byte, length *uint64, mime, fileName string) store.MediaMeta {
	md := store.MediaMeta{
		URL: url, DirectPath: directPath, MediaKey: key, FileSHA256: sha, FileEncSHA256: encSHA,
		MIMEType: mime, FileName: fileName,
	}
	if length != nil {
		// A length beyond int64 is kept out of range, so the store rejects it instead of storing a wrapped value.
		n := int64(math.MaxInt64)
		if *length <= math.MaxInt64 {
			n = int64(*length)
		}
		md.FileLength = &n
	}
	return md
}
