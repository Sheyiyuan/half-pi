package executor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

func init() {
	// 注册测试工具
	Register(Tool{
		Name:        "test.echo",
		Description: "echoes input back",
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{
				{Name: "message", Type: "string", Description: "message to echo"},
			},
			Required: []string{"message"},
		},
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			var m map[string]any
			json.Unmarshal(args, &m)
			msg, _ := m["message"].(string)
			return &ToolResult{Success: true, Output: msg}
		},
	})

	Register(Tool{
		Name:           "test.confirm_default",
		Description:    "requires confirmation by default",
		DefaultConfirm: true,
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			return &ToolResult{Success: true, Output: "confirmed"}
		},
	})

	Register(Tool{
		Name:        "test.panic",
		Description: "panics",
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			panic("intentional test panic")
		},
	})

	Register(Tool{
		Name:        "test.nil",
		Description: "returns nil",
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			return nil
		},
	})

	Register(Tool{
		Name:        "test.error",
		Description: "returns error",
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			return &ToolResult{Success: false, Error: "simulated error"}
		},
	})

	Register(Tool{
		Name:        "test.progress",
		Description: "reports progress",
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			ReportProgress(ctx, Progress{Kind: "stdout", Data: "secret-progress"})
			return &ToolResult{Success: true, Output: "done"}
		},
	})

	Register(Tool{
		Name: "test.rename_source",
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{{Name: "message", Type: "string"}},
			Required:   []string{"message"},
		},
		Execute: func(context.Context, json.RawMessage) *ToolResult {
			return &ToolResult{Success: true, Output: "source"}
		},
	})

	Register(Tool{
		Name: "test.rename_target",
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{{Name: "count", Type: "integer"}},
			Required:   []string{"count"},
		},
		Execute: func(_ context.Context, args json.RawMessage) *ToolResult {
			return &ToolResult{Success: true, Output: string(args)}
		},
	})

	Register(Tool{
		Name:        "test.owns_confirm",
		OwnsConfirm: true,
		Parameters: &ObjectSchema{
			Properties: []PropertySchema{{Name: "confirm", Type: "boolean"}},
		},
		Execute: func(_ context.Context, args json.RawMessage) *ToolResult {
			return &ToolResult{Success: true, Output: string(args)}
		},
	})
}

func TestInvocationFreeze(t *testing.T) {
	meta := lifecycle.NewMeta(lifecycle.SourceMind)
	inv := Invocation{
		Meta:       meta,
		Tool:       "test.echo",
		Args:       json.RawMessage(`{"message":"hello"}`),
		TargetNode: "hand-1",
		Timeout:    5 * time.Second,
	}

	frozen, err := inv.Freeze()
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if frozen.Tool != "test.echo" {
		t.Errorf("Tool mismatch: %s", frozen.Tool)
	}
	if frozen.ArgsDigest == "" {
		t.Error("ArgsDigest should not be empty")
	}
	if frozen.TargetNode != "hand-1" {
		t.Errorf("TargetNode mismatch: %s", frozen.TargetNode)
	}
	if frozen.Timeout != 5*time.Second {
		t.Errorf("Timeout mismatch: %v", frozen.Timeout)
	}

	// digest should be deterministic
	frozen2, _ := inv.Freeze()
	if frozen.ArgsDigest != frozen2.ArgsDigest {
		t.Error("freeze digest should be deterministic")
	}
}

func TestInvocationFreezeUnknownTool(t *testing.T) {
	meta := lifecycle.NewMeta(lifecycle.SourceMind)
	inv := Invocation{Meta: meta, Tool: "nonexistent.tool", Args: json.RawMessage(`{}`)}
	_, err := inv.Freeze()
	if err == nil {
		t.Error("freeze should fail for unknown tool")
	}
}

func TestInvocationFreezeInvalidArgs(t *testing.T) {
	meta := lifecycle.NewMeta(lifecycle.SourceMind)
	inv := Invocation{Meta: meta, Tool: "test.echo", Args: json.RawMessage(`not-json`)}
	_, err := inv.Freeze()
	if err == nil {
		t.Error("freeze should fail for invalid JSON")
	}
}

