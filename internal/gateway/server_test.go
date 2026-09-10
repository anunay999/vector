package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anunay999/vector/internal/budget"
	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/telemetry"
)

type capturedRequest struct {
	path      string
	model     string
	auth      string
	xAPIKey   string
	anthropic string
}

func newTestServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.xAPIKey = r.Header.Get("X-Api-Key")
		cap.anthropic = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		var env map[string]json.RawMessage
		_ = json.Unmarshal(body, &env)
		_ = json.Unmarshal(env["model"], &cap.model)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Default()
	cfg.Providers = []config.Provider{
		{
			ID:               "openrouter",
			Type:             config.ProviderOpenAICompatible,
			BaseURL:          upstream.URL,
			AnthropicBaseURL: upstream.URL,
			APIKey:           "test-key",
		},
		{
			ID:           "anthropic-native",
			Type:         config.ProviderAnthropic,
			BaseURL:      upstream.URL,
			DefaultModel: "claude-opus-5",
			Native:       true,
		},
		{
			ID:      "openai-native",
			Type:    config.ProviderOpenAIResponses,
			BaseURL: upstream.URL,
			Native:  true,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	srv := New(cfg,
		telemetry.New(t.TempDir(), true),
		budget.New(0, nil, "downgrade", 4),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, cap
}

func TestAnthropicSubagentRoutesToCheapModel(t *testing.T) {
	ts, cap := newTestServer(t)
	body := `{"model":"vector-worker","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if cap.model != "z-ai/glm-5.3-flash" {
		t.Fatalf("upstream model = %q, want z-ai/glm-5.3-flash", cap.model)
	}
	if cap.path != "/v1/messages" {
		t.Fatalf("upstream path = %q", cap.path)
	}
	if cap.auth != "Bearer test-key" {
		t.Fatalf("auth = %q, want Bearer test-key", cap.auth)
	}
	if cap.anthropic != "2023-06-01" {
		t.Fatalf("anthropic-version not forwarded: %q", cap.anthropic)
	}
}

func TestAnthropicPrimaryPassthrough(t *testing.T) {
	ts, cap := newTestServer(t)
	body := `{"model":"claude-opus-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer subscription-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if cap.model != "claude-opus-5" {
		t.Fatalf("model = %q, want passthrough claude-opus-5", cap.model)
	}
	// Native provider must forward the inbound credential.
	if cap.auth != "Bearer subscription-token" {
		t.Fatalf("auth = %q, want inbound subscription token", cap.auth)
	}
}

func TestOpenAIChatRoutesToRole(t *testing.T) {
	ts, cap := newTestServer(t)
	body := `{"model":"vector/reviewer","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if cap.model != "moonshotai/kimi-k3" {
		t.Fatalf("model = %q, want moonshotai/kimi-k3", cap.model)
	}
}

func TestModelsDiscovery(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, m := range payload.Data {
		if m.ID == "vector-worker" {
			found = true
		}
	}
	if !found {
		t.Fatal("vector-worker not advertised in /v1/models")
	}
}

func TestInvalidJSONReturnsShapeError(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Type != "error" {
		t.Fatalf("expected anthropic error envelope, got %+v", payload)
	}
}

func TestCountTokensGoesNativeNotCheap(t *testing.T) {
	ts, cap := newTestServer(t)
	body := `{"model":"vector-worker","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(ts.URL+"/v1/messages/count_tokens", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if cap.path != "/v1/messages/count_tokens" {
		t.Fatalf("path = %q", cap.path)
	}
	if cap.model != "claude-opus-5" {
		t.Fatalf("count_tokens model = %q, want native default", cap.model)
	}
}

// TestFallbackOnUpstreamError verifies that a 5xx from the primary cheap model
// transparently retries the next candidate.
func TestFallbackOnUpstreamError(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env map[string]json.RawMessage
		_ = json.Unmarshal(body, &env)
		var model string
		_ = json.Unmarshal(env["model"], &model)
		seen = append(seen, model)
		if model == "z-ai/glm-5.3-flash" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Default()
	cfg.Providers = []config.Provider{
		{ID: "openrouter", Type: config.ProviderOpenAICompatible, BaseURL: upstream.URL, AnthropicBaseURL: upstream.URL, APIKey: "k"},
		{ID: "anthropic-native", Type: config.ProviderAnthropic, BaseURL: upstream.URL, DefaultModel: "claude-opus-5", Native: true},
		{ID: "openai-native", Type: config.ProviderOpenAIResponses, BaseURL: upstream.URL, DefaultModel: "gpt-6-astra", Native: true},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, telemetry.New(t.TempDir(), true), budget.New(0, nil, "downgrade", 4), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := `{"model":"vector-worker","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after fallback", resp.StatusCode)
	}
	if len(seen) < 2 {
		t.Fatalf("expected fallback attempt, saw %v", seen)
	}
	if seen[len(seen)-1] == "z-ai/glm-5.3-flash" {
		t.Fatalf("did not fall through: %v", seen)
	}
}
