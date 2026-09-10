package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anunay999/vector/internal/config"
)

// OpenCode wires OpenCode by adding a vector provider and per-role subagents to
// its JSON config. If the file cannot be parsed as strict JSON (for example it
// contains comments) the adapter refuses to touch it.
type OpenCode struct {
	cfg *config.Config
}

// NewOpenCode constructs the OpenCode adapter.
func NewOpenCode(cfg *config.Config) *OpenCode { return &OpenCode{cfg: cfg} }

// Name implements Adapter.
func (c *OpenCode) Name() string { return "opencode" }

func (c *OpenCode) configPath() string {
	if p := os.Getenv("OPENCODE_CONFIG"); p != "" {
		return p
	}
	dir := envOr("OPENCODE_CONFIG_DIR", filepath.Join(homeDir(), ".config", "opencode"))
	return filepath.Join(dir, "opencode.json")
}

func (c *OpenCode) sidecarPath() string {
	return filepath.Join(config.Dir(), "harness-opencode.json")
}

type opencodeSidecar struct {
	Agents []string `json:"agents"`
}

// Enable implements Adapter.
func (c *OpenCode) Enable() (Report, error) {
	rep := Report{Harness: c.Name()}
	path := c.configPath()
	if _, err := backupOnce(path); err != nil {
		return rep, fmt.Errorf("backup opencode config: %w", err)
	}
	m, err := readJSONMap(path)
	if err != nil {
		return rep, err
	}

	baseURL := "http://" + c.cfg.Listen.OpenAI + "/v1"
	models := map[string]any{}
	for _, role := range c.cfg.RoleNames() {
		models[role] = map[string]any{"name": "Vector " + role}
	}
	provider := map[string]any{
		"npm":  "@ai-sdk/openai-compatible",
		"name": "Vector Router",
		"options": map[string]any{
			"baseURL": baseURL,
			"apiKey":  "vector-local",
		},
		"models": models,
	}
	providers, _ := m["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers["vector"] = provider
	m["provider"] = providers

	agents, _ := m["agent"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
	}
	var installed []string
	for _, role := range c.cfg.RoleNames() {
		name := "vector-" + role
		agents[name] = map[string]any{
			"mode":        "subagent",
			"model":       "vector/" + role,
			"description": c.cfg.Roles[role].Description,
		}
		installed = append(installed, name)
	}
	m["agent"] = agents

	if err := writeJSONMap(path, m); err != nil {
		return rep, err
	}
	rep.Changed = true
	rep.add(path, "added provider \"vector\" and "+fmt.Sprintf("%d subagents", len(installed)))

	data, _ := json.MarshalIndent(opencodeSidecar{Agents: installed}, "", "  ")
	if err := writeFileAtomic(c.sidecarPath(), data, 0o600); err != nil {
		return rep, err
	}
	return rep, nil
}

// Disable implements Adapter.
func (c *OpenCode) Disable() (Report, error) {
	rep := Report{Harness: c.Name()}
	m, err := readJSONMap(c.configPath())
	if err != nil {
		return rep, err
	}
	providers, _ := m["provider"].(map[string]any)
	if providers != nil {
		delete(providers, "vector")
	}
	agents, _ := m["agent"].(map[string]any)
	for name := range agents {
		if strings.HasPrefix(name, "vector-") {
			delete(agents, name)
		}
	}
	if err := writeJSONMap(c.configPath(), m); err != nil {
		return rep, err
	}
	rep.Changed = true
	rep.add(c.configPath(), "removed vector provider and subagents")
	_ = os.Remove(c.sidecarPath())
	return rep, nil
}

// Status implements Adapter.
func (c *OpenCode) Status() (Status, error) {
	m, err := readJSONMap(c.configPath())
	if err != nil {
		return Status{Harness: c.Name()}, err
	}
	providers, _ := m["provider"].(map[string]any)
	_, ok := providers["vector"]
	return Status{
		Harness: c.Name(),
		Enabled: ok,
		Detail:  fmt.Sprintf("config=%s provider vector=%v", c.configPath(), ok),
		Info:    map[string]string{"config": c.configPath()},
	}, nil
}
