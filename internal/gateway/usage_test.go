package gateway

import (
	"testing"
)

func TestExtractUsageAnthropicJSON(t *testing.T) {
	body := []byte(`{"id":"msg_1","usage":{"input_tokens":120,"output_tokens":45,"cache_read_input_tokens":1000}}`)
	u := extractUsage("application/json", body)
	if u.InputTokens != 120 || u.OutputTokens != 45 || u.CacheReadTokens != 1000 {
		t.Fatalf("got %+v", u)
	}
}

func TestExtractUsageOpenAIChatJSON(t *testing.T) {
	body := []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":20}}`)
	u := extractUsage("application/json", body)
	if u.InputTokens != 10 || u.OutputTokens != 20 {
		t.Fatalf("got %+v", u)
	}
}

func TestExtractUsageAnthropicSSE(t *testing.T) {
	body := []byte(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"usage":{"input_tokens":200,"cache_read_input_tokens":50}}}` + "\n\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","usage":{"output_tokens":33}}` + "\n\n" +
			"data: [DONE]\n\n")
	u := extractUsage("text/event-stream", body)
	if u.InputTokens != 200 || u.OutputTokens != 33 || u.CacheReadTokens != 50 {
		t.Fatalf("got %+v", u)
	}
}

func TestExtractUsageOpenAIResponsesSSE(t *testing.T) {
	body := []byte("event: response.completed\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":9}}}` + "\n\n")
	u := extractUsage("text/event-stream", body)
	if u.InputTokens != 7 || u.OutputTokens != 9 {
		t.Fatalf("got %+v", u)
	}
}

func TestCaptureWriterKeepsTail(t *testing.T) {
	c := newCaptureWriter(nil, 10)
	c.append([]byte("0123456789ABC"))
	// The writer is only used for its buffer in this test.
	got := string(c.bytes())
	if got != "3456789ABC" {
		t.Fatalf("tail = %q, want last 10 bytes", got)
	}
}