func TestToolRuntimeExecuteSuccess(t *testing.T) {
	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	meta := lifecycle.NewMeta(lifecycle.SourceMind)
	result := rt.Execute(context.Background(), Invocation{
		Meta: meta, Tool: "test.echo", Args: json.RawMessage(`{"message":"hello world"}`),
	})

	if result.ExecutionOutcome != ExecutionSucceeded {
		t.Errorf("expected succeeded, got %s", result.ExecutionOutcome)
	}
	if result.Output != "hello world" {
		t.Errorf("output mismatch: %s", result.Output)
	}
	if result.ErrorCode != "" {
		t.Errorf("ErrorCode should be empty, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeExecuteUnknownTool(t *testing.T) {
	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "nonexistent",
		Args: json.RawMessage(`{}`),
	})

	if result.ExecutionOutcome != ExecutionFailed {
		t.Errorf("expected failed, got %s", result.ExecutionOutcome)
	}
	if result.ErrorCode != "unknown_tool" {
		t.Errorf("expected unknown_tool, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeExecuteDeniedByAuthorizer(t *testing.T) {
	denyAll := &denyAuthorizer{}
	rt := NewToolRuntime(denyAll, nil)

	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.echo",
		Args: json.RawMessage(`{"message":"hello"}`),
	})

	if result.ExecutionOutcome != ExecutionFailed {
		t.Errorf("expected failed, got %s", result.ExecutionOutcome)
	}
	if result.ErrorCode != "authorizer_denied" {
		t.Errorf("expected authorizer_denied, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeExecutePanicRecovery(t *testing.T) {
	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.panic",
		Args: json.RawMessage(`{}`),
	})

	if result.ExecutionOutcome != ExecutionPanicked {
		t.Errorf("expected panicked, got %s", result.ExecutionOutcome)
	}
	if result.ErrorCode != "panic" {
		t.Errorf("expected panic error code, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeExecuteNilResult(t *testing.T) {
	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.nil",
		Args: json.RawMessage(`{}`),
	})

	if result.ExecutionOutcome != ExecutionFailed {
		t.Errorf("expected failed, got %s", result.ExecutionOutcome)
	}
	if result.ErrorCode != "nil_result" {
		t.Errorf("expected nil_result, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeExecuteErrorResult(t *testing.T) {
	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.error",
		Args: json.RawMessage(`{}`),
	})

	if result.ExecutionOutcome != ExecutionFailed {
		t.Errorf("expected failed, got %s", result.ExecutionOutcome)
	}
	if result.ErrorCode != "tool_error" {
		t.Errorf("expected stable tool_error code, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeExecuteWithTimeout(t *testing.T) {
	Register(Tool{
		Name:        "test.slow",
		Description: "slow tool",
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			select {
			case <-ctx.Done():
				return &ToolResult{Success: false, Error: "cancelled"}
			case <-time.After(5 * time.Second):
				return &ToolResult{Success: true, Output: "done"}
			}
		},
	})

	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	result := rt.Execute(context.Background(), Invocation{
		Meta:    lifecycle.NewMeta(lifecycle.SourceMind),
		Tool:    "test.slow",
		Args:    json.RawMessage(`{}`),
		Timeout: 50 * time.Millisecond,
	})

	if result.ExecutionOutcome != ExecutionTimedOut {
		t.Errorf("expected timed_out, got %s", result.ExecutionOutcome)
	}
}

func TestToolRuntimeExecuteWithContextCancel(t *testing.T) {
	Register(Tool{
		Name:        "test.blocking",
		Description: "blocks until context done",
		Execute: func(ctx context.Context, args json.RawMessage) *ToolResult {
			<-ctx.Done()
			return &ToolResult{Success: false, Error: ctx.Err().Error()}
		},
	})

	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	result := rt.Execute(ctx, Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.blocking",
		Args: json.RawMessage(`{}`),
	})

	if result.ExecutionOutcome != ExecutionCancelled {
		t.Errorf("expected cancelled, got %s", result.ExecutionOutcome)
	}
}

func TestToolRuntimeWithLifecycleRegistry(t *testing.T) {
	reg := lifecycle.NewRegistry()
	obs := &testLifecycleObserver{}
	reg.RegisterObserver(lifecycle.Registration{
		ID: "test-obs", Kind: lifecycle.KindObserver,
		Phases: []lifecycle.Phase{
			lifecycle.PhaseToolFrozen,
			lifecycle.PhaseToolAuthorized,
			lifecycle.PhaseToolStarted,
			lifecycle.PhaseToolFinished,
		},
	}, obs)

	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, reg)

	meta := lifecycle.NewMeta(lifecycle.SourceMind)
	result := rt.Execute(context.Background(), Invocation{
		Meta: meta, Tool: "test.echo", Args: json.RawMessage(`{"message":"hello"}`),
	})

	if result.ExecutionOutcome != ExecutionSucceeded {
		t.Fatalf("expected succeeded, got %s", result.ExecutionOutcome)
	}
	if err := reg.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}

	events := obs.Events()
	phases := make(map[lifecycle.Phase]int)
	for _, e := range events {
		phases[e.Phase]++
	}

	if phases[lifecycle.PhaseToolFrozen] != 1 {
		t.Error("expected one PhaseToolFrozen event")
	}
	if phases[lifecycle.PhaseToolAuthorized] != 1 {
		t.Error("expected one PhaseToolAuthorized event")
	}
	if phases[lifecycle.PhaseToolStarted] != 1 {
		t.Error("expected one PhaseToolStarted event")
	}
	if phases[lifecycle.PhaseToolFinished] != 1 {
		t.Error("expected one PhaseToolFinished event")
	}
}

