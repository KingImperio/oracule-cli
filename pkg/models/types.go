package models

import "time"

// MessageRole represents the author of a message.
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// PartType identifies the kind of content in a Part.
type PartType string

const (
	PartText       PartType = "text"
	PartReasoning  PartType = "reasoning"
	PartToolCall   PartType = "tool_call"
	PartToolResult PartType = "tool_result"
	PartCompaction PartType = "compaction"
)

// Part is a unit of content within a Message.
type Part struct {
	Type PartType
	Text string

	// ToolCall fields (populated when Type == PartToolCall)
	ToolCallID   string
	ToolName     string
	ToolInput    map[string]any
	ToolOutput   any
	ToolIsError  bool
	ToolDuration time.Duration
	ToolCost     float64

	// Reasoning fields (populated when Type == PartReasoning)
	ReasoningText string

	// Compaction fields (populated when Type == PartCompaction)
	CompactionSummary string
	CompactedAt       time.Time
}

// Message is an ordered sequence of Parts, with metadata.
type Message struct {
	ID         string
	SessionID  string
	Role       MessageRole
	Parts      []Part
	Timestamp  time.Time
	CostUSD    float64
	ModelID    string
	TokenIn    int
	TokenOut   int
	StopReason string
}

// Session represents one conversation thread.
type Session struct {
	ID           string
	Title        string
	Directory    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CompactedAt  time.Time
	ArchivedAt   time.Time
	ModelID      string
	AgentName    string
	Permission   PermissionRuleset
	SummaryAdds  int
	SummaryDels  int
	SummaryFiles int
	RevertTo     *RevertPoint
}

// RevertPoint records a message-boundary snapshot for rollback.
type RevertPoint struct {
	MessageID string
	PartID    string
	Snapshot  string // git tree hash
	Diff      string
}

// PermissionRuleset is a map of tool name → action ("allow", "deny", "ask").
type PermissionRuleset map[string]string

// DefaultPermissions returns a conservative deny-first ruleset.
func DefaultPermissions() PermissionRuleset {
	return PermissionRuleset{
		"bash":     "ask",
		"read":     "allow",
		"glob":     "allow",
		"grep":     "allow",
		"edit":     "ask",
		"write":    "ask",
		"lsp":      "allow",
		"mcp":      "ask",
		"webfetch": "ask",
		"todowrite": "allow",
	}
}

// ToolDefinition describes a tool exposed to the agent.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
	Permissions PermissionRuleset
	Handler     func(ctx ToolContext, input map[string]any) (ToolResult, error)
}

// ToolContext carries per-call execution context.
type ToolContext struct {
	SessionID string
	MessageID string
	WorkingDir string
	DryRun    bool
}

// ToolResult is the output of a tool execution.
type ToolResult struct {
	Output   string
	IsError  bool
	CostUSD  float64
	Duration time.Duration
	Metadata map[string]any
}

// HookEvent is the lifecycle event type.
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

// HookPayload is the JSON payload delivered to hook commands.
type HookPayload struct {
	SessionID       string
	TranscriptPath  string
	CWD             string
	PermissionMode  string
	HookEventName   HookEvent
	ToolName        string
	ToolInput       map[string]any
	ToolOutput      string
	ToolIsError     bool
	StopReason      string
	UserPrompt      string
	CompactReason   string
	SubagentName    string
}

// HookOutput is a hook's response JSON.
type HookOutput struct {
	// For PreToolUse / PermissionRequest: allow, deny, ask
	Decision *string
	// For PreToolUse: rewrite the input
	UpdatedInput  *map[string]any
	// For PostToolUse / Stop: inject context text
	ContextInjection *string
	// Any hook: block continuation of agent loop
	StopContinuation *bool
}

// AgentConfig is the runtime configuration for the agent loop.
type AgentConfig struct {
	ModelID          string
	MaxTokens        int
	MaxTurns         int
	Effort           string // low, medium, high, xhigh
	PermissionMode   string // default, acceptEdits, bypassPermissions, plan
	AutoCompact      bool
	SubagentDepth    int
	MemoryEnabled    bool
	HooksEnabled     bool
}

// DefaultAgentConfig returns conservative defaults.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		ModelID:        "openai/gpt-4o",
		MaxTokens:      8192,
		MaxTurns:       90,
		Effort:         "medium",
		PermissionMode: "default",
		AutoCompact:    true,
		SubagentDepth:  1,
		MemoryEnabled:  true,
		HooksEnabled:   true,
	}
}
