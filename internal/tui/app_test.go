package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yubzen/orchestra/internal/agent"
	"github.com/yubzen/orchestra/internal/providers"
	"github.com/yubzen/orchestra/internal/state"
)

func TestRefreshConnectOptionsShowsDiscoveryConnectionState(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)

	for _, option := range app.connectModal.Options {
		if !strings.Contains(option.Label, "(not connected)") {
			t.Fatalf("expected provider to default to not connected, got %q", option.Label)
		}
	}

	app.discoveredModels["openrouter"] = []string{"google/gemma-3-12b-it:free [free]"}
	app.refreshConnectOptions()

	foundConnected := false
	for _, option := range app.connectModal.Options {
		if strings.HasPrefix(option.Label, "OpenRouter") && strings.Contains(option.Label, "(connected)") {
			foundConnected = true
		}
	}
	if !foundConnected {
		t.Fatal("expected OpenRouter to be marked connected after model discovery")
	}
}

func TestNormalizeSelectedModelIDOpenRouterFreeSuffix(t *testing.T) {
	t.Parallel()

	if got := normalizeSelectedModelID("openrouter", "google/gemma-3-12b-it:free [free]"); got != "google/gemma-3-12b-it:free" {
		t.Fatalf("unexpected normalized OpenRouter model: %q", got)
	}
	if got := normalizeSelectedModelID("openai", "gpt-4o [free]"); got != "gpt-4o [free]" {
		t.Fatalf("unexpected non-OpenRouter normalization: %q", got)
	}
}

func TestOpenAPIKeyModalPrefillsStoredCredential(t *testing.T) {
	originalLoader := loadProviderCredential
	defer func() {
		loadProviderCredential = originalLoader
	}()

	loadProviderCredential = func(providerKey string) string {
		if providerKey == "openrouter" {
			return "sk-or-v1-test"
		}
		return ""
	}

	app := NewAppModel(nil, nil, nil, nil)
	provider, ok := app.providerByKey("openrouter")
	if !ok {
		t.Fatal("expected openrouter provider in catalog")
	}
	if len(provider.AuthModes) == 0 {
		t.Fatal("expected at least one auth mode")
	}

	app.openAPIKeyModal(provider, provider.AuthModes[0])
	if !app.apiKeyModal.Visible {
		t.Fatal("expected api key modal to be visible")
	}
	if got := app.apiKeyModal.Value; got != "sk-or-v1-test" {
		t.Fatalf("expected prefilled key, got %q", got)
	}
}

func TestToggleExecutionMode(t *testing.T) {
	t.Parallel()

	session := &state.Session{
		ID:            "s1",
		Mode:          "orchestrated",
		ExecutionMode: state.ExecutionModeFast,
	}
	app := NewAppModel(nil, nil, session, nil)

	msg := app.toggleExecutionMode()
	if !strings.Contains(msg, "PLAN") {
		t.Fatalf("expected toggle message to mention PLAN, got %q", msg)
	}
	if got := app.session.ExecutionMode; got != state.ExecutionModePlan {
		t.Fatalf("expected plan mode after toggle, got %q", got)
	}
}

func TestShiftTabTogglesAndPersistsExecutionMode(t *testing.T) {
	t.Parallel()

	db, err := state.Connect(":memory:")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	session, err := db.CreateSession(context.Background(), ".", "orchestrated")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	app := NewAppModel(nil, db, session, nil)
	if got := app.statusbar.ExecutionMode; got != "FAST" {
		t.Fatalf("expected initial FAST status mode, got %q", got)
	}

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := app.session.ExecutionMode; got != state.ExecutionModePlan {
		t.Fatalf("expected PLAN mode after shift+tab, got %q", got)
	}
	if got := app.statusbar.ExecutionMode; got != "PLAN" {
		t.Fatalf("expected status bar to update to PLAN, got %q", got)
	}
	stored, err := db.GetSessionExecutionMode(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("read persisted execution mode: %v", err)
	}
	if stored != state.ExecutionModePlan {
		t.Fatalf("expected persisted PLAN mode, got %q", stored)
	}

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := app.session.ExecutionMode; got != state.ExecutionModeFast {
		t.Fatalf("expected FAST mode after second shift+tab, got %q", got)
	}
	if got := app.statusbar.ExecutionMode; got != "FAST" {
		t.Fatalf("expected status bar to update to FAST, got %q", got)
	}
}

