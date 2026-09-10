package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads the config from the default path. A missing file yields the built-in
// default configuration rather than an error, so first-run "just works".
func Load() (*Config, error) {
	return LoadFrom(DefaultPath())
}

// LoadFrom reads config from an explicit path. If the file does not exist it
// returns the default configuration with the path recorded.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg := Default()
			cfg.path = path
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.path = path
	cfg.applyDefaults()
	if err := cfg.resolveEnv(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// resolveEnv expands ${VAR} references in all string fields. Values come from the
// process environment first, then an optional env file next to the config.
func (c *Config) resolveEnv() error {
	fileEnv := map[string]string{}
	if c.path != "" {
		if m, err := readEnvFile(filepath.Join(filepath.Dir(c.path), EnvFileName)); err == nil {
			fileEnv = m
		}
	}
	lookup := func(key string) (string, bool) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			return v, true
		}
		v, ok := fileEnv[key]
		return v, ok
	}

	expand := func(s string) string {
		return envRefRe.ReplaceAllStringFunc(s, func(match string) string {
			sub := envRefRe.FindStringSubmatch(match)
			if v, ok := lookup(sub[1]); ok {
				return v
			}
			return ""
		})
	}

	for i := range c.Providers {
		c.Providers[i].BaseURL = expand(c.Providers[i].BaseURL)
		c.Providers[i].AnthropicBaseURL = expand(c.Providers[i].AnthropicBaseURL)
		c.Providers[i].APIKey = expand(c.Providers[i].APIKey)
		if c.Providers[i].Headers != nil {
			for k, v := range c.Providers[i].Headers {
				c.Providers[i].Headers[k] = expand(v)
			}
		}
	}
	c.Telemetry.Dir = expand(c.Telemetry.Dir)
	return nil
}

// UnresolvedVars returns ${VAR} names referenced by the config but missing from
// the environment and env file. Used by validation and `vector doctor`.
func (c *Config) UnresolvedVars() []string {
	seen := map[string]bool{}
	c.collectRefs(seen)
	fileEnv, _ := ReadEnv()
	var missing []string
	for name := range seen {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			continue
		}
		if _, ok := fileEnv[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	sortStrings(missing)
	return missing
}

func (c *Config) collectRefs(dst map[string]bool) {
	collect := func(s string) {
		for _, m := range envRefRe.FindAllStringSubmatch(s, -1) {
			dst[m[1]] = true
		}
	}
	// The raw config already had refs expanded on load, so re-read the file for
	// an accurate reference list when possible.
	if c.path != "" {
		if data, err := os.ReadFile(c.path); err == nil {
			for _, m := range envRefRe.FindAllStringSubmatch(string(data), -1) {
				dst[m[1]] = true
			}
			return
		}
	}
	for _, p := range c.Providers {
		collect(p.BaseURL)
		collect(p.APIKey)
	}
}

// Write serializes the config to path atomically with 0600 permissions.
func Write(c *Config, path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}

// readEnvFile parses a simple KEY=VALUE file. Missing files are not an error.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		out[key] = val
	}
	return out, sc.Err()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
