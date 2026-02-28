package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var modelModalBG = lipgloss.Color("235")

var (
	modelModalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39")).
				Background(modelModalBG).
				Padding(1, 2)
	modelModalTitleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Background(modelModalBG).Bold(true)
	modelModalHintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(modelModalBG)
	modelModalItemStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(modelModalBG)
	modelModalTabActiveStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Background(lipgloss.Color("45")).Bold(true).Padding(0, 1)
	modelModalTabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("238")).Padding(0, 1)
	modelModalSearchLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Background(modelModalBG).Bold(true)
	modelModalSearchValueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(modelModalBG)
	modelModalSearchHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Background(modelModalBG)
	modelModalSearchCursor     = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Background(modelModalBG)
)

type ModelOption struct {
	ProviderName string
	ProviderKey  string
	ModelID      string
}

func (m ModelOption) FilterValue() string {
	return strings.TrimSpace(m.ProviderName + " " + m.ModelID)
}

func (m ModelOption) Title() string {
	return strings.TrimSpace(m.ModelID)
}

func (m ModelOption) Description() string {
	return strings.TrimSpace(m.ProviderName)
}

type modelFilterTab int

const (
	modelFilterAll modelFilterTab = iota
	modelFilterFree
)

type ModelsModal struct {
	Visible        bool
	models         []ModelOption
	filtered       []ModelOption
	list           list.Model
	loading        bool
	loadingMessage string
	query          string
	activeTab      modelFilterTab
}

func NewModelsModal(models []ModelOption) *ModelsModal {
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(1)
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.Foreground(lipgloss.Color("252")).Background(modelModalBG)
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.Foreground(lipgloss.Color("244")).Background(modelModalBG)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(lipgloss.Color("213")).Background(modelModalBG).Bold(true)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(lipgloss.Color("183")).Background(modelModalBG).Bold(true)
	delegate.Styles.DimmedTitle = delegate.Styles.DimmedTitle.Background(modelModalBG)
	delegate.Styles.DimmedDesc = delegate.Styles.DimmedDesc.Background(modelModalBG)
	delegate.Styles.FilterMatch = delegate.Styles.FilterMatch.Background(modelModalBG)

	l := list.New(nil, delegate, 72, 14)
	l.Styles.TitleBar = l.Styles.TitleBar.Background(modelModalBG)
	l.Styles.Title = l.Styles.Title.Background(modelModalBG)
	l.Styles.Spinner = l.Styles.Spinner.Background(modelModalBG)
	l.Styles.FilterPrompt = l.Styles.FilterPrompt.Background(modelModalBG)
	l.Styles.FilterCursor = l.Styles.FilterCursor.Background(modelModalBG)
	l.Styles.DefaultFilterCharacterMatch = l.Styles.DefaultFilterCharacterMatch.Background(modelModalBG)
	l.Styles.StatusBar = l.Styles.StatusBar.Background(modelModalBG)
	l.Styles.StatusEmpty = l.Styles.StatusEmpty.Background(modelModalBG)
	l.Styles.StatusBarActiveFilter = l.Styles.StatusBarActiveFilter.Background(modelModalBG)
	l.Styles.StatusBarFilterCount = l.Styles.StatusBarFilterCount.Background(modelModalBG)
	l.Styles.NoItems = l.Styles.NoItems.Background(modelModalBG)
	l.Styles.PaginationStyle = l.Styles.PaginationStyle.Background(modelModalBG)
	l.Styles.HelpStyle = l.Styles.HelpStyle.Background(modelModalBG)
	l.Styles.ActivePaginationDot = l.Styles.ActivePaginationDot.Background(modelModalBG)
	l.Styles.InactivePaginationDot = l.Styles.InactivePaginationDot.Background(modelModalBG)
	l.Styles.ArabicPagination = l.Styles.ArabicPagination.Background(modelModalBG)
	l.Styles.DividerDot = l.Styles.DividerDot.Background(modelModalBG)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	m := &ModelsModal{
		Visible:   false,
		list:      l,
		activeTab: modelFilterAll,
	}
	m.SetModelOptions(models)
	return m
}

