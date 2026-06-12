package tools

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/models"
)

func GrepTool() models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "grep",
		Description: "Search file contents using a regex pattern. Wraps `rg` (ripgrep) or falls back to `grep -r`. Returns matches with line numbers.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for.",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "File glob pattern to filter (e.g. \"*.go\", \"*.{ts,tsx}\").",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Search root directory (default: working directory).",
				},
			},
			"required": []string{"pattern"},
		},
		Handler: grepHandler,
	}
}

func grepHandler(ctx models.ToolContext, input map[string]any) (models.ToolResult, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return models.ToolResult{IsError: true, Output: "pattern is required"}, nil
	}

	root := ctx.WorkingDir
	if r, ok := input["path"].(string); ok && r != "" {
		root = r
	}

	include, _ := input["include"].(string)

	start := time.Now()

	// Prefer ripgrep for speed, fall back to grep -rn
	var out []byte
	var err error

	args := []string{"-n", "--max-count", "20", pattern}
	if include != "" {
		args = append(args, "-g", include)
	}
	args = append(args, root)

	cmd := exec.Command("rg", args...)
	out, err = cmd.Output()

	// If rg is not available, use grep
	if err != nil {
		var grepArgs []string
		if include != "" {
			grepArgs = []string{"-rn", "--include=" + include, pattern, root}
		} else {
			grepArgs = []string{"-rn", pattern, root}
		}
		cmd := exec.Command("grep", grepArgs...)
		out, err = cmd.Output()
	}

	duration := time.Since(start)

	var lines []string
	outputStr := strings.TrimSpace(string(out))

	if outputStr == "" {
		return models.ToolResult{
			Output:   fmt.Sprintf("No matches found for %q", pattern),
			Duration: duration,
		}, nil
	}

	// Truncate if too long
	lines = strings.Split(outputStr, "\n")
	if len(lines) > 100 {
		outputStr = strings.Join(lines[:100], "\n") + fmt.Sprintf("\n... and %d more matches", len(lines)-100)
	}

	return models.ToolResult{
		Output:   outputStr,
		Duration: duration,
		Metadata: map[string]any{
			"match_count": len(lines),
		},
	}, nil
}
