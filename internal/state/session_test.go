package state

import (
	"context"
	"testing"
)

func TestSessionExecutionModePersists(t *testing.T) {
	t.Parallel()

	db, err := Connect(":memory:")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	session, err := db.CreateSession(context.Background(), ".", "orchestrated")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.ExecutionMode != ExecutionModeFast {
		t.Fatalf("expected default execution mode %q, got %q", ExecutionModeFast, session.ExecutionMode)
	}

	if err := db.SetSessionExecutionMode(context.Background(), session.ID, ExecutionModePlan); err != nil {
		t.Fatalf("set execution mode: %v", err)
	}

	reloaded, err := db.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if reloaded.ExecutionMode != ExecutionModePlan {
		t.Fatalf("expected execution mode %q, got %q", ExecutionModePlan, reloaded.ExecutionMode)
	}
}
