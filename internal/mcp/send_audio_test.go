package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// standInConverter builds the stand-in converter, whose input says what it does.
func standInConverter(t *testing.T) string {
	t.Helper()
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-o", out, "../media/testdata/fakeffmpeg")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the stand-in converter: %v\n%s", err, output)
	}
	return out
}

func TestSendAudioMessage(t *testing.T) {
	program := standInConverter(t)
	opus := voiceReference(t, "tone-3.4s.ogg")
	converted := voiceReference(t, "tone-12s.ogg")
	input := append([]byte("ok\n"), converted...)
	for _, tt := range []struct {
		name    string
		content []byte
		seconds float64
	}{
		// Ogg Opus goes out as it is, whatever allowed extension it has.
		{"voice.ogg", opus, 3},
		{"voice.opus", opus, 3},
		{"VOICE.OGG", opus, 3},
		{"misnamed.mp3", opus, 3},
		// Everything else is converted.
		{"song.mp3", input, 12}, {"song.WAV", input, 12}, {"song.m4a", input, 12}, {"song.aac", input, 12},
		{"song.flac", input, 12}, {"song.amr", input, 12}, {"vorbis.ogg", input, 12}, {"other.opus", input, 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newSendRig(t, func(o *sendOptions) { o.ffmpeg = program })
			reply := call(t, r.client(), "send_audio_message", sendFile(r.place(tt.name, tt.content)))
			if reply.isError || reply.structured["voice_note"] != true || reply.structured["media_type"] != "audio" ||
				reply.structured["duration_seconds"] != tt.seconds {
				t.Fatalf("reply = %+v", reply.structured)
			}
			if !strings.Contains(reply.text, "voice note") {
				t.Errorf("text = %q", reply.text)
			}
			calls := r.port.Calls()
			if len(calls.Uploads) != 1 || len(calls.Deliveries) != 1 {
				t.Fatalf("calls = %+v", calls)
			}
			want := converted
			if tt.seconds == 3 {
				want = opus
			}
			if string(calls.Uploads[0].Data) != string(want) {
				t.Errorf("uploaded %d bytes, want %d", len(calls.Uploads[0].Data), len(want))
			}
			audio := calls.Deliveries[0].Message.GetAudioMessage()
			if !audio.GetPTT() || audio.GetMimetype() != "audio/ogg; codecs=opus" || float64(audio.GetSeconds()) != tt.seconds || len(audio.GetWaveform()) != 64 {
				t.Errorf("message = %v", audio)
			}
			if left := r.workingFiles(); len(left) != 0 {
				t.Errorf("working files left: %v", left)
			}
		})
	}
}

func TestSendAudioRefusals(t *testing.T) {
	program := standInConverter(t)
	r := newSendRig(t, func(o *sendOptions) { o.ffmpeg = program })
	client := r.client()
	for _, name := range []string{"video.mp4", "document.pdf", "noextension", "image.jpg", "audio.mp3.txt", "voice.oga"} {
		reply := call(t, client, "send_audio_message", sendFile(r.place(name, []byte("x"))))
		if reply.code() != "invalid_input" || reply.errorField("field") != "media_path" {
			t.Errorf("%s = %+v", name, reply.structured)
		}
	}
	reply := call(t, client, "send_audio_message", map[string]any{"recipient": sendPeer, "media_path": r.place("a.mp3", []byte("x")), "caption": "y"})
	if reply.code() != "invalid_input" || reply.errorField("field") != "arguments" {
		t.Errorf("a caption = %+v", reply.structured)
	}
	opus := voiceReference(t, "tone-3.4s.ogg")
	for name, tt := range map[string]struct {
		tool, file string
		content    []byte
		code       string
	}{
		"damaged Ogg Opus":               {"send_audio_message", "cut.ogg", opus[:len(opus)-9], "invalid_audio"},
		"a header cut short":             {"send_audio_message", "short.ogg", opus[:20], "invalid_audio"},
		"converter failure":              {"send_audio_message", "bad.mp3", []byte("fail\n"), "conversion_failed"},
		"send_file with Vorbis":          {"send_file", "vorbis.ogg", voiceReference(t, "vorbis-2s.ogg"), "invalid_audio"},
		"send_file with text":            {"send_file", "text.ogg", []byte("ok\nnot converted by send_file"), "invalid_audio"},
		"send_file with damaged content": {"send_file", "cut2.ogg", opus[:len(opus)-9], "invalid_audio"},
	} {
		reply := call(t, client, tt.tool, sendFile(r.place(tt.file, tt.content)))
		if reply.code() != tt.code {
			t.Errorf("%s = %+v", name, reply.structured)
		}
	}
	r.wantNothingSent()
	if left := r.workingFiles(); len(left) != 0 {
		t.Errorf("working files left: %v", left)
	}
}

func TestSendAudioFFmpegMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := newSendRig(t)
	client := r.client()
	reply := call(t, client, "send_audio_message", sendFile(r.place("song.mp3", []byte("x"))))
	if reply.code() != "ffmpeg_missing" || !strings.Contains(reply.text, "ffmpeg setting") {
		t.Errorf("reply = %+v", reply.structured)
	}
	r.wantNothingSent()
	// Ogg Opus needs no converter.
	if reply := call(t, client, "send_audio_message", sendFile(r.place("voice.ogg", voiceReference(t, "tone-1s.ogg")))); reply.isError {
		t.Errorf("Ogg Opus without a converter = %+v", reply.structured)
	}
}
