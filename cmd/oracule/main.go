package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/KingImperio/oracule-cli/internal/agent"
	"github.com/KingImperio/oracule-cli/internal/config"
	"github.com/KingImperio/oracule-cli/internal/hook"
	"github.com/KingImperio/oracule-cli/internal/memory"
	"github.com/KingImperio/oracule-cli/internal/permission"
	"github.com/KingImperio/oracule-cli/internal/provider"
	"github.com/KingImperio/oracule-cli/internal/storage"
	"github.com/KingImperio/oracule-cli/pkg/models"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "oracule",
		Short: "Oracule — next-generation agentic CLI coding tool",
		RunE:  runInteractive,
	}

	root.AddCommand(
		cmdRun(),
		cmdVersion(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runInteractive(cmd *cobra.Command, args []string) error {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	cfg, err := config.Load(cmd)
	if err != nil {
		return err
	}

	// --- Memory layer ---
	mem, err := memory.NewManager(cfg.Memory, logger, nil)
	if err != nil {
		return err
	}
	defer mem.Close()

	// --- Hook layer ---
	hooks := hook.NewManager(true)

	// --- Permission layer (deny-first, merged with config) ---
	perms := permission.NewEngine(permission.Merge(permission.Defaults(), cfg.PermissionOverrides))

	// --- Provider layer ---
	prov, err := provider.New(cfg.Provider)
	if err != nil {
		return err
	}

	// --- Tool registry ---
	tools := registerBuiltinTools()

	// --- Session store (in-memory for v0; swap for SQLite-backed store) ---
	sessionStore := storage.NewInMemory()

	// --- Agent ---
	ag := agent.NewAgent(agent.AgentConfig{
		ModelID:        cfg.Model,
		MaxTokens:      8192,
		MaxTurns:       90,
		Effort:         cfg.Effort,
		PermissionMode: cfg.PermissionMode,
		AutoCompact:    true,
		SubagentDepth:  1,
		MemoryEnabled:  cfg.Memory.Enabled,
		HooksEnabled:   true,
	}, prov, mem, hooks, perms, tools, sessionStore, logger)

	// --- Session lifecycle ---
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = agent.NewSessionID(cfg.WorkingDir)
	}
	if _, err := sessionStore.GetSession(nil, sessionID); err != nil {
		_ = sessionStore.SaveSession(nil, &models.Session{ID: sessionID})
	}

	// --- Signal handling ---
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info().Str("session", sessionID).Str("model", prov.ModelID()).Msg("oracule ready")
	_, _ = ag.Run(ctx, sessionID, "", func(evt agent.StreamEvent) error {
		switch evt.Type {
		case "text_delta":
			os.Stdout.WriteString(evt.Text)
		case "tool_start":
			logger.Info().Str("tool", evt.ToolName).Msg("→ running")
		case "tool_end":
			logger.Info().Str("tool", evt.ToolName).Bool("ok", evt.ToolOk).Msg("← done")
		case "done":
			logger.Info().Float64("cost", evt.CostUSD).Msg("done")
		case "error":
			logger.Error().Str("err", evt.Text).Msg("error")
		}
		return nil
	})

	return nil
}

// cmdRun implements `oracule run` — a single-shot execution (CI/headless).
func cmdRun() *cobra.Command {
	var prompt string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a single prompt and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOnce(cmd.Context(), prompt)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "Prompt to execute (required)")
	_ = cmd.MarkFlagRequired("prompt")
	return cmd
}

// cmdVersion prints version info.
func cmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println("oracule-cli v0.1.0-dev")
		},
	}
}
