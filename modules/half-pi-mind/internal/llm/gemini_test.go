package llm

import "testing"

func TestGeminiBuildRequestUsesUserRoleAndFunctionID(t *testing.T) {
	p := &geminiProvider{}
	req := &LLMRequest{
		Messages: []Message{
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "call_123", Name: "read_file", Args: `{"path":"README.md"}`},
				},
			},
			{Role: RoleTool, ToolID: "call_123", Content: "content"},
		},
	}

	body := p.buildRequestBody(req)
	if len(body.Contents) != 2 {
		t.Fatalf("contents len = %d, want 2", len(body.Contents))
	}
	if body.Contents[1].Role != "user" {
		t.Fatalf("tool result role = %q, want user", body.Contents[1].Role)
	}
	resp := body.Contents[1].Parts[0].FunctionResponse
	if resp == nil {
		t.Fatal("tool result part has no functionResponse")
	}
	if resp.ID != "call_123" {
		t.Fatalf("functionResponse id = %q, want call_123", resp.ID)
	}
	if resp.Name != "read_file" {
		t.Fatalf("functionResponse name = %q, want read_file", resp.Name)
	}
}

func TestGeminiSyntheticToolIDIsNotSentBack(t *testing.T) {
	p := &geminiProvider{}
	req := &LLMRequest{
		Messages: []Message{
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "gemini_noid_0_read_file", Name: "read_file", Args: `{"path":"README.md"}`},
				},
			},
			{Role: RoleTool, ToolID: "gemini_noid_0_read_file", Content: "content"},
		},
	}

	body := p.buildRequestBody(req)
	resp := body.Contents[1].Parts[0].FunctionResponse
	if resp.ID != "" {
		t.Fatalf("synthetic id was sent back as %q", resp.ID)
	}
	if resp.Name != "read_file" {
		t.Fatalf("functionResponse name = %q, want read_file", resp.Name)
	}
}

func TestGeminiParseResponsePreservesFunctionCallID(t *testing.T) {
	p := &geminiProvider{}
	resp, err := p.parseResponse([]byte(`{
		"candidates": [{
			"content": {
				"parts": [{
					"functionCall": {
						"id": "call_123",
						"name": "read_file",
						"args": {"path": "README.md"}
					}
				}]
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("parseResponse failed: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_123" {
		t.Fatalf("tool call id = %q, want call_123", resp.ToolCalls[0].ID)
	}
}
