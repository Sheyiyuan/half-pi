package agentcore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestMessageGuardDenialPreventsPersistenceAndProviderCall(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "unused"}})
	core, db := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	if err := registry.RegisterGuard(corelifecycle.Registration{
		ID: "deny-message", Kind: corelifecycle.KindGuard,
		Phases: []corelifecycle.Phase{corelifecycle.PhaseMessageBeforeAccept},
	}, lifecycleGuard{verdict: corelifecycle.VerdictDeny}); err != nil {
		t.Fatal(err)
	}
	core.SetLifecycleRegistry(registry)
	if _, err := core.Chat(context.Background(), "blocked"); err == nil {
		t.Fatal("denied message was accepted")
	}
	if provider.Calls() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.Calls())
	}
	messages, err := db.GetMessages("lifecycle-session")
	if err != nil || len(messages) != 0 {
		t.Fatalf("persisted denied message: %#v, err=%v", messages, err)
	}
}

func TestAssistantTransformerForcesBufferedDelivery(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{
		Response: llm.LLMResponse{Content: "secret"}, Deltas: []string{"sec", "ret"},
	})
	core, db := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	if err := registry.RegisterTransformer(corelifecycle.Registration{
		ID: "redact-response", Kind: corelifecycle.KindTransformer,
		Phases:       []corelifecycle.Phase{corelifecycle.PhaseAssistantBeforeDeliver},
		Capabilities: []corelifecycle.Capability{corelifecycle.CapabilityTransform},
	}, lifecycleTransformer{transform: func(action corelifecycle.MutableAction) corelifecycle.MutableAction {
		action.Fields["content"] = "public"
		return action
	}}); err != nil {
		t.Fatal(err)
	}
	core.SetLifecycleRegistry(registry)
	var deltas []string
	var completed string
	reply, err := core.ChatWithTransport(context.Background(), "hello", ChatTransport{
		TextDelta: func(delta ChatTextDelta) error {
			deltas = append(deltas, delta.Delta)
			return nil
		},
		ResponseCompleted: func(response ChatResponse) error {
			completed = response.Content
			return nil
		},
	})
	if err != nil || reply != "public" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if len(deltas) != 1 || deltas[0] != "public" || completed != "public" {
		t.Fatalf("unreviewed stream escaped: deltas=%v completed=%q", deltas, completed)
	}
	messages, _ := db.GetMessages("lifecycle-session")
	if len(messages) != 2 || messages[1].Content != "public" {
		t.Fatalf("transformed assistant response was not persisted: %#v", messages)
	}
}

func TestAssistantGuardDenialDoesNotLeakOrPersistResponse(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{
		Response: llm.LLMResponse{Content: "secret"}, Deltas: []string{"secret"},
	})
	core, db := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	if err := registry.RegisterGuard(corelifecycle.Registration{
		ID: "deny-response", Kind: corelifecycle.KindGuard,
		Phases: []corelifecycle.Phase{corelifecycle.PhaseAssistantBeforeDeliver},
	}, lifecycleGuard{verdict: corelifecycle.VerdictDeny}); err != nil {
		t.Fatal(err)
	}
	core.SetLifecycleRegistry(registry)
	var deltas []string
	completed := false
	_, err := core.ChatWithTransport(context.Background(), "hello", ChatTransport{
		TextDelta: func(delta ChatTextDelta) error {
			deltas = append(deltas, delta.Delta)
			return nil
		},
		ResponseCompleted: func(ChatResponse) error {
			completed = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("denied assistant response was delivered")
	}
	if len(deltas) != 0 || completed {
		t.Fatalf("denied assistant response leaked: deltas=%v completed=%t", deltas, completed)
	}
	messages, _ := db.GetMessages("lifecycle-session")
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("denied assistant response was persisted: %#v", messages)
	}
}

