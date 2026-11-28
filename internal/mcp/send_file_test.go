package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/media"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow"
)

// place writes a file into the outbox and returns its path.
func (r *sendRig) place(name string, content []byte) string {
	r.t.Helper()
	path := filepath.Join(r.outbox, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		r.t.Fatal(err)
	}
	return path
}

func voiceReference(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "media", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sendFile(path string) map[string]any {
	return map[string]any{"recipient": sendPeer, "media_path": path}
}

// workingFiles lists what is left in the working directories.
func (r *sendRig) workingFiles() []string {
	r.t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.data, "send-work"))
	if err != nil && !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestSendFileTypes(t *testing.T) {
	for _, tt := range []struct {
		name, mediaType, mime string
		upload                whatsmeow.MediaType
	}{
		{"photo.jpg", "image", "image/jpeg", whatsmeow.MediaImage},
		{"photo.jpeg", "image", "image/jpeg", whatsmeow.MediaImage},
		{"PHOTO.JPG", "image", "image/jpeg", whatsmeow.MediaImage},
		{"photo.png", "image", "image/png", whatsmeow.MediaImage},
		{"photo.gif", "image", "image/gif", whatsmeow.MediaImage},
		{"photo.WebP", "image", "image/webp", whatsmeow.MediaImage},
		{"clip.mp4", "video", "video/mp4", whatsmeow.MediaVideo},
		{"clip.avi", "video", "video/avi", whatsmeow.MediaVideo},
		{"clip.Mov", "video", "video/quicktime", whatsmeow.MediaVideo},
		{"report.pdf", "document", "application/octet-stream", whatsmeow.MediaDocument},
		{"song.mp3", "document", "application/octet-stream", whatsmeow.MediaDocument},
		{"sound.wav", "document", "application/octet-stream", whatsmeow.MediaDocument},
		{"noextension", "document", "application/octet-stream", whatsmeow.MediaDocument},
		{"--help", "document", "application/octet-stream", whatsmeow.MediaDocument},
		{"résumé final.docx", "document", "application/octet-stream", whatsmeow.MediaDocument},
		{"voice.ogg", "audio", "audio/ogg; codecs=opus", whatsmeow.MediaAudio},
		{"VOICE.OGG", "audio", "audio/ogg; codecs=opus", whatsmeow.MediaAudio},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newSendRig(t)
			content := []byte("synthetic content of " + tt.name)
			voice := tt.mediaType == "audio"
			if voice {
				content = voiceReference(t, "tone-3.4s.ogg")
			}
			args := sendFile(r.place(tt.name, content))
			if !voice {
				args["caption"] = "Holiday"
			}
			reply := call(t, r.client(), "send_file", args)
			if reply.isError || reply.structured["media_type"] != tt.mediaType || reply.structured["voice_note"] != voice || reply.structured["local_record"] != "stored" {
				t.Fatalf("reply = %+v", reply.structured)
			}
			if _, ok := reply.structured["duration_seconds"]; ok != voice {
				t.Errorf("duration_seconds in %v", reply.structured)
			}
			calls := r.port.Calls()
			if len(calls.Uploads) != 1 || len(calls.Deliveries) != 1 || len(calls.Records) != 1 {
				t.Fatalf("calls = %d/%d/%d", len(calls.Uploads), len(calls.Deliveries), len(calls.Records))
			}
			if calls.Uploads[0].Kind != tt.upload || !bytes.Equal(calls.Uploads[0].Data, content) {
				t.Errorf("upload kind %v, %d bytes", calls.Uploads[0].Kind, len(calls.Uploads[0].Data))
			}
			m := calls.Deliveries[0].Message
			type described interface {
				GetMimetype() string
				GetURL() string
				GetDirectPath() string
				GetMediaKey() []byte
				GetFileSHA256() []byte
				GetFileEncSHA256() []byte
				GetFileLength() uint64
			}
			var part described
			caption := ""
			switch {
			case tt.mediaType == "image" && m.ImageMessage != nil:
				part, caption = m.ImageMessage, m.ImageMessage.GetCaption()
			case tt.mediaType == "video" && m.VideoMessage != nil:
				part, caption = m.VideoMessage, m.VideoMessage.GetCaption()
				if len(m.VideoMessage.StreamingSidecar) == 0 {
					t.Error("a video without its streaming sidecar")
				}
			case tt.mediaType == "document" && m.DocumentMessage != nil:
				part, caption = m.DocumentMessage, m.DocumentMessage.GetCaption()
				if m.DocumentMessage.GetTitle() != tt.name || m.DocumentMessage.GetFileName() != tt.name {
					t.Errorf("document titled %q, named %q", m.DocumentMessage.GetTitle(), m.DocumentMessage.GetFileName())
				}
			case voice && m.AudioMessage != nil:
				part = m.AudioMessage
				if !m.AudioMessage.GetPTT() || m.AudioMessage.GetSeconds() != 3 || len(m.AudioMessage.Waveform) != 64 {
					t.Errorf("voice note = %v", m.AudioMessage)
				}
			default:
				t.Fatalf("message = %v", m)
			}
			if part.GetMimetype() != tt.mime || part.GetURL() == "" || part.GetDirectPath() == "" || len(part.GetMediaKey()) != 32 ||
				len(part.GetFileSHA256()) != 32 || len(part.GetFileEncSHA256()) != 32 || part.GetFileLength() != uint64(len(content)) {
				t.Errorf("media = %v", part)
			}
			if want, _ := args["caption"].(string); caption != want {
				t.Errorf("caption = %q, want %q", caption, want)
			}
			if left := r.workingFiles(); len(left) != 0 {
				t.Errorf("working files left: %v", left)
			}
			if got, err := os.ReadFile(args["media_path"].(string)); err != nil || !bytes.Equal(got, content) {
				t.Errorf("the user's file changed: %v", err)
			}
		})
	}
}

