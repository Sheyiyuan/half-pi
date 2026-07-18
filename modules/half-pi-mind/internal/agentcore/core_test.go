package agentcore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// stubLLM is a no-op LLM provider for testing.
type stubLLM struct{}

func (s *stubLLM) Chat(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{}, nil
}

// stubExecutor is a no-op tool executor for testing.
type stubExecutor struct{}

type allowApprover struct{}

func (allowApprover) Confirm(context.Context, approval.Request) approval.Resolution {
	return approval.Resolution{Decision: protocol.FaceApprovalAllowOnce}
}

func (s *stubExecutor) ExecuteTool(ctx context.Context, name string, args json.RawMessage) *executor.ToolResult {
	return &executor.ToolResult{Success: true, Output: "ok"}
}

func (s *stubExecutor) Tools() []executor.Tool {
	return nil
}

func TestCheckAndConfirmCannotOverrideDeny(t *testing.T) {
	const toolName = "phase1_mind_deny_tool"
	executor.Register(executor.Tool{
		Name: toolName,
		Check: func(json.RawMessage) (executor.Decision, string) {
			return executor.DecisionDeny, "hard deny"
		},
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			t.Fatal("denied tool must not execute")
			return nil
		},
	})
	core, err := New(&stubLLM{}, &stubExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	core.SetApprover(allowApprover{})
	blocked, reason := core.CheckAndConfirm(context.Background(), toolName, json.RawMessage(`{}`), true)
	if !blocked {
		t.Fatal("DecisionDeny must not be overridden by approval")
	}
	if reason != "hard deny" {
		t.Fatalf("reason = %q, want hard deny", reason)
	}
}

func TestCheckAndConfirmRequiresApprover(t *testing.T) {
	const toolName = "phase1_mind_confirm_tool"
	executor.Register(executor.Tool{
		Name:           toolName,
		DefaultConfirm: true,
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			return &executor.ToolResult{Success: true}
		},
	})
	core, err := New(&stubLLM{}, &stubExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	blocked, _ := core.CheckAndConfirm(context.Background(), toolName, json.RawMessage(`{}`), false)
	if !blocked {
		t.Fatal("confirmation-required tool must be blocked without an approver")
	}
}

