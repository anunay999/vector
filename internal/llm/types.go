// Package llm defines the canonical, provider-neutral representation of a model
// request and response. Every wire shape (Anthropic Messages, OpenAI Chat
// Completions, OpenAI Responses) encodes to and decodes from these types, so the
// router and providers never have to know which harness or upstream is involved.
package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Shape identifies a wire protocol.
type Shape string

const (
	ShapeAnthropic       Shape = "anthropic"
	ShapeOpenAIChat      Shape = "openai_chat"
	ShapeOpenAIResponses Shape = "openai_responses"
)

// Valid reports whether s is a known shape.
func (s Shape) Valid() bool {
	switch s {
	case ShapeAnthropic, ShapeOpenAIChat, ShapeOpenAIResponses:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (s Shape) String() string { return string(s) }

// Role is a normalized conversation role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentKind discriminates a Content block.
type ContentKind string

const (
	KindText       ContentKind = "text"
	KindThinking   ContentKind = "thinking"
	KindToolUse    ContentKind = "tool_use"
	KindToolResult ContentKind = "tool_result"
	KindImage      ContentKind = "image"
)

// Content is one block of a message. Only the fields relevant to Kind are set.
type Content struct {
	Kind ContentKind `json:"kind"`

	// KindText, KindThinking
	Text string `json:"text,omitempty"`

	// KindThinking
	Signature string `json:"signature,omitempty"`

	// KindToolUse
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// KindToolResult
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// KindImage
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// Text builds a text block.
func Text(s string) Content { return Content{Kind: KindText, Text: s} }

// Message is a single conversation turn.
type Message struct {
	Role    Role      `json:"role"`
	Content []Content `json:"content"`
}

// Tool declares a callable function.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ToolChoiceMode mirrors the union of Anthropic and OpenAI tool-choice modes.
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceAny      ToolChoiceMode = "any"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceTool     ToolChoiceMode = "tool"
)

// ToolChoice constrains tool selection.
type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode"`
	Name string         `json:"name,omitempty"`
}

// Reasoning captures a normalized reasoning/thinking budget.
type Reasoning struct {
	Enabled      bool   `json:"enabled"`
	Effort       string `json:"effort,omitempty"`        // minimal|low|medium|high|xhigh
	BudgetTokens int    `json:"budget_tokens,omitempty"` // Anthropic thinking budget
}

// Request is a canonical model request.
type Request struct {
	Model       string            `json:"model"`
	Shape       Shape             `json:"shape"`
	System      []Content         `json:"system,omitempty"`
	Messages    []Message         `json:"messages"`
	Tools       []Tool            `json:"tools,omitempty"`
	ToolChoice  *ToolChoice       `json:"tool_choice,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	TopP        *float64          `json:"top_p,omitempty"`
	Stop        []string          `json:"stop,omitempty"`
	Stream      bool              `json:"stream"`
	Reasoning   *Reasoning        `json:"reasoning,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`

	// Headers carries inbound HTTP headers relevant to routing (e.g. role tags).
	Headers map[string]string `json:"-"`
}

// Usage is normalized token accounting.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// Add accumulates other into u.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheWriteTokens += other.CacheWriteTokens
	u.ReasoningTokens += other.ReasoningTokens
}

// Total returns input+output tokens.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// Response is a canonical non-streaming model response.
type Response struct {
	ID         string    `json:"id"`
	Model      string    `json:"model"`
	Role       Role      `json:"role"`
	Content    []Content `json:"content"`
	StopReason string    `json:"stop_reason,omitempty"`
	Usage      Usage     `json:"usage"`
}

// SystemText concatenates all system blocks into one string.
func (r *Request) SystemText() string {
	var b strings.Builder
	for _, c := range r.System {
		if c.Kind == KindText || c.Kind == "" {
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// PromptText returns a best-effort concatenation of all text content, used for
// token estimation and classification. It is not sent anywhere.
func (r *Request) PromptText() string {
	var b strings.Builder
	b.WriteString(r.SystemText())
	for _, m := range r.Messages {
		for _, c := range m.Content {
			switch c.Kind {
			case KindText, KindThinking, KindToolResult:
				b.WriteString(c.Text)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// Validate performs cheap structural validation that is common to all shapes.
func (r *Request) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("llm: request model is required")
	}
	if len(r.Messages) == 0 && len(r.System) == 0 {
		return fmt.Errorf("llm: request must contain at least one message")
	}
	return nil
}
