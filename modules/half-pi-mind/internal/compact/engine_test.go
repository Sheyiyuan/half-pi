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

func TestEngineWorstCandidateUsesSummaryBodyReserveWithoutMutatingCandidate(t *testing.T) {
	cfg := engineRuntimeConfig()
	environment := EnvironmentSnapshot{System: "system", Revision: 1, Digest: "env-1"}
	engine := &Engine{cfg: cfg, estimator: TokenEstimator{}}
	messages := []store.Message{
		{Role: "user", Content: "current", Seq: 1},
		{Role: "assistant", Content: "answer", Seq: 2},
	}
	candidate := &store.ContextSummary{
		FromSeq: 1, ToSeq: 2, Summary: "original summary body",
		ProjectionVersion: ProjectionVersion, PolicyVersion: cfg.PolicyVersion, Profile: cfg.Profile,
	}
	got, err := engine.estimateWorstCandidate(context.Background(), messages, candidate, environment)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Summary != "original summary body" {
		t.Fatalf("candidate summary mutated to %q", candidate.Summary)
	}
	probe := *candidate
	probe.Summary = ""
	request, err := engine.mainRequest(context.Background(), messages, &probe, environment)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.estimator.EstimateRequest(request) + summaryBodyReserveTokens(cfg)
	if got != want {
		t.Fatalf("worst candidate estimate = %d, want %d", got, want)
	}
}

func TestEngineUsesIncrementalSummaryWhenActiveParentCompatible(t *testing.T) {
	db, sessionID := newEngineStore(t)
	environment := &mutableEnvironment{snapshot: EnvironmentSnapshot{
		System: "system", Revision: 1, Digest: digestDomain("environment", "one"),
	}}
	var calls atomic.Int64
	provider := providerFunc(func(_ context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
		call := calls.Add(1)
		if len(request.Messages) != 1 || request.Messages[0].Role != llm.RoleUser {
			t.Fatalf("summary request = %+v", request)
		}
		content := request.Messages[0].Content
		switch call {
		case 1:
			if !strings.Contains(content, `"mode":"full"`) {
				t.Fatalf("first summary request was not full: %s", content)
			}
			return &llm.LLMResponse{Content: `{"summary":"parent summary"}`}, nil
		case 2:
			for _, required := range []string{
				`"mode":"incremental"`, `"summary":"parent summary"`, `"from_seq":3`, `"to_seq":4`,
			} {
				if !strings.Contains(content, required) {
					t.Fatalf("incremental request omitted %q: %s", required, content)
				}
			}
			return &llm.LLMResponse{Content: `{"summary":"incremental summary"}`}, nil
		default:
			t.Fatalf("unexpected summary provider call %d", call)
		}
		return nil, nil
	})
	engine, err := New(engineRuntimeConfig(), provider, db, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := engine.Compact(context.Background(), CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), Principal: "repl",
		Target: DefaultTarget{}, Trigger: TriggerManual,
	})
	if err != nil || first.GenerationMode != "full" || first.ToSeq != 2 {
		t.Fatalf("first compact = %+v, err=%v", first, err)
	}
	if err := db.AppendMessages(sessionID, 4, []store.Message{
		{Role: "user", Content: strings.Repeat("third context ", 300), RequestID: "request-3", Seq: 5},
		{Role: "assistant", Content: strings.Repeat("third response ", 300), RequestID: "request-3", Seq: 6},
	}); err != nil {
		t.Fatal(err)
	}
	second, err := engine.Compact(context.Background(), CompactRequest{
		SessionID: sessionID, RequestID: newTestUUIDv7(t), Principal: "repl",
		Target: DefaultTarget{}, Trigger: TriggerManual,
	})
	if err != nil || second.GenerationMode != "incremental" || second.ToSeq != 4 {
		t.Fatalf("second compact = %+v, err=%v", second, err)
	}
	snapshot, err := db.GetCompactSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveSummary == nil || snapshot.ActiveSummary.ParentSummaryID != first.SummaryID ||
		snapshot.ActiveSummary.Summary != "incremental summary" {
		t.Fatalf("active summary = %+v, first = %+v", snapshot.ActiveSummary, first)
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
