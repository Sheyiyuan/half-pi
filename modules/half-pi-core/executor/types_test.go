package executor

import (
	"context"
	"encoding/json"
	"strings"
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

	tool, ok := FindTool("find_me")
	if !ok {
		t.Fatal("FindTool returned nil for registered tool")
	}
	if tool.Name != "find_me" {
		t.Errorf("name = %q, want find_me", tool.Name)
	}

	_, ok = FindTool("nonexistent")
	if ok {
		t.Error("FindTool should return false for unregistered tool")
	}
}

func TestFindToolReturnsCopy(t *testing.T) {
	Register(Tool{Name: "pointer_test", Description: "test pointer"})

	tool, ok := FindTool("pointer_test")
	if !ok {
		t.Fatal("FindTool returned false")
	}
	tool.Description = "modified"

	tool2, ok := FindTool("pointer_test")
	if !ok {
		t.Fatal("FindTool second lookup returned false")
	}
	if tool2.Description == "modified" {
		t.Error("FindTool should return a copy, not mutable registry state")
	}
}

func TestRegisteredToolsReturnsCopy(t *testing.T) {
	Register(Tool{Name: "copy_test", Description: "original"})

	tools := RegisteredTools()
	for i := range tools {
		if tools[i].Name == "copy_test" {
			tools[i].Description = "modified"
		}
	}

	tool, ok := FindTool("copy_test")
	if !ok {
		t.Fatal("copy_test not found")
	}
	if tool.Description == "modified" {
		t.Fatal("RegisteredTools should return a copy")
	}
}

func TestRegisterRejectsInvalidTool(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("Register should panic on empty name")
			}
		}()
		Register(Tool{})
	})

	t.Run("duplicate name", func(t *testing.T) {
		name := "duplicate_test"
		Register(Tool{Name: name})
		defer func() {
			if recover() == nil {
				t.Fatal("Register should panic on duplicate name")
			}
		}()
		Register(Tool{Name: name})
	})
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

func TestSchemaParametersNil(t *testing.T) {
	tool := Tool{Name: "no_params"}
	schema := tool.SchemaParameters()
	if schema["type"] != "object" {
		t.Fatalf("schema = %+v", schema)
	}
}

func TestRunnerExecutesAllowedTool(t *testing.T) {
	name := "runner_allowed"
	Register(Tool{
		Name: name,
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			return &ToolResult{Success: true, Output: "ok"}
		},
	})

	result := NewRunner(ExecutionPolicy{}).ExecuteTool(context.Background(), name, nil)
	if !result.Success || result.Output != "ok" {
		t.Fatalf("result = %+v, want ok", result)
	}
}

func TestProgressContextIsOptional(t *testing.T) {
	ReportProgress(context.Background(), Progress{Kind: "stdout", Data: "ignored"})
	var got Progress
	ctx := WithProgress(context.Background(), func(progress Progress) { got = progress })
	ReportProgress(ctx, Progress{Kind: "stderr", Data: "message"})
	if got.Kind != "stderr" || got.Data != "message" {
		t.Fatalf("progress = %+v", got)
	}
}

func TestRunnerRejectsDeniedTool(t *testing.T) {
	name := "runner_denied"
	Register(Tool{
		Name: name,
		Check: func(args json.RawMessage) (Decision, string) {
			return DecisionDeny, "nope"
		},
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			t.Fatal("denied tool should not execute")
			return nil
		},
	})

	result := NewRunner(ExecutionPolicy{}).ExecuteTool(context.Background(), name, nil)
	if !result.Success || !strings.Contains(result.Output, "nope") {
		t.Fatalf("result = %+v, want deny output", result)
	}
}

func TestRunnerConfirmPolicy(t *testing.T) {
	name := "runner_confirm"
	executed := false
	Register(Tool{
		Name:           name,
		DefaultConfirm: true,
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			executed = true
			return &ToolResult{Success: true, Output: "confirmed"}
		},
	})

	denied := NewRunner(ExecutionPolicy{}).ExecuteTool(context.Background(), name, nil)
	if executed || !strings.Contains(denied.Output, "拒绝") {
		t.Fatalf("denied result = %+v executed=%v", denied, executed)
	}

	allowed := NewRunner(ExecutionPolicy{
		Confirm: func(tool Tool, args json.RawMessage, reason string) ConfirmDecision {
			return ConfirmAllow
		},
	}).ExecuteTool(context.Background(), name, nil)
	if !executed || !allowed.Success || allowed.Output != "confirmed" {
		t.Fatalf("allowed result = %+v executed=%v", allowed, executed)
	}
}
