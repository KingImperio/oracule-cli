package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"

	"github.com/KingImperio/oracule-cli/internal/config"
)

func newOpenAI(cfg config.ProviderConfig) (Provider, error) {
	clientCfg := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		clientCfg.BaseURL = cfg.BaseURL
	}
	model := cfg.Model
	if model == "" {
		model = "gpt-4o"
	}
	return &openaiProvider{
		client: openai.NewClientWithConfig(clientCfg),
		model:  model,
	}, nil
}

type openaiProvider struct {
	client *openai.Client
	model  string
}

func (p *openaiProvider) ModelID() string {
	return p.model
}

func (p *openaiProvider) Stream(ctx context.Context, req StreamRequest) (Stream, error) {
	oaiReq := openai.ChatCompletionRequest{
		Model:       p.model,
		Messages:    toOpenAIMessages(req.Messages, req.System),
		MaxTokens:   req.MaxTokens,
		Temperature: float32(req.Temperature),
		Stream:      true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
	}
	if len(req.Tools) > 0 {
		oaiReq.Tools = toOpenAITools(req.Tools)
	}

	stream, err := p.client.CreateChatCompletionStream(ctx, oaiReq)
	if err != nil {
		return nil, fmt.Errorf("openai stream: %w", err)
	}

	s := &openaiStream{
		stream:    stream,
		toolAccum: make(map[int]*accumulatedToolCall),
	}
	return s, nil
}

func (p *openaiProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	oaiReq := openai.ChatCompletionRequest{
		Model:       p.model,
		Messages:    toOpenAIMessages(req.Messages, req.System),
		MaxTokens:   req.MaxTokens,
		Temperature: float32(req.Temperature),
	}

	resp, err := p.client.CreateChatCompletion(ctx, oaiReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("openai complete: %w", err)
	}

	text := ""
	if len(resp.Choices) > 0 {
		text = resp.Choices[0].Message.Content
	}

	return CompleteResponse{
		Text: text,
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
		ModelID: p.model,
	}, nil
}

// ---------------------------------------------------------------------------
// OpenAI streaming
// ---------------------------------------------------------------------------

type accumulatedToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

type openaiStream struct {
	stream    *openai.ChatCompletionStream
	ch        chan Delta
	once      sync.Once
	closeOnce sync.Once
	toolAccum map[int]*accumulatedToolCall
}

func (s *openaiStream) Chunks() <-chan Delta {
	s.once.Do(func() {
		s.ch = make(chan Delta)
		go s.readLoop()
	})
	return s.ch
}

func (s *openaiStream) Close() error {
	s.closeOnce.Do(func() {
		s.stream.Close()
	})
	return nil
}

func (s *openaiStream) readLoop() {
	defer s.closeOnce.Do(func() {
		s.stream.Close()
	})
	defer close(s.ch)

	var finishReason string

	for {
		resp, err := s.stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return
		}

		// Track usage from the final chunk when stream_options.include_usage is set.
		if resp.Usage != nil {
			// This is the final chunk; continue processing so we also capture
			// the finish_reason below.
		}

		if len(resp.Choices) == 0 {
			continue
		}
		choice := resp.Choices[0]

		if choice.FinishReason != "" {
			finishReason = string(choice.FinishReason)
		}

		delta := choice.Delta

		// Text content
		if delta.Content != "" {
			s.ch <- TextDelta{Text: delta.Content}
		}

		// Reasoning content (DeepSeek, o1, etc.)
		if delta.ReasoningContent != "" {
			s.ch <- ReasoningDelta{Text: delta.ReasoningContent}
		}

		// Tool call deltas — accumulate across chunks
		for _, tc := range delta.ToolCalls {
			var idx int
			if tc.Index != nil {
				idx = *tc.Index
			}
			acc, ok := s.toolAccum[idx]
			if !ok {
				acc = &accumulatedToolCall{}
				s.toolAccum[idx] = acc
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			acc.Arguments.WriteString(tc.Function.Arguments)
		}
	}

	// Emit all accumulated tool calls
	for _, acc := range s.toolAccum {
		var args map[string]any
		if acc.Arguments.Len() > 0 {
			_ = json.Unmarshal([]byte(acc.Arguments.String()), &args)
		}
		s.ch <- ToolCallStart{
			ID:        acc.ID,
			Name:      acc.Name,
			Arguments: args,
		}
	}

	// Terminal event
	s.ch <- StreamDone{
		StopReason: finishReason,
	}
}

// ---------------------------------------------------------------------------
// Type converters
// ---------------------------------------------------------------------------

func toOpenAIMessages(msgs []Message, system string) []openai.ChatCompletionMessage {
	var out []openai.ChatCompletionMessage

	if system != "" {
		out = append(out, openai.ChatCompletionMessage{
			Role:    "system",
			Content: system,
		})
	}

	for _, m := range msgs {
		oaiMsg := openai.ChatCompletionMessage{}

		switch m.Role {
		case "system":
			oaiMsg.Role = "system"
		case "user":
			oaiMsg.Role = "user"
		case "assistant":
			oaiMsg.Role = "assistant"
		case "tool":
			oaiMsg.Role = "tool"
		default:
			oaiMsg.Role = "user"
		}

		// Build content from parts
		var textParts []string
		var hasToolCalls bool

		for _, cp := range m.Content {
			switch cp.Type {
			case "text":
				textParts = append(textParts, cp.Text)
			case "tool_use":
				hasToolCalls = true
				argsJSON, _ := json.Marshal(cp.ToolInput)
				oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openai.ToolCall{
					ID:   cp.ToolCallID,
					Type: "function",
					Function: openai.FunctionCall{
						Name:      cp.ToolName,
						Arguments: string(argsJSON),
					},
				})
			case "tool_result":
				oaiMsg.Role = "tool"
				oaiMsg.ToolCallID = cp.ToolCallID
				textParts = append(textParts, contentString(cp.ToolOutput))
			case "reasoning":
				oaiMsg.ReasoningContent = cp.Text
			}
		}

		if !hasToolCalls {
			oaiMsg.Content = strings.Join(textParts, "\n")
		}

		out = append(out, oaiMsg)
	}

	return out
}

func toOpenAITools(tools []ToolDef) []openai.Tool {
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		out = append(out, openai.Tool{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	return out
}

func contentString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
