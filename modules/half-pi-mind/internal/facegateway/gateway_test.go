package facegateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type gatewayTestLLM struct{}

func (gatewayTestLLM) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: "ok"}, nil
}

type gatewayFixture struct {
	gateway       *Gateway
	store         *store.Store
	hub           *hub.Hub
	authority     *remoteexec.Authority
	tasks         *remoteexec.TaskService
	approvals     *approval.Broker
	conversations *conversation.Manager
}

func newGatewayFixture(t *testing.T, queueSize int) *gatewayFixture {
	return newGatewayFixtureWithProvider(t, queueSize, gatewayTestLLM{})
}

func newGatewayFixtureWithProvider(t *testing.T, queueSize int, provider llm.Provider) *gatewayFixture {
	t.Helper()
	db, err := store.New(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := db.UpsertGroup(filepath.Join(t.TempDir(), "workspace"))
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
	manager, err := conversation.NewManager(conversation.Config{
		GroupID: group.ID, Provider: provider, Store: db,
		Approvals: approvals, Authority: authority, Tasks: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := New(Config{
		Hub: h, Store: db, Conversations: manager, Approvals: approvals,
		Authority: authority, Tasks: tasks, QueueSize: queueSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = approvals.Close()
		_ = authority.Close()
		_ = db.Close()
	})
	return &gatewayFixture{
		gateway: gateway, store: db, hub: h, authority: authority,
		tasks: tasks, approvals: approvals, conversations: manager,
	}
}

func newGatewayTestConnection(queueSize int, identity protocol.FaceIdentity) *connection {
	ctx, cancel := context.WithCancel(context.Background())
	return &connection{
		peer: &hub.Peer{ID: identity.Label, Type: hub.PeerFace, PrincipalID: identity.ID},
		ctx:  ctx, cancel: cancel, queue: make(chan protocol.Envelope, queueSize), identity: identity,
	}
}

func commandEnvelope[T any](t *testing.T, typ string, payload T) protocol.Envelope {
	t.Helper()
	env, err := protocol.NewEnvelope("", typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	return *env
}

func nextPayload[T any](t *testing.T, state *connection, wantType string) T {
	t.Helper()
	select {
	case env := <-state.queue:
		if env.Type != wantType {
			t.Fatalf("message type = %q, want %q", env.Type, wantType)
		}
		payload, err := protocol.DecodePayload[T](&env)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", wantType)
		var zero T
		return zero
	}
}

func assertNoQueuedMessage(t *testing.T, state *connection) {
	t.Helper()
	select {
	case env := <-state.queue:
		t.Fatalf("unexpected queued message %q", env.Type)
	default:
	}
}

func TestConversationListLifecycleAndScope(t *testing.T) {
	fixture := newGatewayFixture(t, 16)
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), "conv-1", "Work"); err != nil {
		t.Fatal(err)
	}
	identity := protocol.FaceIdentity{ID: "1", Label: "reader", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}}
	state := newGatewayTestConnection(16, identity)
	request := protocol.FaceConversationList{RequestID: "request-1"}
	fixture.gateway.handleCommand(state, identity, commandEnvelope(t, protocol.TypeFaceConversationList, request))

	accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	if accepted.RequestID != request.RequestID || accepted.Operation != protocol.FaceOperationConversationList {
		t.Fatalf("accepted = %+v", accepted)
	}
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if result.Status != protocol.FaceResultSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if err := protocol.ValidateFaceResultData(protocol.FaceOperationConversationList, result.Data); err != nil {
		t.Fatal(err)
	}
	list, err := protocol.StrictDecode[protocol.ConversationListResult](result.Data)
	if err != nil || len(list.Conversations) != 1 || list.Conversations[0].ConversationID != "conv-1" {
		t.Fatalf("conversation list = %+v, %v", list, err)
	}

	denied := newGatewayTestConnection(4, protocol.FaceIdentity{ID: "2", Label: "chat", Scopes: []protocol.FaceScope{protocol.FaceScopeChat}})
	fixture.gateway.handleCommand(denied, denied.identity, commandEnvelope(t, protocol.TypeFaceConversationList, request))
	faceErr := nextPayload[protocol.FaceError](t, denied, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorForbidden {
		t.Fatalf("scope error = %+v", faceErr)
	}
	assertNoQueuedMessage(t, denied)
}

func TestHandleFaceMessageRejectsInvalidAndRevokedIdentity(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	credential, err := fixture.store.AddFaceToken("operator", []protocol.FaceScope{protocol.FaceScopeSessionsRead})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := fixture.store.FaceIdentityByLabel(credential.Label)
	if err != nil {
		t.Fatal(err)
	}
	state := newGatewayTestConnection(8, *identity)
	peer := state.peer
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[peer] = state
	fixture.gateway.mu.Unlock()

	invalid := protocol.Envelope{
		MsgID: "invalid", Type: protocol.TypeFaceConversationList,
		Payload: json.RawMessage(`{"request_id":"bad","unknown":true}`),
	}
	fixture.gateway.HandleFaceMessage(peer, invalid)
	faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorInvalidRequest || faceErr.RequestID != "bad" {
		t.Fatalf("invalid response = %+v", faceErr)
	}
	assertNoQueuedMessage(t, state)

	if err := fixture.store.RemoveFaceToken(credential.ID); err != nil {
		t.Fatal(err)
	}
	fixture.gateway.HandleFaceMessage(peer, commandEnvelope(t, protocol.TypeFaceConversationList, protocol.FaceConversationList{RequestID: "revoked"}))
	faceErr = nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorUnauthorized || faceErr.RequestID != "revoked" {
		t.Fatalf("revoked response = %+v", faceErr)
	}
	state.mu.Lock()
	subscribed := state.subscribed
	state.mu.Unlock()
	if subscribed {
		t.Fatal("revoked Face retained event subscription")
	}
	if _, err := fixture.store.AddFaceToken("operator", []protocol.FaceScope{protocol.FaceScopeSessionsRead}); err != nil {
		t.Fatal(err)
	}
	fixture.gateway.HandleFaceMessage(peer, commandEnvelope(t, protocol.TypeFaceConversationList, protocol.FaceConversationList{RequestID: "recreated"}))
	faceErr = nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorUnauthorized || faceErr.RequestID != "recreated" {
		t.Fatalf("recreated identity response = %+v", faceErr)
	}
}

func TestConversationCreateRenameAndSnapshotCommands(t *testing.T) {
	fixture := newGatewayFixture(t, 16)
	identity := protocol.FaceIdentity{
		ID: "1", Label: "writer",
		Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead, protocol.FaceScopeSessionsWrite},
	}
	state := newGatewayTestConnection(16, identity)
	create := protocol.FaceConversationCreate{RequestID: "create-1", Name: "Original"}
	fixture.gateway.handleCommand(state, identity, commandEnvelope(t, protocol.TypeFaceConversationCreate, create))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	createResult := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	created, err := protocol.StrictDecode[protocol.ConversationCreateResult](createResult.Data)
	if err != nil || created.Conversation.Name != "Original" {
		t.Fatalf("create result = %+v, %v", created, err)
	}

	rename := protocol.FaceConversationRename{
		RequestID: "rename-1", ConversationID: created.Conversation.ConversationID, Name: "Renamed",
	}
	fixture.gateway.handleCommand(state, identity, commandEnvelope(t, protocol.TypeFaceConversationRename, rename))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	renameResult := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	renamed, err := protocol.StrictDecode[protocol.ConversationRenameResult](renameResult.Data)
	if err != nil || renamed.Conversation.Name != "Renamed" {
		t.Fatalf("rename result = %+v, %v", renamed, err)
	}
	fixture.gateway.handleCommand(state, identity, commandEnvelope(t, protocol.TypeFaceConversationRename, protocol.FaceConversationRename{
		RequestID: "rename-empty", ConversationID: created.Conversation.ConversationID, Name: "   ",
	}))
	faceErr := nextPayload[protocol.FaceError](t, state, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorInvalidRequest {
		t.Fatalf("blank rename response = %+v", faceErr)
	}
	assertNoQueuedMessage(t, state)

	snapshotRequest := protocol.FaceConversationSnapshot{RequestID: "snapshot-1", ConversationID: rename.ConversationID}
	fixture.gateway.handleCommand(state, identity, commandEnvelope(t, protocol.TypeFaceConversationSnapshot, snapshotRequest))
	accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	snapshot := nextPayload[protocol.FaceSnapshot](t, state, protocol.TypeFaceSnapshot)
	if accepted.SnapshotVersion < 1 || snapshot.Snapshot.SnapshotVersion < accepted.SnapshotVersion || snapshot.Snapshot.Name != "Renamed" {
		t.Fatalf("snapshot lifecycle = accepted %+v snapshot %+v", accepted, snapshot.Snapshot)
	}
}

func TestSnapshotRestoresRunsAndTaskScope(t *testing.T) {
	fixture := newGatewayFixture(t, 16)
	conversationID := "conv-snapshot"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Snapshot"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.AppendMessages(conversationID, 0, []store.Message{{Role: "user", Content: "hello", Seq: 1}}); err != nil {
		t.Fatal(err)
	}
	standalone := remoteexec.NewRegistry(fixture.store)
	if err := standalone.Create("stored-run", conversationID, "hand-1", "read_file"); err != nil {
		t.Fatal(err)
	}
	if err := standalone.Transition("stored-run", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	task := remoteexec.Task{
		TaskID: "task-1", SessionID: conversationID, HandID: "hand-1", Tool: "exec_command",
		ArgsDigest: "sha256:test", Status: protocol.TaskLost,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, Stale: false, Error: "connection lost",
	}
	if err := fixture.store.CreateRemoteTask(task); err != nil {
		t.Fatal(err)
	}

	reader := protocol.FaceIdentity{ID: "1", Label: "reader", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}}
	snapshot, err := fixture.gateway.snapshot(reader, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || len(snapshot.ActiveRuns) != 1 || snapshot.ActiveRuns[0].RunID != "stored-run" {
		t.Fatalf("snapshot projection = %+v", snapshot)
	}
	if snapshot.Tasks == nil || len(snapshot.Tasks) != 0 || snapshot.TaskHistoryLimit != 0 || snapshot.TaskHistoryTruncated {
		t.Fatalf("task-redacted snapshot = %+v", snapshot)
	}

	taskReader := reader
	taskReader.Scopes = append(taskReader.Scopes, protocol.FaceScopeTasksRead)
	snapshot, err = fixture.gateway.snapshot(taskReader, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].FinishedAt == nil || snapshot.Tasks[0].ErrorCode != protocol.FaceErrorTaskLost || snapshot.Tasks[0].Error != "task state was lost" {
		t.Fatalf("task snapshot = %+v", snapshot.Tasks)
	}
	payload, err := json.Marshal(protocol.FaceSnapshot{RequestID: "snapshot-1", Snapshot: snapshot})
	if err != nil || protocol.ValidateFacePayload(protocol.TypeFaceSnapshot, payload) != nil {
		t.Fatalf("snapshot does not satisfy wire contract: %s, %v", payload, err)
	}
}

func TestRunGetFallsBackToStore(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	conversationID := "conv-run"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Runs"); err != nil {
		t.Fatal(err)
	}
	standalone := remoteexec.NewRegistry(fixture.store)
	if err := standalone.Create("historical-run", conversationID, "hand-1", "read_file"); err != nil {
		t.Fatal(err)
	}
	if err := standalone.Transition("historical-run", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := standalone.Transition("historical-run", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	if err := standalone.MarkLost("historical-run", "test"); err != nil {
		t.Fatal(err)
	}
	identity := protocol.FaceIdentity{ID: "1", Label: "runs", Scopes: []protocol.FaceScope{protocol.FaceScopeRunsRead}}
	state := newGatewayTestConnection(8, identity)
	request := protocol.FaceRunGet{RequestID: "request-run", ConversationID: conversationID, RunID: "historical-run"}
	fixture.gateway.handleCommand(state, identity, commandEnvelope(t, protocol.TypeFaceRunGet, request))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	result := nextPayload[protocol.FaceResult](t, state, protocol.TypeFaceResult)
	if err := protocol.ValidateFaceResultData(protocol.FaceOperationRunGet, result.Data); err != nil {
		t.Fatal(err)
	}
	data, err := protocol.StrictDecode[protocol.RunGetResult](result.Data)
	if err != nil || data.Run.RunID != request.RunID || data.Run.RequestID != request.RunID ||
		data.Run.Status != protocol.RunLost || data.Run.FinishedAt == nil {
		t.Fatalf("run result = %+v, %v", data, err)
	}
}

func TestRunProjectionUsesFaceRequestAssociation(t *testing.T) {
	now := time.Now().UTC()
	memory := projectMemoryRun(remoteexec.Run{
		ID: "memory-run", SessionID: "conv-run", HandID: "hand-1", Tool: "read_file",
		Status: protocol.RunRunning, CreatedAt: now,
		Metadata: remoteexec.AuditMetadata{RequestID: "face-memory-request"},
	})
	stored := projectStoredRun(store.RemoteRunRecord{
		ID: "stored-run", SessionID: "conv-run", RequestID: "face-stored-request",
		HandID: "hand-1", Tool: "read_file", Status: protocol.RunSucceeded, CreatedAt: now,
	})
	if memory.summary.RequestID != "face-memory-request" || stored.summary.RequestID != "face-stored-request" {
		t.Fatalf("run request projections = memory %q, stored %q", memory.summary.RequestID, stored.summary.RequestID)
	}
}

func TestTaskListCursorIsBoundAndTamperEvident(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	conversationID := "conv-tasks"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Tasks"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index, id := range []string{"task-a", "task-b", "task-c"} {
		task := remoteexec.Task{
			TaskID: id, SessionID: conversationID, HandID: "hand-1", Tool: "read_file",
			ArgsDigest: "sha256:" + id, Status: protocol.TaskRunning,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Duration(index) * time.Second),
		}
		if err := fixture.store.CreateRemoteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	first, err := fixture.gateway.listTasks(protocol.FaceTaskList{ConversationID: conversationID, Limit: 2})
	if err != nil || len(first.Tasks) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := fixture.gateway.listTasks(protocol.FaceTaskList{ConversationID: conversationID, Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Tasks) != 1 || second.Tasks[0].TaskID == first.Tasks[0].TaskID || second.Tasks[0].TaskID == first.Tasks[1].TaskID {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	if _, err := fixture.gateway.listTasks(protocol.FaceTaskList{ConversationID: conversationID, HandID: "other", Cursor: first.NextCursor}); err == nil {
		t.Fatal("cursor reused with different filter")
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	if tampered == first.NextCursor {
		tampered = first.NextCursor[:len(first.NextCursor)-1] + "B"
	}
	if _, err := fixture.gateway.listTasks(protocol.FaceTaskList{ConversationID: conversationID, Cursor: tampered}); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}

func TestSubscriptionsFilterScopesAndAllocateOrderedEventSeq(t *testing.T) {
	fixture := newGatewayFixture(t, 256)
	for _, id := range []string{"conv-a", "conv-b"} {
		if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), id, id); err != nil {
			t.Fatal(err)
		}
	}
	identity := protocol.FaceIdentity{
		ID: "1", Label: "events",
		Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead, protocol.FaceScopeHandsRead},
	}
	state := newGatewayTestConnection(256, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	request := protocol.FaceSubscribe{
		RequestID: "subscribe-1", ConversationIDs: []string{"conv-a"},
		EventTypes: []protocol.FaceEventType{protocol.FaceEventConversationChanged, protocol.FaceEventHandConnected},
	}
	fixture.gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, request))
	accepted := nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)
	if accepted.SnapshotVersion < 1 {
		t.Fatalf("subscribe accepted = %+v", accepted)
	}

	fixture.gateway.PublishConversationChanged("conv-b")
	assertNoQueuedMessage(t, state)
	const eventCount = 50
	var wait sync.WaitGroup
	wait.Add(eventCount)
	for range eventCount {
		go func() {
			defer wait.Done()
			fixture.gateway.PublishConversationChanged("conv-a")
		}()
	}
	wait.Wait()
	var previousVersion int64
	for sequence := int64(1); sequence <= eventCount; sequence++ {
		event := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
		if event.EventSeq != sequence || event.ConversationID != "conv-a" {
			t.Fatalf("event %d = %+v", sequence, event)
		}
		data, err := protocol.StrictDecode[protocol.ConversationChangedEventData](event.Data)
		if err != nil || data.SnapshotVersion <= previousVersion {
			t.Fatalf("event %d version data = %+v, %v", sequence, data, err)
		}
		previousVersion = data.SnapshotVersion
	}
	fixture.gateway.HandleHandConnect(&hub.Peer{
		ID: "hand-1", Type: hub.PeerHand,
		Info: &protocol.HandInfo{Hostname: "dev", OS: "linux", Arch: "amd64"},
	})
	handEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	if handEvent.EventSeq != eventCount+1 || handEvent.Type != protocol.FaceEventHandConnected {
		t.Fatalf("Hand event = %+v", handEvent)
	}

	denied := newGatewayTestConnection(4, protocol.FaceIdentity{ID: "2", Label: "no-tasks", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}})
	fixture.gateway.handleSubscribe(denied, denied.identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-task", EventTypes: []protocol.FaceEventType{protocol.FaceEventTaskChanged},
	}))
	faceErr := nextPayload[protocol.FaceError](t, denied, protocol.TypeFaceError)
	if faceErr.Code != protocol.FaceErrorForbidden {
		t.Fatalf("task subscription error = %+v", faceErr)
	}
}

