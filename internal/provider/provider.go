package provider

import (
	"context"
	"time"

	"github.com/KingImperio/oracule-cli/internal/config"
)

// New returns a provider based on the configuration.
// Routes to the correct adapter: OpenAI-compatible, Anthropic, or Google.
func New(cfg config.ProviderConfig) (Provider, error) {
	switch cfg.Provider {
	case "openai", "ollama", "groq", "together", "deepseek", "openrouter", "mistral", "codestral":
		return newOpenAI(cfg)
	case "anthropic", "claude":
		return newAnthropic(cfg)
	case "google", "gemini":
		return newGoogle(cfg)
	default:
		// Default to OpenAI-compatible — covers 50+ providers.
		return newOpenAI(cfg)
	}
}

// Provider is the unified interface all LLM backends must implement.
type Provider interface {
	// Stream returns a streaming response for a chat completion request.
	Stream(ctx context.Context, req StreamRequest) (Stream, error)

	// Complete is a convenience wrapper for non-streaming calls.
	Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error)

	// ModelID returns the currently configured model identifier.
	ModelID() string
}

// StreamRequest is the normalized request shape across providers.
type StreamRequest struct {
	Model       string
	System      string
	Messages    []Message
	MaxTokens   int
	Temperature float64
	Tools       []ToolDef // optional; provider may ignore
}

// CompleteRequest is the normalized non-streaming request.
type CompleteRequest struct {
	Model       string
	System      string
	Messages    []Message
	MaxTokens   int
	Temperature float64
}

// CompleteResponse is a non-streaming completion result.
type CompleteResponse struct {
	Text      string
	Usage     Usage
	ModelID   string
	CostUSD   float64
}

// Message is the normalized chat message shape (mirrors models.Message but
// avoids circular imports).
type Message struct {
	Role    string // "system", "user", "assistant", "tool"
	Content []ContentPart
}

// ContentPart represents one unit of message content.
type ContentPart struct {
	Type string // "text", "tool_use", "tool_result"
	Text string
	// Tool fields
	ToolCallID string
	ToolName   string
	ToolInput  map[string]any
	ToolOutput any
	ToolIsError bool
}

// Usage captures per-request token accounting.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Stream is a handle on a streaming response.
type Stream interface {
	// Chunks returns a receive-only channel of delta events.
	Chunks() <-chan Delta

	// Close releases any resources held by the stream.
	Close() error
}

// Delta is one incremental update from the model.
type Delta interface {
	// OneOf: TextDelta | ReasoningDelta | ToolCallStart | ToolCallEnd | StreamDone
}

// TextDelta carries a text fragment.
type TextDelta struct{ Text string }

// ReasoningDelta carries a reasoning/thinking fragment.
type ReasoningDelta struct{ Text string }

// ToolCallStart marks the beginning of a tool call from the model.
type ToolCallStart struct {
	ID       string
	Name     string
	Arguments map[string]any
}

// ToolCallEnd carries the result of a completed tool call.
type ToolCallEnd struct {
	ID       string
	Name     string
	Output   any
	IsError  bool
	Duration time.Duration
}

// StreamDone is the terminal event for a stream.
type StreamDone struct {
	Usage      Usage
	CostUSD    float64
	StopReason string // "stop", "length", "tool_calls", "content_filter", etc.
}

// ToolDef is the normalized tool definition shape.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}
