package compact

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

var registerProjectionToolsOnce sync.Once

func registerProjectionTools() {
	registerProjectionToolsOnce.Do(func() {
		for _, name := range []string{"use_hand", "read_file", "exec_command"} {
			if _, exists := executor.FindTool(name); !exists {
				executor.Register(executor.Tool{Name: name, Parameters: &executor.ObjectSchema{}})
			}
		}
	})
}

func TestToolProjectionAllowsOnlyCentralTypedFacts(t *testing.T) {
	registerProjectionTools()
	result := &executor.ToolResult{
		Success: true,
		CompactFacts: []executor.CompactFact{{
			Kind: "remote", HandID: "hand-1", RunID: "run-1", Tool: "read_file", Status: "succeeded",
		}},
	}
	projection := ProjectToolResult("use_hand", result, "secret raw output")
	stored, err := decodeStoredToolProjection(projection)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Tool != "use_hand" || !stored.Success || stored.ReasonCategory != "" ||
		stored.OutputBytes != len("secret raw output") || stored.OutputDigest != contentDigest("secret raw output") {
		t.Fatalf("projection = %+v", stored)
	}
	if stored.Facts["hand_id"] != "hand-1" || stored.Facts["run_id"] != "run-1" || stored.Facts["status"] != "succeeded" {
		t.Fatalf("facts = %+v", stored.Facts)
	}
	if strings.Contains(projection, "secret raw output") {
		t.Fatalf("projection contains output: %s", projection)
	}
}

func TestToolProjectionRejectsSensitiveAndEscapingPaths(t *testing.T) {
	registerProjectionTools()
	projected, err := (ToolFactProjector{}).ProjectToolResult(ToolFactInput{
		Tool: "read_file", Success: true, OutputBytes: 1, OutputDigest: contentDigest("x"),
		CandidateFacts: map[string]any{
			"paths": []string{"internal/config/config.go", "../secret", "/etc/passwd", ".env", "dir/id_rsa"},
			"count": int64(5), "truncated": true, "api_key": "must-not-pass",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeStoredToolProjection(string(projected))
	if err != nil {
		t.Fatal(err)
	}
	paths, ok := stored.Facts["paths"].([]any)
	if !ok || len(paths) != 1 || paths[0] != "internal/config/config.go" {
		t.Fatalf("paths = %#v", stored.Facts["paths"])
	}
	if _, leaked := stored.Facts["api_key"]; leaked {
		t.Fatalf("sensitive fact leaked: %+v", stored.Facts)
	}
}

func TestSourceProjectionNeverSendsToolOutputArgsOrDigest(t *testing.T) {
	registerProjectionTools()
	calls, _ := json.Marshal([]llm.ToolCall{{ID: "call-secret", Name: "use_hand", Args: `{"token":"top-secret"}`}})
	toolProjection := ProjectToolResult("use_hand", &executor.ToolResult{
		Success:      true,
		CompactFacts: []executor.CompactFact{{HandID: "hand-1", RunID: "run-1", Status: "succeeded"}},
	}, "raw stdout top-secret")
	messages := []store.Message{
		{Role: "user", Content: "authorization: Bearer abcdefghijklmnop", Seq: 1},
		{Role: "assistant", Content: "calling", ToolCalls: string(calls), Seq: 2},
		{Role: "tool", Content: "raw stdout top-secret", ToolID: "call-secret", CompactProjection: toolProjection, Seq: 3},
	}
	projected, err := projectSource(messages, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	view, err := encodeJSONNoHTML(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"abcdefghijklmnop", "raw stdout", "top-secret", "call-secret", `\"token\"`, "output_digest"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("source projection leaked %q: %s", forbidden, view)
		}
	}
	for _, required := range []string{"<redacted>", "use_hand", "hand-1", "run-1", "succeeded"} {
		if !strings.Contains(view, required) {
			t.Fatalf("source projection omitted %q: %s", required, view)
		}
	}

	corrupt := append([]store.Message(nil), messages...)
	corrupt[2].Content = "modified output"
	projected, err = projectSource(corrupt, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	tool := projected[len(projected)-1].Tool
	if tool == nil || !tool.Omitted || tool.Success != nil || tool.Facts != nil {
		t.Fatalf("corrupt projection did not degrade to omit: %+v", tool)
	}
}

func TestSourceProjectionRedactsPrivateKeyBlocks(t *testing.T) {
	projected, err := projectSource([]store.Message{{
		Role: "user", Content: "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----", Seq: 1,
	}}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || projected[0].Text != "<redacted>" {
		t.Fatalf("projected = %+v", projected)
	}
}
