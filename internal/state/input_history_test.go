package state

import (
	"context"
	"fmt"
	"testing"
)

func TestSessionInputHistoryPersistsAndCaps(t *testing.T) {
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

	for i := 1; i <= DefaultInputHistoryLimit+5; i++ {
		if err := db.AppendSessionInputHistory(context.Background(), session.ID, fmt.Sprintf("prompt-%03d", i)); err != nil {
			t.Fatalf("append history %d: %v", i, err)
		}
	}

	history, err := db.GetSessionInputHistory(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) != DefaultInputHistoryLimit {
		t.Fatalf("expected %d entries, got %d", DefaultInputHistoryLimit, len(history))
	}
	if history[0] != "prompt-006" {
		t.Fatalf("expected oldest retained entry prompt-006, got %q", history[0])
	}
	if history[len(history)-1] != "prompt-105" {
		t.Fatalf("expected newest entry prompt-105, got %q", history[len(history)-1])
	}
}
