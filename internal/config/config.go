package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/KingImperio/oracule-cli/internal/memory"
)

// Load reads the merged configuration from all sources.
type Load struct {
	Model         string
	Effort        string
	PermissionMode string
	WorkingDir    string
	SessionID     string
	Provider      ProviderConfig
	Memory        memory.Config
	PermitOverrides map[string]string
}

// ProviderConfig is the LLM provider section.
type ProviderConfig struct {
	Provider string // "openai", "anthropic", "google", "ollama"
	APIKey   string
	BaseURL  string
	Model    string
}

// DefaultConfig returns sane defaults.
func DefaultConfig() Load {
	wd, _ := os.Getwd()
	return Load{
		Model:          "openai/gpt-4o",
		Effort:         "medium",
		PermissionMode: "default",
		WorkingDir:     wd,
		Provider: ProviderConfig{
			Provider: "openai",
		},
		Memory: memory.DefaultMemoryConfig(),
		PermitOverrides: nil,
	}
}

// Load reads cobra command + viper bindings and returns the merged config.
// Priority (low to high): viper defaults → env vars → project config → flags.
func Load(cmd *cobra.Command) (Load, error) {
	cfg := DefaultConfig()
	v := viper.New()

	// Bind environment variables with Oracule prefix.
	_ = v.BindEnv("provider.api_key", "ORACULE_API_KEY")
	_ = v.BindEnv("provider.base_url", "ORACULE_BASE_URL")
	_ = v.BindEnv("provider.model", "ORACULE_MODEL")
	_ = v.BindEnv("memory.enabled", "ORACULE_MEMORY_ENABLED")
	_ = v.BindEnv("memory.dir", "ORACULE_MEMORY_DIR")

	// Project-level config file: oracule.json or oracule.yaml in WorkingDir.
	v.AddConfigPath(cfg.WorkingDir)
	v.AddConfigPath("$HOME/.oracule")
	v.AddConfigPath("/etc/oracule")
	v.SetConfigName("oracule")
	v.SetConfigType("json")
	_ = v.ReadInConfig() // best-effort; ignore if missing

	// Flags override config file.
	_ = v.BindPFlag("model", cmd.Flags().Lookup("model"))
	_ = v.BindPFlag("effort", cmd.Flags().Lookup("effort"))
	_ = v.BindPFlag("permission-mode", cmd.Flags().Lookup("permission-mode"))
	_ = v.BindPFlag("session-id", cmd.Flags().Lookup("session-id"))

	cfg.Model = v.GetString("model")
	if cfg.Model == "" {
		cfg.Model = DefaultConfig().Model
	}
	cfg.Effort = v.GetString("effort")
	if cfg.Effort == "" {
		cfg.Effort = DefaultConfig().Effort
	}
	cfg.PermissionMode = v.GetString("permission-mode")
	cfg.SessionID = v.GetString("session-id")
	cfg.Provider.APIKey = v.GetString("provider.api_key")
	cfg.Provider.BaseURL = v.GetString("provider.base_url")
	cfg.Provider.Model = v.GetString("provider.model")
	if cfg.Provider.Model != "" {
		cfg.Model = cfg.Provider.Model
	}
	cfg.Memory.Enabled = v.GetBool("memory.enabled")
	if v.GetString("memory.dir") != "" {
		cfg.Memory.MemoryDir = v.GetString("memory.dir")
		cfg.Memory.UserMemoryPath = filepath.Join(cfg.Memory.MemoryDir, "USER.md")
		cfg.Memory.SessionDBPath = filepath.Join(cfg.Memory.MemoryDir, "state.db")
	}

	// Working directory from cobda flag or cwd.
	if wdFlag, _ := cmd.Flags().GetString("workdir"); wdFlag != "" {
		cfg.WorkingDir = wdFlag
	} else if cfg.WorkingDir == "" {
		cfg.WorkingDir, _ = os.Getwd()
	}

	return cfg, nil
}

// EnsureFlags registers standard flags on the root command.
func EnsureFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("model", "m", "", "Model identifier (provider/model)")
	cmd.Flags().String("effort", "medium", "Effort: low, medium, high, xhigh")
	cmd.Flags().String("permission-mode", "default", "Permission mode: default, plan, bypassPermissions")
	cmd.Flags().String("session-id", "", "Session ID (empty = new session)")
	cmd.Flags().String("workdir", "", "Working directory (default: cwd)")
}
