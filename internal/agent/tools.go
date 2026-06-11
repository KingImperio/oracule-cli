package agent

import (
	"github.com/KingImperio/oracule-cli/pkg/models"
)

// registerBuiltinTools returns the initial tool registry.
// Extend this map as tools are implemented.
func registerBuiltinTools() []models.ToolDefinition {
	return []models.ToolDefinition{}
}
