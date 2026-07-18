package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptedProviderFromFileChecksRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.json")
	fixture := `{
  "version": 1,
  "steps": [
    {
      "expect": {"last_role": "user", "last_content": "hello"},
      "response": {"tool_calls": [{"id": "call-1", "name": "list_hands", "args": {}}]}
    },
    {
      "expect": {"last_role": "tool", "last_tool_id": "call-1"},
      "response": {"content": "done"}
    }
  ]
}`
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewScriptedProviderFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Chat(context.Background(), &LLMRequest{Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].Args != "{}" {
		t.Fatalf("first response = %+v, %v", first, err)
	}
	second, err := provider.Chat(context.Background(), &LLMRequest{Messages: []Message{{Role: RoleTool, ToolID: "call-1"}}})
	if err != nil || second.Content != "done" {
		t.Fatalf("second response = %+v, %v", second, err)
	}
}

func TestScriptedProviderFromFileRejectsInvalidFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{"unknown field", `{"version":1,"steps":[{"response":{"content":"ok"},"secret":true}]}`, "unknown field"},
		{"version", `{"version":2,"steps":[{"response":{"content":"ok"}}]}`, "version"},
		{"empty", `{"version":1,"steps":[]}`, "steps"},
		{"empty response", `{"version":1,"steps":[{"response":{}}]}`, "response"},
		{"invalid args", `{"version":1,"steps":[{"response":{"tool_calls":[{"id":"call","name":"tool","args":[]}]}}]}`, "JSON object"},
		{"duplicate call", `{"version":1,"steps":[{"response":{"tool_calls":[{"id":"call","name":"one","args":{}},{"id":"call","name":"two","args":{}}]}}]}`, "duplicate"},
		{"invalid expectation", `{"version":1,"steps":[{"expect":{"last_content":"hello"},"response":{"content":"ok"}}]}`, "last_role"},
		{"trailing value", `{"version":1,"steps":[{"response":{"content":"ok"}}]} {}`, "multiple JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "script.json")
			if err := os.WriteFile(path, []byte(test.fixture), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := NewScriptedProviderFromFile(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestScriptedProviderFromFileDoesNotExposeExpectedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, []byte(`{
  "version":1,
  "steps":[{"expect":{"last_role":"user","last_content":"fixture-secret"},"response":{"content":"ok"}}]
}`), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewScriptedProviderFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &LLMRequest{Messages: []Message{{Role: RoleUser, Content: "other"}}})
	if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "other") {
		t.Fatalf("request check error leaked content: %v", err)
	}
}
