package agentcore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type serializedLLM struct {
	active atomic.Int32
	max    atomic.Int32
}

func (s *serializedLLM) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	active := s.active.Add(1)
	for {
		current := s.max.Load()
		if active <= current || s.max.CompareAndSwap(current, active) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	s.active.Add(-1)
	return &llm.LLMResponse{Content: "ok"}, nil
}

type allowAlwaysApprover struct{}

func (allowAlwaysApprover) Confirm(string, string) ConfirmResult { return ConfirmAllowAlways }

type reentrantApprover struct{ core *Core }

func (a reentrantApprover) Confirm(string, string) ConfirmResult {
	_ = a.core.SessionID()
	_ = a.core.SecurityMode()
	_ = a.core.ActiveHand()
	return ConfirmAllow
}

func TestCoreSerializesConcurrentChat(t *testing.T) {
	provider := &serializedLLM{}
	core, _ := New(provider, &stubExecutor{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := core.Chat(context.Background(), "hello"); err != nil {
				t.Errorf("Chat: %v", err)
			}
		}()
	}
	wg.Wait()
	if provider.max.Load() != 1 {
		t.Fatalf("concurrent LLM calls = %d, want 1", provider.max.Load())
	}
}

func TestCoreActorsIsolateModeHandAndApprovalCache(t *testing.T) {
	const toolName = "phase4_actor_confirm_tool"
	executor.Register(executor.Tool{Name: toolName, Check: func(json.RawMessage) (executor.Decision, string) {
		return executor.DecisionConfirm, "policy confirmation"
	}, Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
		return &executor.ToolResult{Success: true}
	}})
	first, _ := New(&stubLLM{}, &stubExecutor{})
	second, _ := New(&stubLLM{}, &stubExecutor{})
	_ = first.SetActiveHand("hand-a")
	_ = second.SetActiveHand("hand-b")
	first.SetApprover(allowAlwaysApprover{})
	if blocked, _ := first.CheckAndConfirm(toolName, json.RawMessage(`{}`), false); blocked {
		t.Fatal("first actor should cache approval")
	}
	first.SetApprover(nil)
	if blocked, _ := first.CheckAndConfirm(toolName, json.RawMessage(`{}`), false); blocked {
		t.Fatal("first actor cached approval was not reused")
	}
	if blocked, _ := first.CheckAndConfirm(toolName, json.RawMessage(`{}`), true); !blocked {
		t.Fatal("explicit one-shot confirmation was bypassed by autoAllow")
	}
	first.SetMode("strict")
	second.SetMode("trust")
	if blocked, _ := second.CheckAndConfirm(toolName, json.RawMessage(`{}`), true); !blocked {
		t.Fatal("approval cache leaked to second actor")
	}
	if first.SecurityMode() != "strict" || second.SecurityMode() != "trust" {
		t.Fatal("security modes leaked between actors")
	}
	if first.ActiveHand() != "hand-a" || second.ActiveHand() != "hand-b" {
		t.Fatal("active Hand leaked between actors")
	}
	if decision, _ := first.policy.Check("ls"); decision == security.Allow {
		t.Fatal("strict actor policy was not retained")
	}
	if decision, _ := second.policy.Check("ls"); decision != security.Allow {
		t.Fatal("trust actor policy was not retained")
	}
}