func TestSlowFaceDoesNotBlockOtherConnections(t *testing.T) {
	fixture := newGatewayFixture(t, 1)
	identity := protocol.FaceIdentity{ID: "1", Label: "face", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}}
	slow := newGatewayTestConnection(1, identity)
	slow.subscribed = true
	fast := newGatewayTestConnection(4, identity)
	fast.peer.ID = "fast"
	fast.subscribed = true
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[slow.peer] = slow
	fixture.gateway.connections[fast.peer] = fast
	fixture.gateway.mu.Unlock()
	prefill := commandEnvelope(t, protocol.TypeFaceAccepted, protocol.FaceAccepted{RequestID: "prefill", Operation: protocol.FaceOperationSubscribe})
	slow.queue <- prefill

	started := time.Now()
	fixture.gateway.PublishConversationChanged("conv-1")
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("slow Face blocked domain publisher")
	}
	slow.mu.Lock()
	wasSlow := slow.slow
	slow.mu.Unlock()
	if !wasSlow {
		t.Fatal("full Face queue was not marked slow")
	}
	event := nextPayload[protocol.FaceEvent](t, fast, protocol.TypeFaceEvent)
	if event.EventSeq != 1 {
		t.Fatalf("fast Face event = %+v", event)
	}
}

func TestDomainObserversProjectRunAndTaskChanges(t *testing.T) {
	fixture := newGatewayFixture(t, 16)
	conversationID := "conv-domain"
	if err := fixture.store.CreateSessionNamed(fixture.conversations.GroupID(), conversationID, "Domain"); err != nil {
		t.Fatal(err)
	}
	identity := protocol.FaceIdentity{
		ID: "1", Label: "observer",
		Scopes: []protocol.FaceScope{
			protocol.FaceScopeSessionsRead, protocol.FaceScopeRunsRead, protocol.FaceScopeTasksRead,
		},
	}
	state := newGatewayTestConnection(16, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	fixture.gateway.handleSubscribe(state, identity, commandEnvelope(t, protocol.TypeFaceSubscribe, protocol.FaceSubscribe{
		RequestID: "subscribe-domain", ConversationIDs: []string{conversationID},
		EventTypes: []protocol.FaceEventType{
			protocol.FaceEventRemoteRunChanged, protocol.FaceEventTaskChanged, protocol.FaceEventConversationChanged,
		},
	}))
	_ = nextPayload[protocol.FaceAccepted](t, state, protocol.TypeFaceAccepted)

	if err := fixture.authority.Registry.CreateWithMetadata(
		"run-domain", conversationID, "hand-1", "read_file",
		remoteexec.AuditMetadata{RequestID: "face-domain-request"},
	); err != nil {
		t.Fatal(err)
	}
	runEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	conversationEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	if runEvent.EventSeq != 1 || runEvent.Type != protocol.FaceEventRemoteRunChanged || runEvent.RequestID != "face-domain-request" ||
		conversationEvent.EventSeq != 2 || conversationEvent.Type != protocol.FaceEventConversationChanged {
		t.Fatalf("run observer events = %+v then %+v", runEvent, conversationEvent)
	}

	if err := fixture.tasks.CreateStartSnapshot(remoteexec.Task{
		TaskID: "task-domain", SessionID: conversationID, HandID: "hand-1",
		Tool: "exec_command", ArgsDigest: "sha256:domain",
	}); err != nil {
		t.Fatal(err)
	}
	taskEvent := nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	conversationEvent = nextPayload[protocol.FaceEvent](t, state, protocol.TypeFaceEvent)
	if taskEvent.EventSeq != 3 || taskEvent.Type != protocol.FaceEventTaskChanged || conversationEvent.EventSeq != 4 || conversationEvent.Type != protocol.FaceEventConversationChanged {
		t.Fatalf("task observer events = %+v then %+v", taskEvent, conversationEvent)
	}
}

func TestDisconnectDropsConnectionState(t *testing.T) {
	fixture := newGatewayFixture(t, 4)
	identity := protocol.FaceIdentity{ID: "1", Label: "face", Scopes: []protocol.FaceScope{protocol.FaceScopeSessionsRead}}
	state := newGatewayTestConnection(4, identity)
	fixture.gateway.mu.Lock()
	fixture.gateway.connections[state.peer] = state
	fixture.gateway.mu.Unlock()
	fixture.gateway.HandleFaceDisconnect(state.peer)
	fixture.gateway.mu.Lock()
	_, exists := fixture.gateway.connections[state.peer]
	fixture.gateway.mu.Unlock()
	if exists {
		t.Fatal("disconnected Face state retained")
	}
	select {
	case <-state.ctx.Done():
	default:
		t.Fatal("disconnected Face sender context remains active")
	}
}
