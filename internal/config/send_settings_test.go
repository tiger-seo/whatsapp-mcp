package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/media"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// tomlList renders paths as a TOML array of literal strings.
func tomlList(entries ...string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = "'" + e + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func fakeExecutable(t *testing.T) string {
	t.Helper()
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("not run"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSendSettingDefaults(t *testing.T) {
	fx := newFixture(t, "")
	cfg, _ := mustLoad(t, fx, nil)
	if want := []string{filepath.Join(fx.dataDir, "outbox")}; !slices.Equal(cfg.SendAllowedDirs, want) {
		t.Errorf("SendAllowedDirs = %v, want %v", cfg.SendAllowedDirs, want)
	}
	if len(cfg.SendAllowedRecipients) != 0 || cfg.SendingEnabled() || cfg.FFmpeg != "" {
		t.Errorf("recipients = %v, enabled %v, ffmpeg %q", cfg.SendAllowedRecipients, cfg.SendingEnabled(), cfg.FFmpeg)
	}
	for _, name := range []string{SendAllowedDirs, SendAllowedRecipients, FFmpeg} {
		if o := cfg.Origin(name); o.Source != Default {
			t.Errorf("%s origin = %+v", name, o)
		}
	}
	// Load creates nothing.
	if _, err := os.Stat(fx.dataDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load created the data directory: %v", err)
	}
}

func TestSendSettingDefaultOutboxIsNotSplit(t *testing.T) {
	// A data directory whose name holds the list separator is still one default directory.
	dir := filepath.Join(t.TempDir(), "a"+string(os.PathListSeparator)+"b")
	cfg, _, err := Load(Inputs{Flags: map[string]string{"data-dir": dir}, UserConfigDir: func() (string, error) { return t.TempDir(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "outbox")}; !slices.Equal(cfg.SendAllowedDirs, want) {
		t.Errorf("SendAllowedDirs = %v, want %v", cfg.SendAllowedDirs, want)
	}
}

func TestSendSettingPrecedence(t *testing.T) {
	root := t.TempDir()
	a, b, c, d := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c"), filepath.Join(root, "d")
	sep := string(os.PathListSeparator)
	exeFile, exeEnv, exeFlag := fakeExecutable(t), fakeExecutable(t), fakeExecutable(t)
	file := "send_allowed_dirs = " + tomlList(a, b) + "\n" +
		`send_allowed_recipients = ["+49 151 100-00-01", "120363000000000001@g.us"]` + "\n" +
		"ffmpeg = '" + exeFile + "'\n"
	environ := []string{
		"WHATSAPP_MCP_SEND_ALLOWED_DIRS=" + c,
		"WHATSAPP_MCP_SEND_ALLOWED_RECIPIENTS=491511000002, 100000000042@lid",
		"WHATSAPP_MCP_FFMPEG=" + exeEnv,
	}
	flags := map[string]string{
		"send-allowed-dirs":       d + sep + a + string(filepath.Separator),
		"send-allowed-recipients": "*",
		"ffmpeg":                  exeFlag,
	}
	for _, tt := range []struct {
		name       string
		flags      map[string]string
		environ    []string
		dirs, to   []string
		ffmpeg     string
		wantSource Source
	}{
		{"file", nil, nil, []string{a, b}, []string{"491511000001@s.whatsapp.net", "120363000000000001@g.us"}, exeFile, File},
		{"env", nil, environ, []string{c}, []string{"491511000002@s.whatsapp.net", "100000000042@lid"}, exeEnv, Env},
		{"flag", flags, environ, []string{d, a}, []string{"*"}, exeFlag, Flag},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, warnings := mustLoad(t, newFixture(t, file), tt.flags, tt.environ...)
			if !slices.Equal(cfg.SendAllowedDirs, tt.dirs) || !slices.Equal(cfg.SendAllowedRecipients, tt.to) || cfg.FFmpeg != tt.ffmpeg {
				t.Errorf("dirs %v, recipients %v, ffmpeg %q", cfg.SendAllowedDirs, cfg.SendAllowedRecipients, cfg.FFmpeg)
			}
			for _, name := range []string{SendAllowedDirs, SendAllowedRecipients, FFmpeg} {
				if got := cfg.Origin(name).Source; got != tt.wantSource {
					t.Errorf("%s source = %v, want %v", name, got, tt.wantSource)
				}
			}
			if len(warnings) != 0 {
				t.Errorf("warnings = %v", warnings)
			}
		})
	}
}

func TestSendSettingEmptyValues(t *testing.T) {
	cfg, _ := mustLoad(t, newFixture(t, "send_allowed_dirs = []\nsend_allowed_recipients = []\nffmpeg = ''\n"), nil)
	if len(cfg.SendAllowedDirs) != 0 || cfg.SendingEnabled() || cfg.FFmpeg != "" {
		t.Errorf("empty file values: %v %v %q", cfg.SendAllowedDirs, cfg.SendAllowedRecipients, cfg.FFmpeg)
	}
	// An empty flag overrides the file.
	cfg, _ = mustLoad(t, newFixture(t, `send_allowed_recipients = ["*"]`), map[string]string{"send-allowed-recipients": ""})
	if cfg.SendingEnabled() {
		t.Error("an empty flag did not turn sending off")
	}
}

func TestSendSettingRefusals(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing.exe")
	plain := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	asDir := filepath.Join(root, "dir.exe")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	cases := []struct {
		name, setting, flag, value, toml string
	}{
		{"relative dir", SendAllowedDirs, "send-allowed-dirs", "outbox", ""},
		{"empty entry", SendAllowedDirs, "send-allowed-dirs", root + sep + sep + root, ""},
		{"file relative dir", SendAllowedDirs, "", "", "send_allowed_dirs = ['relative']"},
		{"file empty dir", SendAllowedDirs, "", "", "send_allowed_dirs = ['']"},
		{"file dirs string", SendAllowedDirs, "", "", "send_allowed_dirs = 'x'"},
		{"file dirs number", SendAllowedDirs, "", "", "send_allowed_dirs = [1]"},
		{"bad recipient", SendAllowedRecipients, "send-allowed-recipients", "491511000001,secretvalue", ""},
		{"empty recipient", SendAllowedRecipients, "send-allowed-recipients", "491511000001,,", ""},
		{"star with another", SendAllowedRecipients, "send-allowed-recipients", "*,491511000001", ""},
		{"status", SendAllowedRecipients, "send-allowed-recipients", "status@broadcast", ""},
		{"broadcast", SendAllowedRecipients, "send-allowed-recipients", "1@broadcast", ""},
		{"newsletter", SendAllowedRecipients, "send-allowed-recipients", "1@newsletter", ""},
		{"server", SendAllowedRecipients, "send-allowed-recipients", "s.whatsapp.net", ""},
		{"file recipients string", SendAllowedRecipients, "", "", "send_allowed_recipients = '*'"},
		{"file bad recipient", SendAllowedRecipients, "", "", `send_allowed_recipients = ["secretvalue"]`},
		{"relative ffmpeg", FFmpeg, "ffmpeg", "ffmpeg.exe", ""},
		{"missing ffmpeg", FFmpeg, "ffmpeg", missing, ""},
		{"not executable ffmpeg", FFmpeg, "ffmpeg", plain, ""},
		{"directory ffmpeg", FFmpeg, "ffmpeg", asDir, ""},
		{"file ffmpeg type", FFmpeg, "", "", "ffmpeg = 1"},
	}
	if runtime.GOOS == "windows" {
		batch := filepath.Join(root, "ffmpeg.bat")
		if err := os.WriteFile(batch, nil, 0o700); err != nil {
			t.Fatal(err)
		}
		cases = append(cases,
			struct{ name, setting, flag, value, toml string }{"batch ffmpeg", FFmpeg, "ffmpeg", batch, ""},
			struct{ name, setting, flag, value, toml string }{"unc dir", SendAllowedDirs, "send-allowed-dirs", `\\localhost\share`, ""},
			struct{ name, setting, flag, value, toml string }{"device dir", SendAllowedDirs, "send-allowed-dirs", `\\?\C:\x`, ""},
			struct{ name, setting, flag, value, toml string }{"stream dir", SendAllowedDirs, "send-allowed-dirs", `C:\x:stream`, ""},
		)
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var flags map[string]string
			if tt.flag != "" {
				flags = map[string]string{tt.flag: tt.value}
			}
			_, _, err := newFixture(t, tt.toml).load(flags)
			if err == nil {
				t.Fatal("accepted")
			}
			text := FormatError(err)
			if !strings.Contains(text, tt.setting) {
				t.Errorf("error does not name %s: %s", tt.setting, text)
			}
			if tt.setting == SendAllowedRecipients && strings.Contains(text, "secretvalue") {
				t.Errorf("error repeats a recipient: %s", text)
			}
		})
	}
}

func TestSendSettingTOMLSyntaxErrorHidesRecipients(t *testing.T) {
	_, _, err := newFixture(t, "send_allowed_recipients = [\"491511000001\", \n").load(nil)
	if err == nil || strings.Contains(FormatError(err), "491511000001") || !strings.Contains(err.Error(), SendAllowedRecipients) {
		t.Errorf("error = %v", err)
	}
}

func TestSendSettingDebugDump(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	exe := fakeExecutable(t)
	cfg, _ := mustLoad(t, newFixture(t, ""), map[string]string{
		"send-allowed-dirs": dir, "send-allowed-recipients": "491511000001,491511000002", "ffmpeg": exe,
	})
	dump := ""
	for _, line := range cfg.LogValues() {
		dump += strings.TrimSpace(strings.Join([]string{line[0].String(), line[1].String(), line[2].String()}, " ")) + "\n"
	}
	for _, want := range []string{
		"setting=send_allowed_dirs value=" + dir + " source=flag",
		"setting=send_allowed_recipients value=2 listed source=flag",
		"setting=ffmpeg value=" + exe + " source=flag",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump lacks %q:\n%s", want, dump)
		}
	}
	if strings.Contains(dump, "491511000001") {
		t.Errorf("dump shows a recipient:\n%s", dump)
	}
	for value, want := range map[string]string{"*": "*", "": "none"} {
		cfg, _ := mustLoad(t, newFixture(t, ""), map[string]string{"send-allowed-recipients": value})
		if got := setting(t, SendAllowedRecipients).Text(cfg); got != want {
			t.Errorf("Text(%q) = %q, want %q", value, got, want)
		}
	}
}