func TestNewDefaults(t *testing.T) {
	core, err := New(&stubLLM{}, &stubExecutor{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if core.Mode != "normal" {
		t.Errorf("default Mode = %q, want normal", core.Mode)
	}
	if core.Debug {
		t.Error("default Debug should be false")
	}
	if core.autoAllow == nil {
		t.Error("autoAllow map should be initialized")
	}
	if core.autoDeny == nil {
		t.Error("autoDeny map should be initialized")
	}
}

func TestSetMode(t *testing.T) {
	core, _ := New(&stubLLM{}, &stubExecutor{})

	if err := core.SetMode("trust"); err != nil {
		t.Fatal(err)
	}
	if core.Mode != "trust" {
		t.Errorf("Mode = %q, want trust", core.Mode)
	}
	if len(core.history) != 1 {
		t.Fatalf("SetMode should append 1 message, got %d", len(core.history))
	}
	msg := core.history[0]
	if msg.Role != llm.RoleSystem {
		t.Errorf("role = %q, want system", msg.Role)
	}
	if !strings.Contains(msg.Content, "trust") {
		t.Errorf("content should mention mode, got %q", msg.Content)
	}

	// Second SetMode should append another message
	if err := core.SetMode("yolo"); err != nil {
		t.Fatal(err)
	}
	if len(core.history) != 2 {
		t.Errorf("second SetMode should append, got %d messages", len(core.history))
	}
}

func TestSetModeRejectsInvalidMode(t *testing.T) {
	core, _ := New(&stubLLM{}, &stubExecutor{})
	if err := core.SetMode("unsafe"); err == nil {
		t.Fatal("invalid mode was accepted")
	}
	if core.SecurityMode() != "normal" || len(core.history) != 0 {
		t.Fatal("invalid mode mutated Core state")
	}
}

func TestSetModeUpdatesSecurityPolicy(t *testing.T) {
	core, _ := New(&stubLLM{}, &stubExecutor{})

	// default is normal → ls should be allowed
	decision, _ := core.policy.Check("ls -la")
	if decision != security.Allow {
		t.Fatalf("default policy should allow ls, got %v", decision)
	}

	// switch to strict → ls should NOT be allowed
	core.SetMode("strict")
	decision, _ = core.policy.Check("ls -la")
	if decision == security.Allow {
		t.Fatal("strict mode should not allow ls")
	}

	// switch to trust → ls should be allowed
	core.SetMode("trust")
	decision, _ = core.policy.Check("ls -la")
	if decision != security.Allow {
		t.Errorf("trust mode should allow ls, got %v", decision)
	}

	// switch to yolo → ls should be allowed (unless blacklisted)
	core.SetMode("yolo")
	decision, _ = core.policy.Check("ls -la")
	if decision != security.Allow {
		t.Errorf("yolo mode should allow ls, got %v", decision)
	}
}

func TestToolsToDefs(t *testing.T) {
	tools := []executor.Tool{
		{
			Name:        "read_file",
			Description: "read a file",
			Parameters: &executor.ObjectSchema{
				Properties: []executor.PropertySchema{
					{Name: "path", Type: "string", Description: "file path"},
				},
				Required: []string{"path"},
			},
		},
	}

	defs := toolsToDefs(tools)
	if len(defs) != 1 {
		t.Fatalf("expected 1 def, got %d", len(defs))
	}

	d := defs[0]
	if d.Name != "read_file" {
		t.Errorf("name = %q", d.Name)
	}
	if d.Description != "read a file" {
		t.Errorf("description = %q", d.Description)
	}

	// confirm parameter should be injected
	params, ok := d.Parameters.(map[string]any)
	if !ok {
		t.Fatal("Parameters is not map[string]any")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	if _, ok := props["confirm"]; !ok {
		t.Error("confirm parameter should be injected")
	}
	if _, ok := props["path"]; !ok {
		t.Error("original parameter path should be preserved")
	}
	req, ok := params["required"].([]string)
	if !ok || req[0] != "path" {
		t.Error("required should contain original required params")
	}
}

func TestToolsToDefsInjectConfirmOnlyOnce(t *testing.T) {
	// confirm should be injected even when tool has no properties
	tools := []executor.Tool{
		{
			Name: "no_params",
			Parameters: &executor.ObjectSchema{
				Properties: nil,
			},
		},
	}

	defs := toolsToDefs(tools)
	params := defs[0].Parameters.(map[string]any)
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties should exist after injection")
	}
	if _, ok := props["confirm"]; !ok {
		t.Error("confirm should be injected even for parameterless tools")
	}
}

func TestToolOwnedConfirmIsPreservedForExecution(t *testing.T) {
	const toolName = "phase4_owned_confirm_tool"
	executor.Register(executor.Tool{
		Name:        toolName,
		OwnsConfirm: true,
		Parameters: &executor.ObjectSchema{Properties: []executor.PropertySchema{{
			Name: "confirm", Type: "boolean", Description: "tool-owned confirmation",
		}}},
	})

	args, llmConfirm := prepareToolArgs(toolName, `{"value":"kept","confirm":true}`)
	if llmConfirm {
		t.Fatal("tool-owned confirmation was consumed by Agent Core")
	}
	var decoded map[string]any
	if err := json.Unmarshal(args, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["confirm"] != true || decoded["value"] != "kept" {
		t.Fatalf("prepared args = %s", args)
	}

	defs := toolsToDefs([]executor.Tool{{
		Name: toolName, OwnsConfirm: true,
		Parameters: &executor.ObjectSchema{Properties: []executor.PropertySchema{{
			Name: "confirm", Type: "boolean", Description: "tool-owned confirmation",
		}}},
	}})
	properties := defs[0].Parameters.(map[string]any)["properties"].(map[string]any)
	confirm := properties["confirm"].(map[string]any)
	if confirm["description"] != "tool-owned confirmation" {
		t.Fatalf("owned confirm schema = %+v", confirm)
	}
}

func TestGenericConfirmIsConsumedByAgentCore(t *testing.T) {
	const toolName = "phase4_generic_confirm_tool"
	executor.Register(executor.Tool{Name: toolName})
	args, llmConfirm := prepareToolArgs(toolName, `{"value":"kept","confirm":true}`)
	if !llmConfirm || strings.Contains(string(args), "confirm") {
		t.Fatalf("generic confirmation was not consumed: confirm=%t args=%s", llmConfirm, args)
	}
}

func TestToolsToDefsEmpty(t *testing.T) {
	defs := toolsToDefs(nil)
	if len(defs) != 0 {
		t.Errorf("nil tools should return 0 defs, got %d", len(defs))
	}
	defs = toolsToDefs([]executor.Tool{})
	if len(defs) != 0 {
		t.Errorf("empty tools should return 0 defs, got %d", len(defs))
	}
}

func TestSetSkills(t *testing.T) {
	core, _ := New(&stubLLM{}, &stubExecutor{})
	if core.skills != nil {
		t.Error("skills should be nil before SetSkills")
	}
	core.SetSkills(nil)
	if core.skills != nil {
		t.Error("SetSkills(nil) should set to nil")
	}
}

func TestMsgConversionRoundTrip(t *testing.T) {
	original := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{
			Role: llm.RoleAssistant, Content: "hi",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "read_file", Args: `{"path":"/tmp"}`},
			},
		},
		{Role: llm.RoleTool, Content: "file content", ToolID: "call_1"},
	}

	stored := llmMsgToStore(original)
	if len(stored) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(stored))
	}
	if stored[1].ToolCalls == "" {
		t.Error("ToolCalls should not be empty for assistant message")
	}

	restored := storeMsgToLLM(stored)
	if len(restored) != 3 {
		t.Fatalf("round-trip: expected 3 messages, got %d", len(restored))
	}
	if restored[0].Role != llm.RoleUser || restored[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", restored[0])
	}
	if len(restored[1].ToolCalls) != 1 || restored[1].ToolCalls[0].Name != "read_file" {
		t.Errorf("msg[1] tool calls = %+v", restored[1].ToolCalls)
	}
	if restored[2].Role != llm.RoleTool || restored[2].ToolID != "call_1" {
		t.Errorf("msg[2] = %+v", restored[2])
	}
}

