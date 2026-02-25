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
	MaxWidth int
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

func (m *SelectModal) SetWidth(width int) {
	m.MaxWidth = width
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
	contentWidth := m.modalContentWidth()
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
		line := prefix + opt.Label
		if contentWidth > 0 {
			line = wrapWithPrefix(prefix, opt.Label, contentWidth)
		}
		lines = append(lines, style.Render(line))
	}

	title := m.Title
	hint := m.Hint
	if contentWidth > 0 {
		title = wrapToWidth(title, contentWidth)
		hint = wrapToWidth(hint, contentWidth)
	}

	boxStyle := connectModalBoxStyle
	if m.MaxWidth > 0 {
		boxStyle = boxStyle.MaxWidth(m.MaxWidth)
	}

	return boxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
		connectTitleStyle.Render(title),
		strings.Join(lines, "\n"),
		connectHintStyle.Render(hint),
	))
}

type APIKeyModal struct {
	Visible      bool
	Provider     string
	KeyName      string
	AuthMethod   AuthMethod
	Value        string
	Submitted    bool
	Connecting   bool
	Status       string
	ErrorMessage string
	MaxWidth     int
}

func (m *APIKeyModal) Open(providerName, keyName string, authMethod AuthMethod) {
	m.Visible = true
	m.Provider = providerName
	m.KeyName = keyName
	m.AuthMethod = authMethod
	m.Value = ""
	m.Submitted = false
	m.Connecting = false
	m.Status = ""
	m.ErrorMessage = ""
}

func (m *APIKeyModal) Close() {
	m.Visible = false
	m.Provider = ""
	m.KeyName = ""
	m.AuthMethod = AuthMethod{}
	m.Value = ""
	m.Connecting = false
	m.Status = ""
	m.ErrorMessage = ""
}

func (m *APIKeyModal) BeginConnecting(status string) {
	m.Connecting = true
	m.Status = strings.TrimSpace(status)
	m.ErrorMessage = ""
}

func (m *APIKeyModal) SetError(errMsg string) {
	m.Connecting = false
	m.Status = ""
	m.ErrorMessage = strings.TrimSpace(errMsg)
}

func (m *APIKeyModal) SetWidth(width int) {
	m.MaxWidth = width
}

func (m *APIKeyModal) View() string {
	if !m.Visible {
		return ""
	}
	contentWidth := m.modalContentWidth()
	wrap := func(text string) string {
		if contentWidth <= 0 {
			return text
		}
		return wrapToWidth(text, contentWidth)
	}

	title := connectTitleStyle.Render(wrap(m.AuthMethod.InputLabel))
	subtitleText := fmt.Sprintf("Connect %s with %s.", m.Provider, strings.ToLower(m.AuthMethod.Name))
	if strings.TrimSpace(m.AuthMethod.InputHint) != "" {
		subtitleText = m.AuthMethod.InputHint
	}
	subtitle := connectHintStyle.Render(wrap(subtitleText))
	inputLabel := connectHintStyle.Render(wrap(m.AuthMethod.InputLabel))
	inputValue := m.Value + lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Render("█")
	if strings.TrimSpace(m.Value) == "" {
		inputValue = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Render("█")
	}
	statusLine := ""
	if m.Connecting {
		statusLine = connectHintStyle.Render(wrap("Connecting... " + m.Status))
	} else if m.ErrorMessage != "" {
		statusLine = connectOffStyle.Render(wrap(m.ErrorMessage))
	}
	footer := "enter: submit  esc: cancel"
	if m.Connecting {
		footer = "esc: cancel"
	}

	parts := []string{
		title,
		subtitle,
		"",
		inputLabel,
		wrap(inputValue),
	}
	if statusLine != "" {
		parts = append(parts, "", statusLine)
	}
	parts = append(parts, "", connectHintStyle.Render(wrap(footer)))

	boxStyle := connectModalBoxStyle
	if m.MaxWidth > 0 {
		boxStyle = boxStyle.MaxWidth(m.MaxWidth)
	}
	return boxStyle.Render(strings.Join(parts, "\n"))
}

func (m *SelectModal) modalContentWidth() int {
	if m == nil || m.MaxWidth <= 0 {
		return 0
	}
	width := m.MaxWidth - 8
	if width < 20 {
		width = 20
	}
	return width
}

func (m *APIKeyModal) modalContentWidth() int {
	if m == nil || m.MaxWidth <= 0 {
		return 0
	}
	width := m.MaxWidth - 8
	if width < 20 {
		width = 20
	}
	return width
}
