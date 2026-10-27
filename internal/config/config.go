package config

import (
	"errors"
	"log/slog"
)

// Inputs are everything Load reads, so that tests can supply their own.
type Inputs struct {
	// Flags holds only the flags that were set on the command line, keyed by flag name.
	Flags map[string]string
	// Environ is a snapshot of the environment as KEY=VALUE strings.
	Environ []string
	// UserConfigDir returns the per-user configuration directory (os.UserConfigDir in production).
	UserConfigDir func() (string, error)
}

// Source is the layer a value came from. Later constants take precedence.
type Source int

const (
	Default Source = iota
	File
	Env
	Flag
)

func (s Source) String() string {
	switch s {
	case File:
		return "file"
	case Env:
		return "env"
	case Flag:
		return "flag"
	}
	return "default"
}

// Origin says where a value came from: the source, and the flag, variable, file line or "default".
type Origin struct {
	Source Source
	Detail string
}

// Config is the effective configuration. It is read-only once Load returns it.
type Config struct {
	DataDir  string
	LogLevel slog.Level

	origins map[string]Origin
}

// Origin returns where the named setting's value came from.
func (c *Config) Origin(name string) Origin {
	return c.origins[name]
}

// LogValues returns, per setting, the attributes of its debug log line. A secret's value is masked.
func (c *Config) LogValues() [][]slog.Attr {
	var out [][]slog.Attr
	for _, s := range Settings() {
		value := s.Text(c)
		if s.Secret {
			value = "(secret)"
		}
		o := c.origins[s.Name]
		out = append(out, []slog.Attr{
			slog.String("setting", s.Name),
			slog.String("value", value),
			slog.String("source", o.Source.String()),
			slog.String("origin", o.Detail),
		})
	}
	return out
}

// Load resolves every setting as flag > environment variable > config.toml > default and validates it.
// It reports all invalid values together, joined, and creates nothing: config.toml is only read, and
// the data directory is created later by PrepareDataDir. The warnings are unknown prefixed environment
// variables, for the caller to log.
func Load(in Inputs) (*Config, []Warning, error) {
	settings := Settings()
	env := newEnvironment(in.Environ)
	warnings := env.unknown(settings)
	cfg := &Config{origins: map[string]Origin{}}
	var errs []error

	// The data directory comes first, because config.toml is inside it.
	var f *file
	dataDir := settings[0]
	if err := resolve(dataDir, in, env, nil, cfg); err != nil {
		var ue *UserConfigDirError
		if errors.As(err, &ue) {
			return nil, warnings, err
		}
		errs = append(errs, err)
	} else {
		var err error
		if f, err = readFile(cfg.DataDir); err != nil {
			errs = append(errs, err)
		} else if f != nil {
			errs = append(errs, f.checkKeys(settings)...)
		}
	}
	for _, s := range settings[1:] {
		if err := resolve(s, in, env, f, cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, warnings, errors.Join(errs...)
	}
	return cfg, warnings, nil
}

// resolve finds the value of one setting in the first source that has it, validates it and applies it.
func resolve(s Setting, in Inputs, env *environment, f *file, cfg *Config) error {
	text := func(v string, o Origin) error {
		parsed, err := s.ParseText(v)
		if err != nil {
			return &SettingError{Setting: s.Name, Origin: o, Value: v, Reason: err.Error()}
		}
		s.Apply(cfg, parsed)
		cfg.origins[s.Name] = o
		return nil
	}
	if v, ok := in.Flags[s.Flag]; ok {
		return text(v, Origin{Source: Flag, Detail: "--" + s.Flag})
	}
	if v, ok := env.lookup(s.Env); ok {
		return text(v, Origin{Source: Env, Detail: s.Env})
	}
	if f != nil && s.FileKey != "" {
		if v, ok := f.values[s.FileKey]; ok {
			parsed, err := f.parse(s, v)
			if err != nil {
				return err
			}
			s.Apply(cfg, parsed)
			cfg.origins[s.Name] = f.origin(s.FileKey)
			return nil
		}
	}
	def, err := s.Default(in)
	if err != nil {
		return err
	}
	return text(def, Origin{Source: Default, Detail: "default"})
}
