package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	modelModalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39")).
				Background(lipgloss.Color("235")).
				Padding(1, 2)
	modelModalTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	modelModalHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	modelModalSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	modelModalItemStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
)

type ModelsModal struct {
	Visible  bool
	Models   []string
	Selected int
}

func NewModelsModal(models []string) *ModelsModal {
	return &ModelsModal{
		Visible:  false,
		Models:   append([]string(nil), models...),
		Selected: 0,
	}
}

func (m *ModelsModal) SetModels(models []string) {
	m.Models = append([]string(nil), models...)
	if len(m.Models) == 0 {
		m.Selected = -1
		return
	}
	if m.Selected < 0 || m.Selected >= len(m.Models) {
		m.Selected = 0
	}
}

func (m *ModelsModal) Open() {
	if m == nil {
		return
	}
	m.Visible = true
	if len(m.Models) == 0 {
		m.Selected = -1
		return
	}
	if m.Selected < 0 || m.Selected >= len(m.Models) {
		m.Selected = 0
	}
}

func (m *ModelsModal) SelectByValue(model string) {
	if m == nil {
		return
	}
	for i, item := range m.Models {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(model)) {
			m.Selected = i
			return
		}
	}
}

func (m *ModelsModal) Close() {
	if m == nil {
		return
	}
	m.Visible = false
}

func (m *ModelsModal) Move(delta int) {
	if m == nil || len(m.Models) == 0 {
		return
	}
	next := m.Selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.Models) {
		next = len(m.Models) - 1
	}
	m.Selected = next
}

func (m *ModelsModal) SelectedModel() (string, bool) {
	if m == nil || len(m.Models) == 0 {
		return "", false
	}
	if m.Selected < 0 || m.Selected >= len(m.Models) {
		return "", false
	}
	return m.Models[m.Selected], true
}

func (m *ModelsModal) View() string {
	if m == nil || !m.Visible {
		return ""
	}

	var rows []string
	if len(m.Models) == 0 {
		rows = append(rows, modelModalItemStyle.Render("  No models available. Use /connect first."))
	} else {
		for i, model := range m.Models {
			if i == m.Selected {
				rows = append(rows, modelModalSelStyle.Render("> "+model))
				continue
			}
			rows = append(rows, modelModalItemStyle.Render("  "+model))
		}
	}

	title := modelModalTitleStyle.Render("Select Model")
	hint := modelModalHintStyle.Render("up/down: navigate  enter: select  esc: close")
	body := strings.Join(rows, "\n")

	return modelModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s", title, body, hint))
}
