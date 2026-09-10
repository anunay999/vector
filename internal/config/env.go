package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvPath returns the path of the vector env file that holds secrets referenced
// by the config (for example OPENROUTER_API_KEY).
func EnvPath() string { return filepath.Join(Dir(), EnvFileName) }

// ReadEnv reads the env file into a map. A missing file yields an empty map.
func ReadEnv() (map[string]string, error) {
	m, err := readEnvFile(EnvPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// WriteEnv writes the env file atomically with 0600 permissions. Values are
// quoted only when they contain characters that would break the KEY=VALUE form.
func WriteEnv(m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# vector environment — referenced by ~/.config/vector/config.yaml as ${VAR}.\n")
	b.WriteString("# Keep this file private (chmod 600).\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, quoteEnv(m[k]))
	}
	return writeFileAtomic(EnvPath(), []byte(b.String()), 0o600)
}

// SetEnv sets a single key in the env file.
func SetEnv(key, value string) error {
	m, err := ReadEnv()
	if err != nil {
		return err
	}
	m[key] = value
	return WriteEnv(m)
}

// UnsetEnv removes a single key from the env file.
func UnsetEnv(key string) error {
	m, err := ReadEnv()
	if err != nil {
		return err
	}
	delete(m, key)
	return WriteEnv(m)
}

func quoteEnv(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t\"'#\\") {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		return `"` + v + `"`
	}
	return v
}

// writeFileAtomic writes data to path via a temp file and rename, creating the
// parent directory if needed.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
