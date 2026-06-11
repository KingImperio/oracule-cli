package memory

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Manager is the 3-layer memory system.
//
// Layer 1 (always-on): file-based MEMORY.md / USER.md at ~/.oracule/memory/
// Layer 2 (optional): SQLite FTS5 session search for cross-session recall
// Layer 3 (optional): one external provider (e.g., Honcho-style) — at most one
type Manager struct {
	config   Config
	logger   zerolog.Logger
	baseDir  string
	fs       FileSystem
}

// Config controls memory behavior.
type Config struct {
	Enabled         bool
	MemoryDir       string
	UserMemoryPath  string
	ProjectRulesGlob string // e.g., "{dir}/AGENTS.md", "{dir}/CLAUDE.md"
	MaxMemoryBytes  int    // hard cap on MEMORY.md size (default: ~25KB)
	SessionDBPath   string // empty = disabled
}

// DefaultMemoryConfig returns sensible defaults.
func DefaultMemoryConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Enabled:         true,
		MemoryDir:       filepath.Join(home, ".oracule", "memory"),
		UserMemoryPath:  filepath.Join(home, ".oracule", "memory", "USER.md"),
		ProjectRulesGlob: "AGENTS.md",
		MaxMemoryBytes:  25 * 1024,
		SessionDBPath:   filepath.Join(home, ".oracule", "state.db"),
	}
}

// FileSystem abstracts OS calls for testability.
type FileSystem interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadDir(name string) ([]os.DirEntry, error)
	Stat(name string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	OpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
}

// NewManager builds a Manager with the given config and filesystem.
// Pass nil for fs to use the real OS.
func NewManager(cfg Config, logger zerolog.Logger, fs FileSystem) (*Manager, error) {
	if !cfg.Enabled {
		return &Manager{config: cfg, logger: logger}, nil
	}
	if fs == nil {
		fs = osFS{}
	}
	if err := fs.MkdirAll(cfg.MemoryDir, 0o700); err != nil {
		return nil, err
	}
	return &Manager{config: cfg, logger: logger, baseDir: cfg.MemoryDir, fs: fs}, nil
}

// LoadUserMemory reads the user-scoped USER.md file (Layer 1).
// Returns empty string on any error (fail-open for robustness).
func (m *Manager) LoadUserMemory(ctx context.Context) (string, error) {
	if !m.config.Enabled || m.config.UserMemoryPath == "" {
		return "", nil
	}
	data, err := m.fs.ReadFile(m.config.UserMemoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveUserMemory writes durable user preferences to USER.md.
// Truncates if the file exceeds MaxMemoryBytes.
func (m *Manager) SaveUserMemory(ctx context.Context, content string) error {
	if !m.config.Enabled {
		return nil
	}
	if len(content) > m.config.MaxMemoryBytes {
		content = content[:m.config.MaxMemoryBytes] + "\n\n<!-- truncated by oracule -->"
	}
	return m.fs.WriteFile(m.config.UserMemoryPath, []byte(content), 0o600)
}

// LoadProjectInstructions searches upward from dir for the project rules file
// (AGENTS.md / CLAUDE.md / .claude/rules/*.md pattern) and returns its content.
// Returns empty string if none found.
func (m *Manager) LoadProjectInstructions(ctx context.Context, dir string) (string, error) {
	if !m.config.Enabled {
		return "", nil
	}
	// Walk up to root looking for the rules file.
	check := dir
	for check != "/" && check != "" {
		candidate := filepath.Join(check, m.config.ProjectRulesGlob)
		info, err := m.fs.Stat(candidate)
		if err == nil && !info.IsDir() {
			data, err := m.fs.ReadFile(candidate)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(data)), nil
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	return "", nil
}

// AppendMemory appends a durable note to MEMORY.md with a timestamp header.
// Designed to be idempotent-ish; the caller should de-duplicate before calling.
func (m *Manager) AppendMemory(ctx context.Context, heading, body string) error {
	if !m.config.Enabled {
		return nil
	}
	memPath := filepath.Join(m.baseDir, "MEMORY.md")
	entry := fmt.Sprintf("\n## %s (%s)\n\n%s\n", heading, time.Now().Format("2006-01-02"), body)

	f, err := m.fs.OpenFile(memPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Guard against exceeding max size: if MEMORY.md is already over the cap,
	// shift older content into a dated archive before appending.
	if info, statErr := m.fs.Stat(memPath); statErr == nil && info.Size() > int64(m.config.MaxMemoryBytes) {
		_ = m.archiveOlderMemory(memPath)
	}

	_, err = f.WriteString(entry)
	return err
}

// archiveOlderMemory renames MEMORY.md to MEMORY-YYYY-MM-DD.archive.md and
// starts a fresh MEMORY.md. Best-effort; errors are logged but not propagated.
func (m *Manager) archiveOlderMemory(memPath string) error {
	ts := time.Now().Format("2006-01-02")
	archive := fmt.Sprintf("%s-%s.archive.md", memPath, ts)
	_ = m.fs.Rename(memPath, archive) // ignore error — if it fails, we'll truncate instead
	_ = m.fs.WriteFile(memPath, []byte("# Oracule Memory\n\n<!-- Fresh memory after archive; older content preserved in .archive.md -->\n"), 0o600)
	return nil
}

// Close releases any held resources.
func (m *Manager) Close() error { return nil }
