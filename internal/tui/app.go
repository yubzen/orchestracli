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
var newDiscoveryProvider = providers.NewDiscoveryProvider

type ModelsFetchedMsg struct {
	RequestID    int
	ProviderName string
	ProviderKey  string
	Models       []string
	Err          error
}

type ProviderRestoreResultMsg struct {
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
	inputHistory      []string
	inputHistoryIndex int
	inputDraft        string
	historyBrowsing   bool
	activeRole        int
	width             int
	height            int
	nextConnectReqID  int
	activeConnectReq  int
	pendingMessages   []string
	agentRunActive    bool
	agentRunCancel    context.CancelFunc
	cancelRequested   bool
	repoPath          string
	repoDisplayPath   string
	restoreIssues     map[string]string
}

func NewAppModel(cfg *config.Config, db *state.DB, session *state.Session, orc *agent.Orchestrator) *AppModel {
	chat := NewChatModel()
	sb := NewStatusBarModelWithConfig(cfg, session)
	repoPath := detectRepoPath(session)
	repoDisplayPath := displayRepoPath(repoPath)
	roleOrder := []string{"PLANNER", "CODER", "REVIEWER"}
	roleModels := map[string]string{
		"PLANNER":  "",
		"CODER":    "",
		"REVIEWER": "",
	}
	roleProviders := map[string]string{
		"PLANNER":  "",
		"CODER":    "",
		"REVIEWER": "",
	}

	modelsModal := NewModelsModal(nil)
	planModal := NewPlanReviewModal()
	roleModal := NewSelectModal("Configure Role", "up/down: navigate  enter: configure model  esc: close")
	roleModal.SetOptions([]SelectOption{
		{Label: "PLANNER", Enabled: true},
		{Label: "CODER", Enabled: true},
		{Label: "REVIEWER", Enabled: true},
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
		repoPath:          repoPath,
		repoDisplayPath:   repoDisplayPath,
		restoreIssues:     make(map[string]string),
	}
	model.statusbar.SetRepoPath(repoDisplayPath)
	model.loadPersistedSessionSelections()
	model.collectMissingCredentialIssues()
	model.updateRestoreHint()
	model.loadPersistedInputHistory()
	model.syncExecutionModeFromSession()
	model.refreshConnectOptions()
	model.refreshModelsModal("")
	model.syncRoleAndModelDisplay()
	model.resetInputHistoryNavigation()
	return model
}

func (m *AppModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, m.chat.Init(), m.statusbar.Init(), textinput.Blink)
	cmds = append(cmds, m.startupProviderRestoreCmds()...)
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
				return m.handleCtrlC()
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
				return m.handleCtrlC()
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

		if handled, cmd := m.dispatchUpDownKey(msg); handled {
			return m, cmd
		}

		if m.authMethodModal != nil && m.authMethodModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m.handleCtrlC()
			case "esc":
				m.authMethodModal.Close()
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
				return m.handleCtrlC()
			case "esc":
				m.connectModal.Close()
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
				return m.handleCtrlC()
			case "esc":
				m.rolesModal.Close()
				return m, nil
			case "enter":
				opt, ok := m.rolesModal.SelectedOption()
				if ok {
					m.setActiveRole(opt.Label)
				}
				m.rolesModal.Close()
				if m.modelsModal != nil {
					m.refreshModelsModal("")
					m.modelsModal.SelectByProviderAndModel(m.roleProviderKeys[m.currentRole()], m.currentRoleModel())
					m.modelsModal.Open()
				}
				return m, nil
			default:
				return m, nil
			}
		}

		if m.modelsModal != nil && m.modelsModal.Visible {
			switch msg.String() {
			case "ctrl+c":
				return m.handleCtrlC()
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
			return m.handleCtrlC()
		case "esc":
			if strings.TrimSpace(m.chat.GetInputValue()) == "" && m.agentRunActive {
				if !m.cancelRequested {
					m.cancelRequested = true
					m.chat.AddMessage("System", "● Cancellation requested. Stopping current run...")
					if m.statusbar != nil {
						m.statusbar.SetRoleState("PLANNER", "cancelling")
						m.statusbar.SetRoleState("CODER", "cancelling")
						m.statusbar.SetRoleState("REVIEWER", "cancelling")
					}
					if m.agentRunCancel != nil {
						m.agentRunCancel()
					}
				}
				return m, nil
			}
		case "p":
			if strings.TrimSpace(m.chat.GetInputValue()) == "" {
				modeMsg := m.toggleExecutionMode()
				return m, func() tea.Msg {
					return CommandResultMsg{Msg: modeMsg}
				}
			}
		case "shift+tab":
			modeMsg := m.toggleExecutionMode()
			return m, func() tea.Msg {
				return CommandResultMsg{Msg: modeMsg}
			}
		case "tab":
			if m.chat.ApplyTopSlashSuggestion() {
				return m, nil
			}
		}
		if shouldResetHistoryNavigation(msg) {
			m.resetInputHistoryNavigation()
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

	case AgentRunResultMsg:
		m.chat.SetLoading(false, "")
		m.clearAgentRunState()
		if m.statusbar != nil {
			m.statusbar.ResetTeamActivity()
		}

		shouldDrain := true
		if msg.Err != nil {
			if agent.IsUserCancelled(msg.Err) {
				shouldDrain = false
				m.pendingMessages = nil
				m.chat.AddMessage("System", "✓ Run cancelled by user. Input is ready.")
			} else {
				roleLabel := strings.ToLower(strings.TrimSpace(msg.Role))
				if roleLabel == "" {
					roleLabel = "agent"
				}
				m.chat.AddMessage("System", fmt.Sprintf("%s error: %v", roleLabel, msg.Err))
			}
		} else if strings.TrimSpace(msg.Reply) != "" {
			roleLabel := strings.ToUpper(strings.TrimSpace(msg.Role))
			if roleLabel == "" {
				roleLabel = "ORCHESTRATOR"
			}
			m.chat.AddMessage(roleLabel, msg.Reply)
		}

		if shouldDrain && len(m.pendingMessages) > 0 {
			next := m.pendingMessages[0]
			m.pendingMessages = m.pendingMessages[1:]
			runCtx := m.startAgentRunContext()
			m.chat.SetLoading(true, "ORCHESTRATOR")
			cmds = append(cmds, m.runAgentCmd(runCtx, next), loadingTickCmd())
		}

	case CommandResultMsg:
		if !m.agentRunActive {
			m.chat.SetLoading(false, "")
		}
		m.chat.AddMessage("System", msg.Msg)
		// Drain pending queue on command results too.
		if !m.agentRunActive && len(m.pendingMessages) > 0 {
			next := m.pendingMessages[0]
			m.pendingMessages = m.pendingMessages[1:]
			runCtx := m.startAgentRunContext()
			m.chat.SetLoading(true, "ORCHESTRATOR")
			cmds = append(cmds, m.runAgentCmd(runCtx, next), loadingTickCmd())
		}

	case LoadingTickMsg:
		if m.chat.IsLoading() {
			cmds = append(cmds, loadingTickCmd())
		}

	case OpenModelsModalMsg:
		m.openModelRolePicker()

	case OpenRolesModalMsg:
		m.openModelRolePicker()

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
			m.restoreIssues[strings.ToLower(strings.TrimSpace(msg.ProviderKey))] = fmt.Sprintf("%s needs reconnect", strings.TrimSpace(msg.ProviderName))
			m.updateRestoreHint()
			if m.apiKeyModal != nil && m.apiKeyModal.Visible {
				m.apiKeyModal.SetError(msg.Err.Error())
			}
			m.refreshConnectOptions()
			return m, func() tea.Msg {
				return CommandResultMsg{Msg: fmt.Sprintf("Connection failed for %s: %v", msg.ProviderName, msg.Err)}
			}
		}

		delete(m.restoreIssues, strings.ToLower(strings.TrimSpace(msg.ProviderKey)))
		m.updateRestoreHint()
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

	case ProviderRestoreResultMsg:
		if msg.Err != nil {
			m.setProviderConnected(msg.ProviderKey, false)
			m.restoreIssues[strings.ToLower(strings.TrimSpace(msg.ProviderKey))] = fmt.Sprintf("%s needs reconnect", strings.TrimSpace(msg.ProviderName))
			m.updateRestoreHint()
			m.refreshConnectOptions()
			m.syncRoleAndModelDisplay()
			return m, nil
		}

		delete(m.restoreIssues, strings.ToLower(strings.TrimSpace(msg.ProviderKey)))
		m.discoveredModels[msg.ProviderKey] = uniqueSortedModels(msg.Models)
		m.setProviderConnected(msg.ProviderKey, true)
		m.updateRestoreHint()
		m.refreshConnectOptions()
		m.refreshModelsModal(msg.ProviderKey)
		m.syncRoleAndModelDisplay()
		return m, nil
	}

	if msgKey, ok := msg.(tea.KeyMsg); ok && msgKey.String() == "enter" {
		if selected, ok := m.chat.SelectedSlashSuggestion(); ok {
			m.appendInputHistory(selected.Name)
			cmds = append(cmds, handleSlashCommand(selected.Name, m))
			m.chat.ClearInput()
			m.resetInputHistoryNavigation()
			return m, tea.Batch(cmds...)
		}

		trimmedInput, isCommand := classifyUserInput(m.chat.GetInputValue())
		if trimmedInput != "" {
			if isCommand {
				m.appendInputHistory(trimmedInput)
				cmds = append(cmds, handleSlashCommand(trimmedInput, m))
				m.chat.ClearInput()
				m.resetInputHistoryNavigation()
			} else {
				if !m.hasPlannerModelSelected() {
					m.chat.AddMessage("System", "Planner model is not configured. Run /connect, then /models and assign Planner before sending prompts.")
					return m, tea.Batch(cmds...)
				}
				// Expand @file references and show original in chat.
				expanded := expandFileReferences(trimmedInput)
				m.appendInputHistory(trimmedInput)
				m.chat.AddMessage("User", trimmedInput)
				m.chat.ClearInput()
				m.resetInputHistoryNavigation()

				if m.chat.IsLoading() {
					// Queue the message if the agent is still processing.
					m.pendingMessages = append(m.pendingMessages, expanded)
					m.chat.AddMessage("System", "⏳ Queued — will send after current response.")
				} else {
					runCtx := m.startAgentRunContext()
					m.chat.SetLoading(true, "ORCHESTRATOR")
					cmds = append(cmds, m.runAgentCmd(runCtx, expanded), loadingTickCmd())
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

func (m *AppModel) handleCtrlC() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.chat.GetInputValue()) != "" {
		m.chat.ClearInput()
		return m, nil
	}
	if m.agentRunActive && m.agentRunCancel != nil {
		if !m.cancelRequested {
			m.cancelRequested = true
			m.chat.AddMessage("System", "● Cancellation requested. Stopping current run...")
			if m.statusbar != nil {
				m.statusbar.SetRoleState("PLANNER", "cancelling")
				m.statusbar.SetRoleState("CODER", "cancelling")
				m.statusbar.SetRoleState("REVIEWER", "cancelling")
			}
			m.agentRunCancel()
		}
		return m, nil
	}
	return m, tea.Quit
}

func (m *AppModel) startAgentRunContext() context.Context {
	if m.agentRunCancel != nil {
		m.agentRunCancel()
	}
	runCtx, cancel := context.WithCancel(context.Background())
	m.agentRunActive = true
	m.agentRunCancel = cancel
	m.cancelRequested = false
	return runCtx
}

func (m *AppModel) clearAgentRunState() {
	m.agentRunActive = false
	m.agentRunCancel = nil
	m.cancelRequested = false
	if m.chat != nil {
		m.chat.ClearActivity()
	}
}

func (m *AppModel) runAgentCmd(runCtx context.Context, prompt string) tea.Cmd {
	return func() tea.Msg {
		if runCtx == nil {
			runCtx = context.Background()
		}
		if m.orc == nil {
			return AgentRunResultMsg{
				Role: "ORCHESTRATOR",
				Err:  fmt.Errorf("orchestrator is not initialized"),
			}
		}
		if !m.hasPlannerModelSelected() {
			return AgentRunResultMsg{
				Role: "ORCHESTRATOR",
				Err:  fmt.Errorf("planner model is not configured"),
			}
		}

		err := m.orc.Run(runCtx, prompt)
		if agent.IsUserCancelled(err) {
			err = agent.ErrUserCancelled
		}
		return AgentRunResultMsg{
			Role: "ORCHESTRATOR",
			Err:  err,
		}
	}
}

type AgentRunResultMsg struct {
	Reply string
	Role  string
	Err   error
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
		return "PLANNER"
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

	m.statusbar.ExecutionMode = strings.ToUpper(m.currentExecutionMode())
	for _, roleName := range []string{"PLANNER", "CODER", "REVIEWER"} {
		if m.isRoleConfigured(roleName) {
			m.statusbar.SetRoleModel(roleName, strings.TrimSpace(m.roleModels[roleName]))
			if !m.agentRunActive {
				m.statusbar.SetRoleState(roleName, "")
			}
		} else {
			m.statusbar.SetRoleModel(roleName, "")
			m.statusbar.SetRoleState(roleName, "unset")
		}
		m.applyRoleConfigToAgent(roleName)
	}
	if m.chat != nil {
		m.chat.SetInputAvailability(true, "")
	}
}

func (m *AppModel) openModelRolePicker() {
	if m.rolesModal == nil {
		return
	}
	m.rolesModal.Title = "Configure Role Model"
	m.rolesModal.Hint = "up/down: navigate  enter: choose role  esc: close"
	m.rolesModal.SetOptions([]SelectOption{
		{Label: "PLANNER", Enabled: true},
		{Label: "CODER", Enabled: true},
		{Label: "REVIEWER", Enabled: true},
	})
	if m.activeRole >= 0 && m.activeRole < len(m.rolesModal.Options) {
		m.rolesModal.Selected = m.activeRole
	}
	m.rolesModal.Open()
}

func (m *AppModel) applyRoleConfigToAgent(role string) {
	a := m.agentForRole(role)
	if a == nil {
		return
	}
	a.Model = strings.TrimSpace(m.roleModels[role])
	providerKey := strings.TrimSpace(m.roleProviderKeys[role])
	if providerKey == "" || !m.isRoleConfigured(role) {
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

func (m *AppModel) isModelDiscovered(providerKey, modelID string) bool {
	providerKey = strings.TrimSpace(providerKey)
	modelID = normalizeSelectedModelID(providerKey, modelID)
	if providerKey == "" || modelID == "" {
		return false
	}
	models := uniqueSortedModels(m.discoveredModels[providerKey])
	if len(models) == 0 {
		return false
	}
	for _, model := range models {
		if normalizeSelectedModelID(providerKey, model) == modelID {
			return true
		}
	}
	return false
}

func (m *AppModel) isRoleConfigured(role string) bool {
	role = strings.ToUpper(strings.TrimSpace(role))
	modelID := strings.TrimSpace(m.roleModels[role])
	providerKey := strings.TrimSpace(m.roleProviderKeys[role])
	if modelID == "" || providerKey == "" {
		return false
	}
	if !m.isProviderConnected(providerKey) {
		return false
	}
	return m.isModelDiscovered(providerKey, modelID)
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

	p, err := newDiscoveryProvider(discoveryCfg)
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

		p, err := newDiscoveryProvider(discoveryCfg)
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

func (m *AppModel) startupProviderRestoreCmds() []tea.Cmd {
	if m == nil {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(m.providerCatalog))
	for _, provider := range m.providerCatalog {
		credential := strings.TrimSpace(loadProviderCredential(provider.KeyName))
		if credential == "" {
			continue
		}
		cmds = append(cmds, m.restoreProviderCmd(provider, credential))
	}
	return cmds
}

func (m *AppModel) restoreProviderCmd(provider ProviderCatalog, credential string) tea.Cmd {
	providerKey := strings.TrimSpace(provider.KeyName)
	providerName := strings.TrimSpace(provider.Name)
	discoveryCfg := provider.Discovery
	if strings.TrimSpace(discoveryCfg.KeyName) == "" {
		discoveryCfg.KeyName = providerKey
	}
	credential = strings.TrimSpace(credential)
	return func() tea.Msg {
		if credential == "" {
			return ProviderRestoreResultMsg{
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          fmt.Errorf("no stored credential"),
			}
		}
		p, err := newDiscoveryProvider(discoveryCfg)
		if err != nil {
			return ProviderRestoreResultMsg{
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          err,
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()

		models, err := providers.DiscoverModels(ctx, p)
		if err != nil {
			return ProviderRestoreResultMsg{
				ProviderName: providerName,
				ProviderKey:  providerKey,
				Err:          err,
			}
		}
		return ProviderRestoreResultMsg{
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
	if len(selections) == 0 {
		latest, latestErr := m.db.GetLatestModelSelections(context.Background())
		if latestErr == nil && len(latest) > 0 {
			selections = latest
			for role, selection := range latest {
				_ = m.db.SaveSessionModelSelection(context.Background(), m.session.ID, role, selection.ProviderKey, selection.ModelID)
			}
		}
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
	}
}

func (m *AppModel) collectMissingCredentialIssues() {
	if m == nil {
		return
	}
	if m.restoreIssues == nil {
		m.restoreIssues = make(map[string]string)
	}
	for _, role := range []string{"PLANNER", "CODER", "REVIEWER"} {
		providerKey := strings.ToLower(strings.TrimSpace(m.roleProviderKeys[role]))
		modelID := strings.TrimSpace(m.roleModels[role])
		if providerKey == "" || modelID == "" {
			continue
		}
		if strings.TrimSpace(loadProviderCredential(providerKey)) != "" {
			continue
		}
		m.restoreIssues[providerKey] = fmt.Sprintf("%s needs reconnect", m.providerLabel(providerKey))
	}
}

func (m *AppModel) providerLabel(providerKey string) string {
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	for _, provider := range m.providerCatalog {
		if strings.EqualFold(strings.TrimSpace(provider.KeyName), providerKey) {
			return strings.TrimSpace(provider.Name)
		}
	}
	if providerKey == "" {
		return "provider"
	}
	return providerKey
}

func (m *AppModel) updateRestoreHint() {
	if m == nil || m.statusbar == nil {
		return
	}
	if len(m.restoreIssues) == 0 {
		m.statusbar.SetHint("")
		return
	}
	parts := make([]string, 0, len(m.restoreIssues))
	for providerKey := range m.restoreIssues {
		parts = append(parts, m.providerLabel(providerKey))
	}
	sort.Strings(parts)
	m.statusbar.SetHint("reconnect: " + strings.Join(parts, ", "))
}

func (m *AppModel) loadPersistedInputHistory() {
	if m.db == nil || m.session == nil {
		return
	}
	history, err := m.db.GetSessionInputHistory(context.Background(), m.session.ID)
	if err != nil {
		return
	}
	m.inputHistory = append([]string(nil), history...)
	if len(m.inputHistory) > state.DefaultInputHistoryLimit {
		m.inputHistory = m.inputHistory[len(m.inputHistory)-state.DefaultInputHistoryLimit:]
	}
}

func (m *AppModel) appendInputHistory(entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return
	}
	m.inputHistory = append(m.inputHistory, entry)
	if len(m.inputHistory) > state.DefaultInputHistoryLimit {
		m.inputHistory = m.inputHistory[len(m.inputHistory)-state.DefaultInputHistoryLimit:]
	}
	m.resetInputHistoryNavigation()

	if m.db != nil && m.session != nil {
		if err := m.db.AppendSessionInputHistory(context.Background(), m.session.ID, entry); err != nil && m.chat != nil {
			m.chat.AddMessage("System", fmt.Sprintf("warning: failed to persist input history: %v", err))
		}
	}
}

func (m *AppModel) resetInputHistoryNavigation() {
	m.inputHistoryIndex = len(m.inputHistory)
	m.inputDraft = ""
	m.historyBrowsing = false
}

func (m *AppModel) navigateInputHistory(delta int) bool {
	if m == nil || m.chat == nil {
		return false
	}
	if len(m.inputHistory) == 0 || delta == 0 {
		return false
	}

	if !m.historyBrowsing {
		m.inputDraft = m.chat.GetInputValue()
		m.inputHistoryIndex = len(m.inputHistory)
		m.historyBrowsing = true
	}

	switch {
	case delta < 0:
		if m.inputHistoryIndex > 0 {
			m.inputHistoryIndex--
		}
		m.chat.SetInputValue(m.inputHistory[m.inputHistoryIndex])
		return true
	case delta > 0:
		if m.inputHistoryIndex < len(m.inputHistory)-1 {
			m.inputHistoryIndex++
			m.chat.SetInputValue(m.inputHistory[m.inputHistoryIndex])
			return true
		}
		m.inputHistoryIndex = len(m.inputHistory)
		m.chat.SetInputValue(m.inputDraft)
		m.historyBrowsing = false
		return true
	default:
		return false
	}
}

func (m *AppModel) dispatchUpDownKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	delta, ok := upDownDelta(msg)
	if !ok {
		return false, nil
	}

	// Modal navigation always has highest priority.
	if m.modelsModal != nil && m.modelsModal.Visible {
		return true, m.modelsModal.Update(msg)
	}
	if m.rolesModal != nil && m.rolesModal.Visible {
		m.rolesModal.Move(delta)
		return true, nil
	}
	if m.connectModal != nil && m.connectModal.Visible {
		m.connectModal.Move(delta)
		return true, nil
	}
	if m.authMethodModal != nil && m.authMethodModal.Visible {
		m.authMethodModal.Move(delta)
		return true, nil
	}
	if (m.planModal != nil && m.planModal.Visible) || (m.apiKeyModal != nil && m.apiKeyModal.Visible) {
		return true, nil
	}

	// Command suggestions win over input history.
	if m.chat != nil && m.chat.HasVisibleSuggestions() {
		m.chat.MoveSlashSelection(delta)
		return true, nil
	}

	// Lowest priority: input history owns up/down in normal chat mode.
	m.navigateInputHistory(delta)
	return true, nil
}

func upDownDelta(msg tea.KeyMsg) (int, bool) {
	switch msg.String() {
	case "up":
		return -1, true
	case "down":
		return 1, true
	default:
		return 0, false
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

func (m *AppModel) hasPlannerModelSelected() bool {
	return m.isRoleConfigured("PLANNER")
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
	m.updateActivityLine(event)
	if m.statusbar != nil {
		roleName := strings.ToUpper(strings.TrimSpace(string(event.Role)))
		if roleName != "" {
			m.statusbar.SetRoleState(roleName, statusStateForEvent(event.Type))
		}
	}

	if event.Type == agent.EventFileDiff {
		if diff, ok := extractFileDiffPayload(event.Payload); ok {
			m.chat.AddFileDiff(diff.Path, diff.OldLines, diff.NewLines)
			return
		}
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

func statusStateForEvent(eventType agent.AgentEventType) string {
	switch eventType {
	case agent.EventThinking:
		return "thinking"
	case agent.EventPlanning:
		return "planning"
	case agent.EventReading:
		return "reading"
	case agent.EventWriting:
		return "writing"
	case agent.EventFileDiff:
		return "writing"
	case agent.EventRunning:
		return "running"
	case agent.EventReviewing:
		return "reviewing"
	case agent.EventWaiting:
		return "waiting"
	case agent.EventDone:
		return "idle"
	case agent.EventError:
		return "error"
	default:
		return "working"
	}
}

func (m *AppModel) updateActivityLine(event agent.AgentEvent) {
	if m == nil || m.chat == nil {
		return
	}
	phase, action, target, key, ok := mapEventToActivity(event)
	if !ok {
		return
	}
	if isInternalOrchestraPath(target) || isInternalOrchestraPath(strings.TrimSpace(event.Detail)) {
		return
	}
	m.chat.SetActivity(phase, action, target, key)
}

func mapEventToActivity(event agent.AgentEvent) (phase, action, target, key string, ok bool) {
	role := strings.ToUpper(strings.TrimSpace(string(event.Role)))
	if role == "" {
		role = "AGENT"
	}
	detail := strings.TrimSpace(event.Detail)
	switch event.Type {
	case agent.EventPlanning:
		phase = "Planning"
		action = "runTask"
		target = detail
	case agent.EventThinking:
		phase = "Thinking"
		action = "reasoning"
		target = detail
	case agent.EventReading:
		phase = "Executing"
		action = "readFile"
		target = eventTargetFromPayload(event, "path", detail)
	case agent.EventWriting:
		phase = "Executing"
		action = "writeFile"
		target = eventTargetFromPayload(event, "path", detail)
	case agent.EventRunning:
		phase = "Executing"
		action = "runTask"
		target = detail
		if command, ok := eventPayloadString(event.Payload, "command"); ok {
			action = "runCommand"
			target = command
		}
	case agent.EventReviewing:
		phase = "Reviewing"
		action = "runReview"
		target = detail
	case agent.EventWaiting:
		phase = "Waiting"
		action = "awaitingInput"
		target = detail
	case agent.EventFileDiff:
		phase = "Executing"
		action = "writeFile"
		target = eventTargetFromPayload(event, "path", detail)
	default:
		return "", "", "", "", false
	}
	phase = strings.TrimSpace(phase)
	action = strings.TrimSpace(action)
	target = strings.TrimSpace(target)
	key = fmt.Sprintf("%s|%s|%s|%s", role, phase, action, target)
	return phase, action, target, key, true
}

func eventTargetFromPayload(event agent.AgentEvent, payloadKey, fallback string) string {
	if value, ok := eventPayloadString(event.Payload, payloadKey); ok {
		return value
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return ""
	}
	parts := strings.Fields(fallback)
	if len(parts) > 1 {
		return strings.Join(parts[1:], " ")
	}
	return fallback
}

func eventPayloadString(payload any, key string) (string, bool) {
	values, ok := payload.(map[string]any)
	if !ok || values == nil {
		return "", false
	}
	raw, ok := values[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func extractFileDiffPayload(payload any) (agent.FileDiffPayload, bool) {
	switch typed := payload.(type) {
	case agent.FileDiffPayload:
		if strings.TrimSpace(typed.Path) == "" {
			return agent.FileDiffPayload{}, false
		}
		return typed, true
	case *agent.FileDiffPayload:
		if typed == nil || strings.TrimSpace(typed.Path) == "" {
			return agent.FileDiffPayload{}, false
		}
		return *typed, true
	case map[string]any:
		path, _ := typed["path"].(string)
		if strings.TrimSpace(path) == "" {
			return agent.FileDiffPayload{}, false
		}
		oldLines := anySliceToStringSlice(typed["old_lines"])
		newLines := anySliceToStringSlice(typed["new_lines"])
		return agent.FileDiffPayload{
			Path:     strings.TrimSpace(path),
			OldLines: oldLines,
			NewLines: newLines,
		}, true
	default:
		return agent.FileDiffPayload{}, false
	}
}

func anySliceToStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
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

func detectRepoPath(session *state.Session) string {
	if cwd, err := os.Getwd(); err == nil {
		cwd = strings.TrimSpace(cwd)
		if cwd != "" {
			if abs, absErr := filepath.Abs(cwd); absErr == nil {
				return abs
			}
			return cwd
		}
	}
	if session != nil {
		workingDir := strings.TrimSpace(session.WorkingDir)
		if workingDir != "" {
			if abs, err := filepath.Abs(workingDir); err == nil {
				return abs
			}
			return workingDir
		}
	}
	return "."
}

func displayRepoPath(absPath string) string {
	absPath = strings.TrimSpace(absPath)
	if absPath == "" {
		return "."
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return absPath
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return absPath
	}
	home = filepath.Clean(home)
	path := filepath.Clean(absPath)
	if path == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix)
	}
	return absPath
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

func classifyUserInput(raw string) (trimmed string, isCommand bool) {
	trimmed = strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	return trimmed, strings.HasPrefix(trimmed, "/")
}

func shouldResetHistoryNavigation(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "up", "down", "enter":
		return false
	}
	switch msg.Type {
	case tea.KeyRunes, tea.KeyBackspace, tea.KeyDelete:
		return true
	default:
		return false
	}
}

func isInternalOrchestraPath(text string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(text)), ".orchestra/")
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
