package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

func echoTool(name string) Tool {
	return Tool{
		Name:        name,
		Description: "echoes input back",
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{{Name: "message", Type: "string"}},
			Required:   []string{"message"},
		},
		Execute: func(context.Context, json.RawMessage) *ToolResult {
			return &ToolResult{Success: true, Output: name}
		},
	}
}

func TestCatalogRegisterReportsErrorsWithoutPanic(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(Tool{}); err == nil {
		t.Fatal("Register should reject an empty tool name")
	}
	if err := catalog.Register(echoTool("scoped.echo")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := catalog.Register(echoTool("scoped.echo")); err == nil {
		t.Fatal("Register should reject a duplicate tool name")
	}
	if tools := catalog.Tools(); len(tools) != 1 {
		t.Fatalf("catalog holds %d tools, want 1", len(tools))
	}
}

func TestCatalogIsIsolatedFromDefaultCatalog(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(echoTool("scoped.only")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := FindTool("scoped.only"); ok {
		t.Fatal("instance catalog leaked a tool into the process default catalog")
	}
	if _, ok := catalog.Find("test.echo"); ok {
		t.Fatal("instance catalog resolved a tool it never registered")
	}
}

func TestCatalogFindReturnsDeepCopy(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(echoTool("scoped.copy")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tool, ok := catalog.Find("scoped.copy")
	if !ok {
		t.Fatal("Find returned false for a registered tool")
	}
	tool.Parameters.Properties[0].Name = "mutated"
	tool.Parameters.Required[0] = "mutated"

	again, _ := catalog.Find("scoped.copy")
	if again.Parameters.Properties[0].Name != "message" || again.Parameters.Required[0] != "message" {
		t.Fatal("mutating a Find result corrupted the catalog schema")
	}
}

func TestCatalogRegisterCopiesCallerSchema(t *testing.T) {
	catalog := NewCatalog()
	tool := echoTool("scoped.registered")
	if err := catalog.Register(tool); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tool.Parameters.Properties[0].Name = "mutated"

	stored, _ := catalog.Find("scoped.registered")
	if stored.Parameters.Properties[0].Name != "message" {
		t.Fatal("catalog kept a reference to the caller's schema")
	}
}

func TestCatalogDeriveFiltersAndDetaches(t *testing.T) {
	source := NewCatalog()
	for _, name := range []string{"scoped.keep", "scoped.drop"} {
		if err := source.Register(echoTool(name)); err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
	}
	derived := source.Derive(func(tool Tool) bool { return tool.Name == "scoped.keep" })

	if _, ok := derived.Find("scoped.drop"); ok {
		t.Fatal("Derive kept a tool the filter rejected")
	}
	if _, ok := derived.Find("scoped.keep"); !ok {
		t.Fatal("Derive dropped a tool the filter accepted")
	}
	if err := derived.Register(echoTool("scoped.derived_only")); err != nil {
		t.Fatalf("Register into derived catalog: %v", err)
	}
	if _, ok := source.Find("scoped.derived_only"); ok {
		t.Fatal("registering into a derived catalog mutated its source")
	}
	if derived.Snapshot().Digest == source.Snapshot().Digest {
		t.Fatal("derived catalog with different tools shares the source digest")
	}
}

func TestCatalogDeriveNilFilterCopiesEverything(t *testing.T) {
	source := NewCatalog()
	for _, name := range []string{"scoped.a", "scoped.b"} {
		if err := source.Register(echoTool(name)); err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
	}
	derived := source.Derive(nil)
	if derived.Snapshot().Digest != source.Snapshot().Digest {
		t.Fatal("Derive(nil) should produce an identical tool set")
	}
}

func TestToolRuntimeResolvesThroughInjectedCatalog(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(echoTool("scoped.runtime")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rt := NewToolRuntimeWithCatalog(&allowAuthorizer{}, nil, catalog)
	meta := lifecycle.NewMeta(lifecycle.SourceMind)

	result := rt.Execute(context.Background(), Invocation{
		Meta: meta, Tool: "scoped.runtime", Args: json.RawMessage(`{"message":"hi"}`),
	})
	if result.ExecutionOutcome != ExecutionSucceeded {
		t.Fatalf("scoped tool outcome = %s, want succeeded", result.ExecutionOutcome)
	}

	denied := rt.Execute(context.Background(), Invocation{
		Meta: meta, Tool: "test.echo", Args: json.RawMessage(`{"message":"hi"}`),
	})
	if denied.ErrorCode != "unknown_tool" {
		t.Fatalf("default-catalog tool error code = %q, want unknown_tool", denied.ErrorCode)
	}
}

func TestToolRuntimeDefaultsToProcessCatalog(t *testing.T) {
	rt := NewToolRuntime(&allowAuthorizer{}, nil)
	if rt.Catalog() != DefaultCatalog() {
		t.Fatal("NewToolRuntime should resolve through the process default catalog")
	}
	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo",
		Args: json.RawMessage(`{"message":"hi"}`),
	})
	if result.ExecutionOutcome != ExecutionSucceeded {
		t.Fatalf("default catalog tool outcome = %s, want succeeded", result.ExecutionOutcome)
	}
}