func TestToolRuntimeDeniedDoesNotEmitStarted(t *testing.T) {
	reg := lifecycle.NewRegistry()
	obs := &testLifecycleObserver{}
	reg.RegisterObserver(lifecycle.Registration{
		ID: "test-obs", Kind: lifecycle.KindObserver,
		Phases: []lifecycle.Phase{
			lifecycle.PhaseToolDenied,
			lifecycle.PhaseToolStarted,
			lifecycle.PhaseToolFinished,
		},
	}, obs)

	denyAll := &denyAuthorizer{}
	rt := NewToolRuntime(denyAll, reg)

	rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.echo",
		Args: json.RawMessage(`{"message":"hello"}`),
	})
	if err := reg.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}

	events := obs.Events()
	for _, e := range events {
		if e.Phase == lifecycle.PhaseToolStarted {
			t.Error("denied tool should not emit started event")
		}
		if e.Phase == lifecycle.PhaseToolFinished {
			t.Error("denied tool should not emit finished event")
		}
	}

	hasDenied := false
	for _, e := range events {
		if e.Phase == lifecycle.PhaseToolDenied {
			hasDenied = true
		}
	}
	if !hasDenied {
		t.Error("denied tool should emit denied event")
	}
}

func TestToolRuntimeDefaultNilAuthorizerIsDeny(t *testing.T) {
	rt := NewToolRuntime(nil, nil)
	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.echo",
		Args: json.RawMessage(`{"message":"hello"}`),
	})

	if result.ErrorCode != "authorizer_denied" {
		t.Errorf("nil authorizer should deny, got %s", result.ErrorCode)
	}
}

func TestToolRuntimeGuardDeniesExecution(t *testing.T) {
	reg := lifecycle.NewRegistry()
	denyGuard := &testGuard{id: "deny-all", verdict: lifecycle.VerdictDeny}
	reg.RegisterGuard(lifecycle.Registration{
		ID: "deny-all", Kind: lifecycle.KindGuard,
		Phases: []lifecycle.Phase{lifecycle.PhaseToolBeforeExecute},
	}, denyGuard)

	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, reg)

	result := rt.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind),
		Tool: "test.echo",
		Args: json.RawMessage(`{"message":"hello"}`),
	})

	if result.ErrorCode != "guard_denied" {
		t.Errorf("expected guard_denied, got %s", result.ErrorCode)
	}
}

func TestFrozenInvocationDigestUnique(t *testing.T) {
	meta := lifecycle.NewMeta(lifecycle.SourceMind)
	a, _ := Invocation{Meta: meta, Tool: "test.echo", Args: json.RawMessage(`{"message":"a"}`)}.Freeze()
	b, _ := Invocation{Meta: meta, Tool: "test.echo", Args: json.RawMessage(`{"message":"b"}`)}.Freeze()
	if a.ArgsDigest == b.ArgsDigest {
		t.Error("different args should produce different digests")
	}
}

func TestFrozenInvocationOwnsArgs(t *testing.T) {
	args := json.RawMessage(`{"message":"before"}`)
	frozen, err := (Invocation{Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo", Args: args}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	copy(args, `{"message":"after!"}`)
	if string(frozen.Args) != `{"message":"before"}` {
		t.Fatalf("frozen args changed through caller alias: %s", frozen.Args)
	}
}

func TestToolRuntimeGuardRequiresApproval(t *testing.T) {
	reg := lifecycle.NewRegistry()
	guard := &testGuard{id: "approval", verdict: lifecycle.VerdictRequireApproval}
	if err := reg.RegisterGuard(lifecycle.Registration{
		ID: "approval", Kind: lifecycle.KindGuard,
		Phases: []lifecycle.Phase{lifecycle.PhaseToolBeforeExecute},
	}, guard); err != nil {
		t.Fatal(err)
	}
	authorizer := &assertApprovalAuthorizer{}
	result := NewToolRuntime(authorizer, reg).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo",
		Args: json.RawMessage(`{"message":"guarded"}`),
	})
	if !authorizer.sawRequired || result.ExecutionOutcome != ExecutionSucceeded {
		t.Fatalf("require approval was not propagated: required=%v result=%+v", authorizer.sawRequired, result)
	}
}

func TestToolRuntimeTransformerChangesFrozenArgs(t *testing.T) {
	reg := lifecycle.NewRegistry()
	transformer := &replaceArgsTransformer{}
	if err := reg.RegisterTransformer(lifecycle.Registration{
		ID: "replace", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, transformer); err != nil {
		t.Fatal(err)
	}
	result := NewToolRuntime(&allowAuthorizer{}, reg).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo",
		Args: json.RawMessage(`{"message":"before"}`),
	})
	if result.Output != "after" {
		t.Fatalf("transformer output = %q", result.Output)
	}
}

