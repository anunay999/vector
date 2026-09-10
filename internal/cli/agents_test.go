package cli

import (
	"testing"

	"github.com/anunay999/vector/internal/config"
)

func TestResolveAgentModel(t *testing.T) {
	cfg := config.Default()
	cases := []struct {
		harness, target string
		model, provider string
	}{
		{"claude-code", "scout", "vector-scout", ""},
		{"claude-code", "vector/reviewer", "vector-reviewer", ""},
		{"codex", "worker", "vector/worker", "vector"},
		{"claude-code", "openrouter/z-ai/glm-5.3-flash", "openrouter/z-ai/glm-5.3-flash", ""},
		{"codex", "openrouter/moonshotai/kimi-k3", "openrouter/moonshotai/kimi-k3", "openrouter"},
		{"claude-code", "opus", "opus", ""},
	}
	for _, c := range cases {
		model, provider, err := resolveAgentModel(cfg, c.harness, c.target)
		if err != nil {
			t.Fatalf("%s %s: %v", c.harness, c.target, err)
		}
		if model != c.model || provider != c.provider {
			t.Fatalf("%s %s = (%q,%q), want (%q,%q)", c.harness, c.target, model, provider, c.model, c.provider)
		}
	}
}
