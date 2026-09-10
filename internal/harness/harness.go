// Package harness installs and removes the per-harness wiring that points a
// coding agent at the vector gateway and defines its native subagents. Adapters
// are idempotent, back up user files once, and track exactly which keys they
// own so `off` never clobbers unrelated user edits.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Action is one change an adapter made (or would make).
type Action struct {
	Target string `json:"target"`
	Detail string `json:"detail"`
}

// Report summarizes the outcome of Enable/Disable.
type Report struct {
	Harness string   `json:"harness"`
	Changed bool     `json:"changed"`
	Actions []Action `json:"actions"`
}

func (r *Report) add(target, detail string) {
	r.Actions = append(r.Actions, Action{Target: target, Detail: detail})
}

// Status reports the current wiring state for a harness.
type Status struct {
	Harness string            `json:"harness"`
	Enabled bool              `json:"enabled"`
	Detail  string            `json:"detail"`
	Info    map[string]string `json:"info,omitempty"`
}

// Adapter wires a single harness.
type Adapter interface {
	Name() string
	Enable() (Report, error)
	Disable() (Report, error)
	Status() (Status, error)
}

// homeDir returns the user's home directory or ".".
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// envOr returns the value of an environment variable or a default.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// backupOnce copies path to path+".vector-backup" if the backup does not exist.
// It returns the backup path ("" if the source does not exist).
func backupOnce(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	backup := path + ".vector-backup"
	if _, err := os.Stat(backup); err == nil {
		return backup, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return "", err
	}
	return backup, nil
}

// writeFileAtomic writes data to path via a temp file and rename.
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

// readJSONMap reads a JSON object file into a generic map. A missing file is
// treated as an empty object.
func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// writeJSONMap marshals m with indentation and writes it atomically.
func writeJSONMap(path string, m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

// stringMap extracts a nested map[string]any as map[string]string.
func stringMap(m map[string]any, key string) map[string]string {
	out := map[string]string{}
	raw, ok := m[key].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// setStringMap sets m[key] to a map[string]any built from vals.
func setStringMap(m map[string]any, key string, vals map[string]string) {
	obj := map[string]any{}
	for k, v := range vals {
		obj[k] = v
	}
	m[key] = obj
}
