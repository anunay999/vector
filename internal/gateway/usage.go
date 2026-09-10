package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/anunay999/vector/internal/llm"
)

// captureWriter tees response bytes to a bounded buffer so usage can be
// extracted after the stream completes, without holding the whole response.
type captureWriter struct {
	http.ResponseWriter
	buf []byte
	max int
}

func newCaptureWriter(w http.ResponseWriter, max int) *captureWriter {
	return &captureWriter{ResponseWriter: w, max: max}
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.append(p)
	return c.ResponseWriter.Write(p)
}

// Flush forwards to the underlying writer when it supports flushing.
func (c *captureWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *captureWriter) append(p []byte) {
	if c.max <= 0 {
		return
	}
	if len(p) >= c.max {
		c.buf = append(c.buf[:0], p[len(p)-c.max:]...)
		return
	}
	if len(c.buf)+len(p) > c.max {
		drop := len(c.buf) + len(p) - c.max
		c.buf = append(c.buf[:0], c.buf[drop:]...)
	}
	c.buf = append(c.buf, p...)
}

func (c *captureWriter) bytes() []byte { return c.buf }

// usageJSON is the union of token-accounting fields across shapes.
type usageJSON struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	PromptTokens             int `json:"prompt_tokens"`
	CompletionTokens         int `json:"completion_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	PromptTokensDetails      *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u usageJSON) toUsage() llm.Usage {
	in := u.InputTokens
	if in == 0 {
		in = u.PromptTokens
	}
	out := u.OutputTokens
	if out == 0 {
		out = u.CompletionTokens
	}
	cache := u.CacheReadInputTokens
	if cache == 0 && u.PromptTokensDetails != nil {
		cache = u.PromptTokensDetails.CachedTokens
	}
	return llm.Usage{
		InputTokens:      in,
		OutputTokens:     out,
		CacheReadTokens:  cache,
		CacheWriteTokens: u.CacheCreationInputTokens,
	}
}

// extractUsage parses token accounting out of a response body, handling plain
// JSON and SSE for all three shapes. Returns a zero Usage when nothing is found.
func extractUsage(contentType string, body []byte) llm.Usage {
	if strings.Contains(contentType, "text/event-stream") || looksLikeSSE(body) {
		return usageFromSSE(body)
	}
	return usageFromJSON(body)
}

func looksLikeSSE(body []byte) bool {
	return strings.HasPrefix(strings.TrimSpace(string(body)), "event:") ||
		strings.HasPrefix(strings.TrimSpace(string(body)), "data:")
}

func usageFromJSON(body []byte) llm.Usage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return llm.Usage{}
	}
	if raw, ok := m["usage"]; ok {
		var u usageJSON
		if json.Unmarshal(raw, &u) == nil {
			return u.toUsage()
		}
	}
	// Anthropic message_start nests usage under "message".
	if raw, ok := m["message"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil {
			if ur, ok := nested["usage"]; ok {
				var u usageJSON
				if json.Unmarshal(ur, &u) == nil {
					return u.toUsage()
				}
			}
		}
	}
	// OpenAI Responses nests usage under "response".
	if raw, ok := m["response"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil {
			if ur, ok := nested["usage"]; ok {
				var u usageJSON
				if json.Unmarshal(ur, &u) == nil {
					return u.toUsage()
				}
			}
		}
	}
	return llm.Usage{}
}

func usageFromSSE(body []byte) llm.Usage {
	var acc llm.Usage
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		u := usageFromJSON([]byte(payload))
		if u.InputTokens > 0 {
			acc.InputTokens = u.InputTokens
		}
		if u.OutputTokens > acc.OutputTokens {
			acc.OutputTokens = u.OutputTokens
		}
		if u.CacheReadTokens > 0 {
			acc.CacheReadTokens = u.CacheReadTokens
		}
		if u.CacheWriteTokens > 0 {
			acc.CacheWriteTokens = u.CacheWriteTokens
		}
	}
	return acc
}
