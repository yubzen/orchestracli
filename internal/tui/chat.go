package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	chatViewportStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(lipgloss.Color("238"))
	userInputStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	assistantStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	systemStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	suggestBoxStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	suggestNameStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	suggestDescStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	suggestSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	splashLogoDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Bold(true)
	splashLogoBright  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	splashCardStyle   = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("39")).
				Padding(1, 2).
				Background(lipgloss.Color("236"))
	splashPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	splashHintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	splashCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	splashTipStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
)

type ChatMessage struct {
	Sender  string
	Content string
}

type ChatModel struct {
	viewport         viewport.Model
	textInput        textinput.Model
	messages         []ChatMessage
	slashSuggestions []slashCommand
	selectedSlashIdx int
	lastSuggestInput string
	width            int
	height           int
}

func NewChatModel() *ChatModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 1000
	ti.Width = 50

	vp := viewport.New(0, 0)
	vp.SetContent("")

	return &ChatModel{
		viewport:         vp,
		textInput:        ti,
		selectedSlashIdx: -1,
	}
}

func (m *ChatModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)
	m.updateSlashSuggestions()

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) SetSize(w, h int) {
	if w == 0 || h == 0 {
		return
	}
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.textInput.Width = w - 4
	m.reflow()

	m.renderMessages()
}

func (m *ChatModel) AddMessage(sender, content string) {
	m.messages = append(m.messages, ChatMessage{Sender: sender, Content: content})
	m.renderMessages()
	m.viewport.GotoBottom()
}

func (m *ChatModel) renderMessages() {
	var sb strings.Builder
	for _, msg := range m.messages {
		var styled string
		switch msg.Sender {
		case "User":
			styled = userInputStyle.Render("You: ") + msg.Content
		case "System":
			styled = systemStyle.Render(msg.Content)
		default:
			styled = assistantStyle.Render(msg.Sender+": ") + msg.Content
		}
		sb.WriteString(styled + "\n\n")
	}
	m.viewport.SetContent(sb.String())
}

func (m *ChatModel) View() string {
	if len(m.messages) == 0 {
		return m.emptyStateView()
	}

	vpView := chatViewportStyle.Width(m.width).Render(m.viewport.View())
	inputView := lipgloss.NewStyle().Padding(0, 1).Render("> " + m.textInput.View())
	if len(m.slashSuggestions) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, vpView, inputView)
	}

	var lines []string
	for i, c := range m.slashSuggestions {
		if i == m.selectedSlashIdx {
			lines = append(lines, fmt.Sprintf("%s %s", suggestSelStyle.Render("> "+c.Name), suggestSelStyle.Render(c.Description)))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s", suggestNameStyle.Render("  "+c.Name), suggestDescStyle.Render(c.Description)))
	}
	suggestView := suggestBoxStyle.Width(m.width).Padding(0, 1).Render(strings.Join(lines, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, vpView, suggestView, inputView)
}

func (m *ChatModel) emptyStateView() string {
	cardWidth := m.width - 24
	if cardWidth > 96 {
		cardWidth = 96
	}
	maxByScreen := m.width - 4
	if maxByScreen > 0 && cardWidth > maxByScreen {
		cardWidth = maxByScreen
	}
	if cardWidth < 24 {
		cardWidth = 24
	}

	ti := m.textInput
	if cardWidth > 10 {
		ti.Width = cardWidth - 8
	}

	cardLines := []string{
		splashPromptStyle.Render(`Ask anything... "Fix broken tests"`),
		"",
		"> " + m.renderSimpleInput(ti),
	}
	if len(m.slashSuggestions) > 0 {
		cardLines = append(cardLines, "", m.renderSlashSuggestionsInline())
	}

	card := splashCardStyle.Width(cardWidth).Render(strings.Join(cardLines, "\n"))
	tip := splashTipStyle.Render("Tip: Run /connect to add API keys and enable providers")

	body := lipgloss.JoinVertical(
		lipgloss.Center,
		m.renderOrchestraLogo(),
		"",
		card,
		"",
		tip,
	)

	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
	}
	return body
}

