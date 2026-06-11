module github.com/KingImperio/oracule-cli

go 1.24

require (
    github.com/spf13/cobra v1.8.1
    github.com/spf13/viper v1.19.0
    github.com/mattn/go-sqlite3 v1.14.22
    github.com/anthropic/anthropic-sdk-go v0.0.0-20250611-...
    github.com/openai/openai-go v0.0.0-20250611-...
    github.com/google/generative-ai-go v0.18.0
    github.com/rs/zerolog v1.33.0
    github.com/charmbracelet/bubbletea v1.2.4
    github.com/charmbracelet/lipgloss v1.0.0
    golang.org/x/sync v0.8.0
)

replace github.com/mattn/go-sqlite3 => modernc.org/sqlite v1.32.1
