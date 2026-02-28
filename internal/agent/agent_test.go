package agent

import (
	"encoding/json"
	"testing"
)

func TestParseToolArgumentsSupportsJSONStringPayload(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(`{"path":"hamid.ts","content":"hello"}`)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	params, err := parseToolArguments(json.RawMessage(encoded))
	if err != nil {
		t.Fatalf("parseToolArguments: %v", err)
	}
	if got := params["path"]; got != "hamid.ts" {
		t.Fatalf("expected path hamid.ts, got %#v", got)
	}
	if got := params["content"]; got != "hello" {
		t.Fatalf("expected content hello, got %#v", got)
	}
}
