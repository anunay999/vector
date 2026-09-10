package router

import (
	"testing"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
)

func TestVirtualEscalateUsesNativeDefaultModel(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "vector-escalate"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "anthropic-native" {
		t.Fatalf("provider = %q", d.Provider.ID)
	}
	if d.UpstreamModel != "claude-opus-5" {
		t.Fatalf("upstream model = %q, want the native default (must not forward the virtual name)", d.UpstreamModel)
	}
}

func TestPolicyOverrideForSubagent(t *testing.T) {
	r := newRouter(t)
	// Add a harness-scoped policy that sends codex subagents to the reviewer.
	r.cfg.Policies = append([]config.Policy{{
		Match: config.Match{Harness: "codex", Traffic: "subagent"},
		Route: "reviewer",
	}}, r.cfg.Policies...)

	d, err := r.Route(Input{Harness: "codex", Shape: llm.ShapeAnthropic, Model: "claude-haiku", IsSubagent: true, SubagentSignal: true})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Role != "reviewer" {
		t.Fatalf("role = %q, want reviewer from policy", d.Role)
	}
}

func TestFallbackCandidatesArePopulated(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Harness: "claude-code", Shape: llm.ShapeAnthropic, Model: "vector-worker"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(d.Candidates) == 0 {
		t.Fatal("expected fallback candidates")
	}
	// The primary is DeepSeek V4.1 Flash; a candidate should be the next worker
	// preference (GLM).
	found := false
	for _, c := range d.Candidates {
		if c.UpstreamModel == "z-ai/glm-5.3-flash" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected glm fallback, got %+v", d.Candidates)
	}
}

func TestExplicitModelBypassesPolicy(t *testing.T) {
	r := newRouter(t)
	r.cfg.Policies = []config.Policy{{Match: config.Match{Traffic: "primary"}, Route: "architect"}}
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "openrouter/moonshotai/kimi-k3"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.UpstreamModel != "moonshotai/kimi-k3" {
		t.Fatalf("explicit model was hijacked: %q", d.UpstreamModel)
	}
}
