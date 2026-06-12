# Oracule CLI

> Go-native, provider-agnostic agentic CLI coding tool with ctx-based zero-bloat skill ecosystem, deny-first permission model, SQLite session persistence, and debug-first interactive loop.

## Status: Pre-Alpha / Active Development

Built from scratch. Core architecture is solid. CLI works end-to-end with streaming. Bubble Tea v1 TUI prototype works in separate test project. Ready for Claude and Gemini to pick up.

---

## Table of Contents

1. [Vision & Goals](#vision--goals)
2. [Architecture](#architecture)
3. [What's Built — Detailed](#whats-built--detailed)
4. [Key Technical Decisions](#key-technical-decisions)
5. [Session Summary — What We Did](#session-summary--what-we-did)
6. [Current State & Capabilities](#current-state--capabilities)
7. [Known Issues & Blockers](#known-issues--blockers)
8. [Next Steps for Claude/Gemini](#next-steps-for-claudegemini)
9. [Bubble Tea TUI Test Project](#bubble-tea-tui-test-project)
10. [Running the Project](#running-the-project)

---

## Vision & Goals

Oracule is a next-generation agentic CLI coding tool — like `gh copilot` or `opencode`, but:

- **Provider-agnostic**: OpenAI-compatible, Anthropic, Google — plug your own key
- **Zero-bloat skill ecosystem**: The model only sees skill names + 1-line descriptions in the system prompt. Full body injected on demand via `ctx_skill_get` tool. This keeps the context window usable regardless of how many skills you have.
- **Deny-first by default**: Every tool call is blocked until the user explicitly permits it. No silent file writes.
- **Deterministic execution**: Session replay, structured memory, doom-loop detection.
- **TUI-first eventually**: Bubble Tea v1 prototype works; v2 hit compatibility issues (see blockers).

---

## Architecture

```
oracule-cli/
├── cmd/oracule/            — CLI entry point (cobra)
├── internal/
│   ├── agent/              — Core agentic loop, tools, assembleMessages, streaming
│   ├── config/             — Provider auto-detect, model flags, ctx path
│   ├── hook/               — Lifecycle hook manager
│   ├── memory/             — SQLite-backed session store (modernc.org/sqlite)
│   ├── permission/         — Deny-first interactive permission engine
│   ├── provider/           — Adapter layer: OpenAI, Anthropic, Google
│   └── storage/            — SQLite session CRUD (in-memory + persistent)
├── pkg/
│   ├── ctx/                — Skill registry: load, suggest, merge, lookup
│   ├── models/             — Shared types (Message, Session, ToolCall, Permission, Result)
│   └── tools/              — Built-in tools (Bash, Read, Edit, Glob, Grep)
├── go.mod / go.sum
├── README.md               ← this file
└── bubbletea-test/         — Standalone Bubble Tea test project (see below)
```

---

## What's Built — Detailed

### 1. Provider Layer (`internal/provider/`)

Three adapter implementations, all streaming:

| Provider | Package | Streaming | Notes |
|---|---|---|---|
| **OpenAI-compatible** | `openai.go` | Yes (SSE) | Works with DeepSeek, OpenCode, Groq, Together, Ollama, etc. |
| **Anthropic** | `anthropic.go` | Yes (SSE) | Messages API, tool_use |
| **Google** | `google.go` | Yes (SSE) | Gemini via genai SDK |

Provider auto-detect: checks `OPENCODE_API_KEY`, `NVIDIA_NIM_API_KEY` env vars, maps to known 10 free providers with base URLs and example models.

**Default model**: `deepseek-v4-flash-free` (via OpenCode Zen, free tier).

### 2. Agent Loop (`internal/agent/`)

- Turns user prompt → model → tool calls → execute → loop → final response
- `assembleMessages()` — builds message history from session + system prompt
- **Bug fix applied**: Was creating zero-value Message that caused 400 errors from DeepSeek/OpenCode. Fixed with `make([]models.Message, 0, len(history)+1)` pre-allocation.
- Doom-loop detection (breaks if agent enters infinite tool call cycle)
- Max turns = 90, max tokens = 8192, turn timeout = 5 min
- Streaming: text → stdout, reasoning (dimmed ANSI) + tool status (colored) → stderr

### 3. Built-in Tools (`pkg/tools/`)

| Tool | Permission | Description |
|---|---|---|
| `bash` | Ask | Execute shell commands with timeout |
| `read` | Allow | Read files from local filesystem |
| `edit` | Ask | Edit (search-and-replace) files |
| `glob` | Allow | File pattern matching |
| `grep` | Allow | Content search |

All gated through the permission engine. Bash and Edit require user confirmation (Ask mode).

### 4. ctx Ecosystem (`pkg/ctx/`)

Zero-bloat skill registry inspired by opencode's ctx system. 4 files:

| File | Purpose |
|---|---|
| `types.go` | Registry, SkillSet, Skill, LegacySkill, MCPDefinition, CLIDefinition, Suggestions |
| `load.go` | Scans 4 locations: `skillsets/*/skillset.json`, `skills/*/SKILL.md` (YAML frontmatter), `mcps/registry.json`, `clis/registry.json` |
| `suggest.go` | Keyword-tokenize + weighted scoring (name:3x, desc:2x, tags:1x) + stop-word filter |
| `registry.go` | `Merge()` global + local deep merge, `GetSkill()`/`GetLegacySkill()`, `Stats()` |

**3 ctx tools registered in the agent**:
- `ctx_suggest(query)` — suggest relevant skills from registry
- `ctx_skill_get(name)` — return full skill body for injection
- `ctx_discover()` — list all skillsets/skills/MCPs/CLIs with stats

### 5. Permission Engine (`internal/permission/`)

- Deny-first: ALL tool calls blocked by default
- Interactive `Ask` flow: prompts user on stderr with tool name, args, risk level
- `Merge()` — combines defaults with user overrides
- Permission modes: `ask`, `allow`, `deny`

### 6. Memory / Session Store (`internal/storage/`, `internal/memory/`)

- In-memory session store with SQLite persistence (`modernc.org/sqlite`, pure Go, no CGo)
- Session per working directory
- Auto-saves state between turns

### 7. Interactive CLI (`cmd/oracule/main.go`)

- Cobra command with `--model`, `--effort`, `--session-id`, `--permission-mode` flags
- **Readline integration**: `chzyer/readline` with per-session history files at `/tmp/oracule-history/<session_id>`
- Emacs keybindings (Ctrl+A/E/F/B, Ctrl+R reverse-i-search)
- Slash commands: `/help`, `/model`, `/session`, `/clear`, `/providers`, `/exit`
- `oracule providers` subcommand — lists 10 known providers with URLs and models

### 8. First SkillSet: frontend-dev

`.ctx/skillsets/frontend-dev/skillset.json` — 5 hand-authored skills:
- react-patterns, tailwind-config, responsive-design, a11y-patterns, form-handling

Legacy skills supported: `.ctx/skills/*/SKILL.md` with YAML frontmatter.

The global ctx directory is at `~/.ctx/` and project ctx at `./.ctx/`.

---

## Key Technical Decisions

| Decision | Choice | Rationale |
|---|---|---|
| **Language** | Go 1.26 | Latest stable. `unique` package, new slices/maps stdlib. |
| **SQLite driver** | `modernc.org/sqlite` | Pure Go. No CGo toolchain required, cross-compiles trivially. |
| **Provider adapters** | Hand-rolled per-provider | More control than generic router. Streaming SSE for all. |
| **ctx skill system** | JSON SkillSets + legacy SKILL.md | Agents write JSON more reliably than YAML frontmatter. JSON-only is the future path. |
| **Permission model** | Deny-first, interactive Ask | Safety-first. No silent file writes or arbitrary command execution. |
| **CLI input** | `chzyer/readline` | Pure Go, MIT, battle-tested (InfluxDB, k6). History + Emacs bindings + Ctrl+R search. |
| **Session persistence** | SQLite via modernc.org | Zero CGo, single-file DB, cross-platform. |
| **Logging** | `rs/zerolog` | Zero-allocation, structured, fast. |
| **Config** | `spf13/viper` + `spf13/cobra` | Industry standard for Go CLIs. |


## Session Summary — What We Did

### Phase 1: Research & Planning
- Researched Bubble Tea v2 (`charm.land/bubbletea/v2`) and readline libraries
- Chose `chzyer/readline` for CLI input (pure Go, MIT, 2.3k stars)
- Planned overall architecture: provider-agnostic, ctx ecosystem, deny-first permissions

### Phase 2: Core Architecture
- Scaffolded the project structure (cmd, internal, pkg directories)
- Built provider adapters for OpenAI, Anthropic, Google with streaming
- Built permission engine with deny-first Ask flow
- Built SQLite session persistence
- Built hook system, config layer, tool registry
- Implemented 5 built-in tools (bash, read, edit, glob, grep)

### Phase 3: ctx Ecosystem
- Designed and built `pkg/ctx/` Go library (types, load, suggest, registry)
- Created first SkillSet `frontend-dev` with 5 hand-authored skills
- Registered 3 ctx tools (`ctx_suggest`, `ctx_skill_get`, `ctx_discover`)
- Integrated ctx into agent loop and system prompt

### Phase 4: CLI Polish
- Replaced `bufio.Scanner` with `chzyer/readline` (history, editing, Ctrl+R)
- Added provider auto-detect from env vars (`OPENCODE_API_KEY`, `NVIDIA_NIM_API_KEY`)
- Added `oracule providers` list command with 10 providers
- Added `--model` flag via `EnsureFlags`
- Added slash commands: `/help`, `/model`, `/session`, `/clear`, `/providers`, `/exit`
- End-to-end test passed: `OPENCODE_API_KEY=sk-... oracule --model opencode/deepseek-v4-flash-free`
- Fixed `assembleMessages` zero-value bug causing 400 errors

### Phase 5: Bubble Tea TUI Test
- Created `/tmp/bubbletea-test/` standalone project
- Initially used Bubble Tea v2 (`charm.land/bubbletea/v2`) with Bubbles v2
- **Encountered critical issue**: v2 textarea component doesn't process keyboard input
  - Placeholder renders, Ctrl+C works, no cursor visible, no text input
  - Minimal test confirmed the bug — a bare textarea as sole model
  - Tried: `v.Cursor`, `WindowSizeMsg`, keyboard enhancements, `UseVirtualCursor(false)` — none fixed it
- **Switched to Bubble Tea v1** (`github.com/charmbracelet/bubbletea` v1.3.4) — works perfectly
  - Textarea accepts input, cursor blinks, Enter submits, streaming simulated

### Phase 6: Go Upgrade
- Upgraded Go from 1.24.4 to **1.26.4** (latest, linux/arm64)
- Downloaded from go.dev, installed at `/usr/local/go`
- Required for Bubble Tea v2 compatibility (v2 requires Go ≥1.25)

---

## Current State & Capabilities

### Working ✅
- [x] Streaming multi-provider LLM adapter (OpenAI, Anthropic, Google)
- [x] Interactive CLI with readline, history, slash commands
- [x] ctx skill registry (load, suggest, merge, lookup)
- [x] 5 built-in tools (bash, read, edit, glob, grep) with permission gating
- [x] SQLite session persistence
- [x] Provider auto-detect from env vars
- [x] Doom-loop detection, turn timeout
- [x] First SkillSet: frontend-dev (5 skills)
- [x] End-to-end test: OpenCode Zen with `deepseek-v4-flash-free`
- [x] Bubble Tea v1 chat TUI prototype (bubbletea-test/)

### Not Yet Built / Incomplete 🔧
- [ ] Bubble Tea v2 TUI integration into oracule (blocked — see below)
- [ ] Custom tool registration by plugins
- [ ] MCP server support
- [ ] Subagent orchestration (depth > 1)
- [ ] Full test suite
- [ ] Package distribution (brew, deb, etc.)
- [ ] RPC sandbox for subprocess execution
- [ ] LSP integration
- [ ] Multi-agent parallel execution

---

## Known Issues & Blockers

### Critical: Bubble Tea v2 Textarea does not process input
- **Symptom**: Placeholder renders, Ctrl+C quits, but no cursor and no text input
- **Tested**: Minimal program with bare textarea as sole model — same issue
- **Attempted fixes**:
  - Setting `v.Cursor` from `m.ta.Cursor()` (returns nil when using virtual cursor)
  - Explicit `WindowSizeMsg` handling to set textarea dimensions
  - Keyboard enhancements on View
  - Both virtual and real cursor modes
  - Debug logging via `tea.LogToFile()`
- **Workaround**: Switched to Bubble Tea v1 (`github.com/charmbracelet/bubbletea` v1.3.4) — works perfectly
- **Next step**: Investigate v2 compatibility. Possible root cause: the v2 cursed renderer may handle cursor/key events differently than v1. The textarea's `useVirtualCursor` (default true) might conflict with v2's hardware cursor model.

### Go 1.26.4 build timeouts on arm64
- `go build` and `go vet` time out after 60+ seconds
- Affected by heavy dependency tree (Google Cloud SDK, gRPC, Anthropic Go SDK)
- **Workaround**: Build with increased timeout. Not a code issue.
- The dependency tree is larger than ideal for a CLI tool. Consider trimming unused Google Cloud deps.

---

## Bubble Tea TUI Test Project

Located at `bubbletea-test/` within this repo. A standalone testbed for learning Bubble Tea patterns before integrating into oracule.

### Running

```bash
cd bubbletea-test
go build -o /tmp/bubblechat .
/tmp/bubblechat
```

### v1 Status (working) ✅
- Scrollable viewport for chat history
- Textarea input with placeholder and blinking cursor
- Spinner indicator during "thinking"
- Enter submits, Ctrl+C quits
- Simulated streaming response (800ms delay)

### v2 Status (blocked) ❌
- All deps installed at `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`
- Requires Go ≥1.25 (we have 1.26.4 ✓)
- Core issue: textarea component doesn't receive keyboard input

### Porting Strategy

1. Fix v2 textarea input issue (root cause unknown — see blocker above)
2. Once v2 works, port the v1 test into `internal/tui/` in the main oracule CLI
3. Replace readline loop with Bubble Tea TUI as the primary interface
4. Keep readline as fallback for non-TTY / piped environments

---

## Running the Project

### Build (may take 60+ seconds on arm64)

```bash
cd oracule-cli
go build -o /tmp/oracule ./cmd/oracule/
```

### Interactive Mode

```bash
# With OpenCode Zen (free tier)
export OPENCODE_API_KEY=sk-your-key
/tmp/oracule --model opencode/deepseek-v4-flash-free

# With NVIDIA NIM
export NVIDIA_NIM_API_KEY=nvapi-your-key
/tmp/oracule --model nim/deepseek-ai/deepseek-v4-flash

# List providers
/tmp/oracule providers
```

### Slash Commands (in interactive mode)

| Command | Action |
|---|---|
| `/help` | Show available commands |
| `/model` | Show current model |
| `/session` | Show session ID |
| `/clear` | Clear screen |
| `/providers` | List all providers |
| `/exit` or `/quit` | Exit |

---

## Dependencies

```
Go 1.26+
github.com/charmbracelet/bubbletea v1.3.4    # TUI framework (v1 stable, v2 WIP)
github.com/charmbracelet/bubbles v0.20.0      # TUI components (viewport, textarea, spinner)
github.com/charmbracelet/lipgloss v1.0.0      # Terminal styling
github.com/chzyer/readline v1.5.1             # Interactive CLI input with history
github.com/anthropics/anthropic-sdk-go v1.50.1
github.com/sashabaranov/go-openai v1.41.2
google.golang.org/genai v1.60.0               # Google Gemini SDK
modernc.org/sqlite v1.40.0                    # Pure Go SQLite
github.com/rs/zerolog v1.33.0                 # Structured logging
github.com/spf13/cobra v1.8.1                 # CLI framework
github.com/spf13/viper v1.19.0                # Config management
```

---

## Handoff for Claude & Gemini

This project is ready for you to pick up. Key areas to work on:

1. **Fix Bubble Tea v2 input issue** — Investigate why the v2 textarea doesn't process keyboard input. The `bubbletea-test/` directory has both the working v1 (`main.go`) and the minimal v2 test. Compare the event flow between v1 and v2.

2. **Port TUI into main oracule** — Once v2 works, create `internal/tui/` with the chat interface and replace the readline loop.

3. **Reduce dependency tree** — The Google Cloud SDK + gRPC deps add significant build time. Consider using a lighter Gemini client.

4. **Add tests** — Unit tests for provider, permission, ctx, and agent packages.

5. **MCP server integration** — Allow external tools via the Model Context Protocol.

6. **Plugin system** — Allow custom tools to be registered at runtime.

7. **Distribution** — Homebrew formula, deb/rpm packages, release workflows.