// loadSending loads a private data directory with sending on or off.
func loadSending(t *testing.T, dataDir string, recipients string, dirs ...string) *Config {
	t.Helper()
	flags := map[string]string{"data-dir": dataDir, "send-allowed-recipients": recipients}
	if dirs != nil {
		flags["send-allowed-dirs"] = strings.Join(dirs, string(os.PathListSeparator))
	}
	cfg, _, err := Load(Inputs{Flags: flags, UserConfigDir: func() (string, error) { return t.TempDir(), nil }})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func openDirs(t *testing.T, cfg *Config) ([]media.AllowedDir, error) {
	t.Helper()
	dirs, err := OpenSendDirs(cfg)
	t.Cleanup(func() { CloseSendDirs(dirs) })
	return dirs, err
}

func wantSendDirRefusal(t *testing.T, err error) {
	t.Helper()
	var se *SettingError
	if !errors.As(err, &se) || se.Setting != SendAllowedDirs || !strings.Contains(err.Error(), SendAllowedDirs) {
		t.Errorf("error = %v, want a refusal naming %s", err, SendAllowedDirs)
	}
}

func TestSendDirsOffTouchesNothing(t *testing.T) {
	data := privateDir(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for _, dirs := range [][]string{nil, {missing}, {data}} {
		got, err := openDirs(t, loadSending(t, data, "", dirs...))
		if err != nil || len(got) != 0 {
			t.Errorf("sending off with %v: %v, %v", dirs, got, err)
		}
	}
	for _, path := range []string{missing, filepath.Join(data, "outbox")} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("sending off created %s: %v", path, err)
		}
	}
}

