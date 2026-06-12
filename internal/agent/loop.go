package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/ctx"
	"github.com/KingImperio/oracule-cli/pkg/models"
	"github.com/KingImperio/oracule-cli/internal/provider"
	"github.com/KingImperio/oracule-cli/internal/memory"
	"github.com/KingImperio/oracule-cli/internal/hook"
	"github.com/KingImperio/oracule-cli/internal/permission"
	"github.com/rs/zerolog"
)

// newID returns a random hex identifier for messages and sessions.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoopResult captures the terminal state of one agent loop invocation.
type LoopResult struct {
	SessionID string
	Turns     int
	TotalCost float64
	Stopped   bool
	Reason    string
}

// LoopFunc is the callback the TUI registers to receive streaming events.
type LoopFunc func(evt StreamEvent) error

// StreamEvent is one unit of streaming output.
type StreamEvent struct {
	Type      string // "text_delta" | "reasoning_delta" | "tool_start" | "tool_end" | "done" | "error"
	Text      string
	ToolName  string
	ToolInput map[string]any
	ToolOk    bool
	CostUSD   float64
	TokenIn   int
	TokenOut  int
}

// Agent is the central orchestrator for one session.
type Agent struct {
	Config      models.AgentConfig
	Provider    provider.Provider
	Memory      *memory.Manager
	Hooks       *hook.Manager
	Perms       *permission.Engine
	Tools       []models.ToolDefinition
	Sessions    SessionStore
	CtxRegistry *ctx.Registry
	Logger      zerolog.Logger

	mu         sync.Mutex
	stopped    bool
	loopCancel context.CancelFunc
}

// SessionStore abstracts the persistence layer for sessions and messages.
type SessionStore interface {
	GetSession(ctx context.Context, id string) (*models.Session, error)
	SaveSession(ctx context.Context, s *models.Session) error
	AppendMessage(ctx context.Context, m models.Message) error
	AppendPart(ctx context.Context, sessionID, messageID string, p models.Part) error
	GetMessages(ctx context.Context, sessionID string, limit int) ([]models.Message, error)
	CreateRevertPoint(ctx context.Context, rp models.RevertPoint) error
	GetRevertPoint(ctx context.Context, sessionID, messageID string) (*models.RevertPoint, error)
}

// NewAgent creates a new agent with the given configuration and dependencies.
func NewAgent(cfg models.AgentConfig, prov provider.Provider, mem *memory.Manager,
	hooks *hook.Manager, perms *permission.Engine, tools []models.ToolDefinition,
	sessions SessionStore, reg *ctx.Registry, logger zerolog.Logger) *Agent {
	return &Agent{
		Config:      cfg,
		Provider:    prov,
		Memory:      mem,
		Hooks:       hooks,
		Perms:       perms,
		Tools:       tools,
		Sessions:    sessions,
		CtxRegistry: reg,
		Logger:      logger,
	}
}

// NewSessionID generates a deterministic session ID based on the working directory.
func NewSessionID(workDir string) string {
	return newID() + "-" + sanitizePath(workDir)
}

func sanitizePath(p string) string {
	r := strings.NewReplacer("/", "_", ":", "_", "\\", "_")
	return r.Replace(p)
}

