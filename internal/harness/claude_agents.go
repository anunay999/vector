package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agents lists the Claude Code subagent definitions in ~/.claude/agents.
func (c *Claude) Agents() ([]Agent, error) {
	dir := c.agentsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		a := parseAgentFrontmatter(name, path, string(data))
		a.Harness = c.Name()
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetAgentModel rewrites an agent's `model:` frontmatter to the given model.
func (c *Claude) SetAgentModel(name, model, provider string) error {
	name = strings.TrimSuffix(name, ".md")
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("claude agent %q: empty model", name)
	}
	path := filepath.Join(c.agentsDir(), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("claude agent %q: %w", name, err)
	}
	if _, err := backupOnce(path); err != nil {
		return err
	}
	out, err := setFrontmatterModel(string(data), model)
	if err != nil {
		return fmt.Errorf("claude agent %q: %w", name, err)
	}
	return writeFileAtomic(path, []byte(out), 0o600)
}
