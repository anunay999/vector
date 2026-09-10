package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if len(cfg.Roles) == 0 || len(cfg.Providers) == 0 {
		t.Fatal("default config missing roles/providers")
	}
}

func TestLoadExpandsEnvFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	body := `
version: 1
routing_enabled: true
providers:
  - id: openrouter
    type: openai_compatible
    base_url: https://openrouter.ai/api/v1
    api_key: ${OPENROUTER_API_KEY}
roles:
  worker:
    tier: cheap
    prefer: [openrouter/z-ai/glm-5.3-flash]
models:
  - id: openrouter/z-ai/glm-5.3-flash
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, EnvFileName), []byte("OPENROUTER_API_KEY=sk-or-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Ensure the process environment does not shadow the file.
	t.Setenv("OPENROUTER_API_KEY", "")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Providers[0].APIKey; got != "sk-or-test" {
		t.Fatalf("api key = %q, want sk-or-test", got)
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen.Anthropic == "" {
		t.Fatal("expected default listen address")
	}
}

func TestUnresolvedVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(`
version: 1
providers:
  - id: openrouter
    type: openai_compatible
    base_url: https://openrouter.ai/api/v1
    api_key: ${VECTOR_TEST_MISSING_KEY}
roles:
  worker: {tier: cheap, prefer: [openrouter/z-ai/glm-5.3-flash]}
models:
  - id: openrouter/z-ai/glm-5.3-flash
`), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("VECTOR_TEST_MISSING_KEY")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	missing := cfg.UnresolvedVars()
	if len(missing) != 1 || missing[0] != "VECTOR_TEST_MISSING_KEY" {
		t.Fatalf("missing = %v, want [VECTOR_TEST_MISSING_KEY]", missing)
	}
}

func TestValidateRejectsDuplicateProvider(t *testing.T) {
	cfg := Default()
	cfg.Providers = append(cfg.Providers, cfg.Providers[0])
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate provider error")
	}
}