func TestCoreActorsPersistToTheirOwnSessions(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "actors.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"session-a", "session-b"} {
		if err := db.CreateSession(group.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := New(&stubLLM{}, &stubExecutor{})
	second, _ := New(&stubLLM{}, &stubExecutor{})
	if err := first.SetStore(db, "session-a"); err != nil {
		t.Fatal(err)
	}
	if err := second.SetStore(db, "session-b"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, item := range []struct {
		core  *Core
		input string
	}{{first, "message-a"}, {second, "message-b"}} {
		wg.Add(1)
		go func(core *Core, input string) {
			defer wg.Done()
			_, _ = core.Chat(context.Background(), input)
		}(item.core, item.input)
	}
	wg.Wait()
	firstMessages, _ := db.GetMessages("session-a")
	secondMessages, _ := db.GetMessages("session-b")
	if firstMessages[0].Content != "message-a" || secondMessages[0].Content != "message-b" {
		t.Fatalf("session persistence crossed: a=%+v b=%+v", firstMessages, secondMessages)
	}
}

func TestApproverCanReadCoreStateWithoutDeadlock(t *testing.T) {
	const toolName = "phase4_reentrant_confirm_tool"
	executor.Register(executor.Tool{Name: toolName, DefaultConfirm: true, Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
		return &executor.ToolResult{Success: true}
	}})
	core, _ := New(&stubLLM{}, &stubExecutor{})
	core.SetApprover(reentrantApprover{core: core})
	done := make(chan struct{})
	go func() {
		blocked, _ := core.CheckAndConfirm(toolName, json.RawMessage(`{}`), false)
		if blocked {
			t.Error("reentrant approver unexpectedly blocked")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reentrant approver deadlocked")
	}
}

func TestDefaultConfirmRequiresApprovalInTrustAndYolo(t *testing.T) {
	const toolName = "phase4_default_confirm_modes_tool"
	executor.Register(executor.Tool{Name: toolName, DefaultConfirm: true, Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
		return &executor.ToolResult{Success: true}
	}})
	for _, mode := range []string{"trust", "yolo"} {
		core, _ := New(&stubLLM{}, &stubExecutor{})
		core.SetMode(mode)
		if blocked, _ := core.CheckAndConfirm(toolName, json.RawMessage(`{}`), false); !blocked {
			t.Fatalf("DefaultConfirm was bypassed in %s mode", mode)
		}
		core.SetApprover(allowAlwaysApprover{})
		if blocked, _ := core.CheckAndConfirm(toolName, json.RawMessage(`{}`), false); blocked {
			t.Fatalf("DefaultConfirm approval failed in %s mode", mode)
		}
		core.SetApprover(nil)
		if blocked, _ := core.CheckAndConfirm(toolName, json.RawMessage(`{}`), false); !blocked {
			t.Fatalf("DefaultConfirm autoAllow bypassed confirmation in %s mode", mode)
		}
	}
}

func TestExecuteToolCannotBypassSecurity(t *testing.T) {
	const toolName = "phase4_manual_deny_tool"
	var executed atomic.Bool
	executor.Register(executor.Tool{
		Name: toolName,
		Check: func(json.RawMessage) (executor.Decision, string) {
			return executor.DecisionDeny, "hard deny"
		},
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			executed.Store(true)
			return &executor.ToolResult{Success: true}
		},
	})
	core, _ := New(&stubLLM{}, &stubExecutor{})
	result := core.ExecuteTool(context.Background(), toolName, json.RawMessage(`{}`))
	if result.Error == "" || executed.Load() {
		t.Fatalf("manual execution bypassed security: %+v", result)
	}
}

func TestRemoteOnlyToolRequiresExplicitApproval(t *testing.T) {
	core, _ := New(&stubLLM{}, &stubExecutor{})
	if blocked, _ := core.CheckAndConfirm("remote_only_tool", json.RawMessage(`{}`), false); !blocked {
		t.Fatal("unknown local tool was allowed without remote confirmation")
	}
	core.SetApprover(allowAlwaysApprover{})
	if blocked, _ := core.CheckAndConfirm("remote_only_tool", json.RawMessage(`{}`), true); blocked {
		t.Fatal("approved remote-only tool was blocked")
	}
	core.SetApprover(nil)
	if blocked, _ := core.CheckAndConfirm("remote_only_tool", json.RawMessage(`{}`), true); !blocked {
		t.Fatal("remote-only approval was incorrectly cached")
	}
}
