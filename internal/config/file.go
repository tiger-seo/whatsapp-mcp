package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// FileName is the optional configuration file in the data directory.
const FileName = "config.toml"

// file is a decoded config.toml with the line of each top-level key.
type file struct {
	path   string
	values map[string]any
	lines  map[string]int
}

// readFile reads <dataDir>/config.toml. A missing directory or file gives no file and no error, and
// nothing is created.
func readFile(dataDir string) (*file, error) {
	path := filepath.Join(dataDir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		// ENOTDIR: the data directory is a regular file; PrepareDataDir reports that later.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, nil
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, &FileError{Path: path, Reason: err.Error()}
	}
	values := map[string]any{}
	if err := toml.Unmarshal(data, &values); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			line, col := de.Position()
			return nil, &FileError{Path: path, Line: line, Column: col, Reason: strings.TrimPrefix(de.Error(), "toml: ")}
		}
		return nil, &FileError{Path: path, Reason: err.Error()}
	}
	return &file{path: path, values: values, lines: keyLines(data)}, nil
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
	return nil, &SettingError{Setting: s.Name, Origin: f.origin(s.FileKey), Value: fmt.Sprint(v), Reason: err.Error()}
}

func find(settings []Setting, name string) (Setting, bool) {
	for _, s := range settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}
