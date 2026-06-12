package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type chatMsg struct {
	role string
	body string
}

type streamDoneMsg struct{}

type model struct {
	viewport    viewport.Model
	textarea    textarea.Model
	spinner     spinner.Model
	chatHistory []chatMsg
	ready       bool
	width       int
	height      int
	isStreaming bool
	statusMsg   string
}

func initialModel() model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	sp := spinner.New()
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	sp.Spinner = spinner.Dot
	return model{
		textarea:    ta,
		spinner:     sp,
		chatHistory: []chatMsg{},
		statusMsg:   "ready",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("83")).Bold(true)
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func styledChat(c chatMsg) string {
	if c.role == "assistant" {
		return assistantStyle.Render("  ai > ") + c.body
	}
	return userStyle.Render("you > ") + c.body
}

func renderChat(history []chatMsg) string {
	var b strings.Builder
	for _, c := range history {
		b.WriteString(styledChat(c))
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpHeight := m.height - 5
		if !m.ready {
			m.viewport = viewport.New(m.width, vpHeight)
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpHeight
		}
		m.textarea.SetWidth(m.width - 4)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.isStreaming {
				return m, nil
			}
			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}
			m.chatHistory = append(m.chatHistory, chatMsg{role: "user", body: input})
			m.textarea.Reset()
			m.viewport.SetContent(renderChat(m.chatHistory))
			m.viewport.GotoBottom()
			m.isStreaming = true
			m.statusMsg = "thinking..."
			return m, func() tea.Msg {
				time.Sleep(800 * time.Millisecond)
				return streamDoneMsg{}
			}
		}

	case streamDoneMsg:
		body := "This is a simulated AI response.\n\nUsing Bubble Tea v1 which works reliably."
		m.chatHistory = append(m.chatHistory, chatMsg{role: "assistant", body: body})
		m.isStreaming = false
		m.statusMsg = "ready"
		m.viewport.SetContent(renderChat(m.chatHistory))
		m.viewport.GotoBottom()
		return m, nil
	}

	var cmds []tea.Cmd

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	sp, spCmd := m.spinner.Update(msg)
	m.spinner = sp
	cmds = append(cmds, spCmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.ready {
		return "\n  loading...\n"
	}

	m.viewport.SetContent(renderChat(m.chatHistory))

	statusIcon := " "
	if m.isStreaming {
		statusIcon = m.spinner.View()
	}
	statusLine := mutedStyle.Render(fmt.Sprintf(" %s  %s", statusIcon, m.statusMsg))

	return fmt.Sprintf("%s\n%s\n%s",
		m.viewport.View(),
		statusLine,
		m.textarea.View(),
	)
}

func main() {
	p := tea.NewProgram(
		initialModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		panic(err)
	}
}
