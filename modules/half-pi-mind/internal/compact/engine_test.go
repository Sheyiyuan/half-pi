package compact

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type mutableEnvironment struct {
	mu       sync.RWMutex
	snapshot EnvironmentSnapshot
}

func (environment *mutableEnvironment) Snapshot(context.Context, string) (EnvironmentSnapshot, error) {
	environment.mu.RLock()
	defer environment.mu.RUnlock()
	result := environment.snapshot
	result.Tools = append([]llm.ToolDef(nil), result.Tools...)
	return result, nil
}

func (environment *mutableEnvironment) change() {
	environment.mu.Lock()
	environment.snapshot.Revision++
	environment.snapshot.Digest = digestDomain("environment", newUUIDv7())
	environment.mu.Unlock()
}

func TestEngineCompactsAndReusesExplicitRebaseContract(t *testing.T) {
	db, sessionID := newEngineStore(t)
	environment := &mutableEnvironment{snapshot: EnvironmentSnapshot{
		System: "system", Revision: 1, Digest: digestDomain("environment", "one"),
	}}
	var calls atomic.Int64
	provider := providerFunc(func(_ context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
		calls.Add(1)
		if request.System != summarySystemPrompt || len(request.Messages) != 1 || len(request.Tools) != 0 {
			t.Fatalf("summary request was not isolated: %+v", request)
		}
		return &llm.LLMResponse{Content: `{"summary":"stable summary"}`, Usage: llm.Usage{InputTokens: 30, OutputTokens: 4}}, nil
	})
	engine, err := New(engineRuntimeConfig(), provider, db, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), Principal: "repl", Target: DefaultTarget{}, Trigger: TriggerManual,
	}
	first, err := engine.Compact(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ToSeq != 2 || first.GenerationMode != "full" || first.Reused || first.AfterEstimatedTokens >= first.BeforeEstimatedTokens {
		t.Fatalf("first result = %+v", first)
	}
	messages, err := db.GetMessages(sessionID)
	if err != nil || len(messages) != 4 {
		t.Fatalf("raw messages changed: %+v err=%v", messages, err)
	}

	rebaseRequest := CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), Principal: "repl", Target: RebaseTarget{}, Trigger: TriggerManual,
	}
	rebased, err := engine.Compact(context.Background(), rebaseRequest)
	if err != nil || rebased.GenerationMode != "rebase" || rebased.Reused {
		t.Fatalf("rebase result = %+v, err=%v", rebased, err)
	}
	reused, err := engine.Compact(context.Background(), rebaseRequest)
	if err != nil || !reused.Reused || reused.SummaryID != rebased.SummaryID {
		t.Fatalf("reused result = %+v, err=%v", reused, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", calls.Load())
	}
	status, err := engine.Status(context.Background(), CompactStatusRequest{SessionID: sessionID, OperationState: "idle"})
	if err != nil || status.ActiveSummaryID != rebased.SummaryID || status.SummaryNodeCount != 2 || status.Degraded || status.ContextVersion == 0 {
		t.Fatalf("status = %+v, err=%v", status, err)
	}
}

func TestEngineAutomaticRateLimitKeepsPendingAndCooldown(t *testing.T) {
	db, sessionID := newEngineStore(t)
	environment := &mutableEnvironment{snapshot: EnvironmentSnapshot{System: "system", Revision: 1, Digest: "env-1"}}
	provider := providerFunc(func(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
		return nil, &llm.ProviderError{Category: llm.ProviderErrorRateLimited, StatusCode: 429, RetryAfter: 2 * time.Second}
	})
	engine, err := New(engineRuntimeConfig(), provider, db, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	pendingID := newTestUUIDv7(t)
	if _, err := db.EnsureCompactPending(context.Background(), sessionID, pendingID,
		newLifecycleEvent("compact.requested", sessionID, compactRequestedPayload{Trigger: TriggerAutomatic})); err != nil {
		t.Fatal(err)
	}
	_, err = engine.Compact(context.Background(), CompactRequest{
		SessionID: sessionID, RequestID: pendingID, Target: DefaultTarget{}, Trigger: TriggerAutomatic,
		Pending: store.PendingExpectation{ID: pendingID, Attempt: 0, Required: true},
	})
	if errorCodeOf(err) != ErrRateLimited {
		t.Fatalf("error = %v", err)
	}
	runtime, err := db.GetSessionRuntime(context.Background(), sessionID)
	if err != nil || !runtime.PendingCompact || runtime.PendingCompactID != pendingID || runtime.PendingAttempt != 1 ||
		runtime.PendingNotBefore <= time.Now().UnixMilli() {
		t.Fatalf("runtime = %+v, err=%v", runtime, err)
	}
	snapshot, err := db.GetCompactSnapshot(context.Background(), sessionID)
	if err != nil || snapshot.SummaryNodeCount != 0 || snapshot.Runtime.ActiveSummaryID != "" {
		t.Fatalf("snapshot = %+v, err=%v", snapshot, err)
	}
}

func TestEngineRejectsEnvironmentChangeAfterProviderCall(t *testing.T) {
	db, sessionID := newEngineStore(t)
	environment := &mutableEnvironment{snapshot: EnvironmentSnapshot{System: "system", Revision: 1, Digest: "env-1"}}
	provider := providerFunc(func(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
		environment.change()
		return &llm.LLMResponse{Content: `{"summary":"summary"}`}, nil
	})
	engine, err := New(engineRuntimeConfig(), provider, db, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Compact(context.Background(), CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), Principal: "repl",
		Target: DefaultTarget{}, Trigger: TriggerManual,
	})
	if errorCodeOf(err) != ErrConflict {
		t.Fatalf("error = %v", err)
	}
	snapshot, err := db.GetCompactSnapshot(context.Background(), sessionID)
	if err != nil || snapshot.SummaryNodeCount != 0 || snapshot.Runtime.ActiveSummaryID != "" || snapshot.Runtime.CompactGeneration != 0 {
		t.Fatalf("snapshot after conflict = %+v, err=%v", snapshot, err)
	}
}

