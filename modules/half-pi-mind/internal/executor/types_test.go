package executor

import (
	"testing"
)

func TestRegister(t *testing.T) {
	before := len(RegisteredTools())

	Register(Tool{
		Name:        "test_register_tool",
		Description: "test",
	})

	if len(RegisteredTools()) != before+1 {
		t.Errorf("after register: %d tools, want %d", len(RegisteredTools()), before+1)
	}
}

func TestFindTool(t *testing.T) {
	Register(Tool{Name: "find_me", Description: "test find"})
	Register(Tool{Name: "skip_me", Description: "test skip"})

	tool := FindTool("find_me")
	if tool == nil {
		t.Fatal("FindTool returned nil for registered tool")
	}
	if tool.Name != "find_me" {
		t.Errorf("name = %q, want find_me", tool.Name)
	}

	tool = FindTool("nonexistent")
	if tool != nil {
		t.Error("FindTool should return nil for unregistered tool")
	}
}

func TestFindToolReturnsActualElement(t *testing.T) {
	Register(Tool{Name: "pointer_test", Description: "test pointer"})

	tool := FindTool("pointer_test")
	tool.Description = "modified"

	tool2 := FindTool("pointer_test")
	if tool2.Description != "modified" {
		t.Error("FindTool should return pointer to actual registered element, not a copy")
	}
}

func TestRegisteredTools(t *testing.T) {
	tools := RegisteredTools()
	names := make(map[string]bool)
	for _, tt := range tools {
		if names[tt.Name] {
			t.Errorf("duplicate tool name: %q", tt.Name)
		}
		names[tt.Name] = true
		if tt.Name == "" {
			t.Error("tool with empty name found")
		}
	}
}

func TestSchemaParameters(t *testing.T) {
	tl := Tool{
		Name: "test_params",
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{
				{Name: "path", Type: "string", Description: "file path"},
				{Name: "limit", Type: "number", Description: "max lines"},
			},
			Required: []string{"path"},
		},
	}

	schema := tl.SchemaParameters()
	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties is not a map")
	}
	if len(props) != 2 {
		t.Errorf("props count = %d, want 2", len(props))
	}

	req, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required is not []string")
	}
	if len(req) != 1 || req[0] != "path" {
		t.Errorf("required = %v, want [path]", req)
	}
}

func TestSchemaParametersNoRequired(t *testing.T) {
	tl := Tool{
		Name: "no_req",
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{
				{Name: "opt", Type: "string", Description: "optional"},
			},
		},
	}

	schema := tl.SchemaParameters()
	if _, ok := schema["required"]; ok {
		t.Error("no required should not have required key in schema")
	}
}