func TestMsgConversionEmptyToolCalls(t *testing.T) {
	original := []llm.Message{
		{Role: llm.RoleUser, Content: "no tools"},
	}

	stored := llmMsgToStore(original)
	if stored[0].ToolCalls != "null" {
		t.Errorf("empty ToolCalls should serialize to null, got %q", stored[0].ToolCalls)
	}

	restored := storeMsgToLLM(stored)
	if len(restored[0].ToolCalls) != 0 {
		t.Error("restored ToolCalls should be empty")
	}
}

func TestLoadSoul(t *testing.T) {
	soul := loadSoul("normal")
	if !strings.Contains(soul, "normal") {
		t.Error("soul should mention the mode")
	}
	if !strings.Contains(soul, "strict") {
		t.Error("soul should mention strict mode")
	}
	if !strings.Contains(soul, "confirm") {
		t.Error("soul should mention confirm parameter")
	}
}

func TestSaveSessionNilStore(t *testing.T) {
	core, _ := New(&stubLLM{}, &stubExecutor{})
	// SaveSession with nil store should not panic
	err := core.SaveSession()
	if err != nil {
		t.Errorf("SaveSession with nil store: %v", err)
	}
}

func TestSaveSessionWithStore(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := store.New(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer db.Close()

	g, _ := db.UpsertGroup("/tmp/test-session-save")
	sessionID := "test-save-session"
	db.CreateSession(g.ID, sessionID)

	core, _ := New(&stubLLM{}, &stubExecutor{})
	if err := core.SetStore(db, sessionID); err != nil {
		t.Fatalf("SetStore: %v", err)
	}
	// SetStore 会从 DB 加载历史（空），这里手动填入测试数据
	core.history = []llm.Message{
		{Role: llm.RoleUser, Content: "hello world this is a very long first message that should be truncated"},
	}

	// SaveSession should auto-name from first user message
	if err := core.SaveSession(); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sess, _ := db.GetSession(sessionID)
	if sess.Name == "" {
		t.Error("session should have been auto-named")
	}
	if len([]rune(sess.Name)) > 60 {
		t.Errorf("auto-name too long: %d chars", len([]rune(sess.Name)))
	}

	msgs, _ := db.GetMessages(sessionID)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}
