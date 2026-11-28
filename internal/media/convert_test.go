package media

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

var fakeFFmpegBuild = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "fakeffmpeg")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, executableName("ffmpeg"))
	build := exec.Command("go", "build", "-o", out, "./testdata/fakeffmpeg")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		return nil, errors.New(string(output))
	}
	return os.ReadFile(out)
})

// fakeFFmpeg places the stand-in converter in dir, which is a new temporary directory when dir is "".
func fakeFFmpeg(t *testing.T, dir string) string {
	t.Helper()
	binary, err := fakeFFmpegBuild()
	if err != nil {
		t.Fatalf("building the stand-in converter: %v", err)
	}
	if dir == "" {
		dir = t.TempDir()
	}
	path := filepath.Join(dir, executableName("ffmpeg"))
	if err := os.WriteFile(path, binary, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// conversionDir returns a private working directory whose input tells the stand-in what to do.
func conversionDir(t *testing.T, input string) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(data); err != nil {
		t.Fatal(err)
	}
	dir, err := newWorkDir(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workCopy), []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readPID(dir, role string) int {
	raw, err := os.ReadFile(filepath.Join(dir, "pid-"+role))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(string(raw))
	return pid
}

// wantStopped checks that, within five seconds, none of the processes runs and the working directory can go.
func wantStopped(t *testing.T, dir string, roles ...string) {
	t.Helper()
	pids := map[string]int{}
	for _, role := range roles {
		if pids[role] = readPID(dir, role); pids[role] == 0 {
			t.Fatalf("the %s process never ran", role)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for role, pid := range pids {
		for processAlive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("the %s process %d still runs", role, pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := removeDir(dir); err != nil {
		t.Errorf("the working directory cannot be removed: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the working directory is left: %v", err)
	}
	if time.Now().After(deadline) {
		t.Error("cleanup took longer than five seconds")
	}
}

func TestSendConvertArguments(t *testing.T) {
	c := newConverter(fakeFFmpeg(t, ""))
	t.Setenv("FAKE_SECRET_VARIABLE", "must not reach the converter")
	dir := conversionDir(t, "ok\nconverted bytes")
	if err := c.convert(context.Background(), dir, "mp3"); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, workConverted))
	if err != nil || string(out) != "converted bytes" {
		t.Errorf("output = %q, %v", out, err)
	}
	var report struct{ Args, Env []string }
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil || json.Unmarshal(raw, &report) != nil {
		t.Fatalf("report: %v", err)
	}
	want := strings.Fields("-nostdin -hide_banner -loglevel error -protocol_whitelist file,pipe -f mp3 -i input -map 0:a:0 -vn " +
		"-c:a libopus -b:a 32k -ar 48000 -application voip -vbr on -frame_duration 60 -f ogg pipe:1")
	if !slices.Equal(report.Args, want) {
		t.Errorf("arguments = %q", report.Args)
	}
	for _, variable := range report.Env {
		name, _, _ := strings.Cut(variable, "=")
		if runtime.GOOS == "windows" && (name == "" || strings.EqualFold(name, "SystemRoot")) {
			continue
		}
		t.Errorf("the converter received the variable %q", name)
	}
}

func TestSendConvertDemuxers(t *testing.T) {
	want := map[string]string{"ogg": "ogg", "opus": "ogg", "mp3": "mp3", "wav": "wav", "m4a": "mov", "aac": "aac", "flac": "flac", "amr": "amr"}
	if len(demuxers) != len(want) {
		t.Errorf("demuxers = %v", demuxers)
	}
	for ext, demuxer := range want {
		if demuxers[ext] != demuxer {
			t.Errorf("demuxer of %s = %q, want %q", ext, demuxers[ext], demuxer)
		}
	}
	if c := newConverter(""); c.timeLimit != time.Minute || c.sizeLimit != maxFileSize || cap(c.slot) != 1 {
		t.Errorf("limits = %v, %d, %d", c.timeLimit, c.sizeLimit, cap(c.slot))
	}
}

func TestSendConvertFailure(t *testing.T) {
	c := newConverter(fakeFFmpeg(t, ""))
	dir := conversionDir(t, "fail\n")
	if err := c.convert(context.Background(), dir, "mp3"); !errors.Is(err, errConversion) {
		t.Errorf("error = %v", err)
	}
	wantStopped(t, dir, "main")
}

// Each limit stops the converter and, behind a launcher that does not pass a kill on, the program it started.
func TestSendConvertLimitsStopTheTree(t *testing.T) {
	program := fakeFFmpeg(t, "")
	for _, launcher := range []string{"", "launch-"} {
		roles := []string{"main"}
		if launcher != "" {
			roles = append(roles, "child")
		}
		t.Run(launcher+"time limit", func(t *testing.T) {
			c := newConverter(program)
			c.timeLimit = 2 * time.Second
			dir := conversionDir(t, launcher+"stall\n")
			start := time.Now()
			err := c.convert(context.Background(), dir, "mp3")
			if took := time.Since(start); !errors.Is(err, errConversion) || !errors.Is(err, context.DeadlineExceeded) || took < 1900*time.Millisecond || took > 6*time.Second {
				t.Errorf("error = %v after %v", err, took)
			}
			wantStopped(t, dir, roles...)
		})
		t.Run(launcher+"output limit", func(t *testing.T) {
			c := newConverter(program)
			c.sizeLimit = 16 << 10
			dir := conversionDir(t, launcher+"flood\n")
			err := c.convert(context.Background(), dir, "mp3")
			if !errors.Is(err, errConversion) || !errors.Is(err, errOutputSize) {
				t.Errorf("error = %v", err)
			}
			if info, err := os.Stat(filepath.Join(dir, workConverted)); err != nil || info.Size() > 16<<10 {
				t.Errorf("output = %v, %v", info, err)
			}
			wantStopped(t, dir, roles...)
		})
		t.Run(launcher+"cancellation", func(t *testing.T) {
			c := newConverter(program)
			dir := conversionDir(t, launcher+"stall\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.convert(ctx, dir, "mp3") }()
			waitFor(t, "the converter", func() bool { return readPID(dir, roles[len(roles)-1]) != 0 })
			start := time.Now()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, errConversion) || !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
					t.Errorf("error = %v after %v", err, time.Since(start))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the cancelled conversion did not return")
			}
			wantStopped(t, dir, roles...)
		})
	}
	t.Run("a child left behind", func(t *testing.T) {
		c := newConverter(program)
		dir := conversionDir(t, "linger\noutput")
		// The converter itself succeeds; what it left running is stopped all the same.
		err := c.convert(context.Background(), dir, "mp3")
		if err != nil && !errors.Is(err, errConversion) {
			t.Errorf("error = %v", err)
		}
		wantStopped(t, dir, "main", "child")
	})
}

func TestSendConvertOneAtATime(t *testing.T) {
	c := newConverter(fakeFFmpeg(t, ""))
	first := conversionDir(t, "stall\n")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.convert(ctx, first, "mp3") }()
	waitFor(t, "the first converter", func() bool { return readPID(first, "main") != 0 })
	second := conversionDir(t, "ok\nx")
	short, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	if err := c.convert(short, second, "mp3"); !errors.Is(err, errConversion) {
		t.Errorf("second conversion = %v", err)
	}
	if readPID(second, "main") != 0 {
		t.Error("a second converter started while the first ran")
	}
	cancel()
	<-done
	if err := c.convert(context.Background(), second, "mp3"); err != nil {
		t.Errorf("after the first ended: %v", err)
	}
	wantStopped(t, first, "main")
}

func TestSendConvertLookup(t *testing.T) {
	empty := t.TempDir()
	run := func(c *converter) error {
		return c.convert(context.Background(), conversionDir(t, "ok\nx"), "mp3")
	}
	t.Run("not found", func(t *testing.T) {
		t.Setenv("PATH", empty)
		if err := run(newConverter("")); !errors.Is(err, errConverterMissing) {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("configured path that has gone", func(t *testing.T) {
		if err := run(newConverter(filepath.Join(empty, executableName("ffmpeg")))); !errors.Is(err, errConverterMissing) {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("on PATH", func(t *testing.T) {
		program := fakeFFmpeg(t, "")
		t.Setenv("PATH", filepath.Dir(program))
		if err := run(newConverter("")); err != nil {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("in the current directory", func(t *testing.T) {
		current := t.TempDir()
		fakeFFmpeg(t, current)
		t.Chdir(current)
		t.Setenv("PATH", empty)
		if err := run(newConverter("")); !errors.Is(err, errConverterMissing) {
			t.Errorf("a converter in the current directory = %v", err)
		}
	})
	t.Run("through a relative PATH entry", func(t *testing.T) {
		current := t.TempDir()
		if err := os.Mkdir(filepath.Join(current, "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		fakeFFmpeg(t, filepath.Join(current, "bin"))
		t.Chdir(current)
		for _, entry := range []string{"bin", ".", "." + string(filepath.Separator) + "bin"} {
			t.Setenv("PATH", entry)
			if err := run(newConverter("")); !errors.Is(err, errConverterMissing) {
				t.Errorf("PATH=%s: %v", entry, err)
			}
		}
	})
	t.Run("a relative configured path", func(t *testing.T) {
		current := t.TempDir()
		fakeFFmpeg(t, current)
		t.Chdir(current)
		if err := run(newConverter(executableName("ffmpeg"))); !errors.Is(err, errConverterMissing) {
			t.Errorf("error = %v", err)
		}
	})
}

// The service's conversion: its errors, and that no working directory or process is left.
func TestSendAudioConversionThroughTheService(t *testing.T) {
	program := fakeFFmpeg(t, "")
	voice := string(reference(t, "tone-3.4s.ogg"))
	t.Run("converted", func(t *testing.T) {
		r := newSendRig(t, func(o *Options) { o.FFmpeg = program })
		res, err := r.send(r.file(ToolAudio, "a.mp3", []byte("ok\n"+voice)))
		if err != nil || !res.VoiceNote || res.Seconds != 3 || res.MediaType != typeAudio {
			t.Fatalf("result = %+v, %v", res, err)
		}
		calls := r.port.Calls()
		if len(calls.Uploads) != 1 || string(calls.Uploads[0].Data) != voice {
			t.Error("the converted audio was not uploaded")
		}
		if dirs := r.workDirs(); len(dirs) != 0 {
			t.Errorf("working directories left: %v", dirs)
		}
	})
	for name, content := range map[string]string{
		"converter error": "fail\n", "output is not Opus": "ok\nplain bytes", "no output": "ok\n",
		"damaged output": "ok\n" + voice[:len(voice)-7],
	} {
		t.Run(name, func(t *testing.T) {
			r := newSendRig(t, func(o *Options) { o.FFmpeg = program })
			_, err := r.send(r.file(ToolAudio, "a.mp3", []byte(content)))
			wantCode(t, err, CodeConversionFailed)
			r.wantNothingSent()
		})
	}
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		r := newSendRig(t)
		_, err := r.send(r.file(ToolAudio, "a.mp3", []byte("x")))
		wantCode(t, err, CodeFFmpegMissing)
		if !strings.Contains(err.Message, "ffmpeg setting") {
			t.Errorf("message = %q", err.Message)
		}
		r.wantNothingSent()
	})
	// A cancellation, which the session's stop causes too, ends the converter and removes what it wrote.
	t.Run("cancelled", func(t *testing.T) {
		r := newSendRig(t, func(o *Options) { o.FFmpeg = program })
		req := r.file(ToolAudio, "a.mp3", []byte("launch-stall\n"))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan *Error, 1)
		go func() {
			_, err := r.svc.Send(ctx, req)
			done <- err
		}()
		var pid int
		waitFor(t, "the converter", func() bool {
			for _, dir := range r.workDirs() {
				pid = readPID(filepath.Join(r.data, workDirName, dir), "child")
			}
			return pid != 0
		})
		start := time.Now()
		cancel()
		select {
		case err := <-done:
			wantCode(t, err, CodeCancelled)
		case <-time.After(10 * time.Second):
			t.Fatal("the cancelled send did not return")
		}
		if processAlive(pid) || len(r.workDirs()) != 0 || time.Since(start) > 5*time.Second {
			t.Errorf("after %v: process alive %v, directories %v", time.Since(start), processAlive(pid), r.workDirs())
		}
		r.wantNothingSent()
	})
}

// TestRealFFmpeg runs the installed ffmpeg when WA_FFMPEG_TESTS=1.
func TestRealFFmpeg(t *testing.T) {
	if os.Getenv("WA_FFMPEG_TESTS") != "1" {
		t.Skip("set WA_FFMPEG_TESTS=1 to run the installed ffmpeg")
	}
	program, err := newConverter("").program()
	if err != nil {
		t.Fatalf("no ffmpeg on PATH: %v", err)
	}
	version, err := exec.Command(program, "-version").Output()
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(version), "\n")
	t.Logf("%s (%s/%s)", strings.TrimSpace(first), runtime.GOOS, runtime.GOARCH)
	const seconds = 12
	sources := t.TempDir()
	for _, input := range []struct {
		name, ext string
		encode    []string
	}{
		{"mp3", "mp3", []string{"-c:a", "libmp3lame"}},
		{"wav", "wav", []string{"-c:a", "pcm_s16le"}},
		{"m4a with its index at the end", "m4a", []string{"-c:a", "aac"}},
		{"aac", "aac", []string{"-c:a", "aac", "-f", "adts"}},
		{"flac", "flac", []string{"-c:a", "flac"}},
		{"opus", "opus", []string{"-c:a", "libopus", "-f", "ogg"}},
		{"vorbis", "ogg", []string{"-c:a", "libvorbis"}},
		{"amr", "amr", []string{"-c:a", "libopencore_amrnb", "-ar", "8000", "-ac", "1"}},
	} {
		t.Run(input.name, func(t *testing.T) {
			source := filepath.Join(sources, "source."+input.ext)
			args := append([]string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
				"sine=frequency=440:sample_rate=44100:duration=" + strconv.Itoa(seconds)}, input.encode...)
			if out, err := exec.Command(program, append(args, source)...).CombinedOutput(); err != nil {
				t.Skipf("not verified: this build cannot create the input: %s", strings.TrimSpace(string(out)))
			}
			content, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			dir := conversionDir(t, string(content))
			if err := newConverter(program).convert(context.Background(), dir, demuxers[input.ext]); err != nil {
				t.Fatal(err)
			}
			f, err := openWorkFile(dir, workConverted)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := opusDuration(context.Background(), f, maxFileSize)
			if err != nil || got < seconds-1 || got > seconds+1 {
				t.Errorf("duration = %d, %v; want %d", got, err, seconds)
			}
		})
	}
	// A playlist or a concat script under an audio name is not read as one.
	secret := filepath.Join(sources, "secret.wav")
	if out, err := exec.Command(program, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=1", secret).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	url := "file:" + filepath.ToSlash(secret)
	for _, disguised := range []struct{ name, ext, content string }{
		{"hls as mp3", "mp3", "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n" + url + "\n#EXT-X-ENDLIST\n"},
		{"concat as mp3", "mp3", "ffconcat version 1.0\nfile '" + url + "'\n"},
		{"hls as m4a", "m4a", "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n" + url + "\n#EXT-X-ENDLIST\n"},
		{"network playlist as wav", "wav", "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nhttp://127.0.0.1:1/a.wav\n#EXT-X-ENDLIST\n"},
	} {
		t.Run(disguised.name, func(t *testing.T) {
			dir := conversionDir(t, disguised.content)
			if err := newConverter(program).convert(context.Background(), dir, demuxers[disguised.ext]); !errors.Is(err, errConversion) {
				t.Errorf("error = %v", err)
			}
			if info, err := os.Stat(filepath.Join(dir, workConverted)); err == nil && info.Size() > 0 {
				t.Errorf("%d bytes of output", info.Size())
			}
		})
	}
}
