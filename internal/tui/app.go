package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yubzen/orchestra/internal/agent"
	"github.com/yubzen/orchestra/internal/config"
	"github.com/yubzen/orchestra/internal/state"
)

var (
	appStyle = lipgloss.NewStyle().Margin(0, 0)
)

type AppModel struct {
	cfg              *config.Config
	db               *state.DB
	session          *state.Session
	orc              *agent.Orchestrator
	chat             *ChatModel
	statusbar        *StatusBarModel
	modelsModal      *ModelsModal
	rolesModal       *SelectModal
	connectModal     *SelectModal
	authMethodModal  *SelectModal
	apiKeyModal      *APIKeyModal
	providerCatalog  []ProviderCatalog
	selectedProvider string
	roleOrder        []string
	roleModels       map[string]string
	activeRole       int
	width            int
	height           int
}

func NewAppModel(cfg *config.Config, db *state.DB, session *state.Session, orc *agent.Orchestrator) *AppModel {
	chat := NewChatModel()
	sb := NewStatusBarModelWithConfig(cfg, session)
	roleOrder := []string{"CODER", "REVIEWER", "PLANNER"}
	roleModels := map[string]string{
		"CODER":    "",
		"REVIEWER": "",
		"PLANNER":  "",
	}
	modelsModal := NewModelsModal(nil)
	roleModal := NewSelectModal("Select Role", "up/down: navigate  enter: select  esc: close")
	roleModal.SetOptions([]SelectOption{
		{Label: "CODER", Enabled: true},
		{Label: "REVIEWER", Enabled: true},
		{Label: "PLANNER", Enabled: true},
	})
	model := &AppModel{
		cfg:             cfg,
		db:              db,
		session:         session,
		orc:             orc,
		chat:            chat,
		statusbar:       sb,
		modelsModal:     modelsModal,
		rolesModal:      roleModal,
		connectModal:    NewSelectModal("Select Provider", "up/down: navigate  enter: select  esc: close"),
		authMethodModal: NewSelectModal("Select auth method", "up/down: navigate  enter: select  esc: close"),
		apiKeyModal:     &APIKeyModal{},
		providerCatalog: defaultProviderCatalog(),
		roleOrder:       roleOrder,
		roleModels:      roleModels,
		activeRole:      0,
	}
	model.refreshConnectOptions()
	model.refreshModelsFromConnections()
	model.syncRoleAndModelDisplay()
	return model
}

