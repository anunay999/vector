package router

import (
	"testing"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
	"github.com/anunay999/vector/internal/registry"
)

func newRouter(t *testing.T) *Router {
	t.Helper()
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	return New(cfg, registry.New(cfg))
}

func TestRouteVirtualWorkerToCheapModel(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Harness: "claude-code", Shape: llm.ShapeAnthropic, Model: "vector-worker"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "openrouter" {
		t.Fatalf("provider = %q, want openrouter", d.Provider.ID)
	}
	if d.UpstreamModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("upstream = %q, want deepseek/deepseek-v4.1-flash", d.UpstreamModel)
	}
	if d.Translate {
		t.Fatal("expected no translation: openrouter has an anthropic base URL")
	}
	if !d.IsSubagent {
		t.Fatal("expected subagent decision")
	}
}

func TestRouteVirtualReviewerToKimi(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Shape: llm.ShapeOpenAIChat, Model: "vector/reviewer"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.UpstreamModel != "moonshotai/kimi-k3" {
		t.Fatalf("upstream = %q, want moonshotai/kimi-k3", d.UpstreamModel)
	}
	if d.UpstreamShape != llm.ShapeOpenAIChat {
		t.Fatalf("shape = %s, want openai_chat", d.UpstreamShape)
	}
}

func TestRouteNativePrimaryPassthrough(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Harness: "claude-code", Shape: llm.ShapeAnthropic, Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "anthropic-native" {
		t.Fatalf("provider = %q, want anthropic-native", d.Provider.ID)
	}
	if d.UpstreamModel != "claude-opus-5" {
		t.Fatalf("model = %q, want passthrough", d.UpstreamModel)
	}
	if d.Translate {
		t.Fatal("native anthropic passthrough must not translate")
	}
}

func TestModelMapRedirectsNativeModel(t *testing.T) {
	r := newRouter(t)
	r.cfg.ModelMap = []config.ModelRule{{From: "claude-opus-5", To: "openrouter/deepseek/deepseek-v4.1-flash"}}
	d, err := r.Route(Input{Harness: "claude-code", Shape: llm.ShapeAnthropic, Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "openrouter" || d.UpstreamModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("model_map gave %s/%s, want openrouter/deepseek/deepseek-v4.1-flash", d.Provider.ID, d.UpstreamModel)
	}
}

func TestModelMapGlobMatches(t *testing.T) {
	r := newRouter(t)
	r.cfg.ModelMap = []config.ModelRule{{From: "claude-opus*", To: "openrouter/moonshotai/kimi-k3"}}
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "claude-opus-9-future"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.UpstreamModel != "moonshotai/kimi-k3" {
		t.Fatalf("upstream = %q, want moonshotai/kimi-k3", d.UpstreamModel)
	}
}

// A structurally detected subagent must route to the cheap worker role even
// though its requested model is a frontier Claude id.
func TestSubagentSignalRoutesToWorker(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{
		Harness: "claude-code", Shape: llm.ShapeAnthropic, Model: "claude-sonnet-5",
		IsSubagent: true,
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Role != "worker" {
		t.Fatalf("role = %q, want worker", d.Role)
	}
	if d.Provider.ID != "openrouter" {
		t.Fatalf("provider = %q, want openrouter (cheap)", d.Provider.ID)
	}
	if !d.IsSubagent {
		t.Fatal("expected subagent decision")
	}
}

func TestRouteEscalateKeepsRequestedNativeModel(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "vector-escalate"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "anthropic-native" {
		t.Fatalf("provider = %q, want anthropic-native", d.Provider.ID)
	}
}

func TestRouteExplicitRegistryModel(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "openrouter/deepseek/deepseek-v4.1-flash"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "openrouter" || d.UpstreamModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("got %s / %s", d.Provider.ID, d.UpstreamModel)
	}
}

func TestRouteRoleHeader(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{
		Shape:   llm.ShapeAnthropic,
		Model:   "claude-opus-5",
		Headers: map[string]string{"X-Vector-Role": "scout"},
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Role != "scout" {
		t.Fatalf("role = %q, want scout", d.Role)
	}
	if d.Provider.ID != "openrouter" {
		t.Fatalf("provider = %q, want openrouter", d.Provider.ID)
	}
}

func TestRouteDisabledIsNative(t *testing.T) {
	r := newRouter(t)
	r.cfg.RoutingEnabled = false
	d, err := r.Route(Input{Shape: llm.ShapeAnthropic, Model: "vector-worker"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Provider.ID != "anthropic-native" {
		t.Fatalf("provider = %q, want native passthrough when disabled", d.Provider.ID)
	}
}

func TestRouteSubagentSignal(t *testing.T) {
	r := newRouter(t)
	d, err := r.Route(Input{
		Shape:      llm.ShapeAnthropic,
		Model:      "claude-haiku",
		IsSubagent: true,
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if d.Role != "worker" {
		t.Fatalf("role = %q, want worker", d.Role)
	}
}
