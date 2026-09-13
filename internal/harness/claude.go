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

// Claude Code env keys vector manages beyond the base URL.
const (
	// toolSearchKey re-enables Claude Code's deferred tool loading, which the
	// CLI disables on its own when the base URL is not api.anthropic.com.
	toolSearchKey   = "ENABLE_TOOL_SEARCH"
	toolSearchValue = "auto"
	// autoCompactWindowKey pins the auto-compact window for a session whose
	// model is 1M-entitled but sits behind a gateway URL (doctor reads it to
	// know the [1m] caveat is already handled).
	autoCompactWindowKey = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
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
	// Model is the model string vector wrote (with [1m]); ModelPrev is what the
	// user had, restored on disable.
	Model     string `json:"model,omitempty"`
	ModelPrev string `json:"model_prev,omitempty"`
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
	// Claude Code turns its deferred tool loading (MCP tool schemas behind a
	// ToolSearch tool) off whenever ANTHROPIC_BASE_URL is not api.anthropic.com,
	// so every request re-sends every tool schema. The gateway forwards
	// tool_reference blocks to Anthropic unchanged and expands them for other
	// upstreams, so it is safe to turn back on. A user value is respected.
	_, userSetToolSearch := env[toolSearchKey]
	if !userSetToolSearch {
		env[toolSearchKey] = toolSearchValue
	}
	sub := c.cfg.Harnesses["claude-code"].SubagentModel
	if sub != "" {
		env["CLAUDE_CODE_SUBAGENT_MODEL"] = sub
	}
	setStringMap(settings, "env", env)

	sc, err := c.loadSidecar()
	if err != nil {
		return rep, err
	}

	// Claude Code sizes its context window client-side and, behind a gateway,
	// holds a Claude model to 200k unless the model carries its [1m] variant.
	// Select it so the session gets the 1M window the model already has.
	if m, _ := settings["model"].(string); m != "" && oneMCapable(m) &&
		!strings.Contains(strings.ToLower(m), "[1m]") && env["CLAUDE_CODE_DISABLE_1M_CONTEXT"] != "1" {
		tagged := m + "[1m]"
		settings["model"] = tagged
		if sc.Model == "" {
			sc.ModelPrev = m
		}
		sc.Model = tagged
		rep.add(path, "set model="+tagged+" (1M window behind a gateway)")
	}

	if err := writeJSONMap(path, settings); err != nil {
		return rep, err
	}
	rep.Changed = true
	rep.add(path, "set ANTHROPIC_BASE_URL="+baseURL)
	rep.add(path, "set "+toolSearchKey+"="+env[toolSearchKey]+" (deferred MCP tool schemas)")

	sc.Keys["ANTHROPIC_BASE_URL"] = baseURL
	sc.Keys["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] = "1"
	if !userSetToolSearch {
		sc.Keys[toolSearchKey] = toolSearchValue
	}
	if sub != "" {
		sc.Keys["CLAUDE_CODE_SUBAGENT_MODEL"] = sub
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
	if sc.Model != "" {
		if cur, _ := settings["model"].(string); cur == sc.Model {
			if sc.ModelPrev != "" {
				settings["model"] = sc.ModelPrev
				rep.add(path, "restored model="+sc.ModelPrev)
			} else {
				delete(settings, "model")
				rep.add(path, "removed model")
			}
			rep.Changed = true
		}
	}
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
		"settings":            c.settingsPath(),
		"base_url":            base,
		"tool_search":         env[toolSearchKey],
		"model":               stringValue(settings["model"]),
		"auto_compact_window": env[autoCompactWindowKey],
		"agents_unrestricted": fmt.Sprintf("%d", c.unrestrictedAgents()),
		"one_m_disabled":      env["CLAUDE_CODE_DISABLE_1M_CONTEXT"],
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
		tools := def.Tools
		if len(tools) == 0 {
			tools = defaultAgentTools(role)
		}
		body := claudeAgentDoc(name, def.Description, name, tools)
		path := filepath.Join(c.agentsDir(), name+".md")
		if err := writeFileAtomic(path, []byte(body), 0o600); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	sort.Strings(written)
	return written, nil
}

// stringValue returns v when it is a string, else "".
func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func claudeAgentDoc(name, description, model string, tools []string) string {
	if description == "" {
		description = "Vector subagent " + name
	}
	description = strings.TrimSpace(description)
	const prompt = "You are a vector subagent working on a narrowly scoped task. " +
		"Follow the brief you are given, keep changes minimal, and state clearly when a task " +
		"is underspecified or needs escalation rather than guessing."
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", description)
	fmt.Fprintf(&b, "model: %s\n", model)
	if len(tools) > 0 {
		// An allowlist keeps every MCP server's schemas out of the subagent's
		// prompt — the bulk of a subagent request behind a gateway.
		fmt.Fprintf(&b, "tools: %s\n", strings.Join(tools, ", "))
	} else {
		// No allowlist configured: still deny MCP so the subagent does not
		// inherit every connected server's tool schemas.
		b.WriteString("disallowedTools: mcp__*\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.String()
}

// defaultAgentTools is the tool allowlist written for a role that does not set
// one. MCP tools are absent on purpose: a subagent otherwise inherits every
// connected server's schemas, and a background subagent always keeps MCP tools,
// so that payload is what bloats subagent requests and re-bills on every cache
// miss. Override per role with roles.<name>.tools.
func defaultAgentTools(role string) []string {
	switch role {
	case "scout":
		return []string{"Read", "Grep", "Glob"}
	case "worker":
		return []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash"}
	case "reviewer":
		return []string{"Read", "Grep", "Glob", "Bash"}
	case "researcher":
		return []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch"}
	case "lead":
		return []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash", "Agent"}
	case "architect":
		return []string{"Read", "Grep", "Glob", "Bash", "Agent"}
	case "escalate":
		return []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash", "Agent"}
	}
	return nil
}

// oneMCapable reports whether a Claude model id has a 1M-context variant worth
// selecting behind a gateway. Haiku and other 200k models are left alone.
func oneMCapable(model string) bool {
	m := strings.ToLower(model)
	for _, fam := range []string{"opus", "sonnet", "fable", "mythos"} {
		if strings.Contains(m, fam) {
			return true
		}
	}
	return false
}

// unrestrictedAgents counts installed vector-* agent definitions that would
// inherit every MCP server's tool schemas (no tools allowlist and no MCP deny).
func (c *Claude) unrestrictedAgents() int {
	paths, err := filepath.Glob(filepath.Join(c.agentsDir(), "vector-*.md"))
	if err != nil {
		return 0
	}
	n := 0
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		head := string(data)
		if i := strings.Index(head, "\n---"); i >= 0 {
			head = head[:i]
		}
		if !strings.Contains(head, "tools:") && !strings.Contains(head, "disallowedTools:") {
			n++
		}
	}
	return n
}
