package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/models"
)

const maxGlobResults = 200

func GlobTool() models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "glob",
		Description: "Search for files matching a glob pattern. Returns up to 200 matching file paths sorted by modification time (newest first).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to match (e.g. \"**/*.go\", \"src/**/*.ts\").",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Search root directory (default: working directory).",
				},
			},
			"required": []string{"pattern"},
		},
		Handler: globHandler,
	}
}

func globHandler(ctx models.ToolContext, input map[string]any) (models.ToolResult, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return models.ToolResult{IsError: true, Output: "pattern is required"}, nil
	}

	root := ctx.WorkingDir
	if r, ok := input["path"].(string); ok && r != "" {
		root = r
	}

	start := time.Now()

	matches, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		return models.ToolResult{IsError: true, Output: fmt.Sprintf("invalid glob pattern: %v", err)}, nil
	}

	// Sort by mod time (newest first)
	type entry struct {
		path string
		mod  int64
	}
	var entries []entry
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		entries = append(entries, entry{m, info.ModTime().UnixNano()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mod > entries[j].mod
	})

	duration := time.Since(start)
	total := len(entries)
	if total > maxGlobResults {
		entries = entries[:maxGlobResults]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d file(s) matching %q", total, pattern))
	if total > maxGlobResults {
		b.WriteString(fmt.Sprintf(" (showing %d)", maxGlobResults))
	}
	b.WriteString(":\n")
	for _, e := range entries {
		rel, _ := filepath.Rel(root, e.path)
		b.WriteString("  ")
		b.WriteString(rel)
		b.WriteByte('\n')
	}

	return models.ToolResult{
		Output:   strings.TrimSpace(b.String()),
		Duration: duration,
		Metadata: map[string]any{
			"total":  total,
			"shown":  min(total, maxGlobResults),
		},
	}, nil
}
