package agent

import (
	"encoding/json"
	"fmt"

	"github.com/KingImperio/oracule-cli/pkg/ctx"
	"github.com/KingImperio/oracule-cli/pkg/models"
	"github.com/KingImperio/oracule-cli/pkg/tools"
)

// RegisterBuiltinTools returns the built-in tool registry.
func RegisterBuiltinTools() []models.ToolDefinition {
	return []models.ToolDefinition{
		tools.BashTool(),
		tools.ReadTool(),
		tools.EditTool(),
		tools.GlobTool(),
		tools.GrepTool(),
	}
}

// CtxSuggestTool returns a tool that suggests relevant skills from the ctx
// registry for a given task. Returns only name + 1-line description — zero
// token bloat until ctx_skill_get is called.
func CtxSuggestTool(reg *ctx.Registry) models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "ctx_suggest",
		Description: "Find relevant skills, MCPs, and CLIs for a task. Call this first to discover what's available in the ctx ecosystem.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Description of what you're trying to do.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of skill suggestions (default 8).",
				},
			},
			"required": []string{"task"},
		},
		Permissions: models.PermissionRuleset{"ctx_suggest": "allow"},
		Handler: func(_ models.ToolContext, input map[string]any) (models.ToolResult, error) {
			task, _ := input["task"].(string)
			if task == "" {
				return models.ToolResult{IsError: true, Output: "task is required"}, nil
			}
			max := 8
			if m, ok := input["max_results"].(float64); ok && m > 0 {
				max = int(m)
			}

			sug := reg.Suggest(task, max)
			data, err := json.MarshalIndent(sug, "", "  ")
			if err != nil {
				return models.ToolResult{IsError: true, Output: fmt.Sprintf("marshal error: %v", err)}, nil
			}
			return models.ToolResult{Output: string(data)}, nil
		},
	}
}

// CtxSkillGetTool returns a tool that loads the full body of a named skill.
func CtxSkillGetTool(reg *ctx.Registry) models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "ctx_skill_get",
		Description: "Load the full body of a skill by name. Use this after ctx_suggest returns a skill you want to use.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Name of the skill to load.",
				},
			},
			"required": []string{"name"},
		},
		Permissions: models.PermissionRuleset{"ctx_skill_get": "allow"},
		Handler: func(_ models.ToolContext, input map[string]any) (models.ToolResult, error) {
			name, _ := input["name"].(string)
			if name == "" {
				return models.ToolResult{IsError: true, Output: "name is required"}, nil
			}

			// Check SkillSets first.
			if skill := reg.GetSkill(name); skill != nil {
				return models.ToolResult{
					Output: fmt.Sprintf("# %s\n\n%s\n\n%s", skill.Name, skill.Description, skill.Body),
				}, nil
			}

			// Check legacy skills.
			if legacy := reg.GetLegacySkill(name); legacy != nil {
				return models.ToolResult{
					Output: fmt.Sprintf("# %s\n\n%s\n\n%s", legacy.Name, legacy.Description, legacy.Body),
				}, nil
			}

			return models.ToolResult{IsError: true, Output: fmt.Sprintf("skill not found: %s", name)}, nil
		},
	}
}

// CtxDiscoverTool returns a tool that scans the registry and returns a summary
// of all available capabilities.
func CtxDiscoverTool(reg *ctx.Registry) models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "ctx_discover",
		Description: "Scan the ctx registry and return a summary of all available skillsets, skills, MCPs, and CLIs.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Optional filter: 'skillsets', 'skills', 'mcps', 'clis', or empty for all.",
				},
			},
		},
		Permissions: models.PermissionRuleset{"ctx_discover": "allow"},
		Handler: func(_ models.ToolContext, input map[string]any) (models.ToolResult, error) {
			filter, _ := input["filter"].(string)
			return models.ToolResult{Output: discoverOutput(reg, filter)}, nil
		},
	}
}

func discoverOutput(reg *ctx.Registry, filter string) string {
	switch filter {
	case "skillsets":
		return formatSkillSets(reg)
	case "skills":
		return formatSkills(reg)
	case "mcps":
		return formatMCPs(reg)
	case "clis":
		return formatCLIs(reg)
	}

	ss, s, m, c := reg.Stats()
	out := fmt.Sprintf("## ctx Registry\n\n- **Skillsets:** %d\n- **Skills:** %d\n- **MCPs:** %d\n- **CLIs:** %d\n\n", ss, s, m, c)
	out += "Use `ctx_discover` with `filter` set to one of: skillsets, skills, mcps, clis\n"
	out += "Use `ctx_suggest` with a task description to find relevant skills.\n"
	return out
}

func formatSkillSets(reg *ctx.Registry) string {
	out := "## SkillSets\n\n"
	for name, ss := range reg.SkillSets {
		out += fmt.Sprintf("- **%s** (v%s): %d skills", name, ss.Version, len(ss.Skills))
		if len(ss.CompanionSets) > 0 {
			out += fmt.Sprintf(" | companions: %v", ss.CompanionSets)
		}
		out += "\n"
		for _, sk := range ss.Skills {
			out += fmt.Sprintf("  - `%s` — %s\n", sk.Name, sk.Description)
		}
	}
	return out
}

func formatSkills(reg *ctx.Registry) string {
	out := "## Skills (legacy)\n\n"
	for _, sk := range reg.Skills {
		out += fmt.Sprintf("- `%s` — %s\n", sk.Name, sk.Description)
	}
	return out
}

func formatMCPs(reg *ctx.Registry) string {
	out := "## MCPs\n\n"
	for _, mcp := range reg.MCPs {
		out += fmt.Sprintf("- **%s**: %s (`%s`)\n", mcp.Name, mcp.Description, mcp.Command)
	}
	return out
}

func formatCLIs(reg *ctx.Registry) string {
	out := "## CLIs\n\n"
	for _, cli := range reg.CLIs {
		out += fmt.Sprintf("- **%s**: `%s` — %s\n", cli.Name, cli.Binary, cli.Description)
	}
	return out
}