func (m *ModelsModal) SetModelOptions(models []ModelOption) {
	if m == nil {
		return
	}
	selectedKey := m.selectedModelKey()
	m.models = append([]ModelOption(nil), models...)
	m.applyFilters(selectedKey)
}

func (m *ModelsModal) SetSize(width, height int) {
	if m == nil {
		return
	}
	if width <= 0 || height <= 0 {
		return
	}
	innerWidth := width - 10
	if innerWidth < 44 {
		innerWidth = 44
	}
	innerHeight := height - 14
	if innerHeight < 6 {
		innerHeight = 6
	}
	m.list.SetWidth(innerWidth)
	m.list.SetHeight(innerHeight)
}

func (m *ModelsModal) Open() {
	if m == nil {
		return
	}
	m.Visible = true
}

func (m *ModelsModal) Close() {
	if m == nil {
		return
	}
	m.Visible = false
}

func (m *ModelsModal) SetLoading(loadingMessage string) {
	if m == nil {
		return
	}
	m.loading = true
	m.loadingMessage = strings.TrimSpace(loadingMessage)
}

func (m *ModelsModal) ClearLoading() {
	if m == nil {
		return
	}
	m.loading = false
	m.loadingMessage = ""
}

func (m *ModelsModal) Move(delta int) {
	if m == nil || delta == 0 {
		return
	}
	steps := delta
	if steps < 0 {
		steps = -steps
		for i := 0; i < steps; i++ {
			m.list.CursorUp()
		}
		return
	}
	for i := 0; i < steps; i++ {
		m.list.CursorDown()
	}
}

