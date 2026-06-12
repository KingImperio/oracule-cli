package tools

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/models"
)

func ReadTool() models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "read",
		Description: "Read the contents of a file, with optional line offset and limit. Supports text files up to 2000 lines.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to read.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Starting line number (1-indexed, default 1).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of lines to read (default 2000).",
				},
			},
			"required": []string{"file_path"},
		},
		Handler: readHandler,
	}
}

func readHandler(ctx models.ToolContext, input map[string]any) (models.ToolResult, error) {
	path, _ := input["file_path"].(string)
	if path == "" {
		return models.ToolResult{IsError: true, Output: "file_path is required"}, nil
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.WorkingDir, path)
	}

	// Security: deny path traversal
	clean := filepath.Clean(path)
	abs, err := filepath.Abs(clean)
	if err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("invalid path: %v", err)}, nil
	}

	info, err := os.Stat(abs)
	if err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("cannot read file: %v", err)}, nil
	}
	if info.IsDir() {
		dir, err := os.ReadDir(abs)
		if err != nil {
			return models.ToolResult{IsError: true, Output: fmt.Sprintf("cannot read directory: %v", err)}, nil
		}
		var names []string
		for _, e := range dir {
			names = append(names, e.Name())
		}
		return models.ToolResult{
			Output: fmt.Sprintf("Directory listing for %s:\n  %s", abs, strings.Join(names, "\n  ")),
			Metadata: map[string]any{"file_count": len(names)},
		}, nil
	}

	offset := 1
	if o, ok := input["offset"].(float64); ok {
		offset = int(math.Max(1, o))
	}
	limit := 2000
	if l, ok := input["limit"].(float64); ok {
		limit = int(math.Max(1, l))
	}

	start := time.Now()
	data, err := os.ReadFile(abs)
	if err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("cannot read file: %v", err)}, nil
	}
	duration := time.Since(start)

	lines := strings.Split(string(data), "\n")

	if offset > len(lines) {
		return models.ToolResult{
			Output: fmt.Sprintf("file has %d lines, offset %d is beyond end", len(lines), offset),
			Metadata: map[string]any{
				"total_lines": len(lines),
				"file_size":   len(data),
			},
		}, nil
	}

	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}
	selected := lines[offset-1 : end]

	var b strings.Builder
	totalLines := len(lines)

	b.WriteString(fmt.Sprintf("File: %s (%d lines, %d bytes)\n", abs, totalLines, len(data)))
	b.WriteString("---\n")
	for i, line := range selected {
		b.WriteString(fmt.Sprintf("%d: %s\n", offset+i, line))
	}

	return models.ToolResult{
		Output:   b.String(),
		Duration: duration,
		Metadata: map[string]any{
			"total_lines": totalLines,
			"file_size":   len(data),
			"lines_read":  len(selected),
		},
	}, nil
}
