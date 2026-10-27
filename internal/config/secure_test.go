package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// exposeDir gives other users access to an existing directory: mode 0755 on Unix, read access for
// BUILTIN\Users on Windows.
func exposeDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	cmd := exec.Command("icacls")
	cmd.SysProcAttr = windowsCmdLine(`icacls "` + dir + `" /grant *S-1-5-32-545:(OI)(CI)R`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v\n%s", err, out)
	}
}

func TestPrepareDataDirRefusesOpenDir(t *testing.T) {
	dir := privateDir(t)
	exposeDir(t, dir)
	err := PrepareDataDir(dir)
	var de *DataDirError
	if !errors.As(err, &de) || de.Dir != dir || !strings.Contains(err.Error(), "other users can access it") {
		t.Fatalf("error = %v, want a DataDirError saying other users can access it", err)
	}
	if want := "data directory " + dir + ": "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q", err, want)
	}
	if !strings.Contains(err.Error(), "; run: ") {
		t.Errorf("error = %q, want the command that fixes it", err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v, want 0755 unchanged", fi.Mode().Perm())
		}
	}
}

func TestLoadDoesNotTrustOpenDir(t *testing.T) {
	fx := newFixture(t, "log_level = \"debug\"\nunknown = 1\n")
	exposeDir(t, fx.dataDir)
	_, _, err := fx.load(nil)
	var de *DataDirError
	if !errors.As(err, &de) || !strings.Contains(err.Error(), "other users can access it") {
		t.Fatalf("error = %v, want a DataDirError; config.toml must not be read", err)
	}
	// Invalid settings are still reported first, as usage errors.
	_, _, err = fx.load(map[string]string{"log-level": "verbose"})
	if errs := flatten(err); len(errs) != 1 || !isConfigError(errs[0]) || !strings.Contains(errs[0].Error(), "--log-level") {
		t.Errorf("errors = %v, want only the --log-level error", errs)
	}
}

func TestLoadDataDirIsAFile(t *testing.T) {
	path := filepath.Join(privateDir(t), "afile")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fx := fixture{dataDir: path, userDir: t.TempDir()}
	_, _, err := fx.load(nil)
	var de *DataDirError
	if want := "data directory " + path + ": exists and is not a directory"; !errors.As(err, &de) || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

func TestFileSizeLimit(t *testing.T) {
	line := "log_level = \"warn\"\n"
	exact := line + "#" + strings.Repeat("x", MaxFileSize-len(line)-2) + "\n"
	fx := newFixture(t, exact)
	if cfg, _ := mustLoad(t, fx, nil); cfg.LogLevel != slog.LevelWarn {
		t.Errorf("at the limit: LogLevel = %v, want warn", cfg.LogLevel)
	}
	fx = newFixture(t, exact+"#"+sentinel)
	_, _, err := fx.load(nil)
	if want := fx.configPath() + ": is larger than 1 MiB"; err == nil || err.Error() != want {
		t.Errorf("one byte over: error = %v, want %s", err, want)
	}
}

func TestFileNested(t *testing.T) {
	for _, toml := range []string{
		"log_level = " + strings.Repeat("[", MaxFileSize-20),
		"log_level = " + strings.Repeat("{a=", (MaxFileSize-20)/3),
		strings.Repeat("a.", (MaxFileSize-10)/2) + "b = 1",
	} {
		start := time.Now()
		_, errs := fileErrors(t, toml)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%.20s…: took %v", toml, elapsed)
		}
		if len(errs) != 1 {
			t.Errorf("%.20s…: errors = %v, want one", toml, errs)
		}
	}
}

func TestFileNotRegular(t *testing.T) {
	fx := fixture{dataDir: privateDir(t), userDir: t.TempDir()}
	if err := os.Mkdir(fx.configPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, err := fx.load(nil)
	if want := fx.configPath() + ": is not a regular file"; err == nil || err.Error() != want {
		t.Errorf("directory: error = %v, want %s", err, want)
	}
}

func TestFileSymlink(t *testing.T) {
	target := filepath.Join(privateDir(t), "elsewhere.toml")
	if err := os.WriteFile(target, []byte("log_level = \"debug\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx := fixture{dataDir: privateDir(t), userDir: t.TempDir()}
	if err := os.Symlink(target, fx.configPath()); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	_, _, err := fx.load(nil)
	if err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
		t.Errorf("error = %v, want is a symbolic link", err)
	}
}

func TestFileFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs in the file system on Windows")
	}
	fx := fixture{dataDir: privateDir(t), userDir: t.TempDir()}
	if err := mkfifo(fx.configPath()); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := fx.load(nil); done <- err }()
	select {
	case err := <-done:
		if want := fx.configPath() + ": is not a regular file"; err == nil || err.Error() != want {
			t.Errorf("error = %v, want %s", err, want)
		}
	case <-ctx.Done():
		t.Fatal("Load blocked on a FIFO")
	}
}

// sentinel is the value of a synthetic secret setting, for checking that its value never reaches any output. Its parser
// wrongly repeats the value in the reason, which must not leak either.
const sentinel = "SENTINEL-s3cr3t-value"

func withSecretSetting(t *testing.T) {
	t.Helper()
	old := registry
	t.Cleanup(func() { registry = old })
	parse := func(v string) (any, error) {
		if !strings.HasPrefix(v, "tok_") {
			return nil, fmt.Errorf("%s is not a token", v)
		}
		return v, nil
	}
	registry = func() []Setting {
		return append(old(), Setting{
			Name:      "api_token",
			Flag:      "api-token",
			FileKey:   "api_token",
			Default:   func(Inputs) (string, error) { return "tok_" + sentinel, nil },
			Usage:     "Token",
			Secret:    true,
			ParseText: parse,
			ParseTOML: tomlString(parse),
			Apply:     func(*Config, any) {},
			Text:      func(*Config) string { return "tok_" + sentinel },
		})
	}
}

