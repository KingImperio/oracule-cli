package provider

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/KingImperio/oracule-cli/internal/config"
	"google.golang.org/genai"
)

func newGoogle(cfg config.ProviderConfig) (Provider, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("google/gemini provider requires API key")
	}
	model := cfg.Model
	if model == "" {
		model = "gemini-2.5-pro"
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("google/gemini client: %w", err)
	}
	return &googleProvider{
		client: client,
		model:  model,
	}, nil
}

type googleProvider struct {
	client *genai.Client
	model  string
}

func (p *googleProvider) ModelID() string {
	return p.model
}

func (p *googleProvider) Stream(ctx context.Context, req StreamRequest) (Stream, error) {
	contents, config := p.toGenAI(req)
	streamIter := p.client.Models.GenerateContentStream(ctx, p.model, contents, config)
	return newGoogleStream(streamIter), nil
}

func (p *googleProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	streamReq := StreamRequest{
		Model:       req.Model,
		System:      req.System,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	contents, config := p.toGenAI(streamReq)

	resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("google complete: %w", err)
	}

	text := extractGeminiText(resp)

	usage := Usage{}
	if resp.UsageMetadata != nil {
		usage.PromptTokens = int(resp.UsageMetadata.PromptTokenCount)
		usage.CompletionTokens = int(resp.UsageMetadata.CandidatesTokenCount)
		usage.TotalTokens = int(resp.UsageMetadata.TotalTokenCount)
	}

	return CompleteResponse{
		Text:    text,
		Usage:   usage,
		ModelID: p.model,
	}, nil
}

// ---------------------------------------------------------------------------
// Request conversion
// ---------------------------------------------------------------------------

func (p *googleProvider) toGenAI(req StreamRequest) ([]*genai.Content, *genai.GenerateContentConfig) {
	config := &genai.GenerateContentConfig{}

	if req.System != "" {
		config.SystemInstruction = &genai.Content{
			Role: "model",
			Parts: []*genai.Part{
				genai.NewPartFromText(req.System),
			},
		}
	}

	if req.Temperature > 0 {
		temp := float32(req.Temperature)
		config.Temperature = &temp
	}

	if req.MaxTokens > 0 {
		config.MaxOutputTokens = int32(req.MaxTokens)
	}

	if len(req.Tools) > 0 {
		config.Tools = []*genai.Tool{
			{
				FunctionDeclarations: toGeminiTools(req.Tools),
			},
		}
	}

	contents := toGeminiContents(req.Messages)
	return contents, config
}

func toGeminiContents(msgs []Message) []*genai.Content {
	var out []*genai.Content
	for _, m := range msgs {
		content := &genai.Content{
			Parts: toGeminiParts(m.Content),
		}
		switch m.Role {
		case "user", "tool":
			content.Role = "user"
		case "assistant":
			content.Role = "model"
		default:
			content.Role = "user"
		}
		out = append(out, content)
	}
	return out
}

func toGeminiParts(parts []ContentPart) []*genai.Part {
	var out []*genai.Part
	for _, cp := range parts {
		switch cp.Type {
		case "text":
			out = append(out, genai.NewPartFromText(cp.Text))
		case "tool_use":
			out = append(out, genai.NewPartFromFunctionCall(cp.ToolName, cp.ToolInput))
		case "tool_result":
			var resp map[string]any
			switch v := cp.ToolOutput.(type) {
			case map[string]any:
				resp = v
			default:
				resp = map[string]any{"result": contentString(cp.ToolOutput)}
			}
			out = append(out, genai.NewPartFromFunctionResponse(cp.ToolName, resp))
		}
	}
	return out
}

func toGeminiTools(tools []ToolDef) []*genai.FunctionDeclaration {
	out := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
		}
		if t.InputSchema != nil {
			fd.Parameters = schemaToGemini(t.InputSchema)
		}
		out = append(out, fd)
	}
	return out
}

func schemaToGemini(schema map[string]any) *genai.Schema {
	if schema == nil {
		return nil
	}

	s := &genai.Schema{}

	if t, ok := schema["type"].(string); ok {
		switch t {
		case "object":
			s.Type = genai.TypeObject
		case "string":
			s.Type = genai.TypeString
		case "integer":
			s.Type = genai.TypeInteger
		case "number":
			s.Type = genai.TypeNumber
		case "array":
			s.Type = genai.TypeArray
		case "boolean":
			s.Type = genai.TypeBoolean
		default:
			s.Type = genai.TypeObject
		}
	}

	if desc, ok := schema["description"].(string); ok {
		s.Description = desc
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*genai.Schema)
		for k, v := range props {
			if propMap, ok := v.(map[string]any); ok {
				s.Properties[k] = schemaToGemini(propMap)
			}
		}
	}

	switch req := schema["required"].(type) {
	case []string:
		s.Required = req
	case []any:
		for _, r := range req {
			if rs, ok := r.(string); ok {
				s.Required = append(s.Required, rs)
			}
		}
	}

	return s
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func extractGeminiText(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	c := resp.Candidates[0]
	if c.Content == nil {
		return ""
	}
	var textParts []string
	for _, p := range c.Content.Parts {
		if p.Text != "" {
			textParts = append(textParts, p.Text)
		}
	}
	return strings.Join(textParts, "\n")
}

func extractGeminiFinishReason(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	return string(resp.Candidates[0].FinishReason)
}

// ---------------------------------------------------------------------------
// Streaming
// ---------------------------------------------------------------------------

type googleStream struct {
	iter      iter.Seq2[*genai.GenerateContentResponse, error]
	ch        chan Delta
	once      sync.Once
	closeOnce sync.Once
	cancel    context.CancelFunc
}

func newGoogleStream(iter iter.Seq2[*genai.GenerateContentResponse, error]) *googleStream {
	return &googleStream{
		iter: iter,
	}
}

func (s *googleStream) Chunks() <-chan Delta {
	s.once.Do(func() {
		s.ch = make(chan Delta)
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		go s.readLoop(ctx)
	})
	return s.ch
}

func (s *googleStream) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	return nil
}

func (s *googleStream) readLoop(ctx context.Context) {
	defer close(s.ch)

	var stopReason string
	var finalUsage Usage

	for resp, err := range s.iter {
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.ch <- StreamDone{StopReason: "error"}
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		if resp == nil {
			continue
		}

		// Track usage
		if resp.UsageMetadata != nil {
			finalUsage = Usage{
				PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
				CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
				TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
			}
		}

		if len(resp.Candidates) == 0 {
			continue
		}
		c := resp.Candidates[0]

		// Finish reason in the last chunk
		if c.FinishReason != "" {
			stopReason = string(c.FinishReason)
		}

		if c.Content == nil {
			continue
		}

		for _, part := range c.Content.Parts {
			switch {
			case part.Text != "":
				s.ch <- TextDelta{Text: part.Text}
			case part.FunctionCall != nil:
				fc := part.FunctionCall
				s.ch <- ToolCallStart{
					ID:        fc.ID,
					Name:      fc.Name,
					Arguments: fc.Args,
				}
			}
		}
	}

	s.ch <- StreamDone{
		StopReason: stopReason,
		Usage:      finalUsage,
	}
}
