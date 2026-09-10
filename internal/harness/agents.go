package harness

import (
	"fmt"
	"regexp"
	"strings"
)

// Agent describes a subagent/role defined inside a harness.
type Agent struct {
	Name        string `json:"name"`
	Harness     string `json:"harness"`
	Path        string `json:"path,omitempty"`
	Model       string `json:"model,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Description string `json:"description,omitempty"`
	// Managed is true for agents vector installed.
	Managed bool `json:"managed"`
	// Routed is true when the agent's model points at a vector virtual model.
	Routed bool `json:"routed"`
	// Inline is true for a role declared without its own config file.
	Inline bool `json:"inline,omitempty"`
}

// AgentManager is implemented by adapters that expose per-agent model routing.
type AgentManager interface {
	Agents() ([]Agent, error)
	SetAgentModel(name, target string) error
}

// VirtualTarget normalizes a user target into a harness-appropriate model and
// (for Codex) provider. A bare role name like "worker" becomes the virtual
// model; a full "provider/model" id is passed through.
func VirtualTarget(harness, target string) (model, provider string, err error) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", "", fmt.Errorf("empty target")
	}
	switch harness {
	case "claude-code":
		if strings.HasPrefix(t, "vector/") {
			t = "vector-" + strings.TrimPrefix(t, "vector/")
		} else if !strings.HasPrefix(t, "vector-") && !strings.Contains(t, "/") {
			t = "vector-" + t
		}
		return t, "", nil
	case "codex":
		switch {
		case strings.HasPrefix(t, "vector-"):
			return "vector/" + strings.TrimPrefix(t, "vector-"), "vector", nil
		case strings.HasPrefix(t, "vector/"):
			return t, "vector", nil
		case strings.Contains(t, "/"):
			pid, _, _ := strings.Cut(t, "/")
			return t, pid, nil
		default:
			return "vector/" + t, "vector", nil
		}
	}
	return "", "", fmt.Errorf("agent routing is not supported for %s", harness)
}

// frontmatterModel reads the `model:` value from a markdown agent's YAML
// frontmatter. It returns the parsed fields and the raw document.
func parseAgentFrontmatter(name, path, content string) Agent {
	a := Agent{Name: name, Path: path, Managed: strings.HasPrefix(name, "vector-")}
	s := content
	if strings.HasPrefix(s, "---") {
		if end := strings.Index(s[3:], "\n---"); end >= 0 {
			fm := s[3 : 3+end]
			for _, line := range strings.Split(fm, "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok {
					continue
				}
				k = strings.TrimSpace(k)
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch k {
				case "model":
					a.Model = v
				case "description":
					a.Description = v
				}
			}
		}
	}
	a.Routed = strings.HasPrefix(a.Model, "vector")
	return a
}

var modelLineRe = regexp.MustCompile(`(?m)^[ \t]*model[ \t]*:.*$`)

// setFrontmatterModel replaces or inserts the `model:` field.
func setFrontmatterModel(content, model string) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("agent file has no YAML frontmatter")
	}
	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return "", fmt.Errorf("agent frontmatter is not closed")
	}
	fm := content[3 : 3+end]
	rest := content[3+end:]
	if modelLineRe.MatchString(fm) {
		fm = modelLineRe.ReplaceAllString(fm, "model: "+model)
	} else {
		fm = strings.TrimRight(fm, "\n") + "\nmodel: " + model + "\n"
	}
	return "---" + fm + rest, nil
}

// parseTomlAgents returns the [agents.<name>] tables from a TOML file as a map
// of name -> key/value. It is intentionally small and handles the shapes Codex
// writes for agent roles.
func parseTomlAgents(content string) map[string]map[string]string {
	out := map[string]map[string]string{}
	var cur string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.Trim(line, "[]")
			if rest, ok := strings.CutPrefix(sec, "agents."); ok {
				if i := strings.Index(rest, "."); i >= 0 {
					rest = rest[:i]
				}
				cur = strings.TrimSpace(rest)
				if out[cur] == nil {
					out[cur] = map[string]string{}
				}
			} else {
				cur = ""
			}
			continue
		}
		if cur == "" {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[cur][strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

// parseTomlKV returns top-level key=value pairs (ignoring tables).
func parseTomlKV(content string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

func setTomlKey(content, key, value string) string {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=.*$`)
	line := key + ` = "` + value + `"`
	if re.MatchString(content) {
		return re.ReplaceAllString(content, line)
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + line + "\n"
}
