package config

import (
	"runtime"
	"sort"
	"strings"
)

// Warning is an environment variable with the whatsapp-mcp prefix that names no setting.
type Warning struct {
	Name string
}

// environment is a snapshot of the process environment. Names compare as the OS compares them:
// case-insensitively on Windows, exactly elsewhere. An empty value counts as unset.
type environment struct {
	vars [][2]string
	fold bool
}

func newEnvironment(environ []string) *environment {
	env := &environment{fold: runtime.GOOS == "windows"}
	for _, kv := range environ {
		// Windows keeps per-drive working directories in entries such as "=C:=C:\x".
		if strings.HasPrefix(kv, "=") {
			continue
		}
		name, value, _ := strings.Cut(kv, "=")
		if value == "" {
			continue
		}
		env.vars = append(env.vars, [2]string{name, value})
	}
	return env
}

func (env *environment) same(a, b string) bool {
	if env.fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (env *environment) lookup(name string) (string, bool) {
	for _, kv := range env.vars {
		if env.same(kv[0], name) {
			return kv[1], true
		}
	}
	return "", false
}

// unknown lists, once each and sorted, the prefixed variables that are not in the registry.
func (env *environment) unknown(settings []Setting) []Warning {
	seen := map[string]bool{}
	var warnings []Warning
	for _, kv := range env.vars {
		name := kv[0]
		if len(name) < len(EnvPrefix) || !env.same(name[:len(EnvPrefix)], EnvPrefix) || seen[name] {
			continue
		}
		seen[name] = true
		known := false
		for _, s := range settings {
			if env.same(name, s.Env) {
				known = true
				break
			}
		}
		if !known {
			warnings = append(warnings, Warning{Name: name})
		}
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Name < warnings[j].Name })
	return warnings
}
