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

const codexMarker = "# Managed by vector (github.com/anunay999/vector). Do not edit."

// Codex wires Codex CLI via a managed profile overlay. The user's own
// config.toml is never modified; the main session stays native and only the
// spawned agents use the vector provider.
type Codex struct {
	cfg *config.Config
}

// NewCodex constructs the Codex adapter.
func NewCodex(cfg *config.Config) *Codex { return &Codex{cfg: cfg} }

// Name implements Adapter.
func (c *Codex) Name() string { return "codex" }

func (c *Codex) home() string { return envOr("CODEX_HOME", filepath.Join(homeDir(), ".codex")) }

func (c *Codex) overlayPath() string { return filepath.Join(c.home(), "vector.config.toml") }

func (c *Codex) roleDir() string { return filepath.Join(c.home(), "agents") }

func (c *Codex) sidecarPath() string {
	return filepath.Join(config.Dir(), "harness-codex.json")
}

type codexSidecar struct {
	Roles []string `json:"roles"`
}

// Enable implements Adapter.
func (c *Codex) Enable() (Report, error) {
	rep := Report{Harness: c.Name()}

	// Refuse to clobber a foreign overlay.
	if data, err := os.ReadFile(c.overlayPath()); err == nil {
		if !strings.HasPrefix(string(data), codexMarker) {
			return rep, fmt.Errorf("%s exists and is not vector-managed; refusing to overwrite", c.overlayPath())
		}
	}

	baseURL := "http://" + c.cfg.Listen.OpenAI + "/v1"
	var b strings.Builder
	b.WriteString(codexMarker + "\n\n")
	b.WriteString("[model_providers.vector]\n")
	b.WriteString("name = \"Vector Router\"\n")
	fmt.Fprintf(&b, "base_url = %s\n", tomlString(baseURL))
	b.WriteString("wire_api = \"responses\"\n")
	b.WriteString("requires_openai_auth = false\n")
	b.WriteString("http_headers = { \"Authorization\" = \"Bearer vector-local\", \"X-Vector-Harness\" = \"codex\" }\n\n")

	var roleFiles []string
	for _, role := range c.cfg.RoleNames() {
		def := c.cfg.Roles[role]
		name := "vector-" + role
		desc := def.Description
		if desc == "" {
			desc = "Vector subagent " + name
		}
		fmt.Fprintf(&b, "[agents.%s]\n", name)
		fmt.Fprintf(&b, "description = %s\n", tomlString(desc))
		fmt.Fprintf(&b, "config_file = %s\n\n", tomlString(filepath.Join("agents", name+".toml")))

		rel := filepath.Join("agents", name+".toml")
		roleFiles = append(roleFiles, rel)
	}

	if err := os.MkdirAll(c.roleDir(), 0o700); err != nil {
		return rep, err
	}
	if err := writeFileAtomic(c.overlayPath(), []byte(b.String()), 0o600); err != nil {
		return rep, err
	}
	rep.Changed = true
	rep.add(c.overlayPath(), "wrote managed profile overlay")

	for _, rel := range roleFiles {
		role := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(rel), "vector-"), ".toml")
		body := codexRoleDoc(role)
		path := filepath.Join(c.home(), rel)
		if err := writeFileAtomic(path, []byte(body), 0o600); err != nil {
			return rep, err
		}
	}
	rep.add(c.roleDir(), fmt.Sprintf("installed %d agent role files", len(roleFiles)))

	sc := codexSidecar{Roles: roleFiles}
	data, _ := json.MarshalIndent(sc, "", "  ")
	if err := writeFileAtomic(c.sidecarPath(), data, 0o600); err != nil {
		return rep, err
	}
	return rep, nil
}

// Disable implements Adapter.
func (c *Codex) Disable() (Report, error) {
	rep := Report{Harness: c.Name()}
	data, err := os.ReadFile(c.overlayPath())
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return rep, err
	}
	if !strings.HasPrefix(string(data), codexMarker) {
		return rep, fmt.Errorf("%s is not vector-managed; leaving it in place", c.overlayPath())
	}
	if err := os.Remove(c.overlayPath()); err != nil {
		return rep, err
	}
	rep.Changed = true
	rep.add(c.overlayPath(), "removed managed profile overlay")

	var sc codexSidecar
	if raw, err := os.ReadFile(c.sidecarPath()); err == nil {
		_ = json.Unmarshal(raw, &sc)
	}
	for _, rel := range sc.Roles {
		path := filepath.Join(c.home(), rel)
		if err := os.Remove(path); err == nil {
			rep.add(path, "removed agent role file")
			rep.Changed = true
		}
	}
	_ = os.Remove(c.sidecarPath())
	return rep, nil
}

// Status implements Adapter.
func (c *Codex) Status() (Status, error) {
	data, err := os.ReadFile(c.overlayPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Status{Harness: c.Name(), Enabled: false, Detail: "no managed overlay"}, nil
		}
		return Status{Harness: c.Name()}, err
	}
	managed := strings.HasPrefix(string(data), codexMarker)
	return Status{
		Harness: c.Name(),
		Enabled: managed,
		Detail:  fmt.Sprintf("overlay=%s managed=%v", c.overlayPath(), managed),
		Info:    map[string]string{"overlay": c.overlayPath()},
	}, nil
}

func codexRoleDoc(role string) string {
	return fmt.Sprintf("model = %s\nmodel_provider = \"vector\"\nmodel_reasoning_effort = \"medium\"\n",
		tomlString("vector/"+role))
}

// tomlString quotes and escapes a string for TOML.
func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return `"` + s + `"`
}

// SortedRoles is a small helper for stable reports.
func SortedRoles(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