func TestSendDirsCreatePrivateOutbox(t *testing.T) {
	data := privateDir(t)
	dirs, err := openDirs(t, loadSending(t, data, "*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("OpenSendDirs = %v, %v", dirs, err)
	}
	outbox := filepath.Join(data, "outbox")
	if err := private.CheckDir(outbox); err != nil {
		t.Errorf("outbox is not private: %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(outbox)
	if dirs[0].Path != resolved || dirs[0].Root == nil {
		t.Errorf("dir = %+v, want %s", dirs[0], resolved)
	}
}

func TestSendDirsIdentity(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "data")
	if err := PrepareDataDir(data); err != nil {
		t.Fatal(err)
	}
	outbox := filepath.Join(data, "outbox")
	sub := filepath.Join(outbox, "sub")
	inside := filepath.Join(data, "media")
	outside := filepath.Join(t.TempDir(), "out")
	sibling := filepath.Join(parent, "data-files")
	for _, dir := range []string{inside, outside, sibling} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := openDirs(t, loadSending(t, data, "*", outbox)); err != nil {
		t.Fatalf("the default outbox named explicitly: %v", err)
	}
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, ok := range map[string][]string{
		"outbox subdirectory": {sub},
		"outside":             {outside, sibling},
		"outbox and outside":  {outbox, outside},
	} {
		if dirs, err := openDirs(t, loadSending(t, data, "*", ok...)); err != nil || len(dirs) != len(ok) {
			t.Errorf("%s: %v, %v", name, dirs, err)
		}
	}
	plain := filepath.Join(outside, "file")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]string{
		"data directory":          {data},
		"parent":                  {parent},
		"root":                    {filepath.VolumeName(data) + string(filepath.Separator)},
		"inside":                  {inside},
		"missing":                 {filepath.Join(outside, "missing")},
		"a file":                  {plain},
		"good then bad":           {outside, data},
		"traversal into the data": {outbox + string(filepath.Separator) + ".." + string(filepath.Separator) + "media"},
	} {
		t.Run(name, func(t *testing.T) {
			dirs, err := openDirs(t, loadSending(t, data, "*", bad...))
			wantSendDirRefusal(t, err)
			if len(dirs) != 0 {
				t.Errorf("returned %d open directories with an error", len(dirs))
			}
		})
	}
}

func TestSendDirsCaseAlias(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "DataDir")
	if err := PrepareDataDir(data); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "dATAdIR")
	if _, err := os.Stat(alias); err != nil {
		t.Skip("the file system is case-sensitive: this platform run does not exercise the identity check on a case alias")
	}
	upperParent := filepath.Join(filepath.Dir(parent), strings.ToUpper(filepath.Base(parent)))
	for _, bad := range []string{alias, upperParent, filepath.Join(alias, "sub")} {
		if err := os.MkdirAll(bad, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := openDirs(t, loadSending(t, data, "*", bad))
		wantSendDirRefusal(t, err)
	}
}

func TestSendDirsUnprivateOutbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the access list of a new directory is inherited from the private data directory")
	}
	data := privateDir(t)
	outbox := filepath.Join(data, "outbox")
	if err := os.Mkdir(outbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outbox, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := openDirs(t, loadSending(t, data, "*"))
	wantSendDirRefusal(t, err)
}

func TestSendDirsLinkedOutboxIsNoException(t *testing.T) {
	data := privateDir(t)
	inside := filepath.Join(data, "media")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(data, "outbox")); err != nil {
		t.Skip("cannot create a symbolic link here: " + strconv.Quote(err.Error()))
	}
	_, err := openDirs(t, loadSending(t, data, "*"))
	wantSendDirRefusal(t, err)
	_, err = openDirs(t, loadSending(t, data, "*", filepath.Join(data, "outbox")))
	wantSendDirRefusal(t, err)
}
