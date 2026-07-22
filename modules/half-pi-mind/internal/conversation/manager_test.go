package conversation

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

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type testProvider struct{}

func (testProvider) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: "ok"}, nil
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
}

type recordingProvider struct {
	calls   atomic.Int64
	request *llm.LLMRequest
	content string
}

func (provider *recordingProvider) Chat(_ context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
	provider.calls.Add(1)
	copy := *request
	copy.Messages = append([]llm.Message(nil), request.Messages...)
	provider.request = &copy
	return &llm.LLMResponse{Content: provider.content}, nil
}

func (provider *blockingProvider) Chat(ctx context.Context, _ *llm.LLMRequest) (*llm.LLMResponse, error) {
	close(provider.started)
	select {
	case <-provider.release:
		return &llm.LLMResponse{Content: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	db, err := store.New(filepath.Join(t.TempDir(), "conversation.db"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := db.UpsertGroup(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New()
	authority := remoteexec.NewAuthority(h, remoteexec.NewRegistry(db), nil)
	tasks := remoteexec.NewTaskService(authority, db)
	approvals, err := approval.New(approval.Config{Auditor: db})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		GroupID: group.ID, Provider: testProvider{}, Store: db,
		Approvals: approvals, Authority: authority, Tasks: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = manager.Close(context.Background())
		_ = approvals.Close()
		_ = authority.Close()
		_ = db.Close()
	})
	return manager, db
}

func TestManagerCreateAndRestoreConversationState(t *testing.T) {
	manager, db := newTestManager(t)
	actor, err := manager.Create("restored")
	if err != nil {
		t.Fatal(err)
	}
	id := actor.Core().SessionID()
	if err := actor.SetMode("trust"); err != nil {
		t.Fatal(err)
	}
	if err := actor.SetActiveHand("hand-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Chat(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	secondHub := hub.New()
	secondAuthority := remoteexec.NewAuthority(secondHub, remoteexec.NewRegistry(db), nil)
	secondTasks := remoteexec.NewTaskService(secondAuthority, db)
	secondApprovals, err := approval.New(approval.Config{Auditor: db})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(Config{
		GroupID: manager.GroupID(), Provider: testProvider{}, Store: db,
		Approvals: secondApprovals, Authority: secondAuthority, Tasks: secondTasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = second.Close(context.Background())
		_ = secondApprovals.Close()
		_ = secondAuthority.Close()
	})
	restored, err := second.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Core().SecurityMode() != "review" || restored.Core().ActiveHand() != "hand-a" {
		t.Fatalf("restored state = mode %q, hand %q", restored.Core().SecurityMode(), restored.Core().ActiveHand())
	}
	messages, err := db.GetMessages(id)
	if err != nil || len(messages) != 2 || messages[0].Content != "hello" {
		t.Fatalf("restored messages = %+v, err %v", messages, err)
	}
	session, err := db.GetSession(id)
	if err != nil || session.Name != "restored" || session.Mode != "review" || session.ActiveHand != "hand-a" || session.UpdatedAt.IsZero() {
		t.Fatalf("session metadata = %+v, err %v", session, err)
	}
}

func TestManagerReturnsOneActorUnderConcurrentLoad(t *testing.T) {
	manager, _ := newTestManager(t)
	created, err := manager.Create("")
	if err != nil {
		t.Fatal(err)
	}
	id := created.Core().SessionID()

	const workers = 32
	actors := make(chan *Actor, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actor, getErr := manager.Get(id)
			if getErr != nil {
				t.Errorf("Get: %v", getErr)
				return
			}
			actors <- actor
		}()
	}
	wg.Wait()
	close(actors)
	for actor := range actors {
		if actor != created {
			t.Fatal("concurrent Get returned different Actor instances")
		}
	}
}

func TestManagerRejectsMissingConversation(t *testing.T) {
	manager, _ := newTestManager(t)
	if _, err := manager.Get("missing"); err != ErrNotFound {
		t.Fatalf("Get missing error = %v", err)
	}
}

func TestManagerCloseLifecycleIsIdempotent(t *testing.T) {
	manager, _ := newTestManager(t)
	if _, err := manager.Create("close"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActorLeaseSerializesChatAndContextMutation(t *testing.T) {
	manager, db := newTestManager(t)
	provider := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	manager.config.Provider = provider
	actor, err := manager.Create("lease")
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithRequestID(context.Background(), "chat-request")
	done := make(chan error, 1)
	go func() {
		_, chatErr := actor.Chat(ctx, "hello")
		done <- chatErr
	}()
	<-provider.started
	if err := actor.SetMode("strict"); !errors.Is(err, ErrBusy) {
		t.Fatalf("SetMode error = %v", err)
	}
	if actor.OperationState() != OperationChatRunning {
		t.Fatalf("operation state = %q", actor.OperationState())
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := actor.SetMode("strict"); err != nil {
		t.Fatal(err)
	}
	session, err := db.GetSession(actor.Core().SessionID())
	if err != nil || session.Mode != "strict" {
		t.Fatalf("session = %+v, err=%v", session, err)
	}
}

func TestActorAutomaticallyCompactsBeforeFirstProviderRequest(t *testing.T) {
	manager, db := newTestManager(t)
	mainProvider := &recordingProvider{content: "done"}
	manager.config.Provider = mainProvider
	summaryProvider := &recordingProvider{content: `{"summary":"accumulated history"}`}
	runtime := compact.RuntimeConfig{
		Enabled: true, Automatic: true,
		MainProviderID: "main", MainModelID: "main", MainContextWindow: 6000,
		MainMaxTokens: 256, ReservedOutputTokens: 256,
		SummaryProviderID: "summary", SummaryModelID: "summary", SummaryContextWindow: 20_000,
		SummaryModelMaxTokens: 512, Timeout: 5 * time.Second, MaxTokens: 128,
		HighWatermark: .8, LowWatermark: .6, ProviderMarginTokens: 128, MaxConcurrent: 1,
		RateLimitInitialBackoff: time.Second, RateLimitMaxBackoff: time.Minute,
		PolicyVersion: "compact-v1", Profile: "default",
	}
	engine, err := compact.New(runtime, summaryProvider, db, manager, manager.config.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetCompactor(engine, runtime)
	sessionID := newUUIDv7ForConversation(t)
	if err := db.CreateSession(manager.GroupID(), sessionID); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("historical context ", 500)
	if err := db.AppendMessages(sessionID, 0, []store.Message{
		{Role: "user", Content: long, RequestID: "old-1", Seq: 1},
		{Role: "assistant", Content: long, RequestID: "old-1", Seq: 2},
		{Role: "user", Content: long, RequestID: "old-2", Seq: 3},
		{Role: "assistant", Content: long, RequestID: "old-2", Seq: 4},
	}); err != nil {
		t.Fatal(err)
	}
	actor, err := manager.Get(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithRequestID(context.Background(), newUUIDv7ForConversation(t))
	if _, err := actor.Chat(ctx, "current request"); err != nil {
		t.Fatal(err)
	}
	if summaryProvider.calls.Load() != 1 || mainProvider.calls.Load() != 1 || mainProvider.request == nil {
		t.Fatalf("summary calls=%d main calls=%d request=%+v", summaryProvider.calls.Load(), mainProvider.calls.Load(), mainProvider.request)
	}
	if len(mainProvider.request.Messages) < 3 || !strings.Contains(mainProvider.request.Messages[1].Content, "accumulated history") {
		t.Fatalf("provider context = %+v", mainProvider.request.Messages)
	}
	snapshot, err := db.GetCompactSnapshot(context.Background(), sessionID)
	if err != nil || snapshot.Runtime.ActiveSummaryID == "" || snapshot.Runtime.PendingCompact {
		t.Fatalf("compact snapshot = %+v, err=%v", snapshot, err)
	}
}

func newUUIDv7ForConversation(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