func TestSendFileCaption(t *testing.T) {
	r := newSendRig(t)
	client := r.client()
	photo := r.place("photo.jpg", []byte("image"))
	for name, args := range map[string]map[string]any{
		"omitted": sendFile(photo),
		"empty":   {"recipient": sendPeer, "media_path": photo, "caption": ""},
	} {
		if reply := call(t, client, "send_file", args); reply.isError {
			t.Fatalf("%s: %+v", name, reply.structured)
		}
		sent := r.port.Calls().Deliveries
		if sent[len(sent)-1].Message.ImageMessage.Caption != nil {
			t.Errorf("%s caption was sent", name)
		}
	}
	longest := strings.Repeat("c", 65536)
	if reply := call(t, client, "send_file", map[string]any{"recipient": sendPeer, "media_path": photo, "caption": longest}); reply.isError {
		t.Fatalf("the longest caption: %+v", reply.structured)
	}
	voice := r.place("voice.ogg", voiceReference(t, "tone-1s.ogg"))
	if reply := call(t, client, "send_file", map[string]any{"recipient": sendPeer, "media_path": voice, "caption": ""}); reply.isError {
		t.Fatalf("a voice note with an empty caption: %+v", reply.structured)
	}
	sent := len(r.port.Calls().Uploads)
	for name, tt := range map[string]struct {
		args  any
		field string
	}{
		"too long":      {map[string]any{"recipient": sendPeer, "media_path": photo, "caption": longest + "c"}, "caption"},
		"on a voice":    {map[string]any{"recipient": sendPeer, "media_path": voice, "caption": "text"}, "caption"},
		"null":          {json.RawMessage(`{"recipient":"491511000001","media_path":"/a","caption":null}`), "caption"},
		"no path":       {map[string]any{"recipient": sendPeer}, "media_path"},
		"empty path":    {map[string]any{"recipient": sendPeer, "media_path": ""}, "media_path"},
		"long path":     {map[string]any{"recipient": sendPeer, "media_path": photo + strings.Repeat("a", 4096)}, "media_path"},
		"unknown field": {map[string]any{"recipient": sendPeer, "media_path": photo, "message": "x"}, "arguments"},
	} {
		reply := call(t, client, "send_file", tt.args)
		if reply.code() != "invalid_input" || reply.errorField("field") != tt.field {
			t.Errorf("%s: %+v", name, reply.structured)
		}
	}
	if len(r.port.Calls().Uploads) != sent {
		t.Error("a refused call uploaded")
	}
}

func TestSendFileRefusals(t *testing.T) {
	r := newSendRig(t)
	client := r.client()
	outside := filepath.Join(t.TempDir(), "synthetic-secret.pdf")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(r.data, "whatsapp.db")
	reply := call(t, client, "send_file", sendFile(outside))
	if reply.code() != "file_refused" || !strings.Contains(reply.text, "send_allowed_dirs") || strings.Contains(reply.text, "synthetic-secret") {
		t.Errorf("outside = %+v", reply)
	}
	if reply := call(t, client, "send_file", sendFile(database)); reply.code() != "file_refused" || !strings.Contains(reply.text, "send_allowed_dirs") {
		t.Errorf("database = %+v", reply)
	}
	if reply := call(t, client, "send_file", sendFile("photo.jpg")); reply.code() != "file_refused" || !strings.Contains(reply.text, "absolute") {
		t.Errorf("relative = %+v", reply)
	}
	for _, path := range []string{r.outbox, filepath.Join(r.outbox, "missing.jpg"), r.place("empty.jpg", nil)} {
		if reply := call(t, client, "send_file", sendFile(path)); reply.code() != "file_refused" {
			t.Errorf("%s = %+v", filepath.Base(path), reply)
		}
	}
	r.wantNothingSent()
	if left := r.workingFiles(); len(left) != 0 {
		t.Errorf("working files left: %v", left)
	}
	r.port.SetFailure(wa.FakeUpload, os.ErrDeadlineExceeded)
	if reply := call(t, client, "send_file", sendFile(r.place("a.jpg", []byte("x")))); reply.code() != "upload_failed" {
		t.Errorf("upload failure = %+v", reply)
	}
	calls := r.port.Calls()
	if len(calls.Deliveries) != 0 || len(calls.Records) != 0 || len(r.workingFiles()) != 0 {
		t.Errorf("a failed upload was sent: %+v", calls)
	}
}

// growingHistory changes the file between its checks and its copy: the free space is measured in between.
type growingHistory struct {
	media.History
	grow func()
}

func (h growingHistory) FreshFreeBytes() (uint64, error) {
	h.grow()
	return 100 << 30, nil
}

func TestSendFileGrowingPastTheLimit(t *testing.T) {
	var path string
	r := newSendRig(t, func(o *sendOptions) {
		o.history = func(h media.History) media.History {
			return growingHistory{History: h, grow: func() {
				if err := os.Truncate(path, (100<<20)+4096); err != nil {
					t.Error(err)
				}
			}}
		}
	})
	path = r.place("big.bin", []byte("small when opened"))
	reply := call(t, r.client(), "send_file", sendFile(path))
	if reply.code() != "file_refused" || !strings.Contains(reply.text, "changed") {
		t.Errorf("reply = %+v", reply)
	}
	r.wantNothingSent()
	if left := r.workingFiles(); len(left) != 0 {
		t.Errorf("working files left: %v", left)
	}
}
