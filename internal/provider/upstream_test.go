package provider

import (
	"net/http"
	"testing"
)

func TestJoinURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"https://openrouter.ai/api/v1", "/v1/messages", "https://openrouter.ai/api/v1/messages"},
		{"https://api.anthropic.com", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"https://api.openai.com/v1", "/v1/responses", "https://api.openai.com/v1/responses"},
		{"http://127.0.0.1:9000", "/v1/chat/completions", "http://127.0.0.1:9000/v1/chat/completions"},
		{"https://openrouter.ai/api/v1/", "/v1/messages", "https://openrouter.ai/api/v1/messages"},
	}
	for _, c := range cases {
		if got := JoinURL(c.base, c.path); got != c.want {
			t.Errorf("JoinURL(%q,%q)=%q want %q", c.base, c.path, got, c.want)
		}
	}
}

func TestApplyAuthConfiguredKey(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://x", nil)
	applyAuth(req, http.Header{"Authorization": {"Bearer inbound"}}, Target{ID: "openrouter", Type: "openai_compatible", APIKey: "sk-test"})
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("auth = %q, want configured key", got)
	}
}

func TestApplyAuthNativeForwardsInbound(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://x", nil)
	applyAuth(req, http.Header{"Authorization": {"Bearer plan"}}, Target{ID: "anthropic-native", Type: "anthropic", Native: true})
	if got := req.Header.Get("Authorization"); got != "Bearer plan" {
		t.Fatalf("auth = %q, want inbound plan token", got)
	}
}

func TestHopByHopStripsContentEncoding(t *testing.T) {
	if !isHopByHop("Content-Encoding") {
		t.Fatal("content-encoding must be stripped: transport may have decompressed the body")
	}
}