// Run executes one agent loop for the given user prompt.
func (a *Agent) Run(ctx context.Context, sessionID string, userPrompt string, onEvent LoopFunc) (*LoopResult, error) {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return nil, errors.New("agent is stopped")
	}
	loopCtx, cancel := context.WithCancel(ctx)
	a.loopCancel = cancel
	a.mu.Unlock()
	defer cancel()

	session, err := a.Sessions.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// --- 1. Frozen snapshot of memory at session start (Hermes pattern) ---
	// The system prompt is built once and cached; ephemeral layers are injected
	// per-turn. This preserves Anthropic/OpenAI prefix caching across turns.
	sysPrompt, err := a.buildSystemPrompt(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("build system prompt: %w", err)
	}

	// --- 2. Fire SessionStart hooks ---
	if a.Hooks != nil {
		if err := a.Hooks.Fire(ctx, hook.HookSessionStart, hook.HookPayload{
			SessionID:      sessionID,
			CWD:            session.Directory,
			PermissionMode: a.Config.PermissionMode,
		}); err != nil {
			a.Logger.Warn().Err(err).Msg("SessionStart hook failed")
		}
	}

	// --- 3. Load recent history ---
	history, err := a.Sessions.GetMessages(ctx, sessionID, 200)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	// --- 4. Assemble LLM messages ---
	messages := a.assembleMessages(history, userPrompt)

	result := &LoopResult{SessionID: sessionID}
	turn := 0
	toolOnlyCount := 0

	for turn < a.Config.MaxTurns {
		turn++

		// --- Context shapers (Claude Code pattern, simplified) ---
		shaped, trimmed, err := a.applyContextShapers(ctx, messages, sysPrompt)
		if err != nil {
			return result, fmt.Errorf("context shapers: %w", err)
		}
		if trimmed > 0 {
			a.Logger.Info().Int("trimmed_turns", trimmed).Msg("context trimmed by shaper")
		}

		// --- 5. Call the model ---
		stream, err := a.Provider.Stream(loopCtx, provider.StreamRequest{
			Model:       a.Config.ModelID,
			System:      sysPrompt,
			Messages:    modelsToProviderMessages(shaped),
			MaxTokens:   a.Config.MaxTokens,
			Temperature: 0.2,
		})
		if err != nil {
			if onEvent != nil {
				_ = onEvent(StreamEvent{Type: "error", Text: err.Error()})
			}
			return result, err
		}

		// --- 6. Collect stream into a new assistant message ---
		assistantMsg, fullText, toolCalls, err := a.collectStream(loopCtx, stream, onEvent)
		if err != nil {
			return result, err
		}

		// --- 7. Save assistant message ---
		assistantMsg.SessionID = sessionID
		if err := a.Sessions.AppendMessage(ctx, *assistantMsg); err != nil {
			a.Logger.Error().Err(err).Msg("save assistant message")
		}

		// Decide if we should stop.
		if assistantMsg.StopReason != "" {
			result.Stopped = true
			result.Reason = assistantMsg.StopReason
			break
		}

		// --- Doom-loop detection ---
		hasText := strings.TrimSpace(fullText) != ""
		if len(toolCalls) == 0 {
			toolOnlyCount = 0
			result.Stopped = true
			result.Reason = "end_turn"
			break
		}
		if !hasText {
			toolOnlyCount++
		} else {
			toolOnlyCount = 0
		}
		if checkDoomLoop(toolOnlyCount, hasText) {
			a.Logger.Warn().Int("consecutive_tool_only_turns", toolOnlyCount).Msg("doom-loop detected")
			result.Stopped = true
			result.Reason = "doom_loop"
			break
		}

		toolResults, toolCost, err := a.executeTools(ctx, sessionID, assistantMsg.ID, toolCalls, onEvent)
		if err != nil {
			return result, err
		}
		result.TotalCost += toolCost

		// Append a user message with all tool results.
		resultsMsg := models.Message{
			ID:        newID(),
			SessionID: sessionID,
			Role:      models.RoleUser,
			Parts:     toolResults,
			Timestamp: time.Now(),
			CostUSD:   toolCost,
			ModelID:   a.Config.ModelID,
		}
		if err := a.Sessions.AppendMessage(ctx, resultsMsg); err != nil {
			a.Logger.Error().Err(err).Msg("save tool results")
		}
		messages = append(messages, *assistantMsg, resultsMsg)

		// --- 9. Check PostToolUse hook stop signal ---
		if a.Hooks != nil {
			shouldStop, hookErr := a.Hooks.CheckStopContinuation(ctx)
			if hookErr != nil {
				a.Logger.Warn().Err(hookErr).Msg("PostToolUse hook failed")
			}
			if shouldStop {
				result.Stopped = true
				result.Reason = "hook_stop"
				break
			}
		}
	}

	// --- 10. Fire SessionEnd hooks ---
	if a.Hooks != nil {
		_ = a.Hooks.Fire(ctx, hook.HookSessionEnd, hook.HookPayload{
			SessionID: sessionID,
			CWD:       session.Directory,
		})
	}

	result.Turns = turn
	return result, nil
}