func (m *AppModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, m.chat.Init(), m.statusbar.Init(), textinput.Blink)
	if m.orc.UpdateChan != nil {
		cmds = append(cmds, waitForStepUpdate(m.orc.UpdateChan))
	}
	return tea.Batch(cmds...)
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.apiKeyModal != nil && m.apiKeyModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.apiKeyModal.Close()
				return m, nil
			case "backspace":
				if len(m.apiKeyModal.Value) > 0 {
					m.apiKeyModal.Value = m.apiKeyModal.Value[:len(m.apiKeyModal.Value)-1]
				}
				return m, nil
			case "enter":
				if strings.TrimSpace(m.apiKeyModal.Value) == "" {
					return m, func() tea.Msg { return CommandResultMsg{Msg: "API key cannot be empty"} }
				}
				if err := storeProviderKey(m.apiKeyModal.KeyName, m.apiKeyModal.Value); err != nil {
					return m, func() tea.Msg { return CommandResultMsg{Msg: fmt.Sprintf("failed to store API key: %v", err)} }
				}
				m.apiKeyModal.Close()
				m.refreshModelsFromConnections()
				m.syncRoleAndModelDisplay()
				return m, func() tea.Msg {
					return CommandResultMsg{Msg: "Provider connected successfully"}
				}
			default:
				if len(msg.Runes) > 0 {
					m.apiKeyModal.Value += string(msg.Runes)
					return m, nil
				}
				return m, nil
			}
		}

		if m.authMethodModal != nil && m.authMethodModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.authMethodModal.Close()
				return m, nil
			case "up":
				m.authMethodModal.Move(-1)
				return m, nil
			case "down":
				m.authMethodModal.Move(1)
				return m, nil
			case "enter":
				opt, ok := m.authMethodModal.SelectedOption()
				if ok {
					if strings.EqualFold(opt.Label, "Manually enter API Key") {
						provider, hasProvider := m.providerByName(m.selectedProvider)
						if hasProvider {
							m.authMethodModal.Close()
							m.apiKeyModal.Open(provider.Name, provider.KeyName)
							return m, nil
						}
					}
				}
				m.authMethodModal.Close()
				return m, nil
			default:
				return m, nil
			}
		}

		if m.connectModal != nil && m.connectModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.connectModal.Close()
				return m, nil
			case "up":
				m.connectModal.Move(-1)
				return m, nil
			case "down":
				m.connectModal.Move(1)
				return m, nil
			case "enter":
				opt, ok := m.connectModal.SelectedOption()
				if ok {
					m.selectedProvider = opt.Label
					provider, hasProvider := m.providerByName(opt.Label)
					if hasProvider {
						var authOptions []SelectOption
						for _, method := range provider.AuthModes {
							authOptions = append(authOptions, SelectOption{
								Label:   method.Name,
								Enabled: method.Available,
							})
						}
						m.authMethodModal.SetOptions(authOptions)
						m.authMethodModal.Open()
						return m, nil
					}
				}
				m.connectModal.Close()
				return m, nil
			default:
				return m, nil
			}
		}

		if m.rolesModal != nil && m.rolesModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.rolesModal.Close()
				return m, nil
			case "up":
				m.rolesModal.Move(-1)
				return m, nil
			case "down":
				m.rolesModal.Move(1)
				return m, nil
			case "enter":
				opt, ok := m.rolesModal.SelectedOption()
				if ok {
					m.setActiveRole(opt.Label)
				}
				m.rolesModal.Close()
				return m, nil
			default:
				return m, nil
			}
		}

		if m.modelsModal != nil && m.modelsModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.modelsModal.Close()
				return m, nil
			case "up":
				m.modelsModal.Move(-1)
				return m, nil
			case "down":
				m.modelsModal.Move(1)
				return m, nil
			case "enter":
				model, ok := m.modelsModal.SelectedModel()
				if ok {
					m.applySelectedModel(model)
					m.modelsModal.Close()
					return m, func() tea.Msg {
						return CommandResultMsg{Msg: fmt.Sprintf("Model selected: %s", model)}
					}
				}
				m.modelsModal.Close()
				return m, nil
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.chat.ApplyTopSlashSuggestion() {
				return m, nil
			}
		case "shift+tab":
			m.cycleRole(1)
			return m, nil
		case "up":
			if m.chat.MoveSlashSelection(-1) {
				return m, nil
			}
		case "down":
			if m.chat.MoveSlashSelection(1) {
				return m, nil
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.statusbar.SetWidth(msg.Width)
		sbHeight := 1
		m.chat.SetSize(msg.Width, msg.Height-sbHeight)
	case agent.StepUpdate:
		m.chat.AddMessage("System", fmt.Sprintf("[%s] %s: %s", msg.StepID, msg.Status, msg.Msg))
		cmds = append(cmds, waitForStepUpdate(m.orc.UpdateChan))
	case AgentReplyMsg:
		m.chat.AddMessage("Assistant", msg.Reply)
	case CommandResultMsg:
		m.chat.AddMessage("System", msg.Msg)
	case OpenModelsModalMsg:
		if m.modelsModal != nil {
			m.refreshModelsFromConnections()
			if modelName := m.currentRoleModel(); modelName != "" {
				m.modelsModal.SelectByValue(modelName)
			}
			m.modelsModal.Open()
		}
	case OpenRolesModalMsg:
		if m.rolesModal != nil {
			m.rolesModal.Open()
		}
	case OpenConnectModalMsg:
		if m.connectModal != nil {
			m.refreshConnectOptions()
			m.connectModal.Open()
		}
	}

	if msgKey, ok := msg.(tea.KeyMsg); ok && msgKey.String() == "enter" {
		if selected, ok := m.chat.SelectedSlashSuggestion(); ok {
			cmds = append(cmds, handleSlashCommand(selected.Name, m))
			m.chat.ClearInput()
			return m, tea.Batch(cmds...)
		}

		val := m.chat.GetInputValue()
		if val != "" {
			if strings.HasPrefix(val, "/") {
				cmds = append(cmds, handleSlashCommand(val, m))
				m.chat.ClearInput()
			} else {
				m.chat.AddMessage("User", val)
				m.chat.ClearInput()
				cmds = append(cmds, m.runAgentCmd(val))
			}
		}
	} else {
		chatModel, cmd := m.chat.Update(msg)
		m.chat = chatModel.(*ChatModel)
		cmds = append(cmds, cmd)
	}

	sbModel, cmd := m.statusbar.Update(msg)
	m.statusbar = sbModel.(*StatusBarModel)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *AppModel) runAgentCmd(prompt string) tea.Cmd {
	return func() tea.Msg {
		if m.orc == nil {
			return CommandResultMsg{Msg: "orchestrator is not initialized"}
		}
		if !m.hasAnyModelSelected() {
			return CommandResultMsg{Msg: "no AI selected"}
		}

		if m.session.Mode == "orchestrated" {
			if !m.hasAllOrchestratorModelsSelected() {
				return CommandResultMsg{Msg: "no AI selected"}
			}
			go func() {
				if err := m.orc.Run(context.Background(), prompt); err != nil && m.orc.UpdateChan != nil {
					select {
					case m.orc.UpdateChan <- agent.StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()}:
					default:
					}
				}
			}()
			return nil
		}

		currentRole := m.currentRole()
		selectedModel := strings.TrimSpace(m.roleModels[currentRole])
		if selectedModel == "" {
			return CommandResultMsg{Msg: "no AI selected"}
		}

		activeAgent := m.agentForRole(currentRole)
		if activeAgent == nil {
			return CommandResultMsg{Msg: fmt.Sprintf("role %s is not available", currentRole)}
		}

		reply, err := activeAgent.Run(context.Background(), prompt, m.session, m.db)
		if err != nil {
			return CommandResultMsg{Msg: fmt.Sprintf("%s error: %v", strings.ToLower(currentRole), err)}
		}
		return AgentReplyMsg{Reply: reply}
	}
}

