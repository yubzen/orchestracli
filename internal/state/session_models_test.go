package state

import (
	"context"
	"testing"
)

func TestSessionModelSelectionsPersistAndUpdate(t *testing.T) {
	db, err := Connect(":memory:")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	session, err := db.CreateSession(context.Background(), ".", "solo")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := db.SaveSessionModelSelection(context.Background(), session.ID, "coder", "openai", "gpt-4o"); err != nil {
		t.Fatalf("save model selection: %v", err)
	}
	if err := db.SaveSessionModelSelection(context.Background(), session.ID, "coder", "anthropic", "claude-3-5-sonnet"); err != nil {
		t.Fatalf("update model selection: %v", err)
	}

	selections, err := db.GetSessionModelSelections(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get model selections: %v", err)
	}
	entry, ok := selections["CODER"]
	if !ok {
		t.Fatalf("expected CODER selection, got %#v", selections)
	}
	if entry.ProviderKey != "anthropic" || entry.ModelID != "claude-3-5-sonnet" {
		t.Fatalf("unexpected selection value: %#v", entry)
	}
}
