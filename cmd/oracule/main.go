package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KingImperio/oracule-cli/internal/agent"
	"github.com/KingImperio/oracule-cli/internal/config"
	"github.com/KingImperio/oracule-cli/internal/hook"
	"github.com/KingImperio/oracule-cli/internal/memory"
	"github.com/KingImperio/oracule-cli/internal/permission"
	"github.com/KingImperio/oracule-cli/internal/provider"
	"github.com/KingImperio/oracule-cli/internal/storage"
	"github.com/KingImperio/oracule-cli/pkg/ctx"
	"github.com/KingImperio/oracule-cli/pkg/models"
	"github.com/chzyer/readline"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "oracule",
		Short: "Oracule — next-generation agentic CLI coding tool",
		RunE:  runInteractive,
	}
	config.EnsureFlags(root)

	root.AddCommand(
		cmdRun(),
		cmdVersion(),
		cmdProviderList(),
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

	// --- Permission layer ---
	perms := permission.NewEngine(permission.Merge(permission.Defaults(), cfg.PermitOverrides))

	// --- Provider layer ---
	prov, err := provider.New(cfg.Provider)
	if err != nil {
		return err
	}

	// --- ctx Registry ---
	globalRegistry, err := ctx.Load(cfg.CtxDir)
	if err != nil {
		logger.Warn().Err(err).Str("dir", cfg.CtxDir).Msg("failed to load global ctx")
		globalRegistry = ctx.NewRegistry()
	}
	localRegistry, err := ctx.Load(filepath.Join(cfg.WorkingDir, ".ctx"))
	if err != nil {
		localRegistry = ctx.NewRegistry()
	}
	reg := ctx.Merge(globalRegistry, localRegistry)

	// --- Tool registry ---
	tools := agent.RegisterBuiltinTools()
	tools = append(tools, agent.CtxSuggestTool(reg), agent.CtxSkillGetTool(reg), agent.CtxDiscoverTool(reg))

	// --- Session store ---
	sessionStore := storage.NewInMemory()

	// --- Agent ---
	ag := agent.NewAgent(models.AgentConfig{
		ModelID:        cfg.Model,
		MaxTokens:      8192,
		MaxTurns:       90,
		Effort:         cfg.Effort,
		PermissionMode: cfg.PermissionMode,
		AutoCompact:    true,
		SubagentDepth:  1,
		MemoryEnabled:  cfg.Memory.Enabled,
		HooksEnabled:   true,
	}, prov, mem, hooks, perms, tools, sessionStore, reg, logger)

	// --- Session lifecycle ---
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = agent.NewSessionID(cfg.WorkingDir)
	}
	if _, err := sessionStore.GetSession(nil, sessionID); err != nil {
		_ = sessionStore.SaveSession(nil, &models.Session{ID: sessionID})
	}

	fmt.Fprintf(os.Stderr, "\n  oracule — %s\n", prov.ModelID())
	fmt.Fprintf(os.Stderr, "  session: %s\n", sessionID[:16])
	fmt.Fprintf(os.Stderr, "  ctx: %d skillsets, %d skills, %d MCPs, %d CLIs\n\n",
		len(reg.SkillSets), len(reg.Skills), len(reg.MCPs), len(reg.CLIs))
	fmt.Fprintf(os.Stderr, "  Type /help for commands, Ctrl+C to cancel, Ctrl+D to exit.\n\n")

	// Readline with per-session history.
	histDir := filepath.Join(os.TempDir(), "oracule-history")
	os.MkdirAll(histDir, 0755)
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          ">>> ",
		HistoryFile:     filepath.Join(histDir, sessionID[:16]),
		InterruptPrompt: "^C\n",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return err
	}
	defer rl.Close()

	baseCtx := cmd.Context()

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			// Ctrl+C while idle — skip.
			if line == "" {
				continue
			}
			// Ctrl+C while typing — clear line.
			continue
		}
		if err == io.EOF {
			// Ctrl+D — exit.
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "readline error: %v\n", err)
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Handle built-in commands.
		if strings.HasPrefix(line, "/") {
			if line == "/exit" || line == "/quit" {
				fmt.Fprintf(os.Stderr, "bye\n")
				break
			}
			handleCommand(line, prov, sessionID, rl)
			continue
		}

		// Run a single turn with the user prompt (5-minute timeout).
		// Print a newline first to separate prompt from streaming output.
		fmt.Fprintln(os.Stderr)
		turnCtx, cancel := context.WithTimeout(baseCtx, 5*time.Minute)
		result, runErr := ag.Run(turnCtx, sessionID, line, streamHandler)
		cancel()

		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		}

		if result != nil {
			switch result.Reason {
			case "doom_loop":
				fmt.Fprintf(os.Stderr, "doom-loop detected — interrupting\n")
			case "interrupted":
				fmt.Fprintf(os.Stderr, "interrupted\n")
			}
		}

		// Prompt is redrawn automatically by readline on the next Readline() call.
	}

	return nil
}

// streamHandler renders streaming events to the terminal.
func streamHandler(evt agent.StreamEvent) error {
	switch evt.Type {
	case "text_delta":
		os.Stdout.WriteString(evt.Text)
	case "reasoning_delta":
		fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", evt.Text)
	case "tool_start":
		fmt.Fprintf(os.Stderr, "\n  \033[33m→\033[0m %s\n", evt.ToolName)
	case "tool_end":
		if evt.ToolOk {
			fmt.Fprintf(os.Stderr, "  \033[32m←\033[0m %s OK\n", evt.ToolName)
		} else {
			fmt.Fprintf(os.Stderr, "  \033[31m←\033[0m %s failed\n", evt.ToolName)
		}
	case "done":
		if evt.CostUSD > 0 {
			fmt.Fprintf(os.Stderr, "\n  \033[2mcost: $%.4f (in: %d, out: %d)\033[0m\n", evt.CostUSD, evt.TokenIn, evt.TokenOut)
		}
	}
	return nil
}

