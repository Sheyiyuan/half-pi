package lifecycle

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
)

func TestCoordinatorCreation(t *testing.T) {
	reg := corelifecycle.NewRegistry()
	c, err := NewCoordinator(Config{Registry: reg, Bus: nil})
	if err != nil {
		t.Fatal(err)
	}

	if c.Registry == nil {
		t.Fatal("registry should not be nil")
	}
}

func TestCoordinatorNilRegistry(t *testing.T) {
	c, err := NewCoordinator(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Registry == nil {
		t.Fatal("coordinator should create default registry")
	}
}

func TestCoordinatorRegistersAdapterWithBus(t *testing.T) {
	reg := corelifecycle.NewRegistry()
	// 直接注册 adapter 测试 EventBus adapter 行为
	adapter := &eventBusAdapter{}
	reg.RegisterObserver(corelifecycle.Registration{
		ID: "test-adapter", Kind: corelifecycle.KindObserver,
		Phases: []corelifecycle.Phase{corelifecycle.PhaseToolFinished},
	}, adapter)

	meta := corelifecycle.NewMeta(corelifecycle.SourceMind)
	observers := reg.ObserversForPhase(corelifecycle.PhaseToolFinished, meta)
	if len(observers) == 0 {
		t.Error("expected at least 1 observer")
	}
}

func TestEventBusAdapter(t *testing.T) {
	adapter := &eventBusAdapter{bus: nil}
	if adapter.ID() != "eventbus-adapter" {
		t.Errorf("adapter ID mismatch: %s", adapter.ID())
	}

	// nil bus 时不应 panic
	adapter.Observe(context.Background(), corelifecycle.RedactedEvent{
		Phase: corelifecycle.PhaseToolFinished,
	})
}

func TestMindAuthorizerDeny(t *testing.T) {
	testRegisterToolWithPolicy(t, "exec_command", &coreexec.ObjectSchema{
		Properties: []coreexec.PropertySchema{{Name: "command", Type: "string"}},
		Required:   []string{"command"},
	}, func(args json.RawMessage, policy *security.Policy) (coreexec.Decision, string) {
		var m map[string]any
		json.Unmarshal(args, &m)
		cmd, _ := m["command"].(string)
		dec, reason := policy.Check(cmd)
		return coreexec.Decision(dec), reason
	})

	policy := security.New()
	policy.Mode = security.ModeStrict

	a := NewMindAuthorizer("strict", policy, nil)

	meta := corelifecycle.NewMeta(corelifecycle.SourceMind)
	frozen := coreexec.FrozenInvocation{
		Meta:       meta,
		Tool:       "exec_command",
		Args:       json.RawMessage(`{"command":"rm -rf /tmp/test"}`),
		ArgsDigest: "sha256:test",
	}

	result := a.Authorize(context.Background(), frozen)
	if result.Allowed {
		t.Errorf("strict mode should deny exec_command, got: allowed=%v decision=%s reason=%s", result.Allowed, result.Decision, result.Reason)
	}
}

func TestMindAuthorizerAllow(t *testing.T) {
	testRegisterTool(t, "read_file", &coreexec.ObjectSchema{
		Properties: []coreexec.PropertySchema{{Name: "path", Type: "string"}},
		Required:   []string{"path"},
	})

	policy := security.New()
	policy.Mode = security.ModeYOLO

	a := NewMindAuthorizer("yolo", policy, nil)

	meta := corelifecycle.NewMeta(corelifecycle.SourceMind)
	frozen := coreexec.FrozenInvocation{
		Meta:       meta,
		Tool:       "read_file",
		ArgsDigest: "sha256:test",
	}

	result := a.Authorize(context.Background(), frozen)
	if !result.Allowed {
		t.Errorf("yolo mode should allow read_file, got: allowed=%v decision=%s reason=%s", result.Allowed, result.Decision, result.Reason)
	}
}

func TestConcurrentAuthorizerAccess(t *testing.T) {
	testRegisterTool(t, "read_file", &coreexec.ObjectSchema{
		Properties: []coreexec.PropertySchema{{Name: "path", Type: "string"}},
		Required:   []string{"path"},
	})

	policy := security.New()
	policy.Mode = security.ModeNormal
	a := NewMindAuthorizer("normal", policy, nil)

	meta := corelifecycle.NewMeta(corelifecycle.SourceMind)
	frozen := coreexec.FrozenInvocation{
		Meta:       meta,
		Tool:       "read_file",
		ArgsDigest: "sha256:test",
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.Authorize(context.Background(), frozen)
		}()
	}
	wg.Wait()
}

func TestAllObservationPhases(t *testing.T) {
	phases := allObservationPhases()
	if len(phases) == 0 {
		t.Error("observation phases should not be empty")
	}

	// 验证所有观察阶段都已定义
	for _, p := range phases {
		if p == "" {
			t.Error("phase should not be empty")
		}
	}
}

func TestEventBusAdapterUnmappedPhase(t *testing.T) {
	// 未映射的阶段不应 panic
	adapter := &eventBusAdapter{bus: nil}
	adapter.Observe(context.Background(), corelifecycle.RedactedEvent{
		Phase: corelifecycle.PhaseChatReceived,
	})
}

func testRegisterTool(t *testing.T, name string, schema *coreexec.ObjectSchema) {
	t.Helper()
	if _, ok := coreexec.FindTool(name); ok {
		return
	}
	coreexec.Register(coreexec.Tool{
		Name:        name,
		Description: "test tool: " + name,
		Parameters:  schema,
		Execute: func(ctx context.Context, args json.RawMessage) *coreexec.ToolResult {
			return &coreexec.ToolResult{Success: true, Output: "ok"}
		},
	})
}

func testRegisterToolWithPolicy(t *testing.T, name string, schema *coreexec.ObjectSchema, policyCheck coreexec.ToolPolicyCheck) {
	t.Helper()
	if _, ok := coreexec.FindTool(name); ok {
		return
	}
	coreexec.Register(coreexec.Tool{
		Name:        name,
		Description: "test tool: " + name,
		Parameters:  schema,
		PolicyCheck: policyCheck,
		Execute: func(ctx context.Context, args json.RawMessage) *coreexec.ToolResult {
			return &coreexec.ToolResult{Success: true, Output: "ok"}
		},
	})
}

func TestPolicyMode(t *testing.T) {
	tests := []struct {
		mode security.Mode
		want string
	}{
		{security.ModeStrict, "strict"},
		{security.ModeNormal, "normal"},
		{security.ModeTrust, "review"},
		{security.ModeYOLO, "yolo"},
	}

	for _, tt := range tests {
		p := security.New()
		p.Mode = tt.mode
		got := PolicyMode(p)
		if got != tt.want {
			t.Errorf("PolicyMode(%v) = %s, want %s", tt.mode, got, tt.want)
		}
	}
}
