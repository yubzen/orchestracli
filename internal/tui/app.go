package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yubzen/orchestra/internal/agent"
	"github.com/yubzen/orchestra/internal/config"
	"github.com/yubzen/orchestra/internal/providers"
	"github.com/yubzen/orchestra/internal/state"
)

var (
	appStyle = lipgloss.NewStyle().Margin(0, 0)
)

var loadProviderCredential = storedProviderKey

type ModelsFetchedMsg struct {
	RequestID    int
	ProviderName string
	ProviderKey  string
	Models       []string
	Err          error
}

type AppModel struct {
	cfg               *config.Config
	db                *state.DB
	session           *state.Session
	orc               *agent.Orchestrator
	chat              *ChatModel
	statusbar         *StatusBarModel
	modelsModal       *ModelsModal
	planModal         *PlanReviewModal
	rolesModal        *SelectModal
	connectModal      *SelectModal
	authMethodModal   *SelectModal
	apiKeyModal       *APIKeyModal
	providerCatalog   []ProviderCatalog
	providerClients   map[string]providers.Provider
	discoveredModels  map[string][]string
	providerConnected map[string]bool
	selectedProvider  string
	roleOrder         []string
	roleModels        map[string]string
	roleProviderKeys  map[string]string
	thinkingStarted   map[agent.Role]time.Time
	thinkingBuffer    map[agent.Role]string
	activeRole        int
	width             int
	height            int
	nextConnectReqID  int
	activeConnectReq  int
	pendingMessages   []string
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
	roleProviders := map[string]string{
		"CODER":    "",
		"REVIEWER": "",
		"PLANNER":  "",
	}

	modelsModal := NewModelsModal(nil)
	planModal := NewPlanReviewModal()
	roleModal := NewSelectModal("Select Role", "up/down: navigate  enter: select  esc: close")
	roleModal.SetOptions([]SelectOption{
		{Label: "CODER", Enabled: true},
		{Label: "REVIEWER", Enabled: true},
		{Label: "PLANNER", Enabled: true},
	})

	model := &AppModel{
		cfg:               cfg,
		db:                db,
		session:           session,
		orc:               orc,
		chat:              chat,
		statusbar:         sb,
		modelsModal:       modelsModal,
		planModal:         planModal,
		rolesModal:        roleModal,
		connectModal:      NewSelectModal("Select Provider", "up/down: navigate  enter: select  esc: close"),
		authMethodModal:   NewSelectModal("Select auth method", "up/down: navigate  enter: select  esc: close"),
		apiKeyModal:       &APIKeyModal{},
		providerCatalog:   defaultProviderCatalog(cfg),
		providerClients:   make(map[string]providers.Provider),
		discoveredModels:  make(map[string][]string),
		providerConnected: make(map[string]bool),
		roleOrder:         roleOrder,
		roleModels:        roleModels,
		roleProviderKeys:  roleProviders,
		thinkingStarted:   make(map[agent.Role]time.Time),
		thinkingBuffer:    make(map[agent.Role]string),
		activeRole:        0,
	}
	model.loadPersistedSessionSelections()
	model.syncExecutionModeFromSession()
	model.refreshConnectOptions()
	model.refreshModelsModal("")
	model.syncRoleAndModelDisplay()
	return model
}

