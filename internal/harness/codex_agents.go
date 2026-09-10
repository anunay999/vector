package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agents lists the Codex agent roles declared in the user config and the
// managed overlay. Codex roles live in [agents.<name>] tables that point at a
// config_file; we read the role file for the model and provider.
func (c *Codex) Agents() ([]Agent, error) {
	home := c.home()
	tables := map[string]map[string]string{}
	managed := map[string]bool{}

	if data, err := os.ReadFile(filepath.Join(home, "config.toml")); err == nil {
		for n, kv := range parseTomlAgents(string(data)) {
			tables[n] = kv
		}
	}
	if data, err := os.ReadFile(c.overlayPath()); err == nil {
		for n, kv := range parseTomlAgents(string(data)) {
			tables[n] = kv
			managed[n] = true
		}
	}

	var out []Agent
	for name, kv := range tables {
		a := Agent{Name: name, Harness: c.Name(), Managed: managed[name], Description: kv["description"]}
		cf := kv["config_file"]
		if cf == "" {
			a.Inline = true
		} else {
			rp := cf
			if !filepath.IsAbs(rp) {
				rp = filepath.Join(home, cf)
			}
			a.Path = rp
			if data, err := os.ReadFile(rp); err == nil {
				kvs := parseTomlKV(string(data))
				a.Model = kvs["model"]
				a.Provider = kvs["model_provider"]
			}
		}
		a.Routed = strings.HasPrefix(a.Model, "vector")
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetAgentModel points a Codex role's config file at the given model. It edits
// only the role file, never the user's config.toml.
func (c *Codex) SetAgentModel(name, model, provider string) error {
	home := c.home()
	configFile := ""
	for _, f := range []string{filepath.Join(home, "config.toml"), c.overlayPath()} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if kv, ok := parseTomlAgents(string(data))[name]; ok && kv["config_file"] != "" {
			configFile = kv["config_file"]
		}
	}
	if configFile == "" {
		return fmt.Errorf("codex agent %q has no config_file; give it one in ~/.codex/config.toml (e.g. config_file = \"agents/%s.toml\") before routing it", name, name)
	}
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("codex agent %q: empty model", name)
	}
	path := configFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(home, configFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("codex agent %q: %w", name, err)
	}
	if _, err := backupOnce(path); err != nil {
		return err
	}
	content := setTomlKey(string(data), "model", model)
	if provider != "" {
		content = setTomlKey(content, "model_provider", provider)
	}
	return writeFileAtomic(path, []byte(content), 0o600)
}
