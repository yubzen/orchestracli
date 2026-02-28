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

func TestGetLatestModelSelectionsAcrossSessions(t *testing.T) {
	db, err := Connect(":memory:")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	first, err := db.CreateSession(context.Background(), ".", "solo")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := db.CreateSession(context.Background(), ".", "solo")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	if err := db.SaveSessionModelSelection(context.Background(), first.ID, "planner", "openai", "gpt-4o"); err != nil {
		t.Fatalf("save first planner selection: %v", err)
	}
	if err := db.SaveSessionModelSelection(context.Background(), second.ID, "planner", "anthropic", "claude-3-5-sonnet"); err != nil {
		t.Fatalf("save second planner selection: %v", err)
	}
	if err := db.SaveSessionModelSelection(context.Background(), first.ID, "coder", "openrouter", "google/gemma-3-12b-it:free"); err != nil {
		t.Fatalf("save coder selection: %v", err)
	}

	latest, err := db.GetLatestModelSelections(context.Background())
	if err != nil {
		t.Fatalf("get latest selections: %v", err)
	}

	planner := latest["PLANNER"]
	if planner.ProviderKey != "anthropic" || planner.ModelID != "claude-3-5-sonnet" {
		t.Fatalf("unexpected latest planner selection: %#v", planner)
	}
	coder := latest["CODER"]
	if coder.ProviderKey != "openrouter" || coder.ModelID != "google/gemma-3-12b-it:free" {
		t.Fatalf("unexpected latest coder selection: %#v", coder)
	}
}
