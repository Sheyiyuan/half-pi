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

func TestRegisteredToolsSnapshotIsImmutableAndRevisioned(t *testing.T) {
	before := RegisteredToolsSnapshot()
	Register(Tool{
		Name: "snapshot_tool", Description: "snapshot",
		Parameters: &ObjectSchema{Properties: []PropertySchema{{Name: "path", Type: "string"}}, Required: []string{"path"}},
	})
	after := RegisteredToolsSnapshot()
	if after.Revision != before.Revision+1 || after.Digest == before.Digest {
		t.Fatalf("snapshot transition = before=%+v after=%+v", before, after)
	}
	for i := range after.Tools {
		if after.Tools[i].Name == "snapshot_tool" {
			after.Tools[i].Parameters.Properties[0].Name = "mutated"
		}
	}
	again := RegisteredToolsSnapshot()
	tool, ok := FindTool("snapshot_tool")
	if !ok || tool.Parameters.Properties[0].Name != "path" || again.Digest != after.Digest {
		t.Fatalf("tool snapshot was not immutable")
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

func TestProgressContextIsOptional(t *testing.T) {
	ReportProgress(context.Background(), Progress{Kind: "stdout", Data: "ignored"})
	var got Progress
	ctx := WithProgress(context.Background(), func(progress Progress) { got = progress })
	ReportProgress(ctx, Progress{Kind: "stderr", Data: "message"})
	if got.Kind != "stderr" || got.Data != "message" {
		t.Fatalf("progress = %+v", got)
	}
}

func TestProjectReviewArgsRedactsAndEscalates(t *testing.T) {
	tool := Tool{Name: "review", Parameters: &ObjectSchema{Properties: []PropertySchema{
		{Name: "path", Type: "string"},
		{Name: "content", Type: "string", Review: ReviewRedact},
		{Name: "token", Type: "string", Review: ReviewRequireUser},
	}}}
	projected, complete, err := ProjectReviewArgs(tool, json.RawMessage(`{"path":"/tmp/a","content":"secret","token":"credential"}`))
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatal("require_user field did not force escalation")
	}
	var values map[string]any
	if err := json.Unmarshal(projected, &values); err != nil {
		t.Fatal(err)
	}
	if values["content"] != "[redacted]" || values["path"] != "/tmp/a" {
		t.Fatalf("review projection = %#v", values)
	}
	if _, leaked := values["token"]; leaked || strings.Contains(string(projected), "credential") || strings.Contains(string(projected), "secret") {
		t.Fatalf("review projection leaked sensitive fields: %s", projected)
	}
}