func (m *ModelsModal) Update(msg tea.Msg) tea.Cmd {
	if m == nil {
		return nil
	}
	if m.loading {
		return nil
	}

	switch typed := msg.(type) {
	case tea.KeyMsg:
		switch typed.String() {
		case "tab", "shift+tab", "left", "right":
			m.toggleTab()
			return nil
		case "backspace", "ctrl+h":
			if m.query != "" {
				m.query = trimLastRune(m.query)
				m.applyFilters(m.selectedModelKey())
			}
			return nil
		case "ctrl+u":
			if m.query != "" {
				m.query = ""
				m.applyFilters("")
			}
			return nil
		}

		if typed.Type == tea.KeyRunes && len(typed.Runes) > 0 && !typed.Alt {
			m.query += string(typed.Runes)
			m.applyFilters(m.selectedModelKey())
			return nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return cmd
}

func (m *ModelsModal) SelectByValue(model string) {
	if m == nil {
		return
	}
	target := normalizeModelSelectionKey(model)
	if target == "" {
		return
	}
	for _, option := range m.models {
		if normalizeModelSelectionKey(option.ModelID) == target {
			m.selectModelOption(option)
			return
		}
	}
}

func (m *ModelsModal) SelectByProviderAndModel(providerKey, modelID string) {
	if m == nil {
		return
	}
	providerKey = strings.TrimSpace(strings.ToLower(providerKey))
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	if providerKey == "" || modelID == "" {
		return
	}
	targetModelID := normalizeModelSelectionKey(modelID)
	for _, option := range m.models {
		if strings.EqualFold(strings.TrimSpace(option.ProviderKey), providerKey) &&
			normalizeModelSelectionKey(option.ModelID) == targetModelID {
			m.selectModelOption(option)
			return
		}
	}
}

func (m *ModelsModal) SelectedModel() (ModelOption, bool) {
	if m == nil {
		return ModelOption{}, false
	}
	item := m.list.SelectedItem()
	if item == nil {
		return ModelOption{}, false
	}
	model, ok := item.(ModelOption)
	return model, ok
}

func (m *ModelsModal) View() string {
	if m == nil || !m.Visible {
		return ""
	}
	title := modelModalTitleStyle.Render("Select Model")
	if m.loading {
		body := modelModalItemStyle.Render("Connecting...\n\n" + m.loadingMessage)
		hint := modelModalHintStyle.Render("Please wait")
		return modelModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s", title, body, hint))
	}
	if len(m.models) == 0 {
		body := modelModalItemStyle.Render("No models available. Connect a provider first.")
		hint := modelModalHintStyle.Render("esc: close")
		return modelModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s", title, body, hint))
	}

	body := m.list.View()
	if len(m.filtered) == 0 {
		body = modelModalItemStyle.Render("No models match the current filters.")
	}

	searchText := strings.TrimSpace(m.query)
	if searchText == "" {
		searchText = modelModalSearchHintStyle.Render("type to search models")
	} else {
		searchText = modelModalSearchValueStyle.Render(searchText)
	}

	hint := modelModalHintStyle.Render("type: search  tab: ALL/FREE  backspace: erase  enter: select  esc: close")

	return modelModalBoxStyle.Render(fmt.Sprintf("%s\n\n%s\n%s %s%s\n\n%s\n\n%s",
		title,
		m.renderTabs(),
		modelModalSearchLabelStyle.Render("Search:"),
		searchText,
		modelModalSearchCursor.Render("█"),
		body,
		hint,
	))
}

func (m *ModelsModal) toggleTab() {
	selectedKey := m.selectedModelKey()
	if m.activeTab == modelFilterAll {
		m.activeTab = modelFilterFree
	} else {
		m.activeTab = modelFilterAll
	}
	m.applyFilters(selectedKey)
}

func (m *ModelsModal) applyFilters(preferredKey string) {
	if m == nil {
		return
	}
	query := strings.ToLower(strings.TrimSpace(m.query))
	filtered := make([]ModelOption, 0, len(m.models))
	for _, model := range m.models {
		if m.activeTab == modelFilterFree && !isFreeModelID(model.ModelID) {
			continue
		}
		if query != "" {
			searchSpace := strings.ToLower(strings.TrimSpace(model.ModelID + " " + model.ProviderName + " " + model.ProviderKey))
			if !strings.Contains(searchSpace, query) {
				continue
			}
		}
		filtered = append(filtered, model)
	}

	m.filtered = filtered
	items := make([]list.Item, 0, len(filtered))
	for _, model := range filtered {
		items = append(items, model)
	}
	m.list.SetItems(items)

	if len(filtered) == 0 {
		return
	}

	if preferredKey != "" {
		for idx, option := range filtered {
			if modelOptionKey(option) == preferredKey {
				m.list.Select(idx)
				return
			}
		}
	}

	if m.list.Index() < 0 || m.list.Index() >= len(filtered) {
		m.list.Select(0)
	}
}

func (m *ModelsModal) selectModelOption(option ModelOption) {
	target := modelOptionKey(option)
	for idx, filtered := range m.filtered {
		if modelOptionKey(filtered) == target {
			m.list.Select(idx)
			return
		}
	}
	m.activeTab = modelFilterAll
	m.query = ""
	m.applyFilters(target)
}

func (m *ModelsModal) selectedModelKey() string {
	selected, ok := m.SelectedModel()
	if !ok {
		return ""
	}
	return modelOptionKey(selected)
}

func (m *ModelsModal) renderTabs() string {
	allCount := len(m.models)
	freeCount := 0
	for _, model := range m.models {
		if isFreeModelID(model.ModelID) {
			freeCount++
		}
	}

	allLabel := fmt.Sprintf("ALL (%d)", allCount)
	freeLabel := fmt.Sprintf("FREE (%d)", freeCount)
	if m.activeTab == modelFilterAll {
		return lipgloss.JoinHorizontal(lipgloss.Left,
			modelModalTabActiveStyle.Render(allLabel),
			" ",
			modelModalTabInactiveStyle.Render(freeLabel),
		)
	}
	return lipgloss.JoinHorizontal(lipgloss.Left,
		modelModalTabInactiveStyle.Render(allLabel),
		" ",
		modelModalTabActiveStyle.Render(freeLabel),
	)
}

func modelOptionKey(option ModelOption) string {
	return strings.ToLower(strings.TrimSpace(option.ProviderKey)) + "::" + normalizeModelSelectionKey(option.ModelID)
}

func isFreeModelID(modelID string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasSuffix(normalized, " [free]") || strings.Contains(normalized, ":free")
}

func trimLastRune(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func normalizeModelSelectionKey(model string) string {
	model = strings.TrimSpace(model)
	lower := strings.ToLower(model)
	if strings.HasSuffix(lower, " [free]") {
		model = strings.TrimSpace(model[:len(model)-len(" [free]")])
	}
	return strings.ToLower(strings.TrimSpace(model))
}
