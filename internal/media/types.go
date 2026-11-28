package media

import (
	"math"
	"math/rand"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// The media types of a sent message, as they are stored and reported. A text message has none.
const (
	typeImage    = "image"
	typeVideo    = "video"
	typeAudio    = "audio"
	typeDocument = "document"
)

const voiceMIME = "audio/ogg; codecs=opus"

// fileType is how a file is sent.
type fileType struct {
	kind   string
	mime   string
	upload whatsmeow.MediaType
	// voice is a voice note, which must be Ogg Opus and carries no caption.
	voice bool
}

var (
	voiceType    = fileType{kind: typeAudio, mime: voiceMIME, upload: whatsmeow.MediaAudio, voice: true}
	documentType = fileType{kind: typeDocument, mime: "application/octet-stream", upload: whatsmeow.MediaDocument}
)

// fileTypes is the type of a file by its extension; every other file is a document.
var fileTypes = map[string]fileType{
	"jpg":  {kind: typeImage, mime: "image/jpeg", upload: whatsmeow.MediaImage},
	"jpeg": {kind: typeImage, mime: "image/jpeg", upload: whatsmeow.MediaImage},
	"png":  {kind: typeImage, mime: "image/png", upload: whatsmeow.MediaImage},
	"gif":  {kind: typeImage, mime: "image/gif", upload: whatsmeow.MediaImage},
	"webp": {kind: typeImage, mime: "image/webp", upload: whatsmeow.MediaImage},
	"ogg":  voiceType,
	"mp4":  {kind: typeVideo, mime: "video/mp4", upload: whatsmeow.MediaVideo},
	"avi":  {kind: typeVideo, mime: "video/avi", upload: whatsmeow.MediaVideo},
	"mov":  {kind: typeVideo, mime: "video/quicktime", upload: whatsmeow.MediaVideo},
}

// demuxers names, for each extension send_audio_message accepts, the one input format the converter may read.
var demuxers = map[string]string{
	"ogg": "ogg", "opus": "ogg", "mp3": "mp3", "wav": "wav", "m4a": "mov", "aac": "aac", "flac": "flac", "amr": "amr",
}

// extension is a path's extension in lower case, without the dot.
func extension(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}

func typeOfFile(path string) fileType {
	if t, ok := fileTypes[extension(path)]; ok {
		return t
	}
	return documentType
}

// mediaMessage builds the message for an uploaded file. name is the file's base name, which titles a document.
func mediaMessage(t fileType, up whatsmeow.UploadResponse, name, caption string, seconds int) *waE2E.Message {
	var text *string
	if caption != "" {
		text = proto.String(caption)
	}
	url, path, length := proto.String(up.URL), proto.String(up.DirectPath), proto.Uint64(up.FileLength)
	mime := proto.String(t.mime)
	switch t.kind {
	case typeImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: url, DirectPath: path, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: length, Mimetype: mime, Caption: text,
		}}
	case typeVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: url, DirectPath: path, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: length, Mimetype: mime, Caption: text, StreamingSidecar: up.StreamingSidecar,
		}}
	case typeAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: url, DirectPath: path, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: length, Mimetype: mime, PTT: proto.Bool(true), Seconds: proto.Uint32(uint32(seconds)),
			Waveform: placeholderWaveform(seconds),
		}}
	}
	return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
		URL: url, DirectPath: path, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
		FileLength: length, Mimetype: mime, Title: proto.String(name), FileName: proto.String(name), Caption: text,
	}}
}

// placeholderWaveform is the 64-byte waveform of a voice note. It is made up from the duration and says nothing
// about the audio.
func placeholderWaveform(seconds int) []byte {
	const length = 64
	const amplitude = 35.0
	waveform := make([]byte, length)
	rng := rand.New(rand.NewSource(int64(seconds)))
	frequency := float64(min(seconds, 120)) / 30.0
	for i := range waveform {
		pos := float64(i) / length
		value := amplitude * math.Sin(pos*math.Pi*frequency*8)
		value += (amplitude / 2) * math.Sin(pos*math.Pi*frequency*16)
		value += (rng.Float64() - 0.5) * 15
		value *= 0.7 + 0.3*math.Sin(pos*math.Pi)
		waveform[i] = byte(max(0, min(100, value+50)))
	}
	return waveform
}
