package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionIDPrefersClaudeHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r.Header.Set("X-Claude-Code-Session-Id", "abc-123")
	if got := sessionID(r); got != "abc-123" {
		t.Fatalf("sessionID = %q, want abc-123", got)
	}
}

func TestSessionIDFallsBackToCodex(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("session-id", "codex-1")
	if got := sessionID(r); got != "codex-1" {
		t.Fatalf("sessionID = %q, want codex-1", got)
	}
}

func TestWorkingDir(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"...Working directory: /home/anunay/dev/space2\nIs directory a git repo: Yes"}]}`)
	if got := workingDir(body); got != "/home/anunay/dev/space2" {
		t.Fatalf("workingDir = %q, want /home/anunay/dev/space2", got)
	}
	if got := workingDir([]byte(`{}`)); got != "" {
		t.Fatalf("workingDir(empty) = %q, want empty", got)
	}
}

func TestPrepareBodyStripsContextManagement(t *testing.T) {
	body := []byte(`{"model":"vector-worker","context_management":{"edits":[{"type":"configuration_update"}]},"messages":[]}`)

	// Non-Anthropic upstream: drop context_management, rewrite the model.
	out, err := prepareBody(body, "deepseek/deepseek-v4.1-flash", false)
	if err != nil {
		t.Fatalf("prepareBody: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["context_management"]; ok {
		t.Fatal("context_management must be stripped for a non-Anthropic upstream")
	}
	var got string
	if err := json.Unmarshal(obj["model"], &got); err != nil || got != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("model = %q (err %v), want deepseek/deepseek-v4.1-flash", got, err)
	}
	if _, ok := obj["messages"]; !ok {
		t.Fatal("unrelated fields must be preserved")
	}

	// Native Anthropic upstream: keep it.
	out, _ = prepareBody(body, "claude-opus-5", true)
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["context_management"]; !ok {
		t.Fatal("context_management must be kept for a native Anthropic upstream")
	}
}

func TestIsSubagentRequest(t *testing.T) {
	cases := []struct {
		name   string
		header map[string]string
		body   string
		want   bool
	}{
		{"claude agent id header", map[string]string{"X-Claude-Code-Agent-Id": "af8f6ae412b34d703"}, `{}`, true},
		{"claude system string marker", nil, `{"system":"...cc_is_subagent=true..."}`, true},
		{"claude system array marker", nil, `{"system":[{"type":"text","text":"...cc_is_subagent=true..."}]}`, true},
		{"claude main thread", nil, `{"model":"claude-opus-5"}`, false},
		{"marker in messages only", nil, `{"system":"hi","messages":[{"role":"user","content":"cc_is_subagent=true"}]}`, false},
		{"codex root turn", map[string]string{"X-Codex-Turn-Metadata": `{"agent_name":"/root","session_id":"x"}`}, `{}`, false},
		{"codex root turn spaced", map[string]string{"X-Codex-Turn-Metadata": `{"agent_name": "/root"}`}, `{}`, false},
		{"codex named agent", map[string]string{"X-Codex-Turn-Metadata": `{"agent_name":"vector-worker"}`}, `{}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/messages", nil)
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			if got := isSubagentRequest(r, []byte(tc.body)); got != tc.want {
				t.Fatalf("isSubagentRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestModelsEndpointAdvertisesNativeDefaultModel(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range out.Data {
		if m.ID == "claude-opus-5" && m.OwnedBy == "anthropic-native" {
			found = true
		}
	}
	if !found {
		t.Fatalf("native default model not advertised: %+v", out.Data)
	}
}
