package llm

import "testing"

func TestAnthropicBuildRequestGroupsConsecutiveToolResults(t *testing.T) {
	p := &anthropicProvider{}
	req := &LLMRequest{
		Messages: []Message{
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "call_1", Name: "read_file", Args: `{"path":"a.txt"}`},
					{ID: "call_2", Name: "read_file", Args: `{"path":"b.txt"}`},
				},
			},
			{Role: RoleTool, ToolID: "call_1", Content: "a"},
			{Role: RoleTool, ToolID: "call_2", Content: "b"},
		},
	}

	body := p.buildRequestBody(req)
	if len(body.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(body.Messages))
	}
	if body.Messages[1].Role != "user" {
		t.Fatalf("tool results role = %q, want user", body.Messages[1].Role)
	}
	blocks := body.Messages[1].Content
	if len(blocks) != 2 {
		t.Fatalf("tool result blocks len = %d, want 2", len(blocks))
	}
	if blocks[0].Type != "tool_result" || blocks[0].ToolUseID != "call_1" {
		t.Fatalf("first block = %+v, want call_1 tool_result", blocks[0])
	}
	if blocks[1].Type != "tool_result" || blocks[1].ToolUseID != "call_2" {
		t.Fatalf("second block = %+v, want call_2 tool_result", blocks[1])
	}
}

func TestAnthropicParseResponseConcatenatesTextBlocks(t *testing.T) {
	p := &anthropicProvider{}
	resp, err := p.parseResponse([]byte(`{
		"content": [
			{"type": "text", "text": "hello "},
			{"type": "text", "text": "world"}
		]
	}`))
	if err != nil {
		t.Fatalf("parseResponse failed: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("content = %q, want hello world", resp.Content)
	}
}
