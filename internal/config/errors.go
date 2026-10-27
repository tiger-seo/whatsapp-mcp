package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// SettingError is an invalid value for a setting, from any source. The value of a secret setting is never
// shown.
type SettingError struct {
	Setting string
	Origin  Origin
	Value   string
	Secret  bool
	Reason  string
}

func newSettingError(s Setting, o Origin, value string, err error) *SettingError {
	reason := err.Error()
	if s.Secret && value != "" {
		// A reason should not repeat the value, but a secret must not leak if one does.
		reason = strings.ReplaceAll(reason, value, secretText)
	}
	return &SettingError{Setting: s.Name, Origin: o, Value: value, Secret: s.Secret, Reason: reason}
}

func (e *SettingError) Error() string {
	value := strconv.Quote(e.Value)
	if e.Secret {
		value = secretText
	}
	return fmt.Sprintf("%s: %s from %s %s", e.Setting, value, e.Origin.Detail, e.Reason)
}

// secretText stands for a secret value wherever one would be shown.
const secretText = "(secret)"

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

// FormatError renders an error for the operator, without a trailing newline. Configuration errors, as
// returned by Load, are listed under one header, one per line. Each error is passed through Printable,
// since it may quote the operator's input.
func FormatError(err error) string {
	errs := flatten(err)
	if len(errs) == 1 && !isConfigError(errs[0]) {
		return "whatsapp-mcp: " + Printable(errs[0].Error())
	}
	var b strings.Builder
	b.WriteString("whatsapp-mcp: invalid configuration:")
	for _, e := range errs {
		b.WriteString("\n  ")
		b.WriteString(Printable(e.Error()))
	}
	return b.String()
}

// Printable escapes control characters, including line breaks, and bidirectional formatting characters as
// Go escapes them, so that text from a path, a flag or config.toml cannot move the cursor, fake a line of
// output or reorder what the terminal shows.
func Printable(s string) string {
	if !strings.ContainsFunc(s, unprintable) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if !unprintable(r) {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteRune(r)
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}

func unprintable(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
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