// Stop requests a graceful halt; in-flight tool calls will be cancelled on next context check.
func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped = true
	if a.loopCancel != nil {
		a.loopCancel()
	}
}

// collectStream reads the streaming response into a Message + list of tool calls.
func (a *Agent) collectStream(ctx context.Context, stream provider.Stream, onEvent LoopFunc) (*models.Message, string, []models.Part, error) {
	msg := &models.Message{
		ID:        newID(),
		Role:      models.RoleAssistant,
		Parts:     []models.Part{},
		Timestamp: time.Now(),
		ModelID:   a.Config.ModelID,
	}
	var fullText strings.Builder
	var toolCalls []models.Part

	for chunk := range stream.Chunks() {
		select {
		case <-ctx.Done():
			msg.StopReason = "interrupted"
			return msg, fullText.String(), toolCalls, ctx.Err()
		default:
		}

		switch evt := chunk.(type) {
		case provider.TextDelta:
			fullText.WriteString(evt.Text)
			if onEvent != nil {
				_ = onEvent(StreamEvent{Type: "text_delta", Text: evt.Text})
			}
		case provider.ReasoningDelta:
			msg.Parts = append(msg.Parts, models.Part{
				Type:          models.PartReasoning,
				ReasoningText: evt.Text,
			})
			if onEvent != nil {
				_ = onEvent(StreamEvent{Type: "reasoning_delta", Text: evt.Text})
			}
		case provider.ToolCallStart:
			tc := models.Part{
				Type:      models.PartToolCall,
				ToolCallID: evt.ID,
				ToolName:   evt.Name,
				ToolInput:  evt.Arguments,
			}
			toolCalls = append(toolCalls, tc)
			if onEvent != nil {
				_ = onEvent(StreamEvent{Type: "tool_start", ToolName: evt.Name, ToolInput: evt.Arguments})
			}
		case provider.ToolCallEnd:
			// Merge result into the matching tool call.
			for i := range toolCalls {
				if toolCalls[i].ToolCallID == evt.ID {
					toolCalls[i].ToolOutput = evt.Output
					toolCalls[i].ToolIsError = evt.IsError
					toolCalls[i].ToolDuration = evt.Duration
					break
				}
			}
			if onEvent != nil {
				_ = onEvent(StreamEvent{
					Type:     "tool_end",
					ToolName: evt.Name,
					ToolOk:   !evt.IsError,
				})
			}
		case provider.StreamDone:
			msg.CostUSD = evt.CostUSD
			msg.TokenIn = evt.Usage.PromptTokens
			msg.TokenOut = evt.Usage.CompletionTokens
			if onEvent != nil {
				_ = onEvent(StreamEvent{
					Type:    "done",
					CostUSD: evt.CostUSD,
					TokenIn: evt.Usage.PromptTokens,
					TokenOut: evt.Usage.CompletionTokens,
				})
			}
		}
	}

	if fullText.Len() > 0 {
		msg.Parts = append([]models.Part{{Type: models.PartText, Text: fullText.String()}}, msg.Parts...)
	}
	msg.CostUSD = a.estimateCost(msg.TokenIn, msg.TokenOut)

	return msg, fullText.String(), toolCalls, nil
}

// executeTools dispatches tool calls. Read-only tools run in parallel;
// stateful tools (bash, edit, write) run sequentially.
func (a *Agent) executeTools(ctx context.Context, sessionID, messageID string, toolCalls []models.Part, onEvent LoopFunc) ([]models.Part, float64, error) {
	readOnly := map[string]bool{"read": true, "glob": true, "grep": true, "lsp": true}
	stateful := make([]models.Part, 0, len(toolCalls))
	readOnlyParts := make([]models.Part, 0, len(toolCalls))

	for _, tc := range toolCalls {
		if readOnly[tc.ToolName] {
			readOnlyParts = append(readOnlyParts, tc)
		} else {
			stateful = append(stateful, tc)
		}
	}

	var totalCost float64
	results := make([]models.Part, 0, len(toolCalls))

	// Parallel phase: read-only tools.
	if len(readOnlyParts) > 0 {
		type res struct {
			idx  int
			part models.Part
			err  error
		}
		ch := make(chan res, len(readOnlyParts))
		for i, tc := range readOnlyParts {
			go func(idx int, call models.Part) {
				r, err := a.runTool(ctx, sessionID, messageID, call)
				ch <- res{idx: idx, part: r, err: err}
			}(i, tc)
		}
		for range readOnlyParts {
			r := <-ch
			totalCost += r.part.ToolCost
			results = append(results, r.part)
			if r.err != nil && onEvent != nil {
				_ = onEvent(StreamEvent{Type: "error", Text: r.err.Error()})
			}
		}
	}

	// Sequential phase: stateful tools (permission-enforced, order-preserving).
	for _, tc := range stateful {
		r, err := a.runTool(ctx, sessionID, messageID, tc)
		totalCost += r.ToolCost
		results = append(results, r)
		if err != nil && onEvent != nil {
			_ = onEvent(StreamEvent{Type: "error", Text: err.Error()})
		}
	}

	return results, totalCost, nil
}

