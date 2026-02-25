package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	connectModalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Background(lipgloss.Color("235")).
				Padding(1, 2)
	connectTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	connectHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	connectSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	connectItemStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	connectOffStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

type SelectOption struct {
	Label   string
	Enabled bool
}

type SelectModal struct {
	Title    string
	Hint     string
	Visible  bool
	Selected int
	Options  []SelectOption
}

func NewSelectModal(title, hint string) *SelectModal {
	return &SelectModal{
		Title:    title,
		Hint:     hint,
		Visible:  false,
		Selected: -1,
	}
}

func (m *SelectModal) SetOptions(options []SelectOption) {
	m.Options = append([]SelectOption(nil), options...)
	m.Selected = m.firstEnabledIndex()
}

func (m *SelectModal) firstEnabledIndex() int {
	for i, opt := range m.Options {
		if opt.Enabled {
			return i
		}
	}
	return -1
}

func (m *SelectModal) Open() {
	m.Visible = true
	if m.Selected < 0 || m.Selected >= len(m.Options) || !m.Options[m.Selected].Enabled {
		m.Selected = m.firstEnabledIndex()
	}
}

func (m *SelectModal) Close() {
	m.Visible = false
}

func (m *SelectModal) Move(delta int) {
	if len(m.Options) == 0 || m.Selected < 0 {
		return
	}
	next := m.Selected
	for {
		next += delta
		if next < 0 || next >= len(m.Options) {
			return
		}
		if m.Options[next].Enabled {
			m.Selected = next
			return
		}
	}
}

func (m *SelectModal) SelectedOption() (SelectOption, bool) {
	if m.Selected < 0 || m.Selected >= len(m.Options) {
		return SelectOption{}, false
	}
	opt := m.Options[m.Selected]
	if !opt.Enabled {
		return SelectOption{}, false
	}
	return opt, true
}

func (m *SelectModal) View() string {
	if !m.Visible {
		return ""
	}
	var lines []string
	for i, opt := range m.Options {
		prefix := "  "
		style := connectItemStyle
		if !opt.Enabled {
			style = connectOffStyle
		}
		if i == m.Selected {
			prefix = "> "
			style = connectSelStyle
		}
		lines = append(lines, style.Render(prefix+opt.Label))
	}

	return connectModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
		connectTitleStyle.Render(m.Title),
		strings.Join(lines, "\n"),
		connectHintStyle.Render(m.Hint),
	))
}

type APIKeyModal struct {
	Visible   bool
	Provider  string
	KeyName   string
	Value     string
	Submitted bool
}

func (m *APIKeyModal) Open(providerName, keyName string) {
	m.Visible = true
	m.Provider = providerName
	m.KeyName = keyName
	m.Value = ""
	m.Submitted = false
}

func (m *APIKeyModal) Close() {
	m.Visible = false
	m.Provider = ""
	m.KeyName = ""
	m.Value = ""
}

func (m *APIKeyModal) View() string {
	if !m.Visible {
		return ""
	}
	title := connectTitleStyle.Render("API key")
	subtitle := connectHintStyle.Render(fmt.Sprintf("Connect %s with an API key.", m.Provider))
	inputLabel := connectHintStyle.Render("API key")
	inputValue := m.Value
	if inputValue == "" {
		inputValue = ""
	}
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Render("█")

	body := fmt.Sprintf("%s\n%s\n\n%s\n%s%s\n\n%s",
		title,
		subtitle,
		inputLabel,
		inputValue,
		cursor,
		connectHintStyle.Render("enter: submit  esc: cancel"),
	)

	return connectModalBoxStyle.Render(body)
}
