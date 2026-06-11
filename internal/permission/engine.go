package permission

import (
	"github.com/KingImperio/oracule-cli/pkg/models"
)

// Action is the disposition for a tool call.
type Action int

const (
	Deny Action = iota
	Allow
	Ask
)

// Engine is the deny-first permission checker.
type Engine struct {
	rules       models.PermissionRuleset
	askAllowSudo bool // if true, Ask falls back to Allow in non-interactive contexts
}

// NewEngine builds a permission engine from a fully-resolved ruleset.
// The ruleset is taken by reference; caller should treat it as immutable.
func NewEngine(rules models.PermissionRuleset) *Engine {
	if rules == nil {
		rules = make(models.PermissionRuleset)
	}
	return &Engine{rules: rules}
}

// Evaluate returns the disposition for a tool call given its name and input.
// Lookup order (first match wins):
//  1. Exact tool name → action
//  2. tool_name:* pattern (not yet supported; reserved for future)
//  3. Wildcard "*" → action
//  4. Default: Ask
func (e *Engine) Evaluate(toolName string, _ map[string]any) Action {
	if e == nil {
		return Ask
	}
	if a, ok := e.rules[toolName]; ok {
		return parseAction(a)
	}
	if a, ok := e.rules["*"]; ok {
		return parseAction(a)
	}
	return Ask
}

// Defaults returns the out-of-the-box permission ruleset.
// Reads/files and searches are allowed; shells and edits require confirmation.
func Defaults() models.PermissionRuleset {
	return models.DefaultPermissions()
}

// Merge combines user-supplied overrides onto a base ruleset.
// Overrides take precedence for matching keys; the rest of the base is preserved.
func Merge(base, overrides models.PermissionRuleset) models.PermissionRuleset {
	out := make(models.PermissionRuleset, len(base)+len(overrides))
	for k, v := range base {
		out[k] = parseAction(v)
	}
	for k, v := range overrides {
		out[k] = parseAction(v)
	}
	return out
}

// parseAction converts a string to Action; unknown strings default to Ask.
func parseAction(s string) Action {
	switch s {
	case "allow":
		return Allow
	case "deny":
		return Deny
	case "ask":
		return Ask
	default:
		return Ask
	}
}
