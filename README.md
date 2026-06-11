# Oracule CLI

> Next-generation agentic CLI coding tool. Go-native, provider-agnostic, with deterministic execution, real LSP, fault-tolerant parallel agents, and structured cross-session memory.

## Architecture

```
cmd/oracule/           — CLI entry point (cobra)
internal/
  agent/               — Core agentic loop, tool registry, context shapers
  provider/            — Unified LLM adapter (Anthropic, OpenAI, Google, Ollama)
  memory/              — 3-layer memory: MEMORY.md + SQLite FTS5 + external provider
  hook/                — 27-event lifecycle hook system
  permission/          — Deny-first permission rules engine
  lsp/                 — Language Server Protocol client (full protocol, not just diagnostics)
  mcp/                 — Model Context Protocol client (stdio/HTTP/SSE)
  storage/             — SQLite database layer (WAL-safe, multi-process)
  config/              — 7-layer config merge system
  tui/                 — Bubble Tea TUI renderer
  rpc/                 — Unix socket RPC for sandboxed subprocess execution
pkg/
  tools/               — Built-in tool implementations (Bash, Read, Edit, Glob, Grep, LSP...)
  models/              — Shared types (Message, Session, Part, ToolCall, Permission)
```
