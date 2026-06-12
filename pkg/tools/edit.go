package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/models"
)

func EditTool() models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "edit",
		Description: "Apply a search-and-replace edit to a file. Finds the exact `old_string` and replaces it with `new_string`. Use for making targeted changes to existing files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to edit.",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "The exact text to search for — must match exactly, including whitespace.",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "The replacement text.",
				},
			},
			"required": []string{"file_path", "old_string", "new_string"},
		},
		Handler: editHandler,
	}
}

func editHandler(ctx models.ToolContext, input map[string]any) (models.ToolResult, error) {
	path, _ := input["file_path"].(string)
	oldStr, _ := input["old_string"].(string)
	newStr, _ := input["new_string"].(string)

	if path == "" {
		return models.ToolResult{IsError: true, Output: "file_path is required"}, nil
	}
	if oldStr == "" {
		return models.ToolResult{IsError: true, Output: "old_string is required"}, nil
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.WorkingDir, path)
	}
	clean := filepath.Clean(path)
	abs, err := filepath.Abs(clean)
	if err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("invalid path: %v", err)}, nil
	}

	// Dry-run mode: show the planned change
	if ctx.DryRun {
		data, err := os.ReadFile(abs)
		if err != nil {
			return models.ToolResult{IsError: true, Output: fmt.Sprintf("cannot read file: %v", err)}, nil
		}
		content := string(data)
		count := strings.Count(content, oldStr)
		if count == 0 {
			return models.ToolResult{IsError: true, Output: "old_string not found in file"}, nil
		}
		return models.ToolResult{
			Output: fmt.Sprintf("Will replace %d occurrence(s) of old_string with new_string in %s", count, abs),
			Metadata: map[string]any{
				"replacements": count,
			},
		}, nil
	}

	start := time.Now()
	data, err := os.ReadFile(abs)
	if err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("cannot read file: %v", err)}, nil
	}

	content := string(data)
	count := strings.Count(content, oldStr)
	if count == 0 {
		return models.ToolResult{IsError: true, Output: "old_string not found in file"}, nil
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)

	if err := os.WriteFile(abs, []byte(newContent), 0644); err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("cannot write file: %v", err)}, nil
	}

	duration := time.Since(start)

	return models.ToolResult{
		Output:   fmt.Sprintf("Applied edit: %d occurrence(s) replaced in %s", count, abs),
		Duration: duration,
		Metadata: map[string]any{
			"replacements": count,
		},
	}, nil
}
