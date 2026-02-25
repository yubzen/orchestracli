package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	planModalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("75")).
				Background(lipgloss.Color("235")).
				Padding(1, 2)
	planModalTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	planModalHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	planModalBodyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
)

type PlanReviewAction struct {
	DecisionMade bool
	PlanID       string
	Approved     bool
	EditedPlan   string
}

type PlanReviewModal struct {
	Visible      bool
	PlanID       string
	OriginalPlan string
	editing      bool
	width        int
	height       int
	viewport     viewport.Model
	editor       textarea.Model
}

func NewPlanReviewModal() *PlanReviewModal {
	vp := viewport.New(70, 14)
	ta := textarea.New()
	ta.Prompt = ""
	ta.SetValue("")
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.Focus()
	return &PlanReviewModal{
		viewport: vp,
		editor:   ta,
	}
}

func (m *PlanReviewModal) SetSize(width, height int) {
	if m == nil {
		return
	}
	if width <= 0 || height <= 0 {
		return
	}
	m.width = width
	m.height = height

	bodyWidth := width - 12
	if bodyWidth < 36 {
		bodyWidth = 36
	}
	bodyHeight := height - 12
	if bodyHeight < 10 {
		bodyHeight = 10
	}
	m.viewport.Width = bodyWidth
	m.viewport.Height = bodyHeight
	m.editor.SetWidth(bodyWidth)
	m.editor.SetHeight(bodyHeight)
	m.refreshPlanPreview()
}

func (m *PlanReviewModal) Open(planID, planYAML string) {
	if m == nil {
		return
	}
	m.Visible = true
	m.PlanID = strings.TrimSpace(planID)
	m.OriginalPlan = strings.TrimSpace(planYAML)
	m.editing = false
	m.editor.SetValue(m.OriginalPlan)
	m.refreshPlanPreview()
}

func (m *PlanReviewModal) Close() {
	if m == nil {
		return
	}
	m.Visible = false
	m.PlanID = ""
	m.OriginalPlan = ""
	m.editing = false
	m.editor.SetValue("")
	m.viewport.SetContent("")
}

func (m *PlanReviewModal) Update(msg tea.Msg) (PlanReviewAction, tea.Cmd) {
	if m == nil || !m.Visible {
		return PlanReviewAction{}, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.editing {
			switch key.String() {
			case "ctrl+s":
				return PlanReviewAction{
					DecisionMade: true,
					PlanID:       m.PlanID,
					Approved:     true,
					EditedPlan:   strings.TrimSpace(m.editor.Value()),
				}, nil
			case "esc":
				m.editing = false
				m.refreshPlanPreview()
				return PlanReviewAction{}, nil
			case "ctrl+r":
				m.editor.SetValue(m.OriginalPlan)
				return PlanReviewAction{}, nil
			}
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(msg)
			return PlanReviewAction{}, cmd
		}

		switch key.String() {
		case "enter", "a":
			return PlanReviewAction{
				DecisionMade: true,
				PlanID:       m.PlanID,
				Approved:     true,
			}, nil
		case "r":
			return PlanReviewAction{
				DecisionMade: true,
				PlanID:       m.PlanID,
				Approved:     false,
			}, nil
		case "e":
			m.editing = true
			m.editor.SetValue(m.OriginalPlan)
			return PlanReviewAction{}, nil
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return PlanReviewAction{}, cmd
}

func (m *PlanReviewModal) View() string {
	if m == nil || !m.Visible {
		return ""
	}

	title := "Plan Ready"
	if m.editing {
		title = "Edit Plan"
	}
	titleView := planModalTitleStyle.Render(title)

	if m.editing {
		body := planModalBodyStyle.Render(m.editor.View())
		hint := planModalHintStyle.Render("ctrl+s: approve edited plan  esc: cancel edit  ctrl+r: reset")
		return planModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s", titleView, body, hint))
	}

	body := planModalBodyStyle.Render(m.viewport.View())
	hint := planModalHintStyle.Render("enter/a: approve  e: edit  r: reject  up/down: scroll")
	return planModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s", titleView, body, hint))
}

func (m *PlanReviewModal) refreshPlanPreview() {
	if m == nil {
		return
	}
	width := m.viewport.Width
	if width <= 0 {
		width = 70
	}
	content := m.OriginalPlan
	if strings.TrimSpace(content) == "" {
		content = "Planner returned an empty plan."
	}
	m.viewport.SetContent(wrapToWidth(content, width))
}
