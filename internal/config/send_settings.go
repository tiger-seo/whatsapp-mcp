package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tiger-seo/whatsapp-mcp/internal/media"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// The names of the settings that control sending.
const (
	SendAllowedDirs       = "send_allowed_dirs"
	SendAllowedRecipients = "send_allowed_recipients"
	FFmpeg                = "ffmpeg"
)

// outboxName is the default allowed directory, inside the data directory.
const outboxName = "outbox"

func defaultOutbox(c *Config) string { return filepath.Join(c.DataDir, outboxName) }

func sendAllowedDirsSetting() Setting {
	return Setting{
		Name:        SendAllowedDirs,
		Flag:        "send-allowed-dirs",
		FileKey:     SendAllowedDirs,
		Default:     func(Inputs) (string, error) { return "<data dir>" + string(filepath.Separator) + outboxName, nil },
		DefaultFrom: func(c *Config) any { return []string{defaultOutbox(c)} },
		Usage:       "Directories files may be sent from, separated by " + strconv.QuoteRune(os.PathListSeparator),
		ParseText:   func(s string) (any, error) { return parseDirs(filepath.SplitList(s)) },
		ParseTOML:   tomlStrings(parseDirs),
		Apply:       func(c *Config, v any) { c.SendAllowedDirs = v.([]string) },
		Text: func(c *Config) string {
			return strings.Join(c.SendAllowedDirs, string(os.PathListSeparator))
		},
	}
}

// parseDirs checks the syntax of each directory. Nothing is asked of the file system.
func parseDirs(entries []string) (any, error) {
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		// A trailing separator or a "." component is ordinary in a configured directory.
		if entry == "" || media.CheckPathSyntax(filepath.Clean(entry)) != nil {
			return nil, fmt.Errorf("entry %d %w", i+1, media.ErrPathSyntax)
		}
		out = append(out, filepath.Clean(entry))
	}
	return out, nil
}

func sendAllowedRecipientsSetting() Setting {
	return Setting{
		Name:     SendAllowedRecipients,
		Flag:     "send-allowed-recipients",
		FileKey:  SendAllowedRecipients,
		Redacted: true,
		Default:  func(Inputs) (string, error) { return "none, sending is off", nil },
		DefaultFrom: func(*Config) any {
			return []string{}
		},
		Usage: `Recipients that may be sent to, separated by commas, or "*" for any`,
		ParseText: func(s string) (any, error) {
			var entries []string
			if strings.TrimSpace(s) != "" {
				for _, entry := range strings.Split(s, ",") {
					entries = append(entries, strings.TrimSpace(entry))
				}
			}
			return parseRecipients(entries)
		},
		ParseTOML: tomlStrings(parseRecipients),
		Apply:     func(c *Config, v any) { c.SendAllowedRecipients = v.([]string) },
		Text: func(c *Config) string {
			switch n := len(c.SendAllowedRecipients); {
			case n == 0:
				return "none"
			case c.SendAllowedRecipients[0] == media.AllowAny:
				return media.AllowAny
			default:
				return strconv.Itoa(n) + " listed"
			}
		},
	}
}

func parseRecipients(entries []string) (any, error) {
	out, err := media.NormalizeAllowList(entries)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func ffmpegSetting() Setting {
	return Setting{
		Name:        FFmpeg,
		Flag:        "ffmpeg",
		FileKey:     FFmpeg,
		Default:     func(Inputs) (string, error) { return "found on PATH", nil },
		DefaultFrom: func(*Config) any { return "" },
		Usage:       "Absolute path of the ffmpeg program that converts audio",
		ParseText:   parseFFmpeg,
		ParseTOML:   tomlString(parseFFmpeg),
		Apply:       func(c *Config, v any) { c.FFmpeg = v.(string) },
		Text:        func(c *Config) string { return c.FFmpeg },
	}
}

func parseFFmpeg(s string) (any, error) {
	if s == "" {
		return "", nil
	}
	if !filepath.IsAbs(s) {
		return nil, errors.New("is not an absolute path")
	}
	if err := media.CheckExecutable(s); err != nil {
		return nil, err
	}
	return filepath.Clean(s), nil
}

// tomlStrings accepts only a TOML array of strings and passes it to parse.
func tomlStrings(parse func([]string) (any, error)) func(any) (any, error) {
	return func(v any) (any, error) {
		list, ok := v.([]any)
		if !ok {
			return nil, &TypeError{Want: "an array of strings", Got: v}
		}
		out := make([]string, len(list))
		for i, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, &TypeError{Want: "an array of strings", Got: item}
			}
			out[i] = s
		}
		return parse(out)
	}
}

// SendingEnabled reports whether the allow-list permits any send.
func (c *Config) SendingEnabled() bool { return len(c.SendAllowedRecipients) > 0 }

