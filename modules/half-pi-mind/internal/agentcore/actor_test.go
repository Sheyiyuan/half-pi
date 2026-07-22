package agentcore

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
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

func (allowAlwaysApprover) Confirm(context.Context, approval.Request) approval.Resolution {
	return approval.Resolution{Decision: protocol.FaceApprovalAllowSession}
}

type reentrantApprover struct{ core *Core }

func (a reentrantApprover) Confirm(context.Context, approval.Request) approval.Resolution {
	_ = a.core.SessionID()
	_ = a.core.SecurityMode()
	_ = a.core.ActiveHand()
	return approval.Resolution{Decision: protocol.FaceApprovalAllowOnce}
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
	first.SetApprover(nil)
	first.SetMode("strict")
	second.SetMode("trust")
	if first.SecurityMode() != "strict" || second.SecurityMode() != "review" {
		t.Fatal("security modes leaked between actors")
	}
	if first.ActiveHand() != "hand-a" || second.ActiveHand() != "hand-b" {
		t.Fatal("active Hand leaked between actors")
	}
	if decision, _ := first.policy.Check("ls"); decision == security.Allow {
		t.Fatal("strict actor policy was not retained")
	}
	if decision, _ := second.policy.Check("ls"); decision != security.Allow {
		t.Fatal("review actor policy was not retained")
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
		_ = core
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reentrant approver deadlocked")
	}
}

func TestDefaultConfirmRequiresApprovalInReviewAndYolo(t *testing.T) {
	const toolName = "phase4_default_confirm_modes_tool"
	executor.Register(executor.Tool{Name: toolName, DefaultConfirm: true, Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
		return &executor.ToolResult{Success: true}
	}})
	for _, mode := range []string{"review", "yolo"} {
		core, _ := New(&stubLLM{}, &stubExecutor{})
		core.SetMode(mode)
		core.SetApprover(allowAlwaysApprover{})
		_ = core
		core.SetApprover(nil)
	}
}

func TestSetStoreMigratesLegacyTrustMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mode.db")
	db, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(group.ID, "legacy-session"); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE sessions SET mode = 'trust' WHERE id = 'legacy-session'`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	core, _ := New(&stubLLM{}, &stubExecutor{})
	if err := core.SetStore(db, "legacy-session"); err != nil {
		t.Fatal(err)
	}
	if core.SecurityMode() != "review" {
		t.Fatalf("loaded mode = %q, want review", core.SecurityMode())
	}
	session, err := db.GetSession("legacy-session")
	if err != nil {
		t.Fatal(err)
	}
	if session.Mode != "review" {
		t.Fatalf("persisted mode = %q, want review", session.Mode)
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