func TestHandleAgentEventThinkingCollapse(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.handleAgentEvent(agent.AgentEvent{
		Type:   agent.EventThinking,
		Role:   agent.RolePlanner,
		Detail: "planning task",
	})
	app.handleAgentEvent(agent.AgentEvent{
		Type:   agent.EventWriting,
		Role:   agent.RolePlanner,
		Detail: "writing .orchestra/plans/task_001.md",
	})

	if len(app.chat.messages) < 2 {
		t.Fatalf("expected thinking + collapsed/update messages, got %d", len(app.chat.messages))
	}
	last := app.chat.messages[len(app.chat.messages)-1]
	if !strings.Contains(last.Content, "writing") {
		t.Fatalf("expected event line to mention writing, got %q", last.Content)
	}
}

func TestCtrlCClearsTypedInputBeforeAnythingElse(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.textInput.SetValue("hello")
	app.agentRunActive = true
	app.agentRunCancel = func() {
		t.Fatal("run cancel should not be called when input has text")
	}

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("ctrl+c should not quit when clearing typed input")
	}
	if got := app.chat.GetInputValue(); got != "" {
		t.Fatalf("expected input cleared, got %q", got)
	}
}

func TestCtrlCCancelsActiveRunWhenInputIsEmpty(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.ClearInput()
	cancelCalled := false
	app.agentRunActive = true
	app.agentRunCancel = func() {
		cancelCalled = true
	}

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("ctrl+c should not quit while cancelling an active run")
	}
	if !cancelCalled {
		t.Fatal("expected active run cancel function to be called")
	}
	if !app.cancelRequested {
		t.Fatal("expected cancelRequested to be set")
	}
	if len(app.chat.messages) == 0 {
		t.Fatal("expected cancellation status message")
	}
	last := app.chat.messages[len(app.chat.messages)-1]
	if !strings.Contains(strings.ToLower(last.Content), "cancellation requested") {
		t.Fatalf("unexpected cancellation line: %q", last.Content)
	}
}

func TestCtrlCQuitsWhenIdleAndInputEmpty(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.ClearInput()

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected ctrl+c to quit when idle")
	}
}

func TestAgentRunResultCancellationShowsFriendlyMessage(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.pendingMessages = []string{"keep me?"}
	app.agentRunActive = true
	app.agentRunCancel = func() {}
	app.chat.SetLoading(true, "CODER")

	_, cmd := app.Update(AgentRunResultMsg{
		Role: "CODER",
		Err:  agent.ErrUserCancelled,
	})
	if cmd != nil {
		t.Fatal("expected no follow-up command after cancellation")
	}
	if app.agentRunActive {
		t.Fatal("expected run state to be cleared")
	}
	if app.agentRunCancel != nil {
		t.Fatal("expected cancel function cleared")
	}
	if app.chat.IsLoading() {
		t.Fatal("expected loading to stop after cancellation")
	}
	if len(app.pendingMessages) != 0 {
		t.Fatalf("expected pending messages to be cleared on cancellation, got %d", len(app.pendingMessages))
	}
	if len(app.chat.messages) == 0 {
		t.Fatal("expected cancellation message in chat")
	}
	last := app.chat.messages[len(app.chat.messages)-1]
	if !strings.Contains(strings.ToLower(last.Content), "cancelled") {
		t.Fatalf("unexpected cancellation message: %q", last.Content)
	}
}

func TestEnterWithoutPlannerModelShowsConnectError(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.textInput.SetValue("implement feature")

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no run command when no model is selected")
	}
	if app.chat.IsLoading() {
		t.Fatal("chat should not enter loading state without model selection")
	}
	if got := app.chat.GetInputValue(); got != "implement feature" {
		t.Fatalf("input should remain for retry, got %q", got)
	}
	if len(app.chat.messages) == 0 {
		t.Fatal("expected system guidance message")
	}
	last := app.chat.messages[len(app.chat.messages)-1]
	if !strings.Contains(strings.ToLower(last.Content), "planner model is not configured") {
		t.Fatalf("unexpected guidance message: %q", last.Content)
	}
}

