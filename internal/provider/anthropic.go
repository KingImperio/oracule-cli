package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/KingImperio/oracule-cli/internal/config"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

func newAnthropic(cfg config.ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic provider requires API key")
	}
	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	client := anthropic.NewClient(option.WithAPIKey(cfg.APIKey))
	return &anthropicProvider{
		client: &client,
		model:  model,
	}, nil
}

type anthropicProvider struct {
	client *anthropic.Client
	model  string
}

func (p *anthropicProvider) ModelID() string {
	return p.model
}

func (p *anthropicProvider) Stream(ctx context.Context, req StreamRequest) (Stream, error) {
	params := p.toParams(req)
	stream := p.client.Messages.NewStreaming(ctx, params)
	return newAnthropicStream(stream), nil
}

func (p *anthropicProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	streamReq := StreamRequest{
		Model:       req.Model,
		System:      req.System,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	params := p.toParams(streamReq)

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("anthropic complete: %w", err)
	}

	var textParts []string
	for _, block := range msg.Content {
		if block.Type == "text" {
			textParts = append(textParts, block.Text)
		}
	}

	usage := Usage{}
	if msg.Usage.InputTokens > 0 || msg.Usage.OutputTokens > 0 {
		usage.PromptTokens = int(msg.Usage.InputTokens)
		usage.CompletionTokens = int(msg.Usage.OutputTokens)
		usage.TotalTokens = int(msg.Usage.InputTokens + msg.Usage.OutputTokens)
	}

	return CompleteResponse{
		Text:    strings.Join(textParts, "\n"),
		Usage:   usage,
		ModelID: p.model,
	}, nil
}

// ---------------------------------------------------------------------------
// Request conversion
// ---------------------------------------------------------------------------

func (p *anthropicProvider) toParams(req StreamRequest) anthropic.MessageNewParams {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: maxTokens,
	}

	if req.Temperature > 0 {
		params.Temperature = param.NewOpt(req.Temperature)
	}

	if req.System != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.System},
		}
	}

	params.Messages = toAnthropicMessages(req.Messages)

	if len(req.Tools) > 0 {
		params.Tools = toAnthropicTools(req.Tools)
	}

	return params
}

func toAnthropicMessages(msgs []Message) []anthropic.MessageParam {
	var out []anthropic.MessageParam

	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, toAnthropicUserMessage(m.Content))
		case "assistant":
			out = append(out, toAnthropicAssistantMessage(m.Content))
		case "tool":
			out = append(out, toAnthropicUserMessage(m.Content))
		}
	}

	return out
}

func toAnthropicUserMessage(content []ContentPart) anthropic.MessageParam {
	var blocks []anthropic.ContentBlockParamUnion
	for _, cp := range content {
		switch cp.Type {
		case "text":
			blocks = append(blocks, anthropic.NewTextBlock(cp.Text))
		case "tool_result":
			isError := cp.ToolIsError
			contentStr := contentString(cp.ToolOutput)
			blocks = append(blocks, anthropic.NewToolResultBlock(cp.ToolCallID, contentStr, isError))
		}
	}
	return anthropic.NewUserMessage(blocks...)
}

func toAnthropicAssistantMessage(content []ContentPart) anthropic.MessageParam {
	var blocks []anthropic.ContentBlockParamUnion
	for _, cp := range content {
		switch cp.Type {
		case "text":
			blocks = append(blocks, anthropic.NewTextBlock(cp.Text))
		case "tool_use":
			blocks = append(blocks, anthropic.NewToolUseBlock(cp.ToolCallID, cp.ToolInput, cp.ToolName))
		}
	}
	return anthropic.NewAssistantMessage(blocks...)
}

func toAnthropicTools(tools []ToolDef) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schema := schemaToAnthropic(t.InputSchema)
		tool := anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				InputSchema: schema,
			},
		}
		if t.Description != "" {
			tool.OfTool.Description = param.NewOpt(t.Description)
		}
		out = append(out, tool)
	}
	return out
}