func TestLifecycleEventIdentityIsUniqueAndOrdered(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "done"}})
	core, _ := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	observer := &lifecycleCollector{}
	if err := registry.RegisterObserver(corelifecycle.Registration{
		ID: "collector", Kind: corelifecycle.KindObserver,
		Phases: []corelifecycle.Phase{
			corelifecycle.PhaseChatReceived, corelifecycle.PhaseMessageAdmitted,
			corelifecycle.PhaseMessagePersisted, corelifecycle.PhaseModelRequested,
			corelifecycle.PhaseModelDelta, corelifecycle.PhaseModelResponseReceived,
			corelifecycle.PhaseAssistantDelivered, corelifecycle.PhaseChatFinished,
		},
	}, observer); err != nil {
		t.Fatal(err)
	}
	core.SetLifecycleRegistry(registry)
	if _, err := core.Chat(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := registry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := observer.snapshot()
	if len(events) < 8 {
		t.Fatalf("lifecycle events = %d, want at least 8", len(events))
	}
	ids := make(map[string]struct{}, len(events))
	var previous int64
	traceID := events[0].TraceID
	for _, event := range events {
		if event.EventID == "" || event.TraceID != traceID {
			t.Fatalf("invalid event identity: %#v", event.Meta)
		}
		if _, exists := ids[event.EventID]; exists {
			t.Fatalf("duplicate event ID %q", event.EventID)
		}
		ids[event.EventID] = struct{}{}
		if event.Sequence <= previous {
			t.Fatalf("sequence did not increase: %d after %d", event.Sequence, previous)
		}
		previous = event.Sequence
	}
}

func TestAdmissionAuditFailureFailsClosed(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "unused"}})
	core, db := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	if err := registry.RegisterAuditor(corelifecycle.Registration{
		ID: "failing-auditor", Kind: corelifecycle.KindAuditor,
		Phases:      []corelifecycle.Phase{corelifecycle.PhaseMessageAdmitted},
		FailureMode: corelifecycle.FailureFailClosed,
	}, failingLifecycleAuditor{}); err != nil {
		t.Fatal(err)
	}
	core.SetLifecycleRegistry(registry)
	if _, err := core.Chat(context.Background(), "blocked"); err == nil {
		t.Fatal("audit failure did not fail closed")
	}
	if provider.Calls() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.Calls())
	}
	messages, _ := db.GetMessages("lifecycle-session")
	if len(messages) != 0 {
		t.Fatalf("message persisted after audit failure: %#v", messages)
	}
}

func TestChatRejectsSynchronousLifecycleHookReentry(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "done"}})
	core, _ := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	transformer := &reentrantChatTransformer{core: core}
	if err := registry.RegisterTransformer(corelifecycle.Registration{
		ID: "reentrant-chat", Kind: corelifecycle.KindTransformer,
		Phases:       []corelifecycle.Phase{corelifecycle.PhaseMessageBeforeAccept},
		Capabilities: []corelifecycle.Capability{corelifecycle.CapabilityTransform},
	}, transformer); err != nil {
		t.Fatal(err)
	}
	if err := core.SetLifecycleRegistry(registry); err != nil {
		t.Fatal(err)
	}
	reply, err := core.Chat(context.Background(), "outer")
	if err != nil || reply != "done" {
		t.Fatalf("outer Chat reply=%q err=%v", reply, err)
	}
	if transformer.reentryErr == nil || provider.Calls() != 1 {
		t.Fatalf("reentry error=%v provider calls=%d", transformer.reentryErr, provider.Calls())
	}
}

func TestAssistantDeliveredFollowsPersistenceAndTransport(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "visible"}})
	core, db := newLifecycleChatCore(t, provider)
	core.SetModelIdentity("provider-test", "model-test")
	registry := corelifecycle.NewRegistry()
	observer := &lifecycleCollector{}
	if err := registry.RegisterObserver(corelifecycle.Registration{
		ID: "delivered-order", Kind: corelifecycle.KindObserver,
		Phases: []corelifecycle.Phase{corelifecycle.PhaseAssistantDelivered},
	}, observer); err != nil {
		t.Fatal(err)
	}
	if err := core.SetLifecycleRegistry(registry); err != nil {
		t.Fatal(err)
	}
	transportCalled := false
	_, err := core.ChatWithTransport(context.Background(), "hello", ChatTransport{
		ResponseCompleted: func(response ChatResponse) error {
			transportCalled = true
			messages, loadErr := db.GetMessages("lifecycle-session")
			if loadErr != nil {
				return loadErr
			}
			if len(messages) != 2 || messages[1].Content != response.Content {
				return errors.New("assistant response was transported before persistence")
			}
			return nil
		},
	})
	if err != nil || !transportCalled {
		t.Fatalf("Chat err=%v transportCalled=%t", err, transportCalled)
	}
	if err := registry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := observer.snapshot()
	if len(events) != 1 || events[0].ProviderID != "provider-test" || events[0].ModelID != "model-test" ||
		events[0].InputDigest != corelifecycle.HashString("visible") {
		t.Fatalf("assistant delivered event = %+v", events)
	}
}

