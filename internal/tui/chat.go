package tui

import (
	"fmt"
	"strings"
	"time"

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
	splashPromptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	splashHintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	splashCursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	splashTipStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	loadingStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	loadingTimerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	placeholderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	roleLabelCoderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	roleLabelOtherStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	promptIndicator     = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
)

// LoadingTickMsg is sent periodically to update the loading timer display.
type LoadingTickMsg struct{}

type ChatMessage struct {
	Sender  string
	Content string
}

type ChatModel struct {
	viewport         viewport.Model
	textInput        textinput.Model
	messages         []ChatMessage
	keyedMessages    map[string]int
	slashSuggestions []slashCommand
	selectedSlashIdx int
	lastSuggestInput string
	width            int
	height           int
	isLoading        bool
	loadingStarted   time.Time
	loadingRole      string
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
		keyedMessages:    make(map[string]int),
		selectedSlashIdx: -1,
	}
}

func (m *ChatModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	if _, ok := msg.(LoadingTickMsg); ok {
		// Just re-render — the View() will pick up the new elapsed time.
		return m, nil
	}

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
	m.textInput.Width = m.inputWrapWidth()
	m.reflow()

	m.renderMessages()
}

// SetLoading sets the loading state with the role currently being waited on.
func (m *ChatModel) SetLoading(loading bool, role string) {
	m.isLoading = loading
	if loading {
		m.loadingStarted = time.Now()
		m.loadingRole = strings.ToUpper(strings.TrimSpace(role))
		if m.loadingRole == "" {
			m.loadingRole = "Agent"
		}
	} else {
		m.loadingRole = ""
	}
}

// IsLoading returns whether the chat is waiting for a response.
func (m *ChatModel) IsLoading() bool {
	return m.isLoading
}

// loadingTickCmd returns a command that ticks every second while loading.
func loadingTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg {
		return LoadingTickMsg{}
	})
}

func (m *ChatModel) AddMessage(sender, content string) {
	m.messages = append(m.messages, ChatMessage{Sender: sender, Content: content})
	m.renderMessages()
	m.viewport.GotoBottom()
}

func (m *ChatModel) SetSystemMessageByKey(key, content string) {
	key = strings.TrimSpace(key)
	if key == "" {
		m.AddMessage("System", content)
		return
	}
	if m.keyedMessages == nil {
		m.keyedMessages = make(map[string]int)
	}
	if idx, ok := m.keyedMessages[key]; ok && idx >= 0 && idx < len(m.messages) {
		m.messages[idx] = ChatMessage{Sender: "System", Content: content}
	} else {
		m.messages = append(m.messages, ChatMessage{Sender: "System", Content: content})
		m.keyedMessages[key] = len(m.messages) - 1
	}
	m.renderMessages()
	m.viewport.GotoBottom()
}

func (m *ChatModel) ReleaseMessageKey(key string) {
	key = strings.TrimSpace(key)
	if key == "" || m.keyedMessages == nil {
		return
	}
	delete(m.keyedMessages, key)
}

func (m *ChatModel) renderMessages() {
	contentWidth := m.viewport.Width
	if contentWidth <= 0 {
		contentWidth = m.width
	}
	if contentWidth <= 0 {
		contentWidth = 80
	}

	var blocks []string
	for _, msg := range m.messages {
		content := strings.TrimSpace(msg.Content)
		var block string
		switch msg.Sender {
		case "User":
			indicator := promptIndicator.Render("> ")
			block = indicator + wrapToWidth(content, contentWidth-2)
		case "System":
			block = systemStyle.Render(wrapToWidth(content, contentWidth))
		default:
			// Role-based label: show sender as "CODER:", "PLANNER:", etc.
			label := strings.ToUpper(strings.TrimSpace(msg.Sender)) + ": "
			style := roleLabelOtherStyle
			if strings.ToUpper(strings.TrimSpace(msg.Sender)) == "CODER" {
				style = roleLabelCoderStyle
			}
			block = styleWrappedPrefixStyled(label, content, contentWidth, style, assistantStyle)
		}
		blocks = append(blocks, block)
	}
	m.viewport.SetContent(strings.Join(blocks, "\n\n"))
}

func (m *ChatModel) View() string {
	if len(m.messages) == 0 {
		return m.emptyStateView()
	}

	vpView := chatViewportStyle.Width(m.width).Render(m.viewport.View())

	var loadingView string
	if m.isLoading {
		loadingView = m.renderLoadingIndicator()
	}

	inputView := lipgloss.NewStyle().Padding(0, 1).Render(m.renderInputForView())

	var parts []string
	parts = append(parts, vpView)
	if loadingView != "" {
		parts = append(parts, loadingView)
	}
	if len(m.slashSuggestions) > 0 {
		lines := m.renderSuggestionsForWidth(max(16, m.width-4))
		suggestView := suggestBoxStyle.Width(m.width).Padding(0, 1).Render(strings.Join(lines, "\n"))
		parts = append(parts, suggestView)
	}
	parts = append(parts, inputView)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *ChatModel) renderLoadingIndicator() string {
	elapsed := time.Since(m.loadingStarted).Round(time.Second)
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frameIdx := int(elapsed.Seconds()) % len(spinnerFrames)
	spinner := spinnerFrames[frameIdx]

	role := m.loadingRole
	if role == "" {
		role = "Agent"
	}

	timerStr := formatElapsed(elapsed)
	return loadingStyle.Render(spinner+" "+role+" is thinking...") + " " + loadingTimerStyle.Render(timerStr)
}