func (m *ChatModel) renderSlashSuggestionsInline() string {
	lines := make([]string, 0, len(m.slashSuggestions))
	for i, c := range m.slashSuggestions {
		if i == m.selectedSlashIdx {
			lines = append(lines, fmt.Sprintf("%s %s", suggestSelStyle.Render("> "+c.Name), suggestSelStyle.Render(c.Description)))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s", suggestNameStyle.Render("  "+c.Name), suggestDescStyle.Render(c.Description)))
	}
	return strings.Join(lines, "\n")
}

func (m *ChatModel) renderOrchestraLogo() string {
	chars := []rune("orchestra")
	var b strings.Builder
	for i, ch := range chars {
		if i >= len(chars)-4 {
			b.WriteString(splashLogoBright.Render(strings.ToUpper(string(ch))))
		} else {
			b.WriteString(splashLogoDim.Render(strings.ToUpper(string(ch))))
		}
		b.WriteRune(' ')
	}
	return b.String()
}

func (m *ChatModel) renderSimpleInput(ti textinput.Model) string {
	valueRunes := []rune(ti.Value())
	pos := ti.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(valueRunes) {
		pos = len(valueRunes)
	}

	left := string(valueRunes[:pos])
	right := string(valueRunes[pos:])
	cursor := splashCursorStyle.Render("█")
	return left + cursor + right
}

func (m *ChatModel) GetInputValue() string {
	return m.textInput.Value()
}

func (m *ChatModel) ClearInput() {
	m.textInput.SetValue("")
	m.updateSlashSuggestions()
}

func (m *ChatModel) ApplyTopSlashSuggestion() bool {
	suggestion, ok := m.SelectedSlashSuggestion()
	if !ok {
		return false
	}
	m.textInput.SetValue(suggestion.Name)
	// SetValue keeps the previous cursor in some cases; force the cursor to
	// command end so continued typing appends after autocomplete.
	m.textInput.CursorEnd()
	m.updateSlashSuggestions()
	return true
}

func (m *ChatModel) SelectedSlashSuggestion() (slashCommand, bool) {
	if len(m.slashSuggestions) == 0 {
		return slashCommand{}, false
	}
	if m.selectedSlashIdx < 0 || m.selectedSlashIdx >= len(m.slashSuggestions) {
		return slashCommand{}, false
	}
	return m.slashSuggestions[m.selectedSlashIdx], true
}

func (m *ChatModel) MoveSlashSelection(delta int) bool {
	if len(m.slashSuggestions) == 0 {
		return false
	}
	if m.selectedSlashIdx < 0 || m.selectedSlashIdx >= len(m.slashSuggestions) {
		m.selectedSlashIdx = 0
		return true
	}

	next := m.selectedSlashIdx + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.slashSuggestions) {
		next = len(m.slashSuggestions) - 1
	}
	m.selectedSlashIdx = next
	return true
}

func (m *ChatModel) updateSlashSuggestions() {
	input := m.textInput.Value()
	inputChanged := input != m.lastSuggestInput
	m.lastSuggestInput = input

	m.slashSuggestions = filterSlashCommands(input, 6)
	if len(m.slashSuggestions) == 0 {
		m.selectedSlashIdx = -1
	} else if inputChanged {
		// When the user types, default to the first suggestion.
		m.selectedSlashIdx = 0
	} else {
		// Preserve manual selection on non-input events (blink, redraw, etc).
		if m.selectedSlashIdx < 0 {
			m.selectedSlashIdx = 0
		}
		if m.selectedSlashIdx >= len(m.slashSuggestions) {
			m.selectedSlashIdx = len(m.slashSuggestions) - 1
		}
	}
	m.reflow()
}

func (m *ChatModel) reflow() {
	if m.height == 0 {
		return
	}

	inputHeight := 1
	suggestHeight := 0
	if len(m.slashSuggestions) > 0 {
		suggestHeight = len(m.slashSuggestions)
	}

	vpHeight := m.height - inputHeight - suggestHeight - 1
	if vpHeight < 0 {
		vpHeight = 0
	}
	m.viewport.Height = vpHeight
}
