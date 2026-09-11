package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anunay999/vector/internal/config"
)

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Listen.Anthropic = "127.0.0.1:7331"
	cfg.Listen.OpenAI = "127.0.0.1:7331"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func TestClaudeRoundTripPreservesUserKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, ".claude"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))

	settings := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"env":{"KEEP":"yes"},"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewClaude(testCfg(t))
	if _, err := a.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	m, _ := readJSONMap(settings)
	env := stringMap(m, "env")
	if env["KEEP"] != "yes" {
		t.Fatal("enable clobbered an unrelated env key")
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:7331" {
		t.Fatalf("base url = %q", env["ANTHROPIC_BASE_URL"])
	}
	if m["model"] != "opus" {
		t.Fatal("enable clobbered the model setting")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "agents", "vector-worker.md")); err != nil {
		t.Fatalf("worker agent not installed: %v", err)
	}

	if _, err := a.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	m, _ = readJSONMap(settings)
	env = stringMap(m, "env")
	if env["KEEP"] != "yes" {
		t.Fatal("disable removed an unrelated env key")
	}
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Fatal("disable did not remove vector's base url")
	}
}

func TestClaudeStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, ".claude"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))
	a := NewClaude(testCfg(t))
	if st, _ := a.Status(); st.Enabled {
		t.Fatal("expected disabled before enable")
	}
	if _, err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(); !st.Enabled {
		t.Fatal("expected enabled after enable")
	}
}

func TestCodexNeverTouchesUserConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CODEX_HOME", filepath.Join(dir, ".codex"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))

	home := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	userCfg := filepath.Join(home, "config.toml")
	const original = "model = \"gpt-6-astra\"\n"
	if err := os.WriteFile(userCfg, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewCodex(testCfg(t))
	if _, err := a.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got, _ := os.ReadFile(userCfg)
	if string(got) != original {
		t.Fatal("codex user config was modified")
	}
	if _, err := os.Stat(filepath.Join(home, "vector.config.toml")); err != nil {
		t.Fatalf("managed overlay missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "vector-worker.toml")); err != nil {
		t.Fatalf("role file missing: %v", err)
	}

	if _, err := a.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "vector.config.toml")); !os.IsNotExist(err) {
		t.Fatal("managed overlay not removed")
	}
	got, _ = os.ReadFile(userCfg)
	if string(got) != original {
		t.Fatal("codex user config changed across disable")
	}
}

func TestClaudeAgentListAndRoute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, ".claude"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))

	agentsDir := filepath.Join(dir, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	scout := filepath.Join(agentsDir, "scout.md")
	if err := os.WriteFile(scout, []byte("---\nname: scout\ndescription: read-only recon\nmodel: opus\n---\n\nYou are scout.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewClaude(testCfg(t))
	list, err := a.Agents()
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	if len(list) != 1 || list[0].Name != "scout" || list[0].Model != "opus" || list[0].Routed {
		t.Fatalf("unexpected inventory: %+v", list)
	}

	if err := a.SetAgentModel("scout", "vector-reviewer", ""); err != nil {
		t.Fatalf("route: %v", err)
	}
	list, _ = a.Agents()
	if list[0].Model != "vector-reviewer" || !list[0].Routed {
		t.Fatalf("route did not apply: %+v", list[0])
	}
	data, _ := os.ReadFile(scout)
	if !strings.Contains(string(data), "You are scout.") {
		t.Fatal("route clobbered the agent body")
	}
}

func TestCodexAgentListAndRoute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CODEX_HOME", filepath.Join(dir, ".codex"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))
	home := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(filepath.Join(home, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[agents.worker]\ndescription = \"impl\"\nconfig_file = \"agents/worker.toml\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	role := filepath.Join(home, "agents", "worker.toml")
	if err := os.WriteFile(role, []byte("model = \"gpt-6-astra\"\nmodel_provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewCodex(testCfg(t))
	list, err := a.Agents()
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	if len(list) != 1 || list[0].Model != "gpt-6-astra" || list[0].Provider != "openai" {
		t.Fatalf("unexpected inventory: %+v", list)
	}

	if err := a.SetAgentModel("worker", "vector/worker", "vector"); err != nil {
		t.Fatalf("route: %v", err)
	}
	data, _ := os.ReadFile(role)
	got := string(data)
	if !strings.Contains(got, `model = "vector/worker"`) || !strings.Contains(got, `model_provider = "vector"`) {
		t.Fatalf("route did not apply:\n%s", got)
	}
}

func TestCodexRefusesForeignOverlay(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CODEX_HOME", filepath.Join(dir, ".codex"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))
	home := filepath.Join(dir, ".codex")
	_ = os.MkdirAll(home, 0o700)
	if err := os.WriteFile(filepath.Join(home, "vector.config.toml"), []byte("model = \"custom\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewCodex(testCfg(t))
	if _, err := a.Enable(); err == nil {
		t.Fatal("expected refusal to overwrite a foreign overlay")
	}
}

func TestClaudeEnableSetsToolSearchButRespectsUserValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, ".claude"))
	t.Setenv("VECTOR_CONFIG_DIR", filepath.Join(dir, "vectorcfg"))
	settings := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}

	// Fresh settings: vector sets ENABLE_TOOL_SEARCH=auto and removes it on off.
	if err := os.WriteFile(settings, []byte(`{"model":"opus[1m]"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewClaude(testCfg(t))
	if _, err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	m, _ := readJSONMap(settings)
	if got := stringMap(m, "env")["ENABLE_TOOL_SEARCH"]; got != "auto" {
		t.Fatalf("ENABLE_TOOL_SEARCH = %q, want auto", got)
	}
	st, _ := a.Status()
	if st.Info["tool_search"] != "auto" || st.Info["model"] != "opus[1m]" {
		t.Fatalf("status info = %v", st.Info)
	}
	if _, err := a.Disable(); err != nil {
		t.Fatal(err)
	}
	m, _ = readJSONMap(settings)
	if _, ok := stringMap(m, "env")["ENABLE_TOOL_SEARCH"]; ok {
		t.Fatal("disable did not remove vector's ENABLE_TOOL_SEARCH")
	}

	// A user-chosen value survives enable and disable.
	if err := os.WriteFile(settings, []byte(`{"env":{"ENABLE_TOOL_SEARCH":"true"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	m, _ = readJSONMap(settings)
	if got := stringMap(m, "env")["ENABLE_TOOL_SEARCH"]; got != "true" {
		t.Fatalf("user value clobbered: %q", got)
	}
	if _, err := a.Disable(); err != nil {
		t.Fatal(err)
	}
	m, _ = readJSONMap(settings)
	if got := stringMap(m, "env")["ENABLE_TOOL_SEARCH"]; got != "true" {
		t.Fatalf("disable removed the user's value: %q", got)
	}
}