// handleCommand processes /slash commands.
func handleCommand(line string, prov provider.Provider, sessionID string, rl *readline.Instance) {
	parts := strings.Fields(line)
	cmd := parts[0]

	switch cmd {
	case "/help":
		fmt.Fprintf(os.Stderr, "\n  Commands:\n")
		fmt.Fprintf(os.Stderr, "    /exit, /quit   Exit the session\n")
		fmt.Fprintf(os.Stderr, "    /help          Show this help\n")
		fmt.Fprintf(os.Stderr, "    /model         Show current model\n")
		fmt.Fprintf(os.Stderr, "    /session       Show session ID\n")
		fmt.Fprintf(os.Stderr, "    /clear         Clear screen\n")
		fmt.Fprintf(os.Stderr, "    /providers     List providers\n\n")
		fmt.Fprintf(os.Stderr, "  Any other input is sent to the agent.\n")
		fmt.Fprintf(os.Stderr, "  Ctrl+C during a response cancels it.\n")
		fmt.Fprintf(os.Stderr, "  Ctrl+D at the prompt exits.\n\n")

	case "/model":
		fmt.Fprintf(os.Stderr, "  model: %s\n", prov.ModelID())

	case "/session":
		fmt.Fprintf(os.Stderr, "  session: %s\n", sessionID)

	case "/clear":
		fmt.Fprint(os.Stderr, "\033[H\033[2J")

	case "/providers":
		fmt.Fprintf(os.Stderr, "\n  %-20s %-45s %s\n", "NAME", "BASE URL", "MODELS")
		for _, p := range knownProviders {
			fmt.Fprintf(os.Stderr, "  %-20s %-45s %s\n", p.Name, p.BaseURL, p.Models)
		}
		fmt.Fprintf(os.Stderr, "\n  Usage: oracule --model <prefix>/<model>\n")
		fmt.Fprintf(os.Stderr, "  Example: oracule --model opencode/glm-4.7-free\n")
		fmt.Fprintf(os.Stderr, "  Or set OPENCODE_API_KEY or NVIDIA_NIM_API_KEY for auto-detect.\n\n")

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
	}
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

func runOnce(ctx context.Context, prompt string) error {
	return errors.New("runOnce not implemented yet")
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

// providerInfo describes a known provider preset.
type providerInfo struct {
	Name        string
	Description string
	BaseURL     string
	Models      string
}

var knownProviders = []providerInfo{
	{"OpenAI", "Official OpenAI API (paid)", "https://api.openai.com/v1", "gpt-4o, gpt-4o-mini, o3, o4-mini"},
	{"Anthropic", "Official Anthropic API (paid)", "https://api.anthropic.com", "claude-sonnet-4, claude-haiku-4, claude-opus-4"},
	{"Google Gemini", "Official Google API (paid/free tier)", "https://generativelanguage.googleapis.io/v1beta", "gemini-2.5-flash, gemini-2.5-pro"},
	{"OpenCode Zen", "Curated coding models via opencode.ai (paid/free)", "https://opencode.ai/zen/v1", "nemotron-3-ultra-free, glm-4.7-free, minimax-m2.1-free, gpt-5.1"},
	{"NVIDIA NIM", "Free 100+ open models via build.nvidia.com", "https://integrate.api.nvidia.com/v1", "deepseek-v4-flash, nemotron-super-49b, glm-5.1, qwen3.5"},
	{"Kilo Gateway", "Coding-optimized gateway with free models", "https://api.kilo.ai/api/gateway", "minimax-m2.5:free, grok-code-fast-1:free, nemotron-3-super:free"},
	{"Groq", "Fast inference, generous free tier", "https://api.groq.com/openai/v1", "llama-4.0, mixtral-8x7b, deepseek-r1"},
	{"Together AI", "Open-source model playground (paid/free)", "https://api.together.xyz/v1", "qwen3-coder, deepseek-v4, llama-4"},
	{"OpenRouter", "Multi-model router with free tier", "https://openrouter.ai/api/v1", "200+ models available"},
	{"DeepSeek", "DeepSeek official API (cheap)", "https://api.deepseek.com/v1", "deepseek-chat, deepseek-reasoner"},
	{"Mistral AI", "Mistral official API (paid/free)", "https://api.mistral.ai/v1", "mistral-large, mistral-small, codestral"},
	{"Ollama", "Local models (free, self-hosted)", "http://localhost:11434/v1", "qwen3-coder, deepseek-v4, llama-4, any local model"},
}

func cmdProviderList() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List available LLM providers and their endpoints",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-45s %s\n", "NAME", "BASE URL", "MODELS")
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-45s %s\n", "----", "--------", "------")
			for _, p := range knownProviders {
				fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-45s %s\n", p.Name, p.BaseURL, p.Models)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nUsage: oracule --model <prefix>/<model>\n")
			fmt.Fprintf(cmd.OutOrStdout(), "Example: oracule --model opencode/glm-4.7-free\n")
			fmt.Fprintf(cmd.OutOrStdout(), "\nOr set a provider-specific env var for auto-detect:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  OPENCODE_API_KEY    → opencode.ai/zen (free tier available)\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  NVIDIA_NIM_API_KEY  → build.nvidia.com (100+ free models)\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  ORACULE_API_KEY     → generic (requires --model)\n")
		},
	}
}
