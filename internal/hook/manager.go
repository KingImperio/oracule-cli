package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Manager is the lifecycle hook system (Claude Code pattern, simplified).
//
// Supported events:
//   PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest,
//   PermissionDenied, SessionStart, SessionEnd, Stop, UserPromptSubmit,
//   PreCompact, PostCompact, SubagentStart, SubagentStop
type Manager struct {
	enabled bool
	hooks   map[HookEvent][]HookDef
	mu      sync.RWMutex
}

// HookDef is one registered hook.
type HookDef struct {
	// Type: "command", "prompt", "http", "agent"
	Type       string
	// For "command": shell command to execute. Receives JSON on stdin.
	Command    string
	// For "prompt": LLM prompt template to evaluate.
	Prompt     string
	// Matcher limits this hook to specific tools or events.
	Matcher    *HookMatcher
}

// HookMatcher narrows when a hook fires.
type HookMatcher struct {
	Tools  []string // empty = all tools
	Events []HookEvent
}

// HookEvent mirrors the event type string.
type HookEvent string

const (
	HookPreToolUse       HookEvent = "PreToolUse"
	HookPostToolUse      HookEvent = "PostToolUse"
	HookPostToolUseFail  HookEvent = "PostToolUseFailure"
	HookPermissionReq    HookEvent = "PermissionRequest"
	HookPermissionDenied HookEvent = "PermissionDenied"
	HookSessionStart     HookEvent = "SessionStart"
	HookSessionEnd       HookEvent = "SessionEnd"
	HookStop             HookEvent = "Stop"
	HookUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookPreCompact       HookEvent = "PreCompact"
	HookPostCompact      HookEvent = "PostCompact"
	HookSubagentStart    HookEvent = "SubagentStart"
	HookSubagentStop     HookEvent = "SubagentStop"
)

// HookPayload is the JSON payload delivered to all command-type hooks on stdin.
type HookPayload struct {
	SessionID      string            `json:"session_id"`
	TranscriptPath string            `json:"transcript_path"`
	CWD            string            `json:"cwd"`
	PermissionMode string            `json:"permission_mode"`
	HookEventName  HookEvent         `json:"hook_event_name"`
	ToolName       string            `json:"tool_name,omitempty"`
	ToolInput      map[string]any    `json:"tool_input,omitempty"`
	ToolOutput     string            `json:"tool_output,omitempty"`
	ToolIsError    bool              `json:"tool_is_error,omitempty"`
	StopReason     string            `json:"stop_reason,omitempty"`
	UserPrompt     string            `json:"user_prompt,omitempty"`
	CompactReason  string            `json:"compact_reason,omitempty"`
	SubagentName   string            `json:"subagent_name,omitempty"`
}

// HookOutput is the expected JSON response from command-type hooks.
type HookOutput struct {
	Decision        *string          `json:"decision,omitempty"`        // "allow"|"deny"|"ask"
	UpdatedInput    *map[string]any  `json:"updated_input,omitempty"`
	ContextInjection *string         `json:"context_injection,omitempty"`
	StopContinuation *bool           `json:"stop_continuation,omitempty"`
}

// NewManager creates a new hook manager.
func NewManager(enabled bool) *Manager {
	return &Manager{enabled: enabled, hooks: make(map[HookEvent][]HookDef)}
}

// Register adds a hook definition.
func (m *Manager) Register(event HookEvent, def HookDef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[event] = append(m.hooks[event], def)
}

// Fire runs all hooks registered for the given event.
// Hooks run in order; any hook can return an error which is logged but does not
// block other hooks from running (best-effort delivery).
func (m *Manager) Fire(ctx context.Context, event HookEvent, payload HookPayload) error {
	if !m.enabled {
		return nil
	}
	m.mu.RLock()
	defs := m.hooks[event]
	m.mu.RUnlock()

	var firstErr error
	for _, def := range defs {
		if def.Matcher != nil && !matches(def.Matcher, payload) {
			continue
		}
		if err := m.runHook(ctx, def, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// FirePreToolUse fires PreToolUse hooks and returns the first non-nil output.
// A "deny" decision short-circuits tool execution; UpdatedInput rewrites args.
func (m *Manager) FirePreToolUse(ctx context.Context, payload HookPayload) (*HookOutput, error) {
	if !m.enabled {
		return nil, nil
	}
	m.mu.RLock()
	defs := m.hooks[HookPreToolUse]
	m.mu.RUnlock()

	for _, def := range defs {
		if def.Matcher != nil && !matches(def.Matcher, payload) {
			continue
		}
		out, err := m.runHookOutput(ctx, def, payload)
		if err != nil {
			return nil, err
		}
		if out != nil {
			return out, nil
		}
	}
	return nil, nil
}

// CheckStopContinuation returns true if any PostToolUse or Stop hook requested
// the agent loop to halt.
func (m *Manager) CheckStopContinuation(ctx context.Context) (bool, error) {
	if !m.enabled {
		return false, nil
	}
	m.mu.RLock()
	defs := append(m.hooks[HookPostToolUse], m.hooks[HookStop]...)
	m.mu.RUnlock()

	for _, def := range defs {
		out, err := m.runHookOutput(ctx, def, HookPayload{HookEventName: HookStop})
		if err != nil {
			return false, err
		}
		if out != nil && out.StopContinuation != nil && *out.StopContinuation {
			return true, nil
		}
	}
	return false, nil
}

// runHook executes a single hook definition. Currently only "command" type
// is implemented (shell command receiving JSON on stdin).
func (m *Manager) runHook(ctx context.Context, def HookDef, payload HookPayload) error {
	if def.Type != "command" {
		return nil // prompt/http/agent types not yet implemented
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", def.Command)
	cmd.Stdin = strings.NewReader(string(data))
	// stderr is captured for visibility; stdout is the JSON response.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hook %q %q: %w: %s", def.Type, def.Command, err, string(out))
	}
	// Success: we don't parse output for fire-and-forget hooks.
	return nil
}

// runHookOutput runs a hook and parses HookOutput from stdout.
func (m *Manager) runHookOutput(ctx context.Context, def HookDef, payload HookPayload) (*HookOutput, error) {
	if def.Type != "command" {
		return nil, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", def.Command)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hook %q: %w: %s", def.Type, def.Command, err, string(out))
	}
	var result HookOutput
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("hook %q: bad JSON output: %w", def.Type, err)
	}
	return &result, nil
}

func matches(m *HookMatcher, payload HookPayload) bool {
	if len(m.Tools) > 0 && payload.ToolName != "" {
		for _, t := range m.Tools {
			if t == payload.ToolName {
				return true
			}
		}
		return false
	}
	if len(m.Events) > 0 {
		for _, e := range m.Events {
			if e == payload.HookEventName {
				return true
			}
		}
		return false
	}
	return true
}
