package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anunay999/vector/internal/config"
)

// Claude wires Claude Code by editing its settings.json env block and installing
// native subagent definitions under ~/.claude/agents.
type Claude struct {
	cfg *config.Config
}

// NewClaude constructs the Claude Code adapter.
func NewClaude(cfg *config.Config) *Claude { return &Claude{cfg: cfg} }

// Name implements Adapter.
func (c *Claude) Name() string { return "claude-code" }

func (c *Claude) settingsPath() string {
	dir := envOr("CLAUDE_CONFIG_DIR", filepath.Join(homeDir(), ".claude"))
	return filepath.Join(dir, "settings.json")
}

func (c *Claude) agentsDir() string {
	dir := envOr("CLAUDE_CONFIG_DIR", filepath.Join(homeDir(), ".claude"))
	return filepath.Join(dir, "agents")
}

func (c *Claude) sidecarPath() string {
	return filepath.Join(config.Dir(), "harness-claude.json")
}

type claudeSidecar struct {
	Keys   map[string]string `json:"keys"`
	Agents []string          `json:"agents"`
}

func (c *Claude) loadSidecar() (claudeSidecar, error) {
	var sc claudeSidecar
	data, err := os.ReadFile(c.sidecarPath())
	if err != nil {
		if os.IsNotExist(err) {
			return claudeSidecar{Keys: map[string]string{}}, nil
		}
		return sc, err
	}
	if err := json.Unmarshal(data, &sc); err != nil {
		return sc, err
	}
	if sc.Keys == nil {
		sc.Keys = map[string]string{}
	}
	return sc, nil
}

func (c *Claude) saveSidecar(sc claudeSidecar) error {
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.sidecarPath(), data, 0o600)
}

// Enable implements Adapter.
func (c *Claude) Enable() (Report, error) {
	rep := Report{Harness: c.Name()}
	path := c.settingsPath()

	if _, err := backupOnce(path); err != nil {
		return rep, fmt.Errorf("backup settings: %w", err)
	}
	settings, err := readJSONMap(path)
	if err != nil {
		return rep, err
	}
	env := stringMap(settings, "env")

	baseURL := "http://" + c.cfg.Listen.Anthropic
	env["ANTHROPIC_BASE_URL"] = baseURL
	env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] = "1"
	sub := c.cfg.Harnesses["claude-code"].SubagentModel
	if sub != "" {
		env["CLAUDE_CODE_SUBAGENT_MODEL"] = sub
		if c.cfg.Harnesses["claude-code"].ForceSubagentModel {
			env["CLAUDE_CODE_SUBAGENT_MODEL_FORCE"] = sub
		}
	}
	setStringMap(settings, "env", env)
	if err := writeJSONMap(path, settings); err != nil {
		return rep, err
	}
	rep.Changed = true
	rep.add(path, "set ANTHROPIC_BASE_URL="+baseURL)

	sc, err := c.loadSidecar()
	if err != nil {
		return rep, err
	}
	sc.Keys["ANTHROPIC_BASE_URL"] = baseURL
	sc.Keys["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] = "1"
	if sub != "" {
		sc.Keys["CLAUDE_CODE_SUBAGENT_MODEL"] = sub
		if c.cfg.Harnesses["claude-code"].ForceSubagentModel {
			sc.Keys["CLAUDE_CODE_SUBAGENT_MODEL_FORCE"] = sub
		}
	}

	agents, err := c.writeAgents()
	if err != nil {
		return rep, err
	}
	sc.Agents = agents
	rep.add(c.agentsDir(), fmt.Sprintf("installed %d subagent definitions", len(agents)))

	if err := c.saveSidecar(sc); err != nil {
		return rep, err
	}
	return rep, nil
}

// Disable implements Adapter: it removes only keys/agents vector owns, and keeps
// user edits when a value has drifted.
func (c *Claude) Disable() (Report, error) {
	rep := Report{Harness: c.Name()}
	sc, err := c.loadSidecar()
	if err != nil {
		return rep, err
	}
	path := c.settingsPath()
	settings, err := readJSONMap(path)
	if err != nil {
		return rep, err
	}
	env := stringMap(settings, "env")
	for k, v := range sc.Keys {
		if cur, ok := env[k]; ok && cur == v {
			delete(env, k)
			rep.add(path, "removed "+k)
			rep.Changed = true
		}
	}
	setStringMap(settings, "env", env)
	if err := writeJSONMap(path, settings); err != nil {
		return rep, err
	}
	for _, a := range sc.Agents {
		if err := os.Remove(a); err == nil {
			rep.add(a, "removed subagent definition")
			rep.Changed = true
		}
	}
	_ = os.Remove(c.sidecarPath())
	return rep, nil
}

// Status implements Adapter.
func (c *Claude) Status() (Status, error) {
	settings, err := readJSONMap(c.settingsPath())
	if err != nil {
		return Status{Harness: c.Name()}, err
	}
	env := stringMap(settings, "env")
	base := env["ANTHROPIC_BASE_URL"]
	want := "http://" + c.cfg.Listen.Anthropic
	info := map[string]string{
		"settings": c.settingsPath(),
		"base_url": base,
	}
	return Status{
		Harness: c.Name(),
		Enabled: base == want,
		Detail:  fmt.Sprintf("ANTHROPIC_BASE_URL=%q", base),
		Info:    info,
	}, nil
}

// writeAgents installs one markdown agent definition per configured role and
// returns the paths written. The parent model selects among these by name; each
// pins a virtual model the gateway resolves.
func (c *Claude) writeAgents() ([]string, error) {
	if err := os.MkdirAll(c.agentsDir(), 0o700); err != nil {
		return nil, err
	}
	var written []string
	for _, role := range c.cfg.RoleNames() {
		def := c.cfg.Roles[role]
		name := "vector-" + role
		body := claudeAgentDoc(name, def.Description, name)
		path := filepath.Join(c.agentsDir(), name+".md")
		if err := writeFileAtomic(path, []byte(body), 0o600); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	sort.Strings(written)
	return written, nil
}

func claudeAgentDoc(name, description, model string) string {
	if description == "" {
		description = "Vector subagent " + name
	}
	description = strings.TrimSpace(description)
	prompt := fmt.Sprintf("You are a vector subagent working on a narrowly scoped task. " +
		"Follow the brief you are given, keep changes minimal, and state clearly when a task " +
		"is underspecified or needs escalation rather than guessing.")
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", description)
	fmt.Fprintf(&b, "model: %s\n", model)
	b.WriteString("---\n\n")
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.String()
}
