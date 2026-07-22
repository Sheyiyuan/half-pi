package conversation

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type testProvider struct{}

func (testProvider) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: "ok"}, nil
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
	if err := actor.Core().SetMode("trust"); err != nil {
		t.Fatal(err)
	}
	if err := actor.Core().SetActiveHand("hand-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Core().Chat(context.Background(), "hello"); err != nil {
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
	if err != nil || len(messages) != 3 || messages[1].Content != "hello" {
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
