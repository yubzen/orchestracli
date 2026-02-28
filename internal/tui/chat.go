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

var splashCardBG = lipgloss.Color("236")

var (
	chatViewportStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(lipgloss.Color("238"))
	userInputStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	assistantStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	systemStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	activityStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true).Padding(0, 1)
	activityMetaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	diffBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	diffHeaderStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	diffAddStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	diffDelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	diffCtxStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	suggestBoxStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	suggestNameStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	suggestDescStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	suggestSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	splashLogoBlue    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	splashLogoWhite   = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	splashLogoYellow  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	splashCardStyle   = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("39")).
				Padding(1, 2).
				Background(splashCardBG)
	splashPromptStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(splashCardBG)
	splashHintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Background(splashCardBG)
	splashCursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(splashCardBG).Bold(true)
	splashPlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Background(splashCardBG).Italic(true)
	splashPromptIndicator  = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Background(splashCardBG).Bold(true)
	splashTipStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	loadingStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	loadingTimerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	placeholderStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	roleLabelCoderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	roleLabelOtherStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	promptIndicator        = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
)

var orchestraBlockGlyphs = map[rune][]string{
	'O': {
		" ####### ",
		"##     ##",
		"##     ##",
		"##     ##",
		" ####### ",
	},
	'R': {
		"######## ",
		"##     ##",
		"######## ",
		"##   ##  ",
		"##    ## ",
	},
	'C': {
		" ####### ",
		"##       ",
		"##       ",
		"##       ",
		" ####### ",
	},
	'H': {
		"##     ##",
		"##     ##",
		"#########",
		"##     ##",
		"##     ##",
	},
	'E': {
		"#########",
		"##       ",
		"#######  ",
		"##       ",
		"#########",
	},
	'S': {
		" ####### ",
		"##       ",
		" ######  ",
		"       ##",
		"#######  ",
	},
	'T': {
		"#########",
		"   ##    ",
		"   ##    ",
		"   ##    ",
		"   ##    ",
	},
	'A': {
		" ####### ",
		"##     ##",
		"#########",
		"##     ##",
		"##     ##",
	},
}

// LoadingTickMsg is sent periodically to update the loading timer display.
type LoadingTickMsg struct{}

type ChatMessage struct {
	Sender  string
	Content string
	Diff    *FileDiffMessage
}

type FileDiffMessage struct {
	Path     string
	OldLines []string
	NewLines []string
}

type ActivityLine struct {
	Phase     string
	Action    string
	Target    string
	Key       string
	StartedAt time.Time
}

type ChatModel struct {
	viewport         viewport.Model
	textInput        textinput.Model
	messages         []ChatMessage
	keyedMessages    map[string]int
	slashSuggestions []slashCommand
	selectedSlashIdx int
	lastSuggestInput string
	inputEnabled     bool
	inputHint        string
	width            int
	height           int
	isLoading        bool
	loadingStarted   time.Time
	loadingRole      string
	stickToBottom    bool
	activity         ActivityLine
	activityVisible  bool
}

func NewChatModel() *ChatModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 1000
	ti.Width = 50

	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true
	vp.SetContent("")

	return &ChatModel{
		viewport:         vp,
		textInput:        ti,
		keyedMessages:    make(map[string]int),
		selectedSlashIdx: -1,
		inputEnabled:     true,
		stickToBottom:    true,
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
	if isScrollInteraction(msg) {
		m.stickToBottom = m.viewport.AtBottom()
	}

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
}

func (m *ChatModel) AddFileDiff(path string, oldLines, newLines []string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "(unknown file)"
	}
	m.messages = append(m.messages, ChatMessage{
		Sender: "System",
		Diff: &FileDiffMessage{
			Path:     path,
			OldLines: append([]string(nil), oldLines...),
			NewLines: append([]string(nil), newLines...),
		},
	})
	m.renderMessages()
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

	prevOffset := m.viewport.YOffset
	var blocks []string
	for _, msg := range m.messages {
		if msg.Diff != nil {
			blocks = append(blocks, m.renderDiffBlock(*msg.Diff, contentWidth))
			continue
		}
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
	if m.stickToBottom {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(prevOffset)
	}
}