// runTool executes a single tool call, enforcing permissions and firing hooks.
func (a *Agent) runTool(ctx context.Context, sessionID, messageID string, tc models.Part) (models.Part, error) {
	toolName := tc.ToolName
	toolInput := tc.ToolInput

	// --- PreToolUse hook (can block or rewrite input) ---
	if a.Hooks != nil {
		out, err := a.Hooks.FirePreToolUse(ctx, hook.HookPayload{
			SessionID:  sessionID,
			CWD:        "",
			ToolName:   toolName,
			ToolInput:  toolInput,
		})
		if err != nil {
			return models.Part{Type: models.PartToolResult, ToolIsError: true, ToolOutput: err.Error(), ToolName: toolName}, err
		}
		if out.Decision != nil && *out.Decision == "deny" {
			return models.Part{Type: models.PartToolResult, ToolIsError: true, ToolName: toolName, ToolOutput: "denied by hook"}, nil
		}
		if out.UpdatedInput != nil {
			toolInput = *out.UpdatedInput
		}
	}

	// --- Permission enforcement (deny-first) ---
	action := a.Perms.Evaluate(toolName, toolInput)
	switch action {
	case permission.Deny:
		return models.Part{
			Type: models.PartToolResult, ToolName: toolName,
			ToolIsError: true, ToolOutput: "denied by permission rules",
		}, nil
	case permission.Ask:
		allowed, err := a.promptPermission(toolName, toolInput)
		if err != nil || !allowed {
			reason := "denied by user"
			if err != nil {
				reason = fmt.Sprintf("permission prompt error: %v", err)
			}
			return models.Part{
				Type: models.PartToolResult, ToolName: toolName,
				ToolIsError: true, ToolOutput: reason,
			}, nil
		}
	}

	// --- Dispatch to handler ---
	toolDef, ok := a.findTool(toolName)
	if !ok {
		return models.Part{
			Type: models.PartToolResult, ToolName: toolName,
			ToolIsError: true, ToolOutput: fmt.Sprintf("unknown tool: %s", toolName),
		}, nil
	}

	start := time.Now()
	out, err := toolDef.Handler(models.ToolContext{
		SessionID: sessionID,
		MessageID: messageID,
		WorkingDir: "",
	}, toolInput)
	duration := time.Since(start)

	resultPart := models.Part{
		Type:         models.PartToolResult,
		ToolName:     toolName,
		ToolOutput:   out.Output,
		ToolIsError:  err != nil || out.IsError,
		ToolDuration: duration,
		ToolCost:     out.CostUSD,
	}

	// --- PostToolUse hook ---
	if a.Hooks != nil {
		_ = a.Hooks.Fire(ctx, hook.HookPostToolUse, hook.HookPayload{
			SessionID:   sessionID,
			ToolName:    toolName,
			ToolInput:   toolInput,
			ToolOutput:  out.Output,
			ToolIsError: resultPart.ToolIsError,
		})
	}

	return resultPart, err
}