func formatElapsed(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	secs = secs % 60
	return fmt.Sprintf("%dm%ds", mins, secs)
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
	}
	inputLines := strings.Split(m.renderSimpleInput(ti), "\n")
	if len(inputLines) == 0 {
		inputLines = []string{""}
	}
	inputLines[0] = promptIndicator.Render("> ") + inputLines[0]
	for i := 1; i < len(inputLines); i++ {
		inputLines[i] = "  " + inputLines[i]
	}
	cardLines = append(cardLines, strings.Join(inputLines, "\n"))
	if len(m.slashSuggestions) > 0 {
		cardLines = append(cardLines, "", strings.Join(m.renderSuggestionsForWidth(max(16, cardWidth-6)), "\n"))
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
	return strings.Join(m.renderSuggestionsForWidth(max(16, m.width-4)), "\n")
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
	width := ti.Width
	if width <= 0 {
		width = 32
	}

	// Show placeholder if input is empty
	if len(valueRunes) == 0 {
		placeholder := placeholderStyle.Render("Type your message or @path/to/file")
		return cursor + placeholder
	}

	return wrapToWidth(left+cursor+right, width)
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

	inputHeight := m.inputHeight()
	suggestHeight := m.suggestionsHeight()
	loadingHeight := 0
	if m.isLoading {
		loadingHeight = 1
	}

	vpHeight := m.height - inputHeight - suggestHeight - loadingHeight - 1
	if vpHeight < 0 {
		vpHeight = 0
	}
	m.viewport.Height = vpHeight
}

func (m *ChatModel) inputWrapWidth() int {
	width := m.width - 4
	if width <= 0 {
		width = 48
	}
	if width < 8 {
		width = 8
	}
	return width
}

func (m *ChatModel) renderInputForView() string {
	valueRunes := []rune(m.textInput.Value())
	pos := m.textInput.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(valueRunes) {
		pos = len(valueRunes)
	}

	// Show placeholder when empty
	if len(valueRunes) == 0 {
		placeholder := placeholderStyle.Render("Type your message or @path/to/file")
		return promptIndicator.Render("> ") + "█ " + placeholder
	}

	left := string(valueRunes[:pos])
	right := string(valueRunes[pos:])
	raw := left + "█" + right
	if raw == "" {
		raw = "█"
	}

	wrapped := wrapToWidth(raw, m.inputWrapWidth())
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		lines = []string{"█"}
	}
	lines[0] = promptIndicator.Render("> ") + lines[0]
	for i := 1; i < len(lines); i++ {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *ChatModel) renderSuggestionsForWidth(width int) []string {
	if width <= 0 {
		width = 16
	}
	lines := make([]string, 0, len(m.slashSuggestions))
	for i, c := range m.slashSuggestions {
		line := wrapWithPrefix("  ", c.Name+"  "+c.Description, width)
		style := suggestDescStyle
		if i == m.selectedSlashIdx {
			line = wrapWithPrefix("> ", c.Name+"  "+c.Description, width)
			style = suggestSelStyle
		}
		lines = append(lines, style.Render(line))
	}
	return lines
}

func (m *ChatModel) inputHeight() int {
	input := m.renderInputForView()
	height := lipgloss.Height(input)
	if height < 1 {
		return 1
	}
	return height
}

func (m *ChatModel) suggestionsHeight() int {
	if len(m.slashSuggestions) == 0 {
		return 0
	}
	return lipgloss.Height(strings.Join(m.renderSuggestionsForWidth(max(16, m.width-4)), "\n"))
}

// styleWrappedPrefixStyled renders a prefix with one style and content with another.
func styleWrappedPrefixStyled(prefix, content string, width int, prefStyle, contentStyle lipgloss.Style) string {
	wrapped := wrapWithPrefix(prefix, content, width)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return prefStyle.Render(prefix)
	}
	if strings.HasPrefix(lines[0], prefix) {
		lines[0] = prefStyle.Render(prefix) + contentStyle.Render(strings.TrimPrefix(lines[0], prefix))
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = contentStyle.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

func styleWrappedPrefix(prefix, content string, width int, prefixStyle lipgloss.Style) string {
	wrapped := wrapWithPrefix(prefix, content, width)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return prefixStyle.Render(prefix)
	}
	if strings.HasPrefix(lines[0], prefix) {
		lines[0] = prefixStyle.Render(prefix) + strings.TrimPrefix(lines[0], prefix)
	}
	return strings.Join(lines, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
