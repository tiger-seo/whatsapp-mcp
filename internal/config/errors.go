package config

import (
	"errors"
	"fmt"
	"strings"
)

// SettingError is an invalid value for a setting, from any source.
type SettingError struct {
	Setting string
	Origin  Origin
	Value   string
	Reason  string
}

func (e *SettingError) Error() string {
	return fmt.Sprintf("%s: %q from %s %s", e.Setting, e.Value, e.Origin.Detail, e.Reason)
}

// FileError is a problem with config.toml itself: syntax, an unknown or forbidden key, a wrong type, or a
// file that cannot be read. Line and Column are 0 when not known.
type FileError struct {
	Path   string
	Line   int
	Column int
	Key    string
	Reason string
}

func (e *FileError) Error() string {
	switch {
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("%s line %d, column %d: %s", e.Path, e.Line, e.Column, e.Reason)
	case e.Line > 0:
		return fmt.Sprintf("%s line %d: %s", e.Path, e.Line, e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

// FormatError renders an error returned by Load for the operator, without a trailing newline.
// Configuration errors are listed under one header, one per line.
func FormatError(err error) string {
	errs := flatten(err)
	if len(errs) == 1 && !isConfigError(errs[0]) {
		return "whatsapp-mcp: " + errs[0].Error()
	}
	var b strings.Builder
	b.WriteString("whatsapp-mcp: invalid configuration:")
	for _, e := range errs {
		b.WriteString("\n  ")
		b.WriteString(e.Error())
	}
	return b.String()
}

func isConfigError(err error) bool {
	var se *SettingError
	var fe *FileError
	return errors.As(err, &se) || errors.As(err, &fe)
}

// flatten unwraps the result of errors.Join into its parts.
func flatten(err error) []error {
	if err == nil {
		return nil
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var errs []error
	for _, e := range joined.Unwrap() {
		errs = append(errs, flatten(e)...)
	}
	return errs
}