func TestToolRuntimeTransformerRepairsInvalidArgsBeforeSchemaValidation(t *testing.T) {
	reg := lifecycle.NewRegistry()
	if err := reg.RegisterTransformer(lifecycle.Registration{
		ID: "repair", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, &replaceArgsTransformer{}); err != nil {
		t.Fatal(err)
	}
	result := NewToolRuntime(&allowAuthorizer{}, reg).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo", Args: json.RawMessage(`{}`),
	})
	if result.ExecutionOutcome != ExecutionSucceeded || result.Output != "after" {
		t.Fatalf("transformer could not repair invalid args: %+v", result)
	}
}

func TestToolRuntimeTransformerCannotRemoveOwnedConfirmation(t *testing.T) {
	reg := lifecycle.NewRegistry()
	if err := reg.RegisterTransformer(lifecycle.Registration{
		ID: "remove-confirm", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, removeConfirmTransformer{}); err != nil {
		t.Fatal(err)
	}
	result := NewToolRuntime(&allowAuthorizer{}, reg).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.owns_confirm",
		Args: json.RawMessage(`{"confirm":true}`),
	})
	if result.ExecutionOutcome != ExecutionFailed || result.ErrorCode != "transform_failed" {
		t.Fatalf("transformer removed owned confirmation: %+v", result)
	}
}

func TestToolRuntimeTransformerToolRenameUsesFinalDefinition(t *testing.T) {
	registry := lifecycle.NewRegistry()
	if err := registry.RegisterTransformer(lifecycle.Registration{
		ID: "rename-tool", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, renameToolTransformer{}); err != nil {
		t.Fatal(err)
	}
	authorizer := &captureAuthorizer{}
	result := NewToolRuntime(authorizer, registry).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.rename_source",
		Args: json.RawMessage(`{"message":"before"}`),
	})
	if result.ExecutionOutcome != ExecutionSucceeded || result.Output != `{"count":2}` {
		t.Fatalf("renamed execution = %+v", result)
	}
	if authorizer.frozen.Tool != "test.rename_target" || string(authorizer.frozen.Args) != `{"count":2}` {
		t.Fatalf("authorized invocation = %+v", authorizer.frozen)
	}
}

func TestToolRuntimeRejectsUnboundedInvocationMetadata(t *testing.T) {
	runtime := NewToolRuntime(&allowAuthorizer{}, nil)
	tests := []struct {
		name string
		inv  Invocation
	}{
		{name: "purpose", inv: Invocation{Purpose: string(make([]byte, maxInvocationPurpose+1))}},
		{name: "target", inv: Invocation{TargetNode: string(make([]byte, maxInvocationTargetNode+1))}},
		{name: "risk label", inv: Invocation{RiskLabels: []string{""}}},
		{name: "risk label count", inv: Invocation{RiskLabels: make([]string, maxInvocationRiskLabels+1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.inv.Meta = lifecycle.NewMeta(lifecycle.SourceMind)
			test.inv.Tool = "test.echo"
			test.inv.Args = json.RawMessage(`{"message":"value"}`)
			result := runtime.Execute(context.Background(), test.inv)
			if result.ExecutionOutcome != ExecutionFailed || result.ErrorCode != "transform_failed" {
				t.Fatalf("unbounded metadata result = %+v", result)
			}
		})
	}
}

func TestPrepareExternalRejectsToolRename(t *testing.T) {
	registry := lifecycle.NewRegistry()
	if err := registry.RegisterTransformer(lifecycle.Registration{
		ID: "rename-external", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, renameToolTransformer{}); err != nil {
		t.Fatal(err)
	}
	definition := Tool{Name: "test.rename_source", Parameters: &ObjectSchema{
		Properties: []PropertySchema{{Name: "message", Type: "string"}}, Required: []string{"message"},
	}}
	prepared, terminal := NewToolRuntime(&allowAuthorizer{}, registry).PrepareExternal(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: definition.Name,
		Args: json.RawMessage(`{"message":"before"}`),
	}, definition, testExternalDigest)
	if prepared != nil || terminal.ErrorCode != "external_contract_changed" {
		t.Fatalf("external renamed admission: prepared=%v terminal=%+v", prepared, terminal)
	}
}

func TestPrepareExternalRejectsBoundContractChanges(t *testing.T) {
	registry := lifecycle.NewRegistry()
	if err := registry.RegisterTransformer(lifecycle.Registration{
		ID: "change-external-run", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, changeExternalRunTransformer{}); err != nil {
		t.Fatal(err)
	}
	definition := Tool{Name: "remote.echo", Parameters: &ObjectSchema{
		Properties: []PropertySchema{{Name: "message", Type: "string"}}, Required: []string{"message"},
	}}
	prepared, terminal := NewToolRuntime(&allowAuthorizer{}, registry).PrepareExternal(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: definition.Name,
		Args: json.RawMessage(`{"message":"before"}`), TargetNode: "hand-1", RunID: "run-1",
	}, definition, testExternalDigest)
	if prepared != nil || terminal.ErrorCode != "external_contract_changed" {
		t.Fatalf("external contract changed: prepared=%v terminal=%+v", prepared, terminal)
	}
}

func TestPrepareExternalRequiresContractDigest(t *testing.T) {
	definition := Tool{Name: "remote.echo", Parameters: &ObjectSchema{
		Properties: []PropertySchema{{Name: "message", Type: "string"}}, Required: []string{"message"},
	}}
	prepared, terminal := NewToolRuntime(&allowAuthorizer{}, nil).PrepareExternal(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: definition.Name,
		Args: json.RawMessage(`{"message":"before"}`), TargetNode: "hand-1", RunID: "run-1",
	}, definition, nil)
	if prepared != nil || terminal.ErrorCode != "external_digest_required" {
		t.Fatalf("external admission omitted digest: prepared=%v terminal=%+v", prepared, terminal)
	}
}

