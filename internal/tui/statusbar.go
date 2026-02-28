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
	sbRepoStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	sbHintStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	sbStateBusyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	sbStateIdleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	sbStateErrStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	sbModelOffStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	sbCtxGreenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	sbCtxYellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	sbCtxRedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

var trackedTeamRoles = []string{"PLANNER", "CODER", "REVIEWER"}

type StatusBarModel struct {
	ExecutionMode string
	CtxPercent    int
	roleModels    map[string]string
	roleStates    map[string]string
	repoPath      string
	hint          string
	width         int
}

func NewStatusBarModel() *StatusBarModel {
	m := &StatusBarModel{
		ExecutionMode: "FAST",
		CtxPercent:    0,
		roleModels:    make(map[string]string),
		roleStates:    make(map[string]string),
	}
	for _, role := range trackedTeamRoles {
		m.roleModels[role] = ""
		m.roleStates[role] = ""
	}
	return m
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

func (m *StatusBarModel) SetRoleModel(role, model string) {
	if m == nil {
		return
	}
	role = strings.ToUpper(strings.TrimSpace(role))
	if role == "" {
		return
	}
	if m.roleModels == nil {
		m.roleModels = make(map[string]string)
	}
	m.roleModels[role] = strings.TrimSpace(model)
}

func (m *StatusBarModel) SetRoleState(role, state string) {
	if m == nil {
		return
	}
	role = strings.ToUpper(strings.TrimSpace(role))
	if role == "" {
		return
	}
	if m.roleStates == nil {
		m.roleStates = make(map[string]string)
	}
	m.roleStates[role] = strings.ToLower(strings.TrimSpace(state))
}

func (m *StatusBarModel) SetRepoPath(path string) {
	if m == nil {
		return
	}
	m.repoPath = strings.TrimSpace(path)
}

func (m *StatusBarModel) SetHint(hint string) {
	if m == nil {
		return
	}
	m.hint = strings.TrimSpace(hint)
}

func (m *StatusBarModel) ResetTeamActivity() {
	if m == nil {
		return
	}
	for _, role := range trackedTeamRoles {
		m.SetRoleState(role, "")
	}
}

func (m *StatusBarModel) View() string {
	modeStr := sbModelStyle.Render(fmt.Sprintf("[EXEC: %s]", m.ExecutionMode))

	ctxStyle := sbCtxGreenStyle
	if m.CtxPercent >= 80 {
		ctxStyle = sbCtxRedStyle
	} else if m.CtxPercent >= 60 {
		ctxStyle = sbCtxYellowStyle
	}
	ctxStr := ctxStyle.Render(fmt.Sprintf("[CTX: %d%%]", m.CtxPercent))

	teamParts := make([]string, 0, len(trackedTeamRoles))
	for _, role := range trackedTeamRoles {
		teamParts = append(teamParts, m.teamRoleView(role))
	}
	teamStr := strings.Join(teamParts, "  ")
	repo := truncatePathFromLeft(strings.TrimSpace(m.repoPath), m.repoDisplayWidth())
	parts := []string{modeStr, ctxStr, teamStr}
	if repo != "" {
		parts = append([]string{sbRepoStyle.Render(repo)}, parts...)
	}
	if hint := strings.TrimSpace(m.hint); hint != "" {
		parts = append(parts, sbHintStyle.Render(truncateStatusValue(hint, 44)))
	}
	s := strings.Join(parts, " | ")
	return sbBaseStyle.Width(m.width).Render(s)
}

func (m *StatusBarModel) teamRoleView(role string) string {
	label := strings.ToLower(strings.TrimSpace(role))
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	model := strings.TrimSpace(m.roleModels[strings.ToUpper(strings.TrimSpace(role))])
	state := strings.TrimSpace(m.roleStates[strings.ToUpper(strings.TrimSpace(role))])
	if model == "" {
		if state == "" {
			state = "unset"
		}
		stateStyle := sbModelOffStyle
		if state == "error" {
			stateStyle = sbStateErrStyle
		}
		return fmt.Sprintf("%s %s",
			sbRoleStyle.Render("["+label+"]"),
			stateStyle.Render("("+state+")"),
		)
	}
	if state == "" {
		state = "idle"
	}

	stateStyle := sbStateIdleStyle
	switch state {
	case "thinking", "planning", "reading", "writing", "running", "reviewing", "waiting", "cancelling":
		stateStyle = sbStateBusyStyle
	case "error":
		stateStyle = sbStateErrStyle
	}
	return fmt.Sprintf("%s %s %s",
		sbRoleStyle.Render("["+label+"]"),
		sbModelStyle.Render(truncateStatusValue(model, 28)),
		stateStyle.Render("("+state+")"),
	)
}

func truncateStatusValue(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func (m *StatusBarModel) repoDisplayWidth() int {
	if m == nil {
		return 40
	}
	if m.width <= 0 {
		return 40
	}
	available := m.width - 70
	if available < 16 {
		return 16
	}
	if available > 80 {
		return 80
	}
	return available
}

func truncatePathFromLeft(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 1 {
		return string(runes[len(runes)-maxRunes:])
	}
	return "…" + string(runes[len(runes)-maxRunes+1:])
}
