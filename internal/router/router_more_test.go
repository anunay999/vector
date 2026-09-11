package router

import (
	"testing"

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

// Agent mode, automatic: a structurally detected subagent routes to the worker
// agent when subagents.route is on (the default).
func TestSubagentFlagRoutesToWorker(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Harness: "codex", Shape: llm.ShapeAnthropic, Model: "claude-haiku", IsSubagent: true})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Role != "worker" {
		t.Fatalf("role = %q, want worker", d.Role)
	}
	if d.Provider.ID != "openrouter" {
		t.Fatalf("provider = %q, want openrouter", d.Provider.ID)
	}
}

// Turning the flag off makes a detected subagent a plain subscription
// passthrough.
func TestSubagentFlagOffPassesThrough(t *testing.T) {
	r := newRouter(t)
	off := false
	r.cfg.Subagents.Route = &off
	d, err := r.Route(Input{Harness: "codex", Shape: llm.ShapeAnthropic, Model: "claude-haiku", IsSubagent: true})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "anthropic-native" {
		t.Fatalf("provider = %q, want anthropic-native", d.Provider.ID)
	}
}

// Model mode: an explicit registry model is honored, never swapped.
func TestExplicitModelIsHonored(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "openrouter/moonshotai/kimi-k3"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.UpstreamModel != "moonshotai/kimi-k3" {
		t.Fatalf("explicit model was hijacked: %q", d.UpstreamModel)
	}
}
