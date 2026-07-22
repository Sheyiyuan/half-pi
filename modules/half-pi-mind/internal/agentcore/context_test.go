package agentcore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type fixedCompactEnvironment struct{}

func (fixedCompactEnvironment) Snapshot(context.Context, string) (compact.EnvironmentSnapshot, error) {
	return compact.EnvironmentSnapshot{System: "system", Revision: 1, Digest: "environment"}, nil
}

type captureProvider struct {
	request *llm.LLMRequest
	calls   atomic.Int64
}

type budgetRecorder struct {
	mu           sync.Mutex
	observations []BudgetObservation
	sessionID    string
}

func (recorder *budgetRecorder) ObserveModelBudget(_ context.Context, observation BudgetObservation) (bool, error) {
	recorder.mu.Lock()
	recorder.observations = append(recorder.observations, observation)
	recorder.mu.Unlock()
	return false, nil
}

func (recorder *budgetRecorder) PrepareToolBatchPending(_ context.Context, observation BudgetObservation) (*ToolBatchPending, error) {
	if recorder.sessionID == "" || observation.EstimatedTokens < observation.HighLimit {
		return nil, nil
	}
	pendingID, event := compact.NewAutomaticPendingMutation(recorder.sessionID)
	return &ToolBatchPending{ID: pendingID, Event: event}, nil
}

type oneToolProvider struct{ calls atomic.Int64 }

func (provider *oneToolProvider) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	if provider.calls.Add(1) != 1 {
		return &llm.LLMResponse{Content: "provider should not be called twice"}, nil
	}
	return &llm.LLMResponse{Content: "working", ToolCalls: []llm.ToolCall{{ID: "large-call", Name: "compact_large_output_test", Args: `{}`}}}, nil
}

type fixedToolCatalog struct{ tools []executor.Tool }

func (catalog fixedToolCatalog) Tools() []executor.Tool {
	return append([]executor.Tool(nil), catalog.tools...)
}

func (provider *captureProvider) Chat(_ context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
	provider.calls.Add(1)
	copy := *request
	copy.Messages = cloneMessages(request.Messages)
	provider.request = &copy
	return &llm.LLMResponse{Content: "done", Usage: llm.Usage{InputTokens: 80}}, nil
}