func TestClassifyUserInputWhitespaceAwareCommandDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input       string
		wantTrimmed string
		wantCommand bool
	}{
		{input: "   /models", wantTrimmed: "/models", wantCommand: true},
		{input: "  /models  ", wantTrimmed: "/models", wantCommand: true},
		{input: "\t/connect", wantTrimmed: "/connect", wantCommand: true},
		{input: "   some text /models", wantTrimmed: "some text /models", wantCommand: false},
		{input: "   ", wantTrimmed: "", wantCommand: false},
	}

	for _, tt := range tests {
		trimmed, isCommand := classifyUserInput(tt.input)
		if trimmed != tt.wantTrimmed || isCommand != tt.wantCommand {
			t.Fatalf("classifyUserInput(%q) => (%q,%t), want (%q,%t)", tt.input, trimmed, isCommand, tt.wantTrimmed, tt.wantCommand)
		}
	}
}

func TestUnifiedChatInputAlwaysVisible(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.SetSize(100, 24)

	view := app.chat.View()
	if !strings.Contains(strings.ToLower(view), "type your message or @path/to/file") {
		t.Fatalf("expected input placeholder in unified chat, got %q", view)
	}
	if strings.Contains(strings.ToLower(view), "cannot be addressed directly") {
		t.Fatalf("unexpected internal-only hint in unified chat view: %q", view)
	}
}

func TestModelsCommandOpensRolePicker(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	_, _ = app.Update(OpenModelsModalMsg{})

	if app.rolesModal == nil || !app.rolesModal.Visible {
		t.Fatal("expected role picker to open for model configuration")
	}
	if app.modelsModal != nil && app.modelsModal.Visible {
		t.Fatal("models modal should not open before selecting a role")
	}
}

func TestRolePickerEnterOpensModelsModal(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	_, _ = app.Update(OpenModelsModalMsg{})
	if app.rolesModal == nil || !app.rolesModal.Visible {
		t.Fatal("expected role picker to be visible")
	}

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.rolesModal.Visible {
		t.Fatal("expected role picker to close after selection")
	}
	if app.modelsModal == nil || !app.modelsModal.Visible {
		t.Fatal("expected models modal to open after choosing role")
	}
}

func TestAgentEventUpdatesTeamStatusBarState(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.handleAgentEvent(agent.AgentEvent{Type: agent.EventThinking, Role: agent.RolePlanner, Detail: "planning"})
	view := app.statusbar.View()
	if !strings.Contains(strings.ToLower(view), "(thinking)") {
		t.Fatalf("expected team status to show thinking state, got %q", view)
	}
}

func TestInputHistoryNavigationPreservesDraft(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.appendInputHistory("first prompt")
	app.appendInputHistory("second prompt")
	app.chat.SetInputValue("draft in progress")

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := app.chat.GetInputValue(); got != "second prompt" {
		t.Fatalf("expected most recent history entry, got %q", got)
	}
	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := app.chat.GetInputValue(); got != "first prompt" {
		t.Fatalf("expected oldest history entry, got %q", got)
	}
	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := app.chat.GetInputValue(); got != "second prompt" {
		t.Fatalf("expected forward history entry, got %q", got)
	}
	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := app.chat.GetInputValue(); got != "draft in progress" {
		t.Fatalf("expected draft restored after navigating to end, got %q", got)
	}
}

func TestArrowKeysPreferSlashSuggestionsOverInputHistory(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.appendInputHistory("first prompt")
	app.appendInputHistory("second prompt")
	app.chat.SetInputValue("/")

	if !app.chat.HasVisibleSuggestions() {
		t.Fatal("expected slash suggestions to be visible for / input")
	}

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})

	selected, ok := app.chat.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected selected slash suggestion after down key")
	}
	if selected.Name != "/models" {
		t.Fatalf("expected slash suggestion to move to /models, got %q", selected.Name)
	}
	if got := app.chat.GetInputValue(); got != "/" {
		t.Fatalf("expected slash input to remain unchanged, got %q", got)
	}
	if app.historyBrowsing {
		t.Fatal("history navigation should not activate while slash suggestions are visible")
	}
}

func TestArrowKeysPreferRoleModalNavigation(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.appendInputHistory("first prompt")
	app.chat.SetInputValue("/")

	_, _ = app.Update(OpenModelsModalMsg{})
	if app.rolesModal == nil || !app.rolesModal.Visible {
		t.Fatal("expected role modal to be visible")
	}
	if app.rolesModal.Selected != 0 {
		t.Fatalf("expected initial role modal selection at 0, got %d", app.rolesModal.Selected)
	}

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	if app.rolesModal.Selected != 1 {
		t.Fatalf("expected role modal selection to move down to 1, got %d", app.rolesModal.Selected)
	}

	selected, ok := app.chat.SelectedSlashSuggestion()
	if !ok || selected.Name != "/roles" {
		t.Fatalf("slash suggestions should remain unchanged while role modal is open, got %#v", selected)
	}
	if app.historyBrowsing {
		t.Fatal("history navigation should not activate while role modal is open")
	}
}

