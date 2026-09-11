package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Anthropic-only request features that third-party providers reject or
// misread. Claude Code emits these on a first-party-shaped request whether or
// not the upstream is Anthropic, so a cheap leaf would 400 without this pass.
//
//   - context_management: mid-conversation effort updates (configuration_update).
//   - Tool search (ENABLE_TOOL_SEARCH in Claude Code): tools flagged
//     defer_loading, the server-side tool_search_tool_* tool, tool_reference
//     blocks the client returns from its ToolSearch tool, and the
//     server_tool_use / tool_search_tool_result blocks that appear in history.
//     Only api.anthropic.com expands these; everyone else needs the full tool
//     list and plain text where the references were.
const (
	fieldContextManagement = "context_management"
	fieldDeferLoading      = "defer_loading"
	toolSearchTypePrefix   = "tool_search_tool"
	blockToolReference     = "tool_reference"
	blockServerToolUse     = "server_tool_use"
	blockToolSearchResult  = "tool_search_tool_result"
)

// anthropicOnlyBetas are anthropic-beta flags that only api.anthropic.com
// understands. They are removed from the forwarded header for other upstreams
// so a provider that validates betas does not reject the request.
var anthropicOnlyBetas = map[string]bool{
	"advanced-tool-use-2025-11-20":  true,
	"tool-search-tool-2025-10-19":   true,
	"context-management-2025-06-27": true,
}

// prepareBody replaces the top-level "model" field and, for non-Anthropic
// upstreams, strips the Anthropic-only features listed above. The body is edited
// field by field through json.RawMessage so untouched values (tool inputs,
// numbers, unicode) are forwarded byte-for-byte.
func prepareBody(body []byte, model string, keepAnthropicExtras bool) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	obj["model"] = raw
	if !keepAnthropicExtras {
		delete(obj, fieldContextManagement)
		if tools, ok := obj["tools"]; ok {
			if out, changed := stripToolSearchTools(tools); changed {
				obj["tools"] = out
			}
		}
		if msgs, ok := obj["messages"]; ok {
			if out, changed := stripToolSearchBlocks(msgs); changed {
				obj["messages"] = out
			}
		}
	}
	return json.Marshal(obj)
}

// stripToolSearchTools removes defer_loading from every tool and drops the
// server-side tool search tool. It reports whether anything changed.
func stripToolSearchTools(raw json.RawMessage) (json.RawMessage, bool) {
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return raw, false
	}
	changed := false
	out := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(t, &obj); err != nil {
			out = append(out, t)
			continue
		}
		if typ := rawString(obj["type"]); strings.HasPrefix(typ, toolSearchTypePrefix) {
			changed = true
			continue
		}
		if _, ok := obj[fieldDeferLoading]; ok {
			delete(obj, fieldDeferLoading)
			changed = true
			if enc, err := json.Marshal(obj); err == nil {
				t = enc
			}
		}
		out = append(out, t)
	}
	if !changed {
		return raw, false
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return raw, false
	}
	return enc, true
}

// stripToolSearchBlocks rewrites message content so no tool-search block
// reaches a non-Anthropic upstream. tool_reference blocks (also nested inside
// tool_result content) become plain text naming the tool, so the model still
// learns which tools the search surfaced; server_tool_use and
// tool_search_tool_result blocks are dropped. A message whose content would
// become empty gets a single placeholder text block, since providers reject
// empty content.
func stripToolSearchBlocks(raw json.RawMessage) (json.RawMessage, bool) {
	var msgs []json.RawMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return raw, false
	}
	changed := false
	for i, m := range msgs {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(m, &obj); err != nil {
			continue
		}
		content, ok := obj["content"]
		if !ok || len(content) == 0 || content[0] != '[' {
			continue
		}
		out, c := stripBlocks(content, true)
		if !c {
			continue
		}
		obj["content"] = out
		if enc, err := json.Marshal(obj); err == nil {
			msgs[i] = enc
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	enc, err := json.Marshal(msgs)
	if err != nil {
		return raw, false
	}
	return enc, true
}

// stripBlocks processes one content array. When top is true the array is a
// message's content and may contain tool_result blocks whose own content is
// recursed into.
func stripBlocks(raw json.RawMessage, top bool) (json.RawMessage, bool) {
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return raw, false
	}
	changed := false
	out := make([]json.RawMessage, 0, len(blocks))
	for _, b := range blocks {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(b, &obj); err != nil {
			out = append(out, b)
			continue
		}
		switch rawString(obj["type"]) {
		case blockToolReference:
			name := rawString(obj["tool_name"])
			if name == "" {
				name = rawString(obj["name"])
			}
			out = append(out, textBlock("Tool available: "+name))
			changed = true
		case blockServerToolUse, blockToolSearchResult:
			changed = true
		case "tool_result":
			if !top {
				out = append(out, b)
				continue
			}
			inner, ok := obj["content"]
			if !ok || len(inner) == 0 || inner[0] != '[' {
				out = append(out, b)
				continue
			}
			rewritten, c := stripBlocks(inner, false)
			if !c {
				out = append(out, b)
				continue
			}
			obj["content"] = rewritten
			if enc, err := json.Marshal(obj); err == nil {
				b = enc
			}
			out = append(out, b)
			changed = true
		default:
			out = append(out, b)
		}
	}
	if !changed {
		return raw, false
	}
	if len(out) == 0 {
		out = append(out, textBlock("(tool search result omitted for this upstream)"))
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return raw, false
	}
	return enc, true
}

func textBlock(s string) json.RawMessage {
	enc, _ := json.Marshal(map[string]string{"type": "text", "text": s})
	return enc
}

// rawString decodes a JSON string value, returning "" for anything else.
func rawString(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// upstreamHeaders returns the inbound headers to forward. For non-Anthropic
// upstreams the anthropic-beta list loses the flags only api.anthropic.com
// accepts; everything else is passed through untouched.
func upstreamHeaders(in http.Header, keepAnthropicExtras bool) http.Header {
	if keepAnthropicExtras {
		return in
	}
	vals := in.Values("anthropic-beta")
	if len(vals) == 0 {
		return in
	}
	var kept []string
	changed := false
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if anthropicOnlyBetas[part] {
				changed = true
				continue
			}
			kept = append(kept, part)
		}
	}
	if !changed {
		return in
	}
	out := in.Clone()
	out.Del("anthropic-beta")
	if len(kept) > 0 {
		out.Set("anthropic-beta", strings.Join(kept, ","))
	}
	return out
}

// hasToolSearch reports whether a request body uses Claude Code's tool search
// (any deferred tool or the server tool), for telemetry.
func hasToolSearch(body []byte) bool {
	return bytes.Contains(body, []byte(`"`+fieldDeferLoading+`"`)) ||
		bytes.Contains(body, []byte(`"`+toolSearchTypePrefix))
}