func noSentinel(t *testing.T, what, text string) {
	t.Helper()
	if strings.Contains(text, sentinel) || strings.Contains(text, "s3cr3t") {
		t.Errorf("%s shows the secret:\n%s", what, text)
	}
}

func TestSecretNeverShown(t *testing.T) {
	withSecretSetting(t)
	tests := []struct {
		name    string
		toml    string
		flags   map[string]string
		environ []string
		want    string
	}{
		{name: "flag", flags: map[string]string{"api-token": sentinel}, want: "api_token: (secret) from --api-token (secret) is not a token"},
		{name: "env", environ: []string{"WHATSAPP_MCP_API_TOKEN=" + sentinel}, want: "api_token: (secret) from WHATSAPP_MCP_API_TOKEN"},
		{name: "file value", toml: "api_token = \"" + sentinel + "\"\n", want: "api_token: (secret) from "},
		{name: "file type", toml: "api_token = 5\n", want: "api_token must be a string, not an integer"},
		{name: "file syntax", toml: "api_token = \"" + sentinel + "\n", want: "api_token, which is secret"},
		{name: "file multi-line syntax", toml: "api_token = \"\"\"" + sentinel + "\n" + sentinel + " \\q\"\"\"\n", want: "api_token, which is secret"},
		{name: "joined", toml: "api_token = \"" + sentinel + "\"\n", environ: []string{"WHATSAPP_MCP_LOG_LEVEL=verbose"}, want: "log_level: \"verbose\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixture(t, tt.toml)
			_, _, err := fx.load(tt.flags, tt.environ...)
			if err == nil {
				t.Fatal("Load accepted it")
			}
			out := FormatError(err)
			noSentinel(t, "FormatError", out)
			noSentinel(t, "Error", err.Error())
			if !strings.Contains(out, tt.want) {
				t.Errorf("FormatError =\n%s\nwant it to contain %q", out, tt.want)
			}
		})
	}
}

func TestSecretMaskedInDumpAndHelp(t *testing.T) {
	withSecretSetting(t)
	fx := newFixture(t, "api_token = \"tok_"+sentinel+"\"\n")
	cfg, _ := mustLoad(t, fx, nil)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, attrs := range cfg.LogValues() {
		logger.LogAttrs(context.Background(), slog.LevelDebug, "config", attrs...)
	}
	noSentinel(t, "the debug dump", buf.String())
	if !strings.Contains(buf.String(), "setting=api_token value=(secret)") {
		t.Errorf("debug dump:\n%s\nwant value=(secret) for api_token", buf.String())
	}
	for _, s := range Settings() {
		def, err := s.DefaultHelp(Inputs{UserConfigDir: os.UserConfigDir})
		if err != nil {
			t.Fatal(err)
		}
		noSentinel(t, "help for "+s.Name, def)
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"plain /path/with spaces and ünïcode": "plain /path/with spaces and ünïcode",
		"a\nb":                                `a\nb`,
		"\x1b[2Jclear":                        `\x1b[2Jclear`,
		"over\rwrite":                         `over\rwrite`,
		"rtl\u202eoverride":                   `rtl\u202eoverride`,
		"line\u2028sep":                       `line\u2028sep`,
		"bell\a":                              `bell\a`,
		"del\x7f":                             `del\x7f`,
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatErrorEscapes(t *testing.T) {
	err := errors.Join(
		&FileError{Path: "/d/config.toml", Line: 1, Key: "x\x1b[31m", Reason: "unknown key \"x\x1b[31m\"\nwhatsapp-mcp: fake line"},
		&SettingError{Setting: "log_level", Origin: Origin{Flag, "--log-level"}, Value: "v\n", Reason: "is not valid"},
	)
	out := FormatError(err)
	if lines := strings.Split(out, "\n"); len(lines) != 3 {
		t.Errorf("FormatError gave %d lines, want a header and two errors:\n%s", len(lines), out)
	}
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("FormatError kept a control character: %q", out)
	}
	single := FormatError(&DataDirError{Dir: "/tmp/a\nwhatsapp-mcp: fake", Err: errors.New("exists and is not a directory")})
	if strings.Contains(single, "\n") {
		t.Errorf("single error spans lines: %q", single)
	}
}

func FuzzFormatFileErrors(f *testing.F) {
	for _, seed := range []string{
		"log_level = \"info\"\n",
		"log_level = \"info",
		"[section]\nx = 1\n",
		"log_level = 5\n",
		"\x1b[2J = 1\n",
		"\"a\nb\" = 1\n",
		"log_level = [[[[",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, toml string) {
		dir := filepath.Join(t.TempDir(), "data")
		if err := PrepareDataDir(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := Load(Inputs{Flags: map[string]string{"data-dir": dir}, UserConfigDir: os.UserConfigDir})
		if err == nil {
			return
		}
		out := FormatError(err)
		for _, line := range strings.Split(out, "\n")[1:] {
			if !strings.HasPrefix(line, "  ") {
				t.Errorf("an error spans lines: %q", out)
			}
		}
		if strings.ContainsFunc(out, func(r rune) bool { return r != '\n' && unprintable(r) }) {
			t.Errorf("unescaped control character: %q", out)
		}
	})
}