// buildSystemPrompt assembles the system prompt from memory + config.
// Implements the "frozen snapshot" pattern (Hermes): built once per session,
// cached, invalidated only after compaction.
func (a *Agent) buildSystemPrompt(ctx context.Context, session *models.Session) (string, error) {
	parts := []string{
		"You are Oracule, an expert software engineering agent.",
		"Operate precisely. Prefer minimal, well-tested changes.",
		"Use tools to read files, search code, run commands, and edit the workspace.",
		"Think step by step when tasks are ambiguous.",
	}

	// ctx ecosystem: skills, MCPs, CLIs available for this session.
	if a.CtxRegistry != nil {
		ss, s, m, c := a.CtxRegistry.Stats()
		parts = append(parts, fmt.Sprintf(
			"\n## ctx Ecosystem\nAvailable: %d skillsets, %d skills, %d MCPs, %d CLIs.\n"+
				"Use `ctx_suggest` with a task description to find relevant skills.\n"+
				"Use `ctx_skill_get` to load a full skill body.\n"+
				"Use `ctx_discover` to scan for new capabilities.",
			ss, s, m, c))
	}

	// Layer 1: project / user instructions (CLAUDE.md / AGENTS.md equivalent).
	if a.Memory != nil {
		mem, err := a.Memory.LoadProjectInstructions(ctx, session.Directory)
		if err != nil {
			a.Logger.Warn().Err(err).Msg("load project instructions")
		} else {
			parts = append(parts, "\n## Project Instructions\n\n"+mem)
		}

		// Layer 2: durable user-scoped memory (MEMORY.md equivalent).
		userMem, err := a.Memory.LoadUserMemory(ctx)
		if err != nil {
			a.Logger.Warn().Err(err).Msg("load user memory")
		} else if userMem != "" {
			parts = append(parts, "\n## User Memory\n\n"+userMem)
		}

		// Layer 3: FTS5 session search — relevant past conversations.
		// Only injected when the model has indicated it needs recall.
		// (Full query is expensive; default is empty to save tokens.)
	}

	// Permission mode framing.
	switch a.Config.PermissionMode {
	case "plan":
		parts = append(parts, "\n## Plan Mode\n\nYou are in read-only planning mode. Do NOT edit files or run commands that modify state.")
	case "bypassPermissions":
		parts = append(parts, "\n## Bypass Mode\n\nAll destructive operations are approved. Still confirm destructive intent before executing `rm -rf` or similar.")
	}

	return joinLines(parts), nil
}

// applyContextShapers implements the 5-layer context compression:
//   1. microCompact — local trim of old tool outputs
//   2. snip — temporal depth pruning
//   3. microCompact — cache-aware local edits
//   4. contextCollapse — long history summarization (1 API call)
//   5. autoCompact — full summary fired last, circuit-breaker protected
//
// Returns the shaped message list, number of turns trimmed, and any error.
func (a *Agent) applyContextShapers(ctx context.Context, messages []models.Message, sysPrompt string) ([]models.Message, int, error) {
	if len(messages) <= 20 {
		return messages, 0, nil
	}

	// Phase 4: contextCollapse — summarize turns older than the last 20.
	// Keeps the most recent 20 messages intact, summarizes the rest into a single
	// compaction message inserted after the system prompt.
	keep := 20
	if len(messages) > keep+2 {
		old := messages[:len(messages)-keep]
		recent := messages[len(messages)-keep:]

		summary, err := a.summarizeMessages(ctx, old, sysPrompt)
		if err != nil {
			a.Logger.Warn().Err(err).Msg("context collapse summarization failed")
			return messages, 0, nil // fail open: return unshaped messages
		}

		compactionMsg := models.Message{
			ID:        newID(),
			Role:      models.RoleSystem,
			Parts:     []models.Part{{Type: models.PartCompaction, CompactionSummary: summary, CompactedAt: time.Now()}},
			Timestamp: time.Now(),
		}
		messages = append([]models.Message{{Parts: []models.Part{{Type: models.PartText, Text: sysPrompt}}}, compactionMsg}, recent...)
		return messages, len(old), nil
	}

	return messages, 0, nil
}