func (m *ChatModel) View() string {
	if len(m.messages) == 0 {
		return m.emptyStateView()
	}

	vpView := chatViewportStyle.Width(m.width).Render(m.viewport.View())

	inputView := m.renderInputArea()

	var parts []string
	parts = append(parts, vpView)
	if m.activityVisible {
		parts = append(parts, m.renderActivityLine())
	}
	if m.inputEnabled && len(m.slashSuggestions) > 0 {
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

func (m *ChatModel) SetActivity(phase, action, target, key string) {
	phase = strings.TrimSpace(phase)
	action = strings.TrimSpace(action)
	target = strings.TrimSpace(target)
	key = strings.TrimSpace(key)
	if phase == "" && action == "" && target == "" {
		m.ClearActivity()
		return
	}
	if key == "" {
		key = phase + "|" + action + "|" + target
	}
	if m.activityVisible && m.activity.Key == key {
		m.activity.Phase = phase
		m.activity.Action = action
		m.activity.Target = target
		return
	}
	m.activity = ActivityLine{
		Phase:     phase,
		Action:    action,
		Target:    target,
		Key:       key,
		StartedAt: time.Now(),
	}
	m.activityVisible = true
	m.reflow()
}

func (m *ChatModel) ClearActivity() {
	if !m.activityVisible {
		return
	}
	m.activity = ActivityLine{}
	m.activityVisible = false
	m.reflow()
}

func (m *ChatModel) renderActivityLine() string {
	if !m.activityVisible {
		return ""
	}
	started := m.activity.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	elapsed := time.Since(started).Round(time.Second)
	spinnerFrames := []string{"●", "◉", "○", "◉"}
	frameIdx := int(elapsed.Seconds()) % len(spinnerFrames)
	spinner := spinnerFrames[frameIdx]

	label := strings.TrimSpace(m.activity.Phase + " " + m.activity.Action)
	if label == "" {
		label = "Working"
	}
	target := strings.TrimSpace(m.activity.Target)
	if target != "" {
		label += "  ·  " + target
	}
	meta := fmt.Sprintf("(%s · esc/ctrl+c to interrupt)", formatActivityElapsed(elapsed))
	return activityStyle.Render(spinner+" "+label) + "  " + activityMetaStyle.Render(meta)
}

func formatActivityElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	mins := secs / 60
	secs = secs % 60
	return fmt.Sprintf("%dm %02ds", mins, secs)
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
	if m.inputEnabled {
		inputLines := strings.Split(m.renderSimpleInput(ti), "\n")
		if len(inputLines) == 0 {
			inputLines = []string{""}
		}
		inputLines[0] = splashPromptIndicator.Render("> ") + inputLines[0]
		for i := 1; i < len(inputLines); i++ {
			inputLines[i] = "  " + inputLines[i]
		}
		cardLines = append(cardLines, strings.Join(inputLines, "\n"))
	}
	if m.inputEnabled && len(m.slashSuggestions) > 0 {
		cardLines = append(cardLines, "", strings.Join(m.renderSuggestionsForWidth(max(16, cardWidth-6)), "\n"))
	} else if !m.inputEnabled {
		cardLines = append(cardLines, "", systemStyle.Render(wrapToWidth(m.inputDisabledHint(), max(16, cardWidth-4))))
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
	if m.width > 0 && m.width < 96 {
		return m.renderCompactOrchestraSubLogo()
	}

	word := []rune("ORCHESTRA")
	rows := make([]string, 5)
	for i, ch := range word {
		glyph, ok := orchestraBlockGlyphs[ch]
		if !ok {
			continue
		}
		style := splashLogoBlue
		if i >= len(word)-3 {
			style = splashLogoYellow
		}
		for row := range rows {
			rows[row] += style.Render(glyph[row])
			if i < len(word)-1 {
				rows[row] += "  "
			}
		}
	}
	return strings.Join(rows, "\n") + "\n" + m.renderCompactOrchestraSubLogo()
}

func (m *ChatModel) renderCompactOrchestraSubLogo() string {
	var word strings.Builder
	for i, ch := range []rune("ORCHESTRA") {
		word.WriteString(splashLogoWhite.Render(string(ch)))
		if i < len("ORCHESTRA")-1 {
			word.WriteRune(' ')
		}
	}

	var subtitle strings.Builder
	for i, ch := range []rune("Create Your Team of AI Agents") {
		subtitle.WriteString(splashLogoWhite.Render(string(ch)))
		if i < len([]rune("Create Your Team of AI Agents"))-1 {
			subtitle.WriteRune(' ')
		}
	}

	return word.String() + "\n" + subtitle.String()
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
		placeholder := splashPlaceholderStyle.Render("Type your message or @path/to/file")
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

func (m *ChatModel) SetInputValue(value string) {
	m.textInput.SetValue(value)
	m.textInput.CursorEnd()
	m.updateSlashSuggestions()
}

func (m *ChatModel) ApplyTopSlashSuggestion() bool {
	if !m.inputEnabled {
		return false
	}
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
	if !m.inputEnabled {
		return false
	}
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

func (m *ChatModel) HasVisibleSuggestions() bool {
	if m == nil || !m.inputEnabled {
		return false
	}
	return len(m.slashSuggestions) > 0
}

func (m *ChatModel) updateSlashSuggestions() {
	if !m.inputEnabled {
		m.slashSuggestions = nil
		m.selectedSlashIdx = -1
		m.lastSuggestInput = m.textInput.Value()
		m.reflow()
		return
	}

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
	activityHeight := 0
	if m.activityVisible {
		activityHeight = 1
	}

	vpHeight := m.height - inputHeight - suggestHeight - activityHeight - 1
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
	input := m.renderInputArea()
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

type diffLineKind int

const (
	diffLineContext diffLineKind = iota
	diffLineAdded
	diffLineRemoved
)

type diffLine struct {
	kind diffLineKind
	text string
}

func (m *ChatModel) renderDiffBlock(diff FileDiffMessage, width int) string {
	const maxRenderedLines = 20
	lines := computeLineDiff(diff.OldLines, diff.NewLines)
	totalLines := len(lines)
	if totalLines > maxRenderedLines {
		lines = lines[:maxRenderedLines]
	}

	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}

	var body []string
	for _, line := range lines {
		prefix := "  "
		style := diffCtxStyle
		switch line.kind {
		case diffLineAdded:
			prefix = "+ "
			style = diffAddStyle
		case diffLineRemoved:
			prefix = "- "
			style = diffDelStyle
		}
		body = append(body, style.Render(truncateRunes(prefix+line.text, contentWidth)))
	}
	if totalLines > maxRenderedLines {
		remaining := totalLines - maxRenderedLines
		body = append(body, diffCtxStyle.Render(fmt.Sprintf("… %d more line(s)", remaining)))
	}
	if len(body) == 0 {
		body = append(body, diffCtxStyle.Render("(no textual changes)"))
	}

	header := diffHeaderStyle.Render("Diff  " + diff.Path)
	block := diffBoxStyle.Width(width).Render(header + "\n" + strings.Join(body, "\n"))
	return block
}

func computeLineDiff(oldLines, newLines []string) []diffLine {
	n := len(oldLines)
	m := len(newLines)
	if n == 0 && m == 0 {
		return nil
	}

	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	out := make([]diffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		if oldLines[i] == newLines[j] {
			out = append(out, diffLine{kind: diffLineContext, text: oldLines[i]})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			out = append(out, diffLine{kind: diffLineRemoved, text: oldLines[i]})
			i++
		} else {
			out = append(out, diffLine{kind: diffLineAdded, text: newLines[j]})
			j++
		}
	}
	for i < n {
		out = append(out, diffLine{kind: diffLineRemoved, text: oldLines[i]})
		i++
	}
	for j < m {
		out = append(out, diffLine{kind: diffLineAdded, text: newLines[j]})
		j++
	}
	return out
}

func truncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
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

func (m *ChatModel) SetInputAvailability(enabled bool, hint string) {
	m.inputEnabled = enabled
	m.inputHint = strings.TrimSpace(hint)
	if !enabled {
		m.slashSuggestions = nil
		m.selectedSlashIdx = -1
	}
	m.reflow()
}

func (m *ChatModel) renderInputArea() string {
	if m.inputEnabled {
		return lipgloss.NewStyle().Padding(0, 1).Render(m.renderInputForView())
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(systemStyle.Render(wrapToWidth(m.inputDisabledHint(), m.inputWrapWidth())))
}

func (m *ChatModel) inputDisabledHint() string {
	hint := strings.TrimSpace(m.inputHint)
	if hint != "" {
		return hint
	}
	return "This role is internal and cannot be addressed directly."
}

func isScrollInteraction(msg tea.Msg) bool {
	switch typed := msg.(type) {
	case tea.MouseMsg:
		return true
	case tea.KeyMsg:
		switch typed.String() {
		case "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d", "u", "d", "home", "end":
			return true
		}
	}
	return false
}