func TestToolRuntimeGuardTimeoutFailsClosed(t *testing.T) {
	reg := lifecycle.NewRegistry()
	if err := reg.RegisterGuard(lifecycle.Registration{
		ID: "slow", Kind: lifecycle.KindGuard, Timeout: 10 * time.Millisecond,
		Phases: []lifecycle.Phase{lifecycle.PhaseToolBeforeExecute},
	}, blockingGuard{}); err != nil {
		t.Fatal(err)
	}
	result := NewToolRuntime(&allowAuthorizer{}, reg).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo",
		Args: json.RawMessage(`{"message":"blocked"}`),
	})
	if result.ErrorCode != "guard_denied" {
		t.Fatalf("guard timeout did not fail closed: %+v", result)
	}
}

func TestToolRuntimeObserverPanicFailsOpen(t *testing.T) {
	reg := lifecycle.NewRegistry()
	if err := reg.RegisterObserver(lifecycle.Registration{
		ID: "panic", Kind: lifecycle.KindObserver,
		Phases: []lifecycle.Phase{lifecycle.PhaseToolFinished},
	}, panicObserver{}); err != nil {
		t.Fatal(err)
	}
	result := NewToolRuntime(&allowAuthorizer{}, reg).Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo",
		Args: json.RawMessage(`{"message":"ok"}`),
	})
	if result.ExecutionOutcome != ExecutionSucceeded || result.Output != "ok" {
		t.Fatalf("observer panic affected tool result: %+v", result)
	}
}

func TestToolRuntimeConcurrentExecution(t *testing.T) {
	allowAll := &allowAuthorizer{}
	rt := NewToolRuntime(allowAll, nil)

	var wg sync.WaitGroup
	errs := make(chan string, 10)

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := rt.Execute(context.Background(), Invocation{
				Meta: lifecycle.NewMeta(lifecycle.SourceMind),
				Tool: "test.echo",
				Args: json.RawMessage(`{"message":"concurrent"}`),
			})
			if result.ExecutionOutcome != ExecutionSucceeded {
				errs <- string(result.ExecutionOutcome)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Errorf("concurrent execution failed: %s", e)
	}
}

// ── 测试辅助类型 ──

func TestPrepareExternalBindsDigestAfterTransformation(t *testing.T) {
	registry := lifecycle.NewRegistry()
	if err := registry.RegisterTransformer(lifecycle.Registration{
		ID: "replace", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolBeforeFreeze},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, &replaceArgsTransformer{}); err != nil {
		t.Fatal(err)
	}
	authorizer := &captureAuthorizer{}
	runtime := NewToolRuntime(authorizer, registry)
	definition := Tool{Name: "remote.echo", Parameters: &ObjectSchema{
		Properties: []PropertySchema{{Name: "message", Type: "string"}}, Required: []string{"message"},
	}}
	prepared, terminal := runtime.PrepareExternal(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: definition.Name,
		Args: json.RawMessage(`{"message":"before"}`), RunID: "run-1", TargetNode: "hand-1",
	}, definition, func(frozen FrozenInvocation) (string, error) {
		if string(frozen.Args) != `{"message":"after"}` {
			t.Fatalf("digest binder saw args %s", frozen.Args)
		}
		return "sha256:run-bound", nil
	})
	if prepared == nil {
		t.Fatalf("external admission failed: %+v", terminal)
	}
	if authorizer.frozen.ApprovalDigest != "sha256:run-bound" || authorizer.frozen.ArgsDigest == authorizer.frozen.ApprovalDigest {
		t.Fatalf("frozen digests = %+v", authorizer.frozen)
	}
	result := prepared.Complete(context.Background(), Result{
		ExecutionOutcome: ExecutionSucceeded, DeliveryOutcome: lifecycle.OutcomeSucceeded, Output: "done",
	})
	if result.ExecutionOutcome != ExecutionSucceeded || result.Output != "done" {
		t.Fatalf("external terminal = %+v", result)
	}
	if reused := prepared.Complete(context.Background(), Result{}); reused.ErrorCode != "prepared_execution_reused" {
		t.Fatalf("external execution was reusable: %+v", reused)
	}
}

