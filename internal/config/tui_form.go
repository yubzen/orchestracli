package config

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	itemStyle  = lipgloss.NewStyle().PaddingLeft(2)
)

type FormModel struct {
	cfg *Config
}

func NewFormModel(cfg *Config) *FormModel {
	return &FormModel{cfg: cfg}
}

func (m *FormModel) Init() tea.Cmd {
	return nil
}

func (m *FormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *FormModel) View() string {
	s := titleStyle.Render("Orchestra Configuration Form") + "\n\n"
	s += itemStyle.Render(fmt.Sprintf("Mode: %s", m.cfg.Defaults.Mode)) + "\n"
	s += itemStyle.Render(fmt.Sprintf("Anthropic Model: %s", m.cfg.Providers.Anthropic.DefaultModel)) + "\n"
	s += itemStyle.Render(fmt.Sprintf("Google Model: %s", m.cfg.Providers.Google.DefaultModel)) + "\n"
	s += itemStyle.Render(fmt.Sprintf("OpenAI Model: %s", m.cfg.Providers.OpenAI.DefaultModel)) + "\n"
	s += "\nPress 'q' or 'esc' to quit (Form editing is a placeholder for now).\n"
	return lipgloss.NewStyle().Padding(1, 2).Render(s)
}

func RunConfigForm(cfg *Config) error {
	p := tea.NewProgram(NewFormModel(cfg))
	_, err := p.Run()
	return err
}