func TestEngineManualRateLimitDoesNotCreateCooldown(t *testing.T) {
	db, sessionID := newEngineStore(t)
	environment := &mutableEnvironment{snapshot: EnvironmentSnapshot{System: "system", Revision: 1, Digest: "env-1"}}
	provider := providerFunc(func(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
		return nil, &llm.ProviderError{Category: llm.ProviderErrorRateLimited, StatusCode: 429, RetryAfter: 2 * time.Second}
	})
	engine, err := New(engineRuntimeConfig(), provider, db, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Compact(context.Background(), CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), Principal: "repl",
		Target: DefaultTarget{}, Trigger: TriggerManual,
	})
	var compactErr *Error
	if !errors.As(err, &compactErr) || compactErr.Code != ErrRateLimited || compactErr.RetryNotBefore.IsZero() {
		t.Fatalf("error = %#v", err)
	}
	runtime, err := db.GetSessionRuntime(context.Background(), sessionID)
	if err != nil || runtime.PendingCompact || runtime.PendingNotBefore != 0 || runtime.PendingAttempt != 0 {
		t.Fatalf("runtime = %+v, err=%v", runtime, err)
	}
}

func TestEngineLifecycleEventsShareOperationTrace(t *testing.T) {
	db, sessionID := newEngineStore(t)
	environment := &mutableEnvironment{snapshot: EnvironmentSnapshot{System: "system", Revision: 1, Digest: "env-1"}}
	engine, err := New(engineRuntimeConfig(), providerFunc(func(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
		return &llm.LLMResponse{Content: `{"summary":"summary"}`}, nil
	}), db, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	traceID := newTestUUIDv7(t)
	if _, err := engine.Compact(context.Background(), CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), TraceID: traceID, Principal: "repl",
		Target: DefaultTarget{}, Trigger: TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := db.DueOutbox(context.Background(), time.Now().Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"compact.requested": false, "compact.started": false, "compact.completed": false}
	for _, event := range events {
		if _, ok := wanted[event.EventType]; !ok {
			continue
		}
		wanted[event.EventType] = true
		if event.TraceID != traceID {
			t.Fatalf("%s trace = %q, want %q", event.EventType, event.TraceID, traceID)
		}
	}
	for eventType, found := range wanted {
		if !found {
			t.Fatalf("missing %s event", eventType)
		}
	}
}

func newEngineStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	db, err := store.New(filepath.Join(t.TempDir(), "compact-engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := newTestUUIDv7(t)
	if err := db.CreateSession(group.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendMessages(sessionID, 0, []store.Message{
		{Role: "user", Content: strings.Repeat("first context ", 300), RequestID: "request-1", Seq: 1},
		{Role: "assistant", Content: strings.Repeat("first response ", 300), RequestID: "request-1", Seq: 2},
		{Role: "user", Content: strings.Repeat("current context ", 300), RequestID: "request-2", Seq: 3},
		{Role: "assistant", Content: strings.Repeat("current response ", 300), RequestID: "request-2", Seq: 4},
	}); err != nil {
		t.Fatal(err)
	}
	return db, sessionID
}

func engineRuntimeConfig() RuntimeConfig {
	cfg := testRuntimeConfig()
	cfg.MainContextWindow = 10_000
	cfg.SummaryContextWindow = 10_000
	cfg.RateLimitMaxBackoff = time.Minute
	return cfg
}

func newTestUUIDv7(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