func (m *AppModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, m.chat.Init(), m.statusbar.Init(), textinput.Blink)
	if m.orc != nil && m.orc.UpdateChan != nil {
		cmds = append(cmds, waitForStepUpdate(m.orc.UpdateChan))
	}
	if m.orc != nil && m.orc.EventChan != nil {
		cmds = append(cmds, waitForAgentEvent(m.orc.EventChan))
	}
	if m.chat.IsLoading() {
		cmds = append(cmds, loadingTickCmd())
	}
	return tea.Batch(cmds...)
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.planModal != nil && m.planModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			}

			action, cmd := m.planModal.Update(msg)
			if action.DecisionMade {
				if m.orc != nil {
					m.orc.SubmitPlanApproval(agent.PlanApproval{
						PlanID:     action.PlanID,
						Approved:   action.Approved,
						EditedPlan: action.EditedPlan,
					})
				}
				m.planModal.Close()
				msgText := "Plan rejected."
				if action.Approved {
					msgText = "Plan approved."
					if strings.TrimSpace(action.EditedPlan) != "" {
						msgText = "Edited plan approved."
					}
				}
				return m, func() tea.Msg {
					return CommandResultMsg{Msg: msgText}
				}
			}
			return m, cmd
		}

		if m.apiKeyModal != nil && m.apiKeyModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.activeConnectReq = 0
				m.modelsModal.ClearLoading()
				m.apiKeyModal.Close()
				return m, nil
			case "backspace":
				if m.apiKeyModal.Connecting {
					return m, nil
				}
				if len(m.apiKeyModal.Value) > 0 {
					m.apiKeyModal.Value = m.apiKeyModal.Value[:len(m.apiKeyModal.Value)-1]
				}
				return m, nil
			case "enter":
				if m.apiKeyModal.Connecting {
					return m, nil
				}
				credential := strings.TrimSpace(m.apiKeyModal.Value)
				if err := providers.ValidateCredential(credential); err != nil {
					m.apiKeyModal.SetError(err.Error())
					return m, nil
				}
				provider, ok := m.providerByKey(m.apiKeyModal.KeyName)
				if !ok {
					m.apiKeyModal.SetError("Unknown provider selected.")
					return m, nil
				}
				m.nextConnectReqID++
				reqID := m.nextConnectReqID
				m.activeConnectReq = reqID
				m.apiKeyModal.BeginConnecting("validating credentials and fetching models")
				if m.modelsModal != nil {
					m.modelsModal.SetLoading(fmt.Sprintf("Discovering models for %s...", provider.Name))
				}
				return m, m.fetchProviderModelsCmd(reqID, provider, credential)
			default:
				if m.apiKeyModal.Connecting {
					return m, nil
				}
				if len(msg.Runes) > 0 {
					m.apiKeyModal.Value += string(msg.Runes)
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
				if !ok {
					m.authMethodModal.Close()
					return m, nil
				}
				provider, hasProvider := m.providerByName(m.selectedProvider)
				if !hasProvider {
					m.authMethodModal.Close()
					return m, nil
				}
				method, foundMethod := m.authMethodByName(provider, opt.Label)
				if !foundMethod {
					m.authMethodModal.Close()
					return m, nil
				}
				m.authMethodModal.Close()
				m.openAPIKeyModal(provider, method)
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
				if !ok {
					m.connectModal.Close()
					return m, nil
				}
				m.selectedProvider = opt.Label
				provider, hasProvider := m.providerByName(opt.Label)
				if !hasProvider {
					m.connectModal.Close()
					return m, nil
				}
				if len(provider.AuthModes) == 1 && provider.AuthModes[0].Available {
					method := provider.AuthModes[0]
					if method.Kind == AuthMethodAPIKey {
						m.openAPIKeyModal(provider, method)
						return m, nil
					}
				}
				authOptions := make([]SelectOption, 0, len(provider.AuthModes))
				for _, method := range provider.AuthModes {
					authOptions = append(authOptions, SelectOption{
						Label:   method.Name,
						Enabled: method.Available,
					})
				}
				m.authMethodModal.SetOptions(authOptions)
				m.authMethodModal.Open()
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
			case "enter":
				model, ok := m.modelsModal.SelectedModel()
				if ok {
					m.applySelectedModel(model)
					m.modelsModal.Close()
					return m, func() tea.Msg {
						return CommandResultMsg{
							Msg: fmt.Sprintf("Model selected for %s: %s (%s)", m.currentRole(), model.ModelID, model.ProviderName),
						}
					}
				}
				return m, nil
			default:
				cmd := m.modelsModal.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "p":
			if strings.TrimSpace(m.chat.GetInputValue()) == "" {
				modeMsg := m.toggleExecutionMode()
				return m, func() tea.Msg {
					return CommandResultMsg{Msg: modeMsg}
				}
			}
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
		m.chat.SetSize(msg.Width, msg.Height-1)
		modalWidth := msg.Width - 4
		if modalWidth < 32 {
			modalWidth = 32
		}
		if m.connectModal != nil {
			m.connectModal.SetWidth(modalWidth)
		}
		if m.authMethodModal != nil {
			m.authMethodModal.SetWidth(modalWidth)
		}
		if m.apiKeyModal != nil {
			m.apiKeyModal.SetWidth(modalWidth)
		}
		if m.modelsModal != nil {
			m.modelsModal.SetSize(msg.Width, msg.Height)
		}
		if m.planModal != nil {
			m.planModal.SetSize(msg.Width, msg.Height)
		}

	case agent.StepUpdate:
		if strings.EqualFold(strings.TrimSpace(msg.Status), "plan_ready") && m.planModal != nil {
			planText := strings.TrimSpace(msg.PlanYAML)
			if planText == "" {
				planText = strings.TrimSpace(msg.Msg)
			}
			m.chat.AddMessage("System", fmt.Sprintf("[%s] %s", msg.StepID, msg.Msg))
			m.planModal.Open(msg.PlanID, planText)
		} else {
			m.chat.AddMessage("System", fmt.Sprintf("[%s] %s: %s", msg.StepID, msg.Status, msg.Msg))
		}
		if m.orc != nil && m.orc.UpdateChan != nil {
			cmds = append(cmds, waitForStepUpdate(m.orc.UpdateChan))
		}

	case agent.AgentEvent:
		m.handleAgentEvent(msg)
		if m.orc != nil && m.orc.EventChan != nil {
			cmds = append(cmds, waitForAgentEvent(m.orc.EventChan))
		}

	case AgentReplyMsg:
		m.chat.SetLoading(false, "")
		roleLabel := strings.ToUpper(strings.TrimSpace(msg.Role))
		if roleLabel == "" {
			roleLabel = m.currentRole()
		}
		m.chat.AddMessage(roleLabel, msg.Reply)
		// Drain pending queue.
		if len(m.pendingMessages) > 0 {
			next := m.pendingMessages[0]
			m.pendingMessages = m.pendingMessages[1:]
			m.chat.SetLoading(true, m.currentRole())
			cmds = append(cmds, m.runAgentCmd(next), loadingTickCmd())
		}

	case CommandResultMsg:
		m.chat.SetLoading(false, "")
		m.chat.AddMessage("System", msg.Msg)
		// Drain pending queue on command results too.
		if len(m.pendingMessages) > 0 {
			next := m.pendingMessages[0]
			m.pendingMessages = m.pendingMessages[1:]
			m.chat.SetLoading(true, m.currentRole())
			cmds = append(cmds, m.runAgentCmd(next), loadingTickCmd())
		}

	case LoadingTickMsg:
		if m.chat.IsLoading() {
			cmds = append(cmds, loadingTickCmd())
		}

	case OpenModelsModalMsg:
		if m.modelsModal != nil {
			m.refreshModelsModal("")
			m.modelsModal.SelectByProviderAndModel(m.roleProviderKeys[m.currentRole()], m.currentRoleModel())
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

	case ModelsFetchedMsg:
		if msg.RequestID != m.activeConnectReq {
			return m, nil
		}
		m.activeConnectReq = 0
		if m.modelsModal != nil {
			m.modelsModal.ClearLoading()
		}
		if msg.Err != nil {
			m.setProviderConnected(msg.ProviderKey, false)
			if m.apiKeyModal != nil && m.apiKeyModal.Visible {
				m.apiKeyModal.SetError(msg.Err.Error())
			}
			m.refreshConnectOptions()
			return m, func() tea.Msg {
				return CommandResultMsg{Msg: fmt.Sprintf("Connection failed for %s: %v", msg.ProviderName, msg.Err)}
			}
		}

		m.discoveredModels[msg.ProviderKey] = uniqueSortedModels(msg.Models)
		m.setProviderConnected(msg.ProviderKey, true)
		m.refreshConnectOptions()
		m.refreshModelsModal(msg.ProviderKey)
		if m.modelsModal != nil {
			m.modelsModal.Open()
		}
		if m.apiKeyModal != nil {
			m.apiKeyModal.Close()
		}
		if m.authMethodModal != nil {
			m.authMethodModal.Close()
		}
		if m.connectModal != nil {
			m.connectModal.Close()
		}

		return m, func() tea.Msg {
			return CommandResultMsg{
				Msg: fmt.Sprintf("%s connected successfully. Discovered %d model(s).", msg.ProviderName, len(msg.Models)),
			}
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
				// Expand @file references and show original in chat.
				expanded := expandFileReferences(val)
				m.chat.AddMessage("User", val)
				m.chat.ClearInput()

				if m.chat.IsLoading() {
					// Queue the message if the agent is still processing.
					m.pendingMessages = append(m.pendingMessages, expanded)
					m.chat.AddMessage("System", "⏳ Queued — will send after current response.")
				} else {
					m.chat.SetLoading(true, m.currentRole())
					cmds = append(cmds, m.runAgentCmd(expanded), loadingTickCmd())
				}
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
		selectedProvider := strings.TrimSpace(m.roleProviderKeys[currentRole])
		if selectedModel == "" || selectedProvider == "" {
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
		return AgentReplyMsg{Reply: reply, Role: currentRole}
	}
}

type AgentReplyMsg struct {
	Reply string
	Role  string
}

func waitForStepUpdate(ch chan agent.StepUpdate) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitForAgentEvent(ch chan agent.AgentEvent) tea.Cmd {
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
	if m.planModal != nil && m.planModal.Visible {
		overlay := m.planModal.View()
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

func (m *AppModel) applySelectedModel(model ModelOption) {
	if strings.TrimSpace(model.ModelID) == "" || strings.TrimSpace(model.ProviderKey) == "" {
		return
	}
	selectedModelID := normalizeSelectedModelID(model.ProviderKey, model.ModelID)
	if selectedModelID == "" {
		return
	}

	role := m.currentRole()
	m.roleModels[role] = selectedModelID
	m.roleProviderKeys[role] = strings.TrimSpace(model.ProviderKey)
	m.discoveredModels[model.ProviderKey] = uniqueSortedModels(append(m.discoveredModels[model.ProviderKey], model.ModelID))
	m.setProviderConnected(model.ProviderKey, true)

	m.syncRoleAndModelDisplay()
	if m.db != nil && m.session != nil {
		if err := m.db.SaveSessionModelSelection(context.Background(), m.session.ID, role, model.ProviderKey, selectedModelID); err != nil {
			m.chat.AddMessage("System", fmt.Sprintf("warning: failed to persist model selection: %v", err))
		}
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
	m.syncExecutionModeFromSession()

	role := m.currentRole()
	m.statusbar.Role = role
	model := strings.TrimSpace(m.roleModels[role])
	if model == "" {
		model = "no-model-selected"
	}
	m.statusbar.ModelName = model
	m.statusbar.ExecutionMode = strings.ToUpper(m.currentExecutionMode())

	for _, roleName := range []string{"CODER", "REVIEWER", "PLANNER"} {
		m.applyRoleConfigToAgent(roleName)
	}
}

func (m *AppModel) applyRoleConfigToAgent(role string) {
	a := m.agentForRole(role)
	if a == nil {
		return
	}
	a.Model = strings.TrimSpace(m.roleModels[role])

	providerKey := strings.TrimSpace(m.roleProviderKeys[role])
	if providerKey == "" {
		a.Provider = nil
		return
	}
	provider, err := m.providerInstanceByKey(providerKey)
	if err != nil {
		a.Provider = nil
		return
	}
	a.Provider = provider
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

func (m *AppModel) providerByKey(key string) (ProviderCatalog, bool) {
	for _, provider := range m.providerCatalog {
		if strings.EqualFold(strings.TrimSpace(provider.KeyName), strings.TrimSpace(key)) {
			return provider, true
		}
	}
	return ProviderCatalog{}, false
}

func (m *AppModel) authMethodByName(provider ProviderCatalog, name string) (AuthMethod, bool) {
	for _, method := range provider.AuthModes {
		if strings.EqualFold(strings.TrimSpace(method.Name), strings.TrimSpace(name)) {
			return method, true
		}
	}
	return AuthMethod{}, false
}

func (m *AppModel) openAPIKeyModal(provider ProviderCatalog, method AuthMethod) {
	if m.apiKeyModal == nil {
		return
	}
	m.apiKeyModal.Open(provider.Name, provider.KeyName, method)
	m.apiKeyModal.Value = loadProviderCredential(provider.KeyName)
}

func (m *AppModel) setProviderConnected(providerKey string, connected bool) {
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	if providerKey == "" {
		return
	}
	m.providerConnected[providerKey] = connected
}

func (m *AppModel) isProviderConnected(providerKey string) bool {
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	if providerKey == "" {
		return false
	}
	if m.providerConnected[providerKey] {
		return true
	}
	return len(uniqueSortedModels(m.discoveredModels[providerKey])) > 0
}

func (m *AppModel) refreshConnectOptions() {
	if m.connectModal == nil {
		return
	}
	var options []SelectOption
	for _, provider := range m.providerCatalog {
		status := "not connected"
		if m.isProviderConnected(provider.KeyName) {
			status = "connected"
		}
		label := fmt.Sprintf("%s (%s)", provider.Name, status)
		options = append(options, SelectOption{
			Label:   label,
			Enabled: true,
		})
	}
	m.connectModal.SetOptions(options)
}

func (m *AppModel) refreshModelsModal(filterProviderKey string) {
	if m.modelsModal == nil {
		return
	}
	modelOptions := m.buildModelOptions(filterProviderKey)
	m.modelsModal.SetModelOptions(modelOptions)
	m.modelsModal.SelectByProviderAndModel(m.roleProviderKeys[m.currentRole()], m.currentRoleModel())
}

func (m *AppModel) buildModelOptions(filterProviderKey string) []ModelOption {
	filterProviderKey = strings.TrimSpace(strings.ToLower(filterProviderKey))
	var options []ModelOption
	for _, provider := range m.providerCatalog {
		if filterProviderKey != "" && !strings.EqualFold(provider.KeyName, filterProviderKey) {
			continue
		}
		models := uniqueSortedModels(m.discoveredModels[provider.KeyName])
		for _, model := range models {
			options = append(options, ModelOption{
				ProviderName: provider.Name,
				ProviderKey:  provider.KeyName,
				ModelID:      model,
			})
		}
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].ProviderName == options[j].ProviderName {
			return options[i].ModelID < options[j].ModelID
		}
		return options[i].ProviderName < options[j].ProviderName
	})
	return options
}

func (m *AppModel) providerInstanceByKey(key string) (providers.Provider, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("provider key is empty")
	}
	if p, ok := m.providerClients[key]; ok && p != nil {
		return p, nil
	}

	providerEntry, ok := m.providerByKey(key)
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", key)
	}
	discoveryCfg := providerEntry.Discovery
	if strings.TrimSpace(discoveryCfg.KeyName) == "" {
		discoveryCfg.KeyName = providerEntry.KeyName
	}

	p, err := providers.NewDiscoveryProvider(discoveryCfg)
	if err != nil {
		return nil, err
	}
	m.providerClients[key] = p
	return p, nil
}

func (m *AppModel) fetchProviderModelsCmd(requestID int, provider ProviderCatalog, credential string) tea.Cmd {
	providerKey := provider.KeyName
	providerName := provider.Name
	credential = strings.TrimSpace(credential)
	discoveryCfg := provider.Discovery
	if strings.TrimSpace(discoveryCfg.KeyName) == "" {
		discoveryCfg.KeyName = providerKey
	}

	return func() tea.Msg {
		if err := providers.ValidateCredential(credential); err != nil {
			return ModelsFetchedMsg{
				RequestID:    requestID,
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          err,
			}
		}
		if err := storeProviderKey(providerKey, credential); err != nil {
			return ModelsFetchedMsg{
				RequestID:    requestID,
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          fmt.Errorf("failed to persist credential: %w", err),
			}
		}

		p, err := providers.NewDiscoveryProvider(discoveryCfg)
		if err != nil {
			return ModelsFetchedMsg{
				RequestID:    requestID,
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          err,
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		models, err := providers.DiscoverModels(ctx, p)
		if err != nil {
			return ModelsFetchedMsg{
				RequestID:    requestID,
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          err,
			}
		}
		return ModelsFetchedMsg{
			RequestID:    requestID,
			ProviderName: providerName,
			ProviderKey:  providerKey,
			Models:       models,
		}
	}
}

func (m *AppModel) loadPersistedSessionSelections() {
	if m.db == nil || m.session == nil {
		return
	}
	selections, err := m.db.GetSessionModelSelections(context.Background(), m.session.ID)
	if err != nil {
		return
	}
	for role, selection := range selections {
		role = strings.ToUpper(strings.TrimSpace(role))
		if _, ok := m.roleModels[role]; !ok {
			continue
		}
		providerKey := strings.TrimSpace(selection.ProviderKey)
		modelID := normalizeSelectedModelID(providerKey, selection.ModelID)
		m.roleModels[role] = modelID
		m.roleProviderKeys[role] = providerKey
		if providerKey != "" && modelID != "" {
			m.discoveredModels[providerKey] = uniqueSortedModels(
				append(m.discoveredModels[providerKey], modelID),
			)
			m.setProviderConnected(providerKey, true)
		}
	}
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
		if strings.TrimSpace(m.roleModels[role]) != "" && strings.TrimSpace(m.roleProviderKeys[role]) != "" {
			return true
		}
	}
	return false
}

func (m *AppModel) hasAllOrchestratorModelsSelected() bool {
	for _, role := range []string{"PLANNER", "CODER", "REVIEWER"} {
		if strings.TrimSpace(m.roleModels[role]) == "" || strings.TrimSpace(m.roleProviderKeys[role]) == "" {
			return false
		}
	}
	return true
}

func (m *AppModel) currentExecutionMode() string {
	if m.session == nil {
		return state.ExecutionModeFast
	}
	return state.NormalizeExecutionMode(m.session.ExecutionMode)
}

func (m *AppModel) syncExecutionModeFromSession() {
	if m.session == nil {
		return
	}
	mode := state.NormalizeExecutionMode(m.session.ExecutionMode)
	m.session.ExecutionMode = mode
}

func (m *AppModel) toggleExecutionMode() string {
	if m.session == nil {
		return "execution mode toggle unavailable: no session loaded"
	}

	current := m.currentExecutionMode()
	next := state.ExecutionModePlan
	if current == state.ExecutionModePlan {
		next = state.ExecutionModeFast
	}

	if m.db != nil {
		if err := m.db.SetSessionExecutionMode(context.Background(), m.session.ID, next); err != nil {
			return fmt.Sprintf("failed to persist execution mode: %v", err)
		}
	}
	m.session.ExecutionMode = next
	m.syncRoleAndModelDisplay()
	return fmt.Sprintf("Execution mode set to %s", strings.ToUpper(next))
}

func (m *AppModel) handleAgentEvent(event agent.AgentEvent) {
	if m == nil || m.chat == nil {
		return
	}
	roleLabel := formatAgentRoleLabel(event.Role)
	detail := strings.TrimSpace(event.Detail)
	if detail == "" {
		detail = "working"
	}
	streamKey := thinkingStreamKey(event.Role)

	if token, ok := extractThinkingToken(event.Payload); event.Type == agent.EventThinking && ok {
		if _, started := m.thinkingStarted[event.Role]; !started {
			m.thinkingStarted[event.Role] = time.Now()
		}
		m.thinkingBuffer[event.Role] += token
		streamText := strings.TrimSpace(m.thinkingBuffer[event.Role])
		if streamText == "" {
			streamText = detail
		}
		m.chat.SetSystemMessageByKey(streamKey, fmt.Sprintf("◌ %s is thinking...\n%s", roleLabel, streamText))
		return
	}

	switch event.Type {
	case agent.EventThinking:
		if _, started := m.thinkingStarted[event.Role]; !started {
			m.thinkingStarted[event.Role] = time.Now()
		}
		if strings.TrimSpace(m.thinkingBuffer[event.Role]) == "" {
			m.thinkingBuffer[event.Role] = detail
		}
		m.chat.SetSystemMessageByKey(streamKey, fmt.Sprintf("◌ %s thinking... %s", roleLabel, detail))
		return
	default:
		if startedAt, ok := m.thinkingStarted[event.Role]; ok || strings.TrimSpace(m.thinkingBuffer[event.Role]) != "" {
			if !ok {
				startedAt = time.Now()
			}
			dur := time.Since(startedAt)
			summary := summarizeThinkingText(m.thinkingBuffer[event.Role], detail)
			m.chat.SetSystemMessageByKey(streamKey, fmt.Sprintf("✓ %s thought for %s  › %s", roleLabel, formatThinkingDuration(dur), summary))
			m.chat.ReleaseMessageKey(streamKey)
			delete(m.thinkingStarted, event.Role)
			delete(m.thinkingBuffer, event.Role)
		}
	}

	if event.Type == agent.EventDone {
		if reply, ok := extractAgentReply(event.Payload); ok {
			m.chat.SetLoading(false, "")
			m.chat.AddMessage(roleLabel, reply)
			return
		}
	}

	line := renderAgentEventLine(roleLabel, event.Type, detail)
	if line != "" {
		m.chat.AddMessage("System", line)
	}
}

func formatAgentRoleLabel(role agent.Role) string {
	raw := strings.TrimSpace(string(role))
	if raw == "" {
		return "System"
	}
	raw = strings.ToLower(raw)
	return strings.ToUpper(raw[:1]) + raw[1:]
}

func formatThinkingDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	seconds := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%ds", seconds)
}

func renderAgentEventLine(roleLabel string, eventType agent.AgentEventType, detail string) string {
	switch eventType {
	case agent.EventPlanning:
		return fmt.Sprintf("● %s planning  %s", roleLabel, detail)
	case agent.EventReading:
		return fmt.Sprintf("● %s reading   %s", roleLabel, detail)
	case agent.EventWriting:
		return fmt.Sprintf("● %s writing   %s", roleLabel, detail)
	case agent.EventRunning:
		return fmt.Sprintf("● %s running   %s", roleLabel, detail)
	case agent.EventReviewing:
		return fmt.Sprintf("● %s reviewing %s", roleLabel, detail)
	case agent.EventWaiting:
		return fmt.Sprintf("● %s waiting   %s", roleLabel, detail)
	case agent.EventDone:
		return fmt.Sprintf("✓ %s %s", roleLabel, detail)
	case agent.EventError:
		return fmt.Sprintf("✗ %s %s", roleLabel, detail)
	default:
		return ""
	}
}

func thinkingStreamKey(role agent.Role) string {
	return "thinking:" + strings.ToLower(strings.TrimSpace(string(role)))
}

func extractThinkingToken(payload any) (string, bool) {
	values, ok := payload.(map[string]any)
	if !ok || values == nil {
		return "", false
	}
	raw, ok := values["token"]
	if !ok {
		return "", false
	}
	token, ok := raw.(string)
	if !ok {
		return "", false
	}
	return token, true
}

func summarizeThinkingText(thinkingText, fallback string) string {
	text := strings.TrimSpace(thinkingText)
	if text == "" {
		return strings.TrimSpace(fallback)
	}
	runes := []rune(text)
	if len(runes) > 60 {
		return strings.TrimSpace(string(runes[:60])) + "..."
	}
	return text
}

func extractAgentReply(payload any) (string, bool) {
	values, ok := payload.(map[string]any)
	if !ok || values == nil {
		return "", false
	}
	raw, ok := values["reply"]
	if !ok {
		return "", false
	}
	reply, ok := raw.(string)
	if !ok {
		return "", false
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", false
	}
	return reply, true
}

func normalizeSelectedModelID(providerKey, modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if strings.EqualFold(strings.TrimSpace(providerKey), "openrouter") {
		lower := strings.ToLower(modelID)
		if strings.HasSuffix(lower, " [free]") {
			modelID = strings.TrimSpace(modelID[:len(modelID)-len(" [free]")])
		}
	}
	return strings.TrimSpace(modelID)
}

// expandFileReferences scans input for @path/to/file patterns, reads the
// referenced files or directory listings, and injects their content as context.
func expandFileReferences(input string) string {
	words := strings.Fields(input)
	var refs []string
	var nonRefs []string
	for _, w := range words {
		if strings.HasPrefix(w, "@") && len(w) > 1 {
			refs = append(refs, w[1:])
		} else {
			nonRefs = append(nonRefs, w)
		}
	}
	if len(refs) == 0 {
		return input
	}

	var contextParts []string
	for _, ref := range refs {
		path := ref
		if !filepath.IsAbs(path) {
			cwd, err := os.Getwd()
			if err != nil {
				continue
			}
			path = filepath.Join(cwd, path)
		}

		info, err := os.Stat(path)
		if err != nil {
			contextParts = append(contextParts, fmt.Sprintf("--- @%s (not found) ---", ref))
			continue
		}

		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				contextParts = append(contextParts, fmt.Sprintf("--- @%s (cannot read directory) ---", ref))
				continue
			}
			var listing []string
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				listing = append(listing, name)
			}
			contextParts = append(contextParts, fmt.Sprintf("--- @%s (directory listing) ---\n%s", ref, strings.Join(listing, "\n")))
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				contextParts = append(contextParts, fmt.Sprintf("--- @%s (cannot read) ---", ref))
				continue
			}
			content := string(data)
			// Cap at ~32KB to avoid blowing up context.
			if len(content) > 32*1024 {
				content = content[:32*1024] + "\n... (truncated)"
			}
			contextParts = append(contextParts, fmt.Sprintf("--- @%s ---\n%s", ref, content))
		}
	}

	prompt := strings.Join(nonRefs, " ")
	if len(contextParts) > 0 {
		prompt = prompt + "\n\n" + strings.Join(contextParts, "\n\n")
	}
	return prompt
}
