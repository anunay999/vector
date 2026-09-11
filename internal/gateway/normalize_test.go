package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const toolSearchBody = `{
 "model":"vector-worker",
 "tools":[
  {"type":"tool_search_tool_regex_20251119","name":"tool_search_tool_regex"},
  {"name":"Read","description":"read a file","input_schema":{"type":"object"}},
  {"name":"mcp__linear__list_issues","defer_loading":true,"input_schema":{"type":"object","properties":{"limit":{"type":"integer","default":1000000}}}}
 ],
 "messages":[
  {"role":"user","content":"find issues"},
  {"role":"assistant","content":[
    {"type":"text","text":"searching"},
    {"type":"server_tool_use","id":"srvtoolu_1","name":"tool_search_tool_regex","input":{"pattern":"linear"}},
    {"type":"tool_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"tool_reference","tool_name":"mcp__linear__list_issues"}]},
    {"type":"tool_use","id":"toolu_1","name":"ToolSearch","input":{"query":"linear"}}
  ]},
  {"role":"user","content":[
    {"type":"tool_result","tool_use_id":"toolu_1","content":[
      {"type":"text","text":"Loaded 1 tool"},
      {"type":"tool_reference","tool_name":"mcp__linear__list_issues"}
    ]}
  ]}
 ]
}`

func TestPrepareBodyStripsToolSearchForNonAnthropic(t *testing.T) {
	out, err := prepareBody([]byte(toolSearchBody), "deepseek/deepseek-v4.1-flash", false)
	if err != nil {
		t.Fatalf("prepareBody: %v", err)
	}
	s := string(out)
	for _, bad := range []string{`"defer_loading"`, `tool_search_tool`, `"tool_reference"`, `"server_tool_use"`} {
		if strings.Contains(s, bad) {
			t.Fatalf("%s must not reach a non-Anthropic upstream:\n%s", bad, s)
		}
	}
	var obj struct {
		Tools    []map[string]json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if len(obj.Tools) != 2 {
		t.Fatalf("tools = %d, want 2 (server tool dropped, deferred tool kept in full)", len(obj.Tools))
	}
	// The deferred tool's schema must be forwarded byte-for-byte: a large
	// integer default must not turn into a float.
	if !strings.Contains(s, `"default":1000000`) {
		t.Fatalf("tool schema was re-encoded lossily:\n%s", s)
	}
	// The assistant turn keeps its text and client tool_use, loses the two
	// server-side blocks.
	var assistant []json.RawMessage
	if err := json.Unmarshal(obj.Messages[1].Content, &assistant); err != nil {
		t.Fatal(err)
	}
	if got := len(assistant); got != 2 {
		t.Fatalf("assistant blocks = %d, want 2", got)
	}
	// The tool_result keeps its text and gets a text block in place of the
	// reference, so the model still learns the tool name.
	if !strings.Contains(s, `Tool available: mcp__linear__list_issues`) {
		t.Fatalf("tool_reference was not rewritten to text:\n%s", s)
	}
	// String content is left alone.
	if string(obj.Messages[0].Content) != `"find issues"` {
		t.Fatalf("string content changed: %s", obj.Messages[0].Content)
	}
}

func TestPrepareBodyKeepsToolSearchForAnthropic(t *testing.T) {
	out, err := prepareBody([]byte(toolSearchBody), "claude-opus-5", true)
	if err != nil {
		t.Fatalf("prepareBody: %v", err)
	}
	s := string(out)
	for _, keep := range []string{`"defer_loading":true`, `tool_search_tool_regex_20251119`, `"tool_reference"`, `"server_tool_use"`} {
		if !strings.Contains(s, keep) {
			t.Fatalf("%s must be forwarded unchanged to Anthropic:\n%s", keep, s)
		}
	}
}

func TestStripBlocksNeverLeavesEmptyContent(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"x","name":"tool_search_tool_regex","input":{}}]}]}`
	out, err := prepareBody([]byte(body), "m2", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"content":[]`) {
		t.Fatalf("empty content array must be replaced with a placeholder:\n%s", out)
	}
	if !strings.Contains(string(out), `"type":"text"`) {
		t.Fatalf("placeholder text block missing:\n%s", out)
	}
}

func TestUpstreamHeadersFiltersAnthropicOnlyBetas(t *testing.T) {
	in := http.Header{}
	in.Add("anthropic-beta", "claude-code-20250219,advanced-tool-use-2025-11-20, context-1m-2025-08-07")
	in.Add("anthropic-beta", "tool-search-tool-2025-10-19")
	in.Set("anthropic-version", "2023-06-01")

	got := upstreamHeaders(in, false)
	if v := got.Get("anthropic-beta"); v != "claude-code-20250219,context-1m-2025-08-07" {
		t.Fatalf("anthropic-beta = %q", v)
	}
	if got.Get("anthropic-version") != "2023-06-01" {
		t.Fatal("unrelated header lost")
	}
	// The inbound header must not be mutated.
	if len(in.Values("anthropic-beta")) != 2 {
		t.Fatal("inbound headers were mutated")
	}
	if kept := upstreamHeaders(in, true); kept.Get("anthropic-beta") != in.Get("anthropic-beta") {
		t.Fatal("Anthropic upstream must receive betas unchanged")
	}
	// A header made only of Anthropic-only betas disappears entirely.
	only := http.Header{}
	only.Set("anthropic-beta", "advanced-tool-use-2025-11-20")
	if v := upstreamHeaders(only, false).Values("anthropic-beta"); len(v) != 0 {
		t.Fatalf("expected header removed, got %v", v)
	}
}

func TestHasToolSearch(t *testing.T) {
	if !hasToolSearch([]byte(toolSearchBody)) {
		t.Fatal("tool search body not detected")
	}
	if hasToolSearch([]byte(`{"model":"m","tools":[{"name":"Read"}]}`)) {
		t.Fatal("plain body misdetected")
	}
}