func TestArrowKeysPreferModelModalNavigation(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.appendInputHistory("first prompt")
	app.chat.SetInputValue("/")
	app.modelsModal.SetModelOptions([]ModelOption{
		{ProviderName: "OpenAI", ProviderKey: "openai", ModelID: "gpt-4o"},
		{ProviderName: "OpenAI", ProviderKey: "openai", ModelID: "gpt-4.1"},
	})
	app.modelsModal.Open()

	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyDown})
	selected, ok := app.modelsModal.SelectedModel()
	if !ok {
		t.Fatal("expected selected model after navigating model modal")
	}
	if selected.ModelID != "gpt-4.1" {
		t.Fatalf("expected model modal selection to move to gpt-4.1, got %q", selected.ModelID)
	}

	slashSelected, ok := app.chat.SelectedSlashSuggestion()
	if !ok || slashSelected.Name != "/roles" {
		t.Fatalf("slash suggestions should remain unchanged while model modal is open, got %#v", slashSelected)
	}
	if app.historyBrowsing {
		t.Fatal("history navigation should not activate while model modal is open")
	}
}

func TestInputHistoryPersistsAcrossSessions(t *testing.T) {
	t.Parallel()

	db, err := state.Connect(":memory:")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	session, err := db.CreateSession(context.Background(), ".", "orchestrated")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	app := NewAppModel(nil, db, session, nil)
	app.appendInputHistory("first persisted prompt")
	app.appendInputHistory("second persisted prompt")

	reloaded := NewAppModel(nil, db, session, nil)
	if len(reloaded.inputHistory) < 2 {
		t.Fatalf("expected persisted input history, got %d entry(ies)", len(reloaded.inputHistory))
	}
	if got := reloaded.inputHistory[len(reloaded.inputHistory)-1]; got != "second persisted prompt" {
		t.Fatalf("expected latest persisted prompt, got %q", got)
	}
}

func TestDisplayRepoPathUsesTildeForHomePrefix(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("home directory unavailable in test environment")
	}

	full := filepath.Join(home, "work", "private", "orchestra")
	got := displayRepoPath(full)
	want := filepath.Join("~", "work", "private", "orchestra")
	if got != want {
		t.Fatalf("expected tilde path %q, got %q", want, got)
	}
}

func TestNewSessionLoadsLatestRoleSelections(t *testing.T) {
	t.Parallel()

	db, err := state.Connect(":memory:")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	first, err := db.CreateSession(context.Background(), ".", "orchestrated")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	if err := db.SaveSessionModelSelection(context.Background(), first.ID, "planner", "openai", "gpt-4o"); err != nil {
		t.Fatalf("save planner model: %v", err)
	}

	second, err := db.CreateSession(context.Background(), ".", "orchestrated")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	app := NewAppModel(nil, db, second, nil)
	if got := app.roleModels["PLANNER"]; got != "gpt-4o" {
		t.Fatalf("expected planner model restored from previous session, got %q", got)
	}
	if got := app.roleProviderKeys["PLANNER"]; got != "openai" {
		t.Fatalf("expected planner provider restored from previous session, got %q", got)
	}
}

func TestProviderRestoreSuccessConfiguresRole(t *testing.T) {
	t.Parallel()

	db, err := state.Connect(":memory:")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	session, err := db.CreateSession(context.Background(), ".", "orchestrated")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.SaveSessionModelSelection(context.Background(), session.ID, "planner", "openai", "gpt-4o"); err != nil {
		t.Fatalf("save model selection: %v", err)
	}

	origLoad := loadProviderCredential
	origDiscovery := newDiscoveryProvider
	defer func() {
		loadProviderCredential = origLoad
		newDiscoveryProvider = origDiscovery
	}()

	loadProviderCredential = func(providerKey string) string {
		if providerKey == "openai" {
			return "sk-openai"
		}
		return ""
	}
	newDiscoveryProvider = func(cfg providers.DiscoveryConfig) (providers.Provider, error) {
		return appStubProvider{
			name:   strings.TrimSpace(cfg.KeyName),
			models: []string{"gpt-4o"},
		}, nil
	}

	app := NewAppModel(nil, db, session, nil)
	cmds := app.startupProviderRestoreCmds()
	if len(cmds) == 0 {
		t.Fatal("expected startup restore command(s)")
	}
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		msg := cmd()
		if msg == nil {
			continue
		}
		updated, _ := app.Update(msg)
		app = updated.(*AppModel)
	}

	if !app.isProviderConnected("openai") {
		t.Fatal("expected openai provider connected after restore")
	}
	if !app.isRoleConfigured("PLANNER") {
		t.Fatal("expected planner role configured after restore")
	}
	view := app.statusbar.View()
	if !strings.Contains(view, "gpt-4o") {
		t.Fatalf("expected status bar to show restored planner model, got %q", view)
	}
}

