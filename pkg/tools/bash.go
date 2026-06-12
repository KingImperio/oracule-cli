package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/models"
)

const maxOutputBytes = 1_000_000

func BashTool() models.ToolDefinition {
	return models.ToolDefinition{
		Name:        "bash",
		Description: "Execute a bash command and return its stdout, stderr, and exit code. Use for running shell commands, scripts, and development tools.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to execute. Prefer single commands or `&&` chains over scripts.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "A short description of what this command does (for logging and approval).",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in milliseconds (default 30000).",
				},
			},
			"required": []string{"command"},
		},
		Handler: bashHandler,
	}
}

func bashHandler(ctx models.ToolContext, input map[string]any) (models.ToolResult, error) {
	cmdStr, _ := input["command"].(string)
	if cmdStr == "" {
		return models.ToolResult{IsError: true, Output: "command is required"}, nil
	}

	timeoutMs := 30000
	if t, ok := input["timeout"].(float64); ok {
		timeoutMs = int(t)
	}

	execCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(execCtx, "bash", "-c", cmdStr)
	cmd.Dir = ctx.WorkingDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if execCtx.Err() != nil {
			exitCode = -1
			err = fmt.Errorf("command timed out after %dms", timeoutMs)
		} else {
			exitCode = -1
		}
	}

	out := stdout.String()
	errOut := stderr.String()

	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n... [output truncated]"
	}
	if len(errOut) > maxOutputBytes/2 {
		errOut = errOut[:maxOutputBytes/2] + "\n... [stderr truncated]"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Exit code: %d\n", exitCode))
	if out != "" {
		b.WriteString("--- stdout ---\n")
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteByte('\n')
		}
	}
	if errOut != "" {
		b.WriteString("--- stderr ---\n")
		b.WriteString(errOut)
		if !strings.HasSuffix(errOut, "\n") {
			b.WriteByte('\n')
		}
	}

	return models.ToolResult{
		Output:   strings.TrimSpace(b.String()),
		IsError:  exitCode != 0,
		Duration: duration,
		Metadata: map[string]any{
			"exit_code": exitCode,
			"duration":  duration.String(),
		},
	}, nil
}
