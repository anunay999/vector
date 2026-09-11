package gateway

import (
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

func TestIsSubagentRequest(t *testing.T) {
	cases := []struct {
		name   string
		header map[string]string
		body   string
		want   bool
	}{
		{"claude agent id header", map[string]string{"X-Claude-Code-Agent-Id": "af8f6ae412b34d703"}, `{}`, true},
		{"claude body marker", nil, `{"system":"...cc_is_subagent=true..."}`, true},
		{"claude main thread", nil, `{"model":"claude-opus-5"}`, false},
		{"codex root turn", map[string]string{"X-Codex-Turn-Metadata": `{"agent_name":"/root","session_id":"x"}`}, `{}`, false},
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