func schemaToAnthropic(schema map[string]any) anthropic.ToolInputSchemaParam {
	if schema == nil {
		return anthropic.ToolInputSchemaParam{
			Properties: map[string]any{},
		}
	}
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]string)
	if required == nil {
		required = []string{}
	}

	extra := make(map[string]any)
	for k, v := range schema {
		if k != "type" && k != "properties" && k != "required" {
			extra[k] = v
		}
	}

	return anthropic.ToolInputSchemaParam{
		Properties:  props,
		Required:    required,
		ExtraFields: extra,
	}
}

// ---------------------------------------------------------------------------
// Streaming
// ---------------------------------------------------------------------------

type anthropicStream struct {
	stream    *ssestream.Stream[anthropic.MessageStreamEventUnion]
	ch        chan Delta
	once      sync.Once
	closeOnce sync.Once

	toolAccum map[int64]*anthropicToolAccum
	nextIdx   int64
}

type anthropicToolAccum struct {
	ID          string
	Name        string
	partialJSON strings.Builder
}

func newAnthropicStream(stream *ssestream.Stream[anthropic.MessageStreamEventUnion]) *anthropicStream {
	return &anthropicStream{
		stream:    stream,
		toolAccum: make(map[int64]*anthropicToolAccum),
	}
}

func (s *anthropicStream) Chunks() <-chan Delta {
	s.once.Do(func() {
		s.ch = make(chan Delta)
		go s.readLoop()
	})
	return s.ch
}

func (s *anthropicStream) Close() error {
	s.closeOnce.Do(func() {
		s.stream.Close()
	})
	return nil
}

func (s *anthropicStream) readLoop() {
	defer s.closeOnce.Do(func() {
		s.stream.Close()
	})
	defer close(s.ch)

	var stopReason string
	var finalUsage Usage

	for s.stream.Next() {
		evt := s.stream.Current()

		switch evt.Type {
		case "content_block_start":
			cb := evt.AsContentBlockStart()
			idx := cb.Index

			if cb.ContentBlock.Type == "tool_use" {
				tu := cb.ContentBlock.AsToolUse()
				acc := &anthropicToolAccum{
					ID:   tu.ID,
					Name: tu.Name,
				}
				// Tool input may be present in the start event as raw JSON
				if len(tu.Input) > 0 {
					acc.partialJSON.Write(tu.Input)
				}
				s.toolAccum[idx] = acc
				continue
			}

		case "content_block_delta":
			cd := evt.AsContentBlockDelta()

			switch cd.Delta.Type {
			case "text_delta":
				if cd.Delta.Text != "" {
					s.ch <- TextDelta{Text: cd.Delta.Text}
				}
			case "input_json_delta":
				if cd.Delta.PartialJSON != "" {
					if acc, ok := s.toolAccum[cd.Index]; ok {
						acc.partialJSON.WriteString(cd.Delta.PartialJSON)
					}
				}
			case "thinking_delta":
				if cd.Delta.Thinking != "" {
					s.ch <- ReasoningDelta{Text: cd.Delta.Thinking}
				}
			}

		case "content_block_stop":
			cs := evt.AsContentBlockStop()

			if acc, ok := s.toolAccum[cs.Index]; ok {
				var args map[string]any
				if acc.partialJSON.Len() > 0 {
					_ = json.Unmarshal([]byte(acc.partialJSON.String()), &args)
				}
				s.ch <- ToolCallStart{
					ID:        acc.ID,
					Name:      acc.Name,
					Arguments: args,
				}
				delete(s.toolAccum, cs.Index)
			}

		case "message_delta":
			md := evt.AsMessageDelta()
			stopReason = string(md.Delta.StopReason)
			if md.Usage.InputTokens > 0 || md.Usage.OutputTokens > 0 {
				finalUsage = Usage{
					PromptTokens:     int(md.Usage.InputTokens),
					CompletionTokens: int(md.Usage.OutputTokens),
					TotalTokens:      int(md.Usage.InputTokens + md.Usage.OutputTokens),
				}
			}

		case "message_stop":
			s.ch <- StreamDone{
				StopReason: stopReason,
				Usage:      finalUsage,
			}
		}
	}

	if err := s.stream.Err(); err != nil {
		if stopReason == "" {
			s.ch <- StreamDone{
				StopReason: "error",
			}
		}
	}
}