type AgentReplyMsg struct {
	Reply string
}

func waitForStepUpdate(ch chan agent.StepUpdate) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func (m *AppModel) View() string {
	view := lipgloss.JoinVertical(lipgloss.Left,
		m.chat.View(),
		m.statusbar.View(),
	)
	base := appStyle.Render(view)

	if m.apiKeyModal != nil && m.apiKeyModal.Visible {
		overlay := m.apiKeyModal.View()
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
		return overlay
	}
	if m.authMethodModal != nil && m.authMethodModal.Visible {
		overlay := m.authMethodModal.View()
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
		return overlay
	}
	if m.connectModal != nil && m.connectModal.Visible {
		overlay := m.connectModal.View()
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
		return overlay
	}
	if m.modelsModal != nil && m.modelsModal.Visible {
		overlay := m.modelsModal.View()
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
		return overlay
	}
	if m.rolesModal != nil && m.rolesModal.Visible {
		overlay := m.rolesModal.View()
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
		return overlay
	}

	return base
}

func (m *AppModel) applySelectedModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}

	role := m.currentRole()
	m.roleModels[role] = model
	m.syncRoleAndModelDisplay()
	if a := m.agentForRole(role); a != nil {
		a.Model = model
	}
}

func (m *AppModel) currentRole() string {
	if len(m.roleOrder) == 0 {
		return "CODER"
	}
	if m.activeRole < 0 || m.activeRole >= len(m.roleOrder) {
		m.activeRole = 0
	}
	return m.roleOrder[m.activeRole]
}

func (m *AppModel) currentRoleModel() string {
	role := m.currentRole()
	return strings.TrimSpace(m.roleModels[role])
}

func (m *AppModel) setActiveRole(role string) {
	for i, item := range m.roleOrder {
		if strings.EqualFold(item, strings.TrimSpace(role)) {
			m.activeRole = i
			m.syncRoleAndModelDisplay()
			return
		}
	}
}

func (m *AppModel) cycleRole(delta int) {
	if len(m.roleOrder) == 0 {
		return
	}
	m.activeRole += delta
	for m.activeRole >= len(m.roleOrder) {
		m.activeRole -= len(m.roleOrder)
	}
	for m.activeRole < 0 {
		m.activeRole += len(m.roleOrder)
	}
	m.syncRoleAndModelDisplay()
}

func (m *AppModel) syncRoleAndModelDisplay() {
	if m.statusbar == nil {
		return
	}
	role := m.currentRole()
	m.statusbar.Role = role
	model := strings.TrimSpace(m.roleModels[role])
	if model == "" {
		model = "no-model-selected"
	}
	m.statusbar.ModelName = model

	// Until user selects models explicitly, agents should start unconfigured.
	if m.orc != nil {
		if m.orc.Coder != nil {
			m.orc.Coder.Model = strings.TrimSpace(m.roleModels["CODER"])
		}
		if m.orc.Reviewer != nil {
			m.orc.Reviewer.Model = strings.TrimSpace(m.roleModels["REVIEWER"])
		}
		if m.orc.Planner != nil {
			m.orc.Planner.Model = strings.TrimSpace(m.roleModels["PLANNER"])
		}
	}
}

func (m *AppModel) providerByName(name string) (ProviderCatalog, bool) {
	baseName := strings.TrimSpace(name)
	if idx := strings.Index(baseName, " ("); idx > 0 {
		baseName = baseName[:idx]
	}
	for _, provider := range m.providerCatalog {
		if strings.EqualFold(strings.TrimSpace(provider.Name), baseName) {
			return provider, true
		}
	}
	return ProviderCatalog{}, false
}

func (m *AppModel) refreshConnectOptions() {
	if m.connectModal == nil {
		return
	}
	var options []SelectOption
	for _, provider := range m.providerCatalog {
		label := provider.Name
		if hasProviderKey(provider.KeyName) {
			label += " (connected)"
		}
		options = append(options, SelectOption{
			Label:   label,
			Enabled: true,
		})
	}
	m.connectModal.SetOptions(options)
}

func (m *AppModel) refreshModelsFromConnections() {
	if m.modelsModal == nil {
		return
	}
	m.modelsModal.SetModels(connectedModels(m.providerCatalog))
}

func (m *AppModel) agentForRole(role string) *agent.Agent {
	if m.orc == nil {
		return nil
	}
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "CODER":
		return m.orc.Coder
	case "REVIEWER":
		return m.orc.Reviewer
	case "PLANNER":
		return m.orc.Planner
	default:
		return nil
	}
}

func (m *AppModel) hasAnyModelSelected() bool {
	for _, role := range m.roleOrder {
		if strings.TrimSpace(m.roleModels[role]) != "" {
			return true
		}
	}
	return false
}

func (m *AppModel) hasAllOrchestratorModelsSelected() bool {
	for _, role := range []string{"PLANNER", "CODER", "REVIEWER"} {
		if strings.TrimSpace(m.roleModels[role]) == "" {
			return false
		}
	}
	return true
}
