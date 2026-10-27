package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// FileName is the optional configuration file in the data directory.
const FileName = "config.toml"

// file is a decoded config.toml with the line of each top-level key.
type file struct {
	path   string
	values map[string]any
	lines  map[string]int
}

// MaxFileSize is the largest config.toml that is read, in bytes.
const MaxFileSize = 1 << 20

// readFile reads <dataDir>/config.toml, which must be a private regular file of at most MaxFileSize bytes.
// A missing file gives no file and no error, and nothing is created.
func readFile(dataDir string, settings []Setting) (*file, error) {
	path := filepath.Join(dataDir, FileName)
	r, err := private.OpenFile(path, os.O_RDONLY)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &FileError{Path: path, Reason: err.Error()}
	}
	defer r.Close()
	// One byte more than allowed tells a file at the limit from a larger one, even if it grows meanwhile.
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, &FileError{Path: path, Reason: err.Error()}
	}
	if len(data) > MaxFileSize {
		return nil, &FileError{Path: path, Reason: "is larger than 1 MiB"}
	}
	values := map[string]any{}
	if err := toml.Unmarshal(data, &values); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, col := de.Position()
			reason := strings.TrimPrefix(de.Error(), "toml: ")
			if key := secretKeyBefore(data, line, settings); key != "" {
				// The parser's message may quote the text around the error.
				reason = fmt.Sprintf("invalid TOML in or after the value of %s, which is secret", key)
			}
			return nil, &FileError{Path: path, Line: line, Column: col, Reason: reason}
		}
		return nil, &FileError{Path: path, Reason: err.Error()}
	}
	return &file{path: path, values: values, lines: keyLines(data)}, nil
}

// secretKeyBefore returns the key of a secret setting that starts a line at or before line, if there is one.
func secretKeyBefore(data []byte, line int, settings []Setting) string {
	var keys []string
	for _, s := range settings {
		if s.Secret && s.FileKey != "" {
			keys = append(keys, s.FileKey)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	for i, text := range strings.Split(string(data), "\n") {
		if i >= line {
			break
		}
		text = strings.TrimLeft(text, " \t")
		for _, k := range keys {
			for _, form := range []string{k, `"` + k + `"`, "'" + k + "'"} {
				if rest, ok := strings.CutPrefix(text, form); ok && strings.HasPrefix(strings.TrimLeft(rest, " \t"), "=") {
					return k
				}
			}
		}
	}
	return ""
}

// keyLines maps each top-level key, and each table name, to the line where it first appears.
func keyLines(data []byte) map[string]int {
	lines := map[string]int{}
	p := unstable.Parser{}
	p.Reset(data)
	inTable := false
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			inTable = true
		case unstable.KeyValue:
			if inTable {
				continue
			}
		default:
			continue
		}
		it := e.Key()
		if !it.Next() {
			continue
		}
		key := it.Node()
		name := string(key.Data)
		if _, ok := lines[name]; !ok {
			lines[name] = p.Shape(key.Raw).Start.Line
		}
	}
	return lines
}

func (f *file) origin(key string) Origin {
	detail := f.path
	if line := f.lines[key]; line > 0 {
		detail = fmt.Sprintf("%s line %d", f.path, line)
	}
	return Origin{Source: File, Detail: detail}
}

// checkKeys reports every top-level key that is not a setting allowed in the file, in line order.
func (f *file) checkKeys(settings []Setting) []error {
	var errs []*FileError
	for key := range f.values {
		var reason string
		switch s, ok := find(settings, key); {
		case !ok:
			reason = fmt.Sprintf("unknown key %q", key)
		case s.FileKey == "":
			reason = fmt.Sprintf("%s cannot be set in %s, because the file is inside the data directory; use --%s or %s",
				key, FileName, s.Flag, s.Env)
		default:
			continue
		}
		errs = append(errs, &FileError{Path: f.path, Line: f.lines[key], Key: key, Reason: reason})
	}
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Line != errs[j].Line {
			return errs[i].Line < errs[j].Line
		}
		return errs[i].Key < errs[j].Key
	})
	out := make([]error, len(errs))
	for i, e := range errs {
		out[i] = e
	}
	return out
}

// parse validates the file's value for a setting. A wrong TOML type is a FileError; an invalid value is a
// SettingError, as for the other sources.
func (f *file) parse(s Setting, v any) (any, error) {
	parsed, err := s.ParseTOML(v)
	if err == nil {
		return parsed, nil
	}
	var te *TypeError
	if errors.As(err, &te) {
		return nil, &FileError{Path: f.path, Line: f.lines[s.FileKey], Key: s.FileKey, Reason: s.FileKey + " " + te.Error()}
	}
	return nil, newSettingError(s, f.origin(s.FileKey), fmt.Sprint(v), err)
}

func find(settings []Setting, name string) (Setting, bool) {
	for _, s := range settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}
