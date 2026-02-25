package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yubzen/orchestra/internal/config"
	"github.com/yubzen/orchestra/internal/state"
)

var (
	sbBaseStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("235")).Padding(0, 1)
	sbRoleStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	sbModelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	sbCtxGreenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	sbCtxYellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	sbCtxRedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

type StatusBarModel struct {
	Role          string
	ModelName     string
	ExecutionMode string
	CtxPercent    int
	width         int
}

func NewStatusBarModel() *StatusBarModel {
	return &StatusBarModel{
		Role:          "CODER",
		ModelName:     "no-model-selected",
		ExecutionMode: "FAST",
		CtxPercent:    0,
	}
}

func NewStatusBarModelWithConfig(_ *config.Config, session *state.Session) *StatusBarModel {
	m := NewStatusBarModel()
	if session != nil {
		mode := state.NormalizeExecutionMode(session.ExecutionMode)
		m.ExecutionMode = strings.ToUpper(mode)
	}
	return m
}

func (m *StatusBarModel) Init() tea.Cmd { return nil }

func (m *StatusBarModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m *StatusBarModel) SetWidth(w int) {
	m.width = w
}

func (m *StatusBarModel) View() string {
	roleStr := sbRoleStyle.Render(fmt.Sprintf("[ROLE: %s]", m.Role))
	modelStr := sbModelStyle.Render(fmt.Sprintf("[MODEL: %s]", m.ModelName))
	modeStr := sbModelStyle.Render(fmt.Sprintf("[EXEC: %s]", m.ExecutionMode))

	ctxStyle := sbCtxGreenStyle
	if m.CtxPercent >= 80 {
		ctxStyle = sbCtxRedStyle
	} else if m.CtxPercent >= 60 {
		ctxStyle = sbCtxYellowStyle
	}
	ctxStr := ctxStyle.Render(fmt.Sprintf("[CTX: %d%%]", m.CtxPercent))

	s := fmt.Sprintf("%s | %s | %s | %s", roleStr, modelStr, modeStr, ctxStr)
	return sbBaseStyle.Width(m.width).Render(s)
}