func TestChatUsesActiveSummaryViewAndKeepsRawHistoryAppendOnly(t *testing.T) {
	db, sessionID := newContextTestStore(t)
	if err := db.AppendMessages(sessionID, 0, []store.Message{
		{Role: "user", Content: strings.Repeat("old question ", 200), RequestID: "old-1", Seq: 1},
		{Role: "assistant", Content: strings.Repeat("old answer ", 200), RequestID: "old-1", Seq: 2},
		{Role: "user", Content: "recent question", RequestID: "old-2", Seq: 3},
		{Role: "assistant", Content: "recent answer", RequestID: "old-2", Seq: 4},
	}); err != nil {
		t.Fatal(err)
	}
	runtime := compact.RuntimeConfig{
		Enabled: true, MainProviderID: "main", MainModelID: "main", MainContextWindow: 10_000,
		MainMaxTokens: 512, ReservedOutputTokens: 512, SummaryProviderID: "summary", SummaryModelID: "summary",
		SummaryContextWindow: 10_000, SummaryModelMaxTokens: 512, Timeout: time.Second, MaxTokens: 128,
		HighWatermark: .8, LowWatermark: .6, ProviderMarginTokens: 128, MaxConcurrent: 1,
		RateLimitInitialBackoff: time.Second, RateLimitMaxBackoff: time.Minute,
		PolicyVersion: "compact-v1", Profile: "default",
	}
	engine, err := compact.New(runtime, providerFuncForContext(func(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
		return &llm.LLMResponse{Content: `{"summary":"old work summary"}`}, nil
	}), db, fixedCompactEnvironment{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestID, _ := uuid.NewV7()
	if _, err := engine.Compact(context.Background(), compact.CompactRequest{
		SessionID: sessionID, RequestID: requestID.String(), Principal: "test",
		Target: compact.DefaultTarget{}, Trigger: compact.TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}

	mainProvider := &captureProvider{}
	core, err := New(mainProvider, &stubExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetStore(db, sessionID); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithRequestID(context.Background(), "current-request")
	if _, err := core.Chat(ctx, "current question"); err != nil {
		t.Fatal(err)
	}
	if mainProvider.request == nil || len(mainProvider.request.Messages) != 5 {
		t.Fatalf("provider messages = %+v", mainProvider.request)
	}
	if mainProvider.request.Messages[0].Role != llm.RoleUser ||
		mainProvider.request.Messages[1].Role != llm.RoleAssistant ||
		!strings.Contains(mainProvider.request.Messages[1].Content, "old work summary") {
		t.Fatalf("summary prefix = %+v", mainProvider.request.Messages[:2])
	}
	for _, message := range mainProvider.request.Messages {
		if strings.Contains(message.Content, "old question") || strings.Contains(message.Content, "old answer") {
			t.Fatalf("covered raw history leaked to provider: %+v", mainProvider.request.Messages)
		}
	}
	messages, err := db.GetMessages(sessionID)
	if err != nil || len(messages) != 6 || !strings.Contains(messages[0].Content, "old question") {
		t.Fatalf("raw messages = %+v, err=%v", messages, err)
	}
}

func TestHardContextBudgetStopsProviderAdmission(t *testing.T) {
	provider := &captureProvider{}
	core, err := New(provider, &stubExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	core.SetCompactRuntime(compact.RuntimeConfig{
		MainProviderID: "main", MainModelID: "main", MainContextWindow: 256,
		MainMaxTokens: 64, ReservedOutputTokens: 64, ProviderMarginTokens: 32,
	}, nil)
	_, err = core.Chat(context.Background(), strings.Repeat("large context ", 500))
	if compact.ErrorCodeOf(err) != compact.ErrContextLimit {
		t.Fatalf("error = %v", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d", provider.calls.Load())
	}
}

func TestToolLoopHardBudgetStopsSecondProviderAfterCompleteBatch(t *testing.T) {
	tool := executor.Tool{
		Name: "compact_large_output_test",
		Execute: func(context.Context, json.RawMessage) *executor.ToolResult {
			return &executor.ToolResult{Success: true, Output: strings.Repeat("large tool output ", 4000)}
		},
	}
	executor.Register(tool)
	provider := &oneToolProvider{}
	db, sessionID := newContextTestStore(t)
	core, err := New(provider, fixedToolCatalog{tools: []executor.Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.SetStore(db, sessionID); err != nil {
		t.Fatal(err)
	}
	recorder := &budgetRecorder{sessionID: sessionID}
	core.SetCompactRuntime(compact.RuntimeConfig{
		Automatic:      true,
		MainProviderID: "main", MainModelID: "main", MainContextWindow: 4000,
		MainMaxTokens: 256, ReservedOutputTokens: 256, ProviderMarginTokens: 128,
		HighWatermark: .8, LowWatermark: .6,
	}, recorder)
	_, err = core.Chat(requestctx.WithRequestID(context.Background(), "tool-loop-request"), "run the tool")
	if compact.ErrorCodeOf(err) != compact.ErrContextLimit {
		t.Fatalf("error = %v", err)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d", provider.calls.Load())
	}
	messages, err := db.GetMessages(sessionID)
	if err != nil || len(messages) != 3 || messages[1].Role != "assistant" || messages[2].Role != "tool" || messages[2].ToolID != "large-call" {
		t.Fatalf("persisted tool batch = %+v, err=%v", messages, err)
	}
	runtime, err := db.GetSessionRuntime(context.Background(), sessionID)
	if err != nil || !runtime.PendingCompact || runtime.PendingCompactID == "" || runtime.PendingAttempt != 0 {
		t.Fatalf("tool batch pending = %+v, err=%v", runtime, err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.observations) != 2 || recorder.observations[0].FirstRequest != true || recorder.observations[1].FirstRequest ||
		recorder.observations[1].EstimatedTokens <= recorder.observations[1].HardLimit {
		t.Fatalf("budget observations = %+v", recorder.observations)
	}
}

func newContextTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	db, err := store.New(filepath.Join(t.TempDir(), "context.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "context-session"
	if err := db.CreateSession(group.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	return db, sessionID
}

type providerFuncForContext func(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error)

func (fn providerFuncForContext) Chat(ctx context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
	return fn(ctx, request)
}