func TestTerminalAuditFailurePreservesExecutionOutcome(t *testing.T) {
	registry := lifecycle.NewRegistry()
	if err := registry.RegisterAuditor(lifecycle.Registration{
		ID: "terminal-failure", Kind: lifecycle.KindAuditor,
		Phases:      []lifecycle.Phase{lifecycle.PhaseToolFinished},
		FailureMode: lifecycle.FailureFailClosed,
	}, failingRuntimeAuditor{}); err != nil {
		t.Fatal(err)
	}
	runtime := NewToolRuntime(&allowAuthorizer{}, registry)
	result := runtime.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo", Args: json.RawMessage(`{"message":"executed"}`),
	})
	if result.ExecutionOutcome != ExecutionSucceeded || !result.AuditDegraded || result.ErrorCode != "terminal_audit_failed" {
		t.Fatalf("terminal audit semantics = %+v", result)
	}
	if result.DeliveryOutcome != lifecycle.OutcomeFailed {
		t.Fatalf("degraded result was delivered as success: %+v", result)
	}
}

func TestToolRuntimeUnknownAndInvalidInvocationsAreAuditedDenials(t *testing.T) {
	registry := lifecycle.NewRegistry()
	observer := &testLifecycleObserver{}
	auditor := &captureRuntimeAuditor{}
	if err := registry.RegisterObserver(lifecycle.Registration{
		ID: "denial-events", Kind: lifecycle.KindObserver,
		Phases: []lifecycle.Phase{lifecycle.PhaseToolProposed, lifecycle.PhaseToolDenied},
	}, observer); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterAuditor(lifecycle.Registration{
		ID: "denial-audit", Kind: lifecycle.KindAuditor, Phases: []lifecycle.Phase{lifecycle.PhaseToolDenied},
	}, auditor); err != nil {
		t.Fatal(err)
	}
	runtime := NewToolRuntime(&allowAuthorizer{}, registry)
	unknown := runtime.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "missing.tool", Args: json.RawMessage(`{"secret":"value"}`),
	})
	invalid := runtime.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo", Args: json.RawMessage(`{"unknown":true}`),
	})
	if unknown.ErrorCode != "unknown_tool" || invalid.ErrorCode != "freeze_error" {
		t.Fatalf("terminal results: unknown=%+v invalid=%+v", unknown, invalid)
	}
	if err := registry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := observer.Events()
	var proposed, denied int
	for _, event := range events {
		switch event.Phase {
		case lifecycle.PhaseToolProposed:
			proposed++
		case lifecycle.PhaseToolDenied:
			denied++
		}
		if event.Sensitive != nil {
			t.Fatalf("ordinary observer received raw invocation: %#v", event.Sensitive)
		}
	}
	if proposed != 2 || denied != 2 {
		t.Fatalf("event counts: proposed=%d denied=%d events=%+v", proposed, denied, events)
	}
	mutations := auditor.Mutations()
	if len(mutations) != 2 || mutations[0].Decision != "deny" || mutations[1].Decision != "deny" ||
		mutations[0].InputDigest == "" || mutations[1].InputDigest == "" {
		t.Fatalf("denial mutations = %+v", mutations)
	}
}