func TestFaceRequestIdentityEntersLifecycleMeta(t *testing.T) {
	provider := llm.NewScriptedProvider(llm.ScriptedStep{Response: llm.LLMResponse{Content: "done"}})
	core, _ := newLifecycleChatCore(t, provider)
	registry := corelifecycle.NewRegistry()
	observer := &lifecycleCollector{}
	if err := registry.RegisterObserver(corelifecycle.Registration{
		ID: "face-meta", Kind: corelifecycle.KindObserver, Phases: []corelifecycle.Phase{corelifecycle.PhaseChatReceived},
	}, observer); err != nil {
		t.Fatal(err)
	}
	if err := core.SetLifecycleRegistry(registry); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithRequestID(context.Background(), "request-face")
	ctx = requestctx.WithPrincipalID(ctx, "principal-face")
	ctx = requestctx.WithSource(ctx, corelifecycle.SourceFace)
	if _, err := core.Chat(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := registry.FlushObservers(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := observer.snapshot()
	if len(events) != 1 || events[0].Source != corelifecycle.SourceFace || events[0].PrincipalID != "principal-face" ||
		events[0].RequestID != "request-face" || events[0].GroupID == "" || events[0].ConversationID != "lifecycle-session" {
		t.Fatalf("Face lifecycle meta = %+v", events)
	}
}

func newLifecycleChatCore(t *testing.T, provider llm.Provider) (*Core, *store.Store) {
	t.Helper()
	db, err := store.New(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(group.ID, "lifecycle-session"); err != nil {
		t.Fatal(err)
	}
	core, err := New(provider, &stubExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetStore(db, "lifecycle-session"); err != nil {
		t.Fatal(err)
	}
	return core, db
}

type lifecycleGuard struct{ verdict corelifecycle.Verdict }

func (lifecycleGuard) ID() string { return "test-guard" }

func (g lifecycleGuard) Evaluate(context.Context, corelifecycle.FrozenAction) corelifecycle.Verdict {
	return g.verdict
}

type lifecycleTransformer struct {
	transform func(corelifecycle.MutableAction) corelifecycle.MutableAction
}

type reentrantChatTransformer struct {
	core       *Core
	reentryErr error
}

func (*reentrantChatTransformer) ID() string { return "reentrant-chat" }

func (t *reentrantChatTransformer) Transform(ctx context.Context, action corelifecycle.MutableAction) (corelifecycle.MutableAction, error) {
	_, t.reentryErr = t.core.Chat(ctx, "inner")
	return action, nil
}

func (lifecycleTransformer) ID() string { return "test-transformer" }

func (t lifecycleTransformer) Transform(_ context.Context, action corelifecycle.MutableAction) (corelifecycle.MutableAction, error) {
	return t.transform(action), nil
}

type failingLifecycleAuditor struct{}

func (failingLifecycleAuditor) ID() string { return "failing-auditor" }

func (failingLifecycleAuditor) Commit(context.Context, corelifecycle.AuditMutation) error {
	return errors.New("audit unavailable")
}

type lifecycleCollector struct {
	mu     sync.Mutex
	events []corelifecycle.RedactedEvent
}

func (*lifecycleCollector) ID() string { return "collector" }

func (c *lifecycleCollector) Observe(_ context.Context, event corelifecycle.RedactedEvent) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

func (c *lifecycleCollector) snapshot() []corelifecycle.RedactedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]corelifecycle.RedactedEvent(nil), c.events...)
}
