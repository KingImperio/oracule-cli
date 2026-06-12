package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/KingImperio/oracule-cli/internal/memory"
)

// Config holds the merged configuration from all sources.
type Config struct {
	Model         string
	Effort        string
	PermissionMode string
	WorkingDir    string
	SessionID     string
	CtxDir        string
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
func DefaultConfig() Config {
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	ctxDir := filepath.Join(home, ".ctx")
	return Config{
		Model:          "openai/gpt-4o",
		Effort:         "medium",
		PermissionMode: "default",
		WorkingDir:     wd,
		CtxDir:         ctxDir,
		Provider: ProviderConfig{
			Provider: "openai",
		},
		Memory: memory.DefaultMemoryConfig(),
		PermitOverrides: nil,
	}
}

// Load reads cobra command + viper bindings and returns the merged config.
// Priority (low to high): viper defaults → env vars → project config → flags.
func Load(cmd *cobra.Command) (Config, error) {
	cfg := DefaultConfig()
	v := viper.New()

	// Bind environment variables with Oracule prefix.
	_ = v.BindEnv("provider.api_key", "ORACULE_API_KEY")
	_ = v.BindEnv("provider.base_url", "ORACULE_BASE_URL")
	_ = v.BindEnv("provider.model", "ORACULE_MODEL")
	_ = v.BindEnv("memory.enabled", "ORACULE_MEMORY_ENABLED")
	_ = v.BindEnv("memory.dir", "ORACULE_MEMORY_DIR")
	_ = v.BindEnv("ctx_dir", "ORACULE_CTX_DIR")
	// Provider-specific env vars (auto-detect without requiring --model).
	_ = v.BindEnv("opencode.api_key", "OPENCODE_API_KEY")
	_ = v.BindEnv("nvidia.api_key", "NVIDIA_NIM_API_KEY")

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

	// Ctx directory.
	if v.GetString("ctx_dir") != "" {
		cfg.CtxDir = v.GetString("ctx_dir")
	}
	if cfg.CtxDir == "" {
		home, _ := os.UserHomeDir()
		cfg.CtxDir = filepath.Join(home, ".ctx")
	}

	// Auto-detect provider from model prefix (e.g. "kilo/..." → openai + base_url)
	// or from known provider env vars (e.g. OPENCODE_API_KEY → opencode/...).
	detectProviderFromModel(&cfg)
	autoDetectFromEnv(v, &cfg)

	// Working directory from cobda flag or cwd.
	if wdFlag, _ := cmd.Flags().GetString("workdir"); wdFlag != "" {
		cfg.WorkingDir = wdFlag
	} else if cfg.WorkingDir == "" {
		cfg.WorkingDir, _ = os.Getwd()
	}

	return cfg, nil
}

// knownFreeProviders maps model prefixes to provider + base_url for
// free/community model access without requiring a separate config step.
var knownFreeProviders = map[string]struct {
	provider string
	baseURL  string
}{
	"kilo":           {"openai", "https://api.kilo.ai/api/gateway"},
	"groq":           {"openai", "https://api.groq.com/openai/v1"},
	"ollama":         {"openai", "http://localhost:11434/v1"},
	"together":       {"openai", "https://api.together.xyz/v1"},
	"openrouter":     {"openai", "https://openrouter.ai/api/v1"},
	"deepseek":       {"openai", "https://api.deepseek.com/v1"},
	"mistral":        {"openai", "https://api.mistral.ai/v1"},
	"opencode":       {"openai", "https://opencode.ai/zen/v1"},
	"nim":            {"openai", "https://integrate.api.nvidia.com/v1"},
	"nvidia":         {"openai", "https://integrate.api.nvidia.com/v1"},
}

// autoDetectFromEnv checks for known provider-specific env vars and
// auto-configures the provider if no explicit model was set and the
// corresponding API key is present.
func autoDetectFromEnv(v *viper.Viper, cfg *Config) {
	// Only auto-detect if the model is still the default and no explicit flags.
	if !isDefaultModel(cfg.Model) {
		return
	}
	if cfg.Provider.APIKey != "" {
		return // ORACULE_API_KEY set but no explicit model — user must specify --model
	}
	if cfg.Provider.BaseURL != "" {
		return
	}

	// Check known provider env vars in priority order.
	type providerDefault struct {
		apiKey   string
		model    string
		baseURL  string
		provider string
	}

	checks := []providerDefault{
		{apiKey: v.GetString("opencode.api_key"), model: "opencode/deepseek-v4-flash-free", baseURL: "https://opencode.ai/zen/v1", provider: "openai"},
		{apiKey: v.GetString("nvidia.api_key"), model: "nim/deepseek-ai/deepseek-v4-flash", baseURL: "https://integrate.api.nvidia.com/v1", provider: "openai"},
	}

	for _, ch := range checks {
		if ch.apiKey != "" {
			cfg.Provider.Provider = ch.provider
			cfg.Provider.BaseURL = ch.baseURL
			cfg.Provider.APIKey = ch.apiKey
			cfg.Model = ch.model
			// Strip prefix for Provider.Model.
			if slash := strings.Index(ch.model, "/"); slash > 0 {
				cfg.Provider.Model = ch.model[slash+1:]
			}
			return
		}
	}
}

// isDefaultModel returns true if the model string is the built-in default or empty.
func isDefaultModel(model string) bool {
	return model == "" || model == "openai/gpt-4o"
}

func detectProviderFromModel(cfg *Config) {
	if cfg.Provider.Provider != "openai" {
		return // explicit provider set, don't override
	}
	if cfg.Provider.BaseURL != "" {
		return // explicit base URL set, don't override
	}

	// Parse prefix from model string: "kilo/model-name" → "kilo"
	slash := strings.Index(cfg.Model, "/")
	if slash <= 0 {
		return
	}
	prefix := cfg.Model[:slash]

	if p, ok := knownFreeProviders[prefix]; ok {
		cfg.Provider.Provider = p.provider
		cfg.Provider.BaseURL = p.baseURL
		// Strip the prefix from the model name for the API call.
		cfg.Provider.Model = cfg.Model[slash+1:]
	}
}

// EnsureFlags registers standard flags on the root command.
func EnsureFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("model", "m", "", "Model identifier (provider/model)")
	cmd.Flags().String("effort", "medium", "Effort: low, medium, high, xhigh")
	cmd.Flags().String("permission-mode", "default", "Permission mode: default, plan, bypassPermissions")
	cmd.Flags().String("session-id", "", "Session ID (empty = new session)")
	cmd.Flags().String("workdir", "", "Working directory (default: cwd)")
}
