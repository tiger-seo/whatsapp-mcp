package config

import (
	"errors"
	"testing"
)

func TestMessageForms(t *testing.T) {
	const path = "/home/me/.config/whatsapp-mcp/config.toml"
	tests := []struct {
		err  error
		want string
	}{
		{&SettingError{Setting: "log_level", Origin: Origin{Flag, "--log-level"}, Value: "verbose", Reason: "is not valid; use debug, info, warn or error"},
			`log_level: "verbose" from --log-level is not valid; use debug, info, warn or error`},
		{&SettingError{Setting: "log_level", Origin: Origin{Env, "WHATSAPP_MCP_LOG_LEVEL"}, Value: "verbose", Reason: "is not valid; use debug, info, warn or error"},
			`log_level: "verbose" from WHATSAPP_MCP_LOG_LEVEL is not valid; use debug, info, warn or error`},
		{&SettingError{Setting: "log_level", Origin: Origin{File, path + " line 3"}, Value: "verbose", Reason: "is not valid; use debug, info, warn or error"},
			`log_level: "verbose" from ` + path + ` line 3 is not valid; use debug, info, warn or error`},
		{&FileError{Path: path, Line: 1, Column: 18, Reason: "basic strings cannot have new lines"},
			path + " line 1, column 18: basic strings cannot have new lines"},
		{&FileError{Path: path, Line: 2, Key: "loglevel", Reason: `unknown key "loglevel"`},
			path + ` line 2: unknown key "loglevel"`},
		{&FileError{Path: path, Line: 1, Key: "log_level", Reason: "log_level must be a string, not an integer"},
			path + " line 1: log_level must be a string, not an integer"},
		{&FileError{Path: path, Line: 4, Key: "data_dir", Reason: "data_dir cannot be set in config.toml, because the file is inside the data directory; use --data-dir or WHATSAPP_MCP_DATA_DIR"},
			path + " line 4: data_dir cannot be set in config.toml, because the file is inside the data directory; use --data-dir or WHATSAPP_MCP_DATA_DIR"},
		{&FileError{Path: path, Reason: "permission denied"},
			path + ": permission denied"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("got  %s\nwant %s", got, tt.want)
		}
	}
}

func TestFormatError(t *testing.T) {
	err := errors.Join(
		&SettingError{Setting: "log_level", Origin: Origin{Env, "WHATSAPP_MCP_LOG_LEVEL"}, Value: "verbose", Reason: "is not valid; use debug, info, warn or error"},
		&SettingError{Setting: "data_dir", Origin: Origin{Flag, "--data-dir"}, Value: "data", Reason: "is not an absolute path"},
	)
	want := "whatsapp-mcp: invalid configuration:\n" +
		`  log_level: "verbose" from WHATSAPP_MCP_LOG_LEVEL is not valid; use debug, info, warn or error` + "\n" +
		`  data_dir: "data" from --data-dir is not an absolute path`
	if got := FormatError(err); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	ue := &UserConfigDirError{Err: errors.New("neither $XDG_CONFIG_HOME nor $HOME are defined")}
	want = "whatsapp-mcp: cannot find the per-user configuration directory (neither $XDG_CONFIG_HOME nor $HOME are defined); set --data-dir or WHATSAPP_MCP_DATA_DIR"
	if got := FormatError(ue); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