func TestToolRuntimeProgressLifecycleAndResultBuffering(t *testing.T) {
	plainRegistry := lifecycle.NewRegistry()
	plainObserver := &testLifecycleObserver{}
	if err := plainRegistry.RegisterObserver(lifecycle.Registration{
		ID: "plain-progress", Kind: lifecycle.KindObserver, Phases: []lifecycle.Phase{lifecycle.PhaseToolProgress},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityReadResults},
	}, plainObserver); err != nil {
		t.Fatal(err)
	}
	var forwarded []Progress
	plainResult := NewToolRuntime(&allowAuthorizer{}, plainRegistry).Execute(WithProgress(context.Background(), func(progress Progress) {
		forwarded = append(forwarded, progress)
	}), Invocation{Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.progress", Args: json.RawMessage(`{}`)})
	if plainResult.ExecutionOutcome != ExecutionSucceeded || len(forwarded) != 1 || forwarded[0].Data != "secret-progress" {
		t.Fatalf("plain progress result=%+v forwarded=%+v", plainResult, forwarded)
	}
	if err := plainRegistry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	plainEvents := plainObserver.Events()
	if len(plainEvents) != 1 || plainEvents[0].OutputLength != len("secret-progress") || plainEvents[0].Sensitive == nil {
		t.Fatalf("plain lifecycle progress = %+v", plainEvents)
	}

	bufferedRegistry := lifecycle.NewRegistry()
	bufferedObserver := &testLifecycleObserver{}
	if err := bufferedRegistry.RegisterTransformer(lifecycle.Registration{
		ID: "result-filter", Kind: lifecycle.KindTransformer, Phases: []lifecycle.Phase{lifecycle.PhaseToolResultBeforeCommit},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, noopRuntimeTransformer{}); err != nil {
		t.Fatal(err)
	}
	if err := bufferedRegistry.RegisterObserver(lifecycle.Registration{
		ID: "buffered-progress", Kind: lifecycle.KindObserver, Phases: []lifecycle.Phase{lifecycle.PhaseToolProgress},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityReadResults},
	}, bufferedObserver); err != nil {
		t.Fatal(err)
	}
	forwarded = nil
	bufferedResult := NewToolRuntime(&allowAuthorizer{}, bufferedRegistry).Execute(WithProgress(context.Background(), func(progress Progress) {
		forwarded = append(forwarded, progress)
	}), Invocation{Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.progress", Args: json.RawMessage(`{}`)})
	if bufferedResult.ExecutionOutcome != ExecutionSucceeded || len(forwarded) != 0 {
		t.Fatalf("buffered progress leaked: result=%+v forwarded=%+v", bufferedResult, forwarded)
	}
	if err := bufferedRegistry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	bufferedEvents := bufferedObserver.Events()
	if len(bufferedEvents) != 1 || bufferedEvents[0].Sensitive == nil {
		t.Fatalf("internal buffered progress observation = %+v", bufferedEvents)
	}
}

func TestPreparedExternalProgressUsesResultProjectionPolicy(t *testing.T) {
	registry := lifecycle.NewRegistry()
	observer := &testLifecycleObserver{}
	if err := registry.RegisterTransformer(lifecycle.Registration{
		ID: "external-result-filter", Kind: lifecycle.KindTransformer,
		Phases:       []lifecycle.Phase{lifecycle.PhaseToolResultBeforeCommit},
		Capabilities: []lifecycle.Capability{lifecycle.CapabilityTransform},
	}, noopRuntimeTransformer{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterObserver(lifecycle.Registration{
		ID: "external-progress", Kind: lifecycle.KindObserver, Phases: []lifecycle.Phase{lifecycle.PhaseToolProgress},
	}, observer); err != nil {
		t.Fatal(err)
	}
	definition := Tool{Name: "remote.progress", Parameters: &ObjectSchema{}}
	prepared, terminal := NewToolRuntime(&allowAuthorizer{}, registry).PrepareExternal(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: definition.Name, Args: json.RawMessage(`{}`), RunID: "run-progress",
	}, definition, testExternalDigest)
	if prepared == nil {
		t.Fatalf("prepare external: %+v", terminal)
	}
	if prepared.ObserveProgress(context.Background(), Progress{Kind: "stdout", Data: "remote-secret"}) {
		t.Fatal("external progress was allowed through a result projection boundary")
	}
	result := prepared.Complete(context.Background(), Result{
		ExecutionOutcome: ExecutionSucceeded, DeliveryOutcome: lifecycle.OutcomeSucceeded, Output: "done",
	})
	if result.ExecutionOutcome != ExecutionSucceeded {
		t.Fatalf("external result = %+v", result)
	}
	if err := registry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := observer.Events()
	if len(events) != 1 || events[0].OutputLength != len("remote-secret") || events[0].Sensitive != nil {
		t.Fatalf("external redacted progress = %+v", events)
	}
}

func TestToolRuntimeRejectsSynchronousHookReentry(t *testing.T) {
	registry := lifecycle.NewRegistry()
	guard := &reentrantRuntimeGuard{}
	if err := registry.RegisterGuard(lifecycle.Registration{
		ID: "reentrant", Kind: lifecycle.KindGuard, Phases: []lifecycle.Phase{lifecycle.PhaseToolBeforeExecute},
	}, guard); err != nil {
		t.Fatal(err)
	}
	runtime := NewToolRuntime(&allowAuthorizer{}, registry)
	guard.runtime = runtime
	outer := runtime.Execute(context.Background(), Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo", Args: json.RawMessage(`{"message":"outer"}`),
	})
	if outer.ExecutionOutcome != ExecutionSucceeded || guard.calls != 1 {
		t.Fatalf("outer execution=%+v guard calls=%d", outer, guard.calls)
	}
	if guard.inner.ErrorCode != "hook_reentrancy" || guard.inner.ExecutionOutcome != ExecutionFailed {
		t.Fatalf("inner reentrant execution = %+v", guard.inner)
	}
}

type allowAuthorizer struct{}

func (a *allowAuthorizer) Authorize(ctx context.Context, frozen FrozenInvocation) Authorization {
	return Authorization{Allowed: true, Decision: "allow"}
}

type captureAuthorizer struct{ frozen FrozenInvocation }

func (a *captureAuthorizer) Authorize(_ context.Context, frozen FrozenInvocation) Authorization {
	a.frozen = cloneFrozenInvocation(frozen)
	return Authorization{Allowed: true, Decision: "allow", PolicyVersion: "test"}
}

type failingRuntimeAuditor struct{}

func (failingRuntimeAuditor) ID() string { return "terminal-failure" }

func (failingRuntimeAuditor) Commit(context.Context, lifecycle.AuditMutation) error {
	return context.Canceled
}

type captureRuntimeAuditor struct {
	mu        sync.Mutex
	mutations []lifecycle.AuditMutation
}

func (*captureRuntimeAuditor) ID() string { return "denial-audit" }

func (a *captureRuntimeAuditor) Commit(_ context.Context, mutation lifecycle.AuditMutation) error {
	a.mu.Lock()
	a.mutations = append(a.mutations, mutation)
	a.mu.Unlock()
	return nil
}

func (a *captureRuntimeAuditor) Mutations() []lifecycle.AuditMutation {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]lifecycle.AuditMutation(nil), a.mutations...)
}

type noopRuntimeTransformer struct{}

func (noopRuntimeTransformer) ID() string { return "noop-result" }

func (noopRuntimeTransformer) Transform(_ context.Context, action lifecycle.MutableAction) (lifecycle.MutableAction, error) {
	return action, nil
}

type reentrantRuntimeGuard struct {
	runtime *ToolRuntime
	inner   Result
	calls   int
}

func (*reentrantRuntimeGuard) ID() string { return "reentrant" }

func (g *reentrantRuntimeGuard) Evaluate(ctx context.Context, _ lifecycle.FrozenAction) lifecycle.Verdict {
	g.calls++
	g.inner = g.runtime.Execute(ctx, Invocation{
		Meta: lifecycle.NewMeta(lifecycle.SourceMind), Tool: "test.echo", Args: json.RawMessage(`{"message":"inner"}`),
	})
	return lifecycle.VerdictAllow
}

type assertApprovalAuthorizer struct {
	sawRequired bool
}

func (a *assertApprovalAuthorizer) Authorize(_ context.Context, frozen FrozenInvocation) Authorization {
	a.sawRequired = frozen.ForceUserApproval
	return Authorization{Allowed: frozen.ForceUserApproval, Decision: "allow"}
}

type replaceArgsTransformer struct{}

func (*replaceArgsTransformer) ID() string { return "replace" }

func (*replaceArgsTransformer) Transform(_ context.Context, action lifecycle.MutableAction) (lifecycle.MutableAction, error) {
	action.Fields["args"] = json.RawMessage(`{"message":"after"}`)
	return action, nil
}

type renameToolTransformer struct{}

func (renameToolTransformer) ID() string { return "rename-tool" }

func (renameToolTransformer) Transform(_ context.Context, action lifecycle.MutableAction) (lifecycle.MutableAction, error) {
	action.Fields["tool"] = "test.rename_target"
	action.Fields["args"] = json.RawMessage(`{"count":2}`)
	return action, nil
}

type removeConfirmTransformer struct{}

func (removeConfirmTransformer) ID() string { return "remove-confirm" }

func (removeConfirmTransformer) Transform(_ context.Context, action lifecycle.MutableAction) (lifecycle.MutableAction, error) {
	action.Fields["args"] = json.RawMessage(`{}`)
	return action, nil
}

type changeExternalRunTransformer struct{}

func (changeExternalRunTransformer) ID() string { return "change-external-run" }

func (changeExternalRunTransformer) Transform(_ context.Context, action lifecycle.MutableAction) (lifecycle.MutableAction, error) {
	action.Fields["run_id"] = "run-2"
	return action, nil
}

func testExternalDigest(FrozenInvocation) (string, error) {
	return "sha256:test-external-contract", nil
}

type blockingGuard struct{}

func (blockingGuard) ID() string { return "slow" }

func (blockingGuard) Evaluate(ctx context.Context, _ lifecycle.FrozenAction) lifecycle.Verdict {
	<-ctx.Done()
	return lifecycle.VerdictAllow
}

type panicObserver struct{}

func (panicObserver) ID() string { return "panic" }

func (panicObserver) Observe(context.Context, lifecycle.RedactedEvent) { panic("observer") }

type testGuard struct {
	id      string
	verdict lifecycle.Verdict
}

func (g *testGuard) ID() string { return g.id }
func (g *testGuard) Evaluate(ctx context.Context, action lifecycle.FrozenAction) lifecycle.Verdict {
	return g.verdict
}

type testLifecycleObserver struct {
	mu     sync.Mutex
	events []lifecycle.RedactedEvent
}

func (o *testLifecycleObserver) ID() string { return "test-lifecycle-observer" }
func (o *testLifecycleObserver) Observe(ctx context.Context, event lifecycle.RedactedEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *testLifecycleObserver) Events() []lifecycle.RedactedEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]lifecycle.RedactedEvent, len(o.events))
	copy(result, o.events)
	return result
}
