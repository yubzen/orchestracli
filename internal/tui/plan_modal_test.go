package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPlanReviewModalApprove(t *testing.T) {
	t.Parallel()

	modal := NewPlanReviewModal()
	modal.SetSize(120, 40)
	modal.Open("plan-1", "tasks:\n- id: t1\n  description: test")

	action, _ := modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !action.DecisionMade || !action.Approved {
		t.Fatalf("expected approve decision, got %#v", action)
	}
	if action.PlanID != "plan-1" {
		t.Fatalf("expected plan id plan-1, got %q", action.PlanID)
	}
}

func TestPlanReviewModalEditAndApprove(t *testing.T) {
	t.Parallel()

	modal := NewPlanReviewModal()
	modal.SetSize(120, 40)
	modal.Open("plan-2", "tasks:\n- id: t1\n  description: old")

	_, _ = modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	action, _ := modal.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !action.DecisionMade || !action.Approved {
		t.Fatalf("expected edited approve decision, got %#v", action)
	}
	if action.PlanID != "plan-2" {
		t.Fatalf("expected plan id plan-2, got %q", action.PlanID)
	}
}
