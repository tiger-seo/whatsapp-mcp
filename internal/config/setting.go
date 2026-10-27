package config

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// EnvPrefix starts the name of every environment variable that whatsapp-mcp reads.
const EnvPrefix = "WHATSAPP_MCP_"

// Setting describes one configuration value. Flags, help, the environment lookup, the config.toml checks
// and the debug dump are all generated from these descriptors, so a new setting is one descriptor plus
// one Config field.
type Setting struct {
	// Name is the canonical snake_case name, used in messages and the debug dump.
	Name string
	// Flag is the command-line flag without dashes.
	Flag string
	// Env is the environment variable, always EnvPrefix + upper-case Name.
	Env string
	// FileKey is the key in config.toml; empty means the setting is not allowed in the file.
	FileKey string
	// Default returns the default value as text.
	Default func(in Inputs) (string, error)
	// Usage is the one-line description shown in help.
	Usage string
	// Secret keeps the value out of logs and output.
	Secret bool
	// ParseText parses and validates a flag, environment or default value.
	ParseText func(string) (any, error)
	// ParseTOML checks the TOML type of a config.toml value, then validates it as ParseText does.
	ParseTOML func(any) (any, error)
	// Apply stores a parsed value in the Config.
	Apply func(*Config, any)
	// Text renders the effective value for the debug dump.
	Text func(*Config) string
}

// Settings returns the registry, in the order used by help and the debug dump. The data directory comes
// first because config.toml is found through it.
func Settings() []Setting {
	settings := []Setting{
		{
			Name:      "data_dir",
			Flag:      "data-dir",
			Default:   defaultDataDir,
			Usage:     "Data directory",
			ParseText: parseAbsPath,
			Apply:     func(c *Config, v any) { c.DataDir = v.(string) },
			Text:      func(c *Config) string { return c.DataDir },
		},
		{
			Name:      "log_level",
			Flag:      "log-level",
			FileKey:   "log_level",
			Default:   func(Inputs) (string, error) { return "info", nil },
			Usage:     "Log level: debug, info, warn, error",
			ParseText: parseLogLevel,
			ParseTOML: tomlString(parseLogLevel),
			Apply:     func(c *Config, v any) { c.LogLevel = v.(slog.Level) },
			Text:      func(c *Config) string { return strings.ToLower(c.LogLevel.String()) },
		},
	}
	for i := range settings {
		settings[i].Env = EnvPrefix + strings.ToUpper(settings[i].Name)
	}
	return settings
}

func parseLogLevel(s string) (any, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return nil, errors.New("is not valid; use debug, info, warn or error")
}

func parseAbsPath(s string) (any, error) {
	if !filepath.IsAbs(s) {
		return nil, errors.New("is not an absolute path")
	}
	return filepath.Clean(s), nil
}

// TypeError reports a config.toml value of the wrong TOML type.
type TypeError struct {
	Want string // with its article, e.g. "a string"
	Got  any
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("must be %s, not %s", e.Want, tomlTypeName(e.Got))
}

// tomlString accepts only TOML strings and passes them to parse.
func tomlString(parse func(string) (any, error)) func(any) (any, error) {
	return func(v any) (any, error) {
		s, ok := v.(string)
		if !ok {
			return nil, &TypeError{Want: "a string", Got: v}
		}
		return parse(s)
	}
}

// tomlTypeName names the TOML type of a value decoded by go-toml into an any, with its article.
func tomlTypeName(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case int64:
		return "an integer"
	case float64:
		return "a float"
	case bool:
		return "a boolean"
	case time.Time:
		return "a datetime"
	case []any:
		return "an array"
	case map[string]any:
		return "a table"
	}
	// go-toml's LocalDate, LocalTime and LocalDateTime.
	if strings.HasPrefix(fmt.Sprintf("%T", v), "toml.Local") {
		return "a datetime"
	}
	return fmt.Sprintf("a %T", v)
}