func TestProviderRestoreFailureSetsHint(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	updated, _ := app.Update(ProviderRestoreResultMsg{
		ProviderName: "OpenAI",
		ProviderKey:  "openai",
		Err:          errors.New("unauthorized"),
	})
	app = updated.(*AppModel)

	view := app.statusbar.View()
	if !strings.Contains(strings.ToLower(view), "reconnect: openai") {
		t.Fatalf("expected reconnect hint in status bar, got %q", view)
	}
}

type appStubProvider struct {
	name   string
	models []string
}

func (s appStubProvider) Name() string {
	if strings.TrimSpace(s.name) == "" {
		return "stub"
	}
	return s.name
}

func (s appStubProvider) Complete(ctx context.Context, model string, messages []providers.Message, tools []providers.Tool, onToken providers.TokenCallback) (providers.CompletionResponse, error) {
	return providers.CompletionResponse{}, errors.New("not implemented in test stub")
}

func (s appStubProvider) ListModels(ctx context.Context) ([]string, error) {
	return append([]string(nil), s.models...), nil
}

func (s appStubProvider) Ping(ctx context.Context) error {
	return nil
}

func TestHandleAgentEventFileDiffRendersDiffMessage(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.SetSize(100, 24)
	app.chat.AddMessage("System", "ready")

	app.handleAgentEvent(agent.AgentEvent{
		Type: agent.EventFileDiff,
		Role: agent.RoleCoder,
		Payload: agent.FileDiffPayload{
			Path:     "hamid.ts",
			OldLines: []string{"const a = 1"},
			NewLines: []string{"const a = 2"},
		},
	})

	if len(app.chat.messages) == 0 {
		t.Fatal("expected diff message appended")
	}
	last := app.chat.messages[len(app.chat.messages)-1]
	if last.Diff == nil {
		t.Fatalf("expected last chat message to be a diff block, got %#v", last)
	}
	if last.Diff.Path != "hamid.ts" {
		t.Fatalf("expected diff path hamid.ts, got %q", last.Diff.Path)
	}
	view := app.chat.View()
	if !strings.Contains(view, "Diff  hamid.ts") {
		t.Fatalf("expected rendered diff block, got %q", view)
	}
}

func TestHandleAgentEventUpdatesActivityLine(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.SetSize(100, 24)
	app.chat.AddMessage("System", "ready")

	app.handleAgentEvent(agent.AgentEvent{
		Type:    agent.EventWriting,
		Role:    agent.RolePlanner,
		Detail:  "writing hamid.ts",
		Payload: map[string]any{"path": "hamid.ts"},
	})

	view := app.chat.View()
	if !strings.Contains(view, "Executing writeFile") {
		t.Fatalf("expected activity line in view, got %q", view)
	}
	if !strings.Contains(view, "hamid.ts") {
		t.Fatalf("expected activity target in view, got %q", view)
	}
}

func TestHandleAgentEventSkipsInternalOrchestraActivityLine(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.chat.SetSize(100, 24)
	app.chat.AddMessage("System", "ready")
	app.chat.SetActivity("Executing", "writeFile", "visible.ts", "visible")

	app.handleAgentEvent(agent.AgentEvent{
		Type:    agent.EventWriting,
		Role:    agent.RolePlanner,
		Detail:  "writing .orchestra/plans/task_001.lock",
		Payload: map[string]any{"path": ".orchestra/plans/task_001.lock"},
	})

	view := app.chat.View()
	if strings.Contains(view, "Executing writeFile  ·  .orchestra/") {
		t.Fatalf("expected internal .orchestra target to be hidden from activity line, got %q", view)
	}
	if !strings.Contains(view, "visible.ts") {
		t.Fatalf("expected previous visible activity to remain, got %q", view)
	}
}