// OpenSendDirs resolves the allowed directories and opens each of them. Only a command that serves tools calls it,
// after it has taken the data-directory lock. With sending off it resolves, opens and creates nothing. The default
// outbox is created, private, when it is configured and missing. Each directory must exist and, compared by
// identity rather than by spelling, must not be the data directory, contain it or lie inside it, except the default
// outbox and its subdirectories. The caller closes the returned roots.
func OpenSendDirs(c *Config) (dirs []media.AllowedDir, err error) {
	if !c.SendingEnabled() {
		return nil, nil
	}
	defer func() {
		if err != nil {
			CloseSendDirs(dirs)
			dirs = nil
		}
	}()
	refuse := func(entry, reason string, cause error) error {
		return &SettingError{Setting: SendAllowedDirs, Origin: c.Origin(SendAllowedDirs), Value: entry, Reason: reason, cause: cause}
	}
	outbox := defaultOutbox(c)
	for _, entry := range c.SendAllowedDirs {
		if entry != outbox {
			continue
		}
		if err := private.MkdirAll(outbox); err != nil {
			return nil, refuse(outbox, "cannot be used as the private outbox: "+err.Error(), err)
		}
	}
	data, err := newDataIdentity(c.DataDir)
	if err != nil {
		return nil, refuse(c.DataDir, "cannot be compared with the data directory, whose path does not resolve", err)
	}
	for _, entry := range c.SendAllowedDirs {
		resolved, err := filepath.EvalSymlinks(entry)
		if err != nil {
			return dirs, refuse(entry, "does not exist or cannot be resolved (a path through a junction is refused)", err)
		}
		info, err := os.Lstat(resolved)
		if err != nil || info.Mode()&(fs.ModeDir|fs.ModeIrregular|fs.ModeSymlink) != fs.ModeDir {
			return dirs, refuse(entry, "is not a directory (a junction is refused)", err)
		}
		root, err := os.OpenRoot(resolved)
		if err != nil {
			return dirs, refuse(entry, "cannot be opened", err)
		}
		dirs = append(dirs, media.AllowedDir{Path: resolved, Root: root})
		opened, err := root.Stat(".")
		if err != nil || !os.SameFile(opened, info) {
			return dirs, refuse(entry, "changed while it was opened", err)
		}
		overlaps, inOutbox, err := data.overlaps(resolved, opened)
		if err != nil {
			return dirs, refuse(entry, "cannot be compared with the data directory", err)
		}
		if overlaps {
			return dirs, refuse(entry, "is the data directory, contains it or lies inside it; only the default outbox may lie inside", nil)
		}
		if inOutbox {
			if err := private.CheckDir(data.outboxPath); err != nil {
				return dirs, refuse(entry, "cannot be used as the private outbox: "+err.Error(), err)
			}
			current, err := os.Lstat(data.outboxPath)
			if err != nil || !os.SameFile(current, data.outbox) {
				return dirs, refuse(entry, "the default outbox changed while it was checked", err)
			}
		}
	}
	return dirs, nil
}

// CloseSendDirs closes the handles OpenSendDirs opened.
func CloseSendDirs(dirs []media.AllowedDir) {
	for _, d := range dirs {
		_ = d.Root.Close()
	}
}

// dataIdentity is the data directory, its ancestors and its outbox, as files rather than as path text.
type dataIdentity struct {
	dir       fs.FileInfo
	ancestors []fs.FileInfo
	// outbox is nil unless the data directory holds a real directory of that name.
	outbox     fs.FileInfo
	outboxPath string
}

func newDataIdentity(dataDir string) (dataIdentity, error) {
	resolved, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return dataIdentity{}, err
	}
	chain, err := ancestry(resolved)
	if err != nil {
		return dataIdentity{}, err
	}
	id := dataIdentity{dir: chain[0], ancestors: chain[1:], outboxPath: filepath.Join(resolved, outboxName)}
	info, err := os.Lstat(id.outboxPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return dataIdentity{}, err
	case info.Mode()&(fs.ModeDir|fs.ModeIrregular|fs.ModeSymlink) == fs.ModeDir:
		id.outbox = info
	}
	return id, nil
}

// ancestry returns a resolved directory and every directory above it.
func ancestry(dir string) ([]fs.FileInfo, error) {
	var out []fs.FileInfo
	for {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
		parent := filepath.Dir(dir)
		if parent == dir {
			return out, nil
		}
		dir = parent
	}
}

// overlaps reports whether the allowed directory at resolved, opened as entry, is or contains the data directory,
// or lies inside it outside the outbox. inOutbox identifies entries that use that exception.
func (id dataIdentity) overlaps(resolved string, entry fs.FileInfo) (overlaps, inOutbox bool, err error) {
	for _, ancestor := range id.ancestors {
		if os.SameFile(entry, ancestor) {
			return true, false, nil
		}
	}
	chain, err := ancestry(resolved)
	if err != nil {
		return false, false, err
	}
	inside := false
	for _, info := range chain {
		inside = inside || os.SameFile(info, id.dir)
		inOutbox = inOutbox || (id.outbox != nil && os.SameFile(info, id.outbox))
	}
	return inside && !inOutbox, inside && inOutbox, nil
}