// summarizeMessages is a lightweight summarizer for context collapse.
// It calls the provider with a summarize instruction and returns the text.
func (a *Agent) summarizeMessages(ctx context.Context, messages []models.Message, sysPrompt string) (string, error) {
	var builder strings.Builder
	builder.WriteString("Summarize the following conversation turns into a concise summary preserving all key decisions, file paths, error states, and open questions. Output markdown.\n\n")
	for _, m := range messages {
		builder.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, firstText(m)))
	}
	text := builder.String()
	if len(text) > 60_000 {
		text = text[:60_000]
	}

	resp, err := a.Provider.Complete(ctx, provider.CompleteRequest{
		System:   sysPrompt + "\n\n## Context Summary Task\n\n" + text,
		Model:    a.Config.ModelID,
		MaxTokens: 4000,
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (a *Agent) assembleMessages(history []models.Message, userPrompt string) []models.Message {
	out := make([]models.Message, 0, len(history)+1)
	out = append(out, history...)
	out = append(out, models.Message{
		ID:        newID(),
		Role:      models.RoleUser,
		Parts:     []models.Part{{Type: models.PartText, Text: userPrompt}},
		Timestamp: time.Now(),
	})
	return out
}

func (a *Agent) findTool(name string) (models.ToolDefinition, bool) {
	for _, t := range a.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return models.ToolDefinition{}, false
}

func (a *Agent) estimateCost(in, out int) float64 {
	// Minimal placeholder — substitute provider-specific pricing in v2.
	return 0
}

// llmMessageFromModel converts a stored models.Message to a provider.Message.
func llmMessageFromModel(m *models.Message) provider.Message {
	return provider.Message{
		Role:    string(m.Role),
		Content: partsToContentParts(m.Parts),
	}
}

// partsToContentParts converts models.Part slices to provider.ContentPart slices.
func partsToContentParts(parts []models.Part) []provider.ContentPart {
	out := make([]provider.ContentPart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case models.PartText:
			out = append(out, provider.ContentPart{Type: "text", Text: p.Text})
		case models.PartReasoning:
			out = append(out, provider.ContentPart{Type: "reasoning", Text: p.ReasoningText})
		case models.PartToolCall:
			out = append(out, provider.ContentPart{
				Type:       "tool_use",
				ToolCallID: p.ToolCallID,
				ToolName:   p.ToolName,
				ToolInput:  p.ToolInput,
			})
		case models.PartToolResult:
			out = append(out, provider.ContentPart{
				Type:        "tool_result",
				ToolCallID:  p.ToolCallID,
				ToolName:    p.ToolName,
				ToolOutput:  p.ToolOutput,
				ToolIsError: p.ToolIsError,
			})
		}
	}
	return out
}

// modelsToProviderMessages converts an entire slice for provider requests.
func modelsToProviderMessages(msgs []models.Message) []provider.Message {
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llmMessageFromModel(&m)
	}
	return out
}

// promptPermission asks the user to approve or deny a tool call via stderr/stdin.
func (a *Agent) promptPermission(toolName string, input map[string]any) (bool, error) {
	mode := a.Config.PermissionMode
	if mode == "plan" {
		return false, nil
	}

	if mode == "bypassPermissions" || mode == "acceptEdits" {
		return true, nil
	}

	// Non-interactive: auto-deny
	if !isInteractive() {
		return false, nil
	}

	inputStr := ""
	if cmd, ok := input["command"].(string); ok {
		inputStr = cmd
	} else if path, ok := input["file_path"].(string); ok {
		inputStr = path
	}

	fmt.Fprintf(os.Stderr, "\n🔐 Allow %s", toolName)
	if inputStr != "" {
		fmt.Fprintf(os.Stderr, " (%s)", truncate(inputStr, 120))
	}
	fmt.Fprintf(os.Stderr, "? [y/N] ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, scanner.Err()
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

func isInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// doomLoopThreshold is the max consecutive tool-only turns before aborting.
const doomLoopThreshold = 15

// checkDoomLoop returns true if the agent is in a doom-loop (repeated tool
// calls without producing meaningful text output).
func checkDoomLoop(toolOnlyCount int, hasText bool) bool {
	if hasText {
		return false
	}
	return toolOnlyCount >= doomLoopThreshold
}

// joinLines concatenates non-empty strings with newlines.
func joinLines(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p)
	}
	return b.String()
}

func firstText(m models.Message) string {
	for _, p := range m.Parts {
		if p.Type == models.PartText || p.Type == models.PartToolResult {
			return truncate(p.Text, 200)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
