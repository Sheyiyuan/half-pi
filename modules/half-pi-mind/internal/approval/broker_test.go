package approval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

type memoryAuditor struct {
	mu        sync.Mutex
	records   map[string]AuditRecord
	finishErr error
}

func newMemoryAuditor() *memoryAuditor {
	return &memoryAuditor{records: make(map[string]AuditRecord)}
}

func (a *memoryAuditor) CreateApproval(record AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.records[record.Request.ApprovalID]; exists {
		return fmt.Errorf("duplicate approval")
	}
	a.records[record.Request.ApprovalID] = record
	return nil
}

func (a *memoryAuditor) FinishApproval(id string, status Status, resolution Resolution) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finishErr != nil {
		return a.finishErr
	}
	record, exists := a.records[id]
	if !exists || record.Status != StatusPending {
		return fmt.Errorf("approval is not pending")
	}
	record.Status = status
	record.Decision = resolution.Decision
	record.Actor = resolution.Actor
	record.ResolutionReason = resolution.Reason
	record.ResolvedAt = resolution.ResolvedAt
	a.records[id] = record
	return nil
}

func (a *memoryAuditor) LookupApproval(id string) (AuditRecord, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record, exists := a.records[id]
	return record, exists, nil
}

func (a *memoryAuditor) record(id string) AuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.records[id]
}

func newTestBroker(t *testing.T, auditor Auditor, config ...func(*Config)) *Broker {
	t.Helper()
	cfg := Config{Auditor: auditor, TTL: time.Hour}
	for _, apply := range config {
		apply(&cfg)
	}
	broker, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	return broker
}

func approvalRequest() Request {
	return Request{
		ConversationID: "conversation-1", RequestID: "request-1", RunID: "run-1",
		Tool: "exec_command", Reason: "requires approval", ArgsDigest: "sha256:digest",
	}
}

func receiveRequest(t *testing.T, requests <-chan protocol.ApprovalRequest) protocol.ApprovalRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval request")
		return protocol.ApprovalRequest{}
	}
}

func receiveResolution(t *testing.T, resolutions <-chan Resolution) Resolution {
	t.Helper()
	select {
	case resolution := <-resolutions:
		return resolution
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval resolution")
		return Resolution{}
	}
}

func TestBrokerPersistsAcceptedResolutionBeforeUnblockingConfirm(t *testing.T) {
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor)
	requests := make(chan protocol.ApprovalRequest, 1)
	order := make(chan string, 3)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, func(_ protocol.ApprovalRequest, status Status, _ Resolution) {
		if status == StatusResolved {
			order <- "finished"
		}
	})
	resolutions := make(chan Resolution, 1)
	go func() {
		resolutions <- broker.Confirm(context.Background(), approvalRequest())
		order <- "confirmed"
	}()

	request := receiveRequest(t, requests)
	actor := Actor{ID: "face-1", Label: "Operator", Source: "face"}
	resolved, err := broker.Resolve(request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "approved", ResolveHooks{
		Accepted: func(protocol.ApprovalRequest) bool {
			if auditor.record(request.ApprovalID).Status != StatusResolved {
				t.Error("accepted response ran before the audit reached resolved")
			}
			order <- "accepted"
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ConversationID != request.ConversationID || !resolved.Allowed() {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
	confirmed := receiveResolution(t, resolutions)
	if confirmed.ApprovalID != request.ApprovalID || confirmed.Actor != actor {
		t.Fatalf("Confirm returned %+v", confirmed)
	}
	for i, want := range []string{"accepted", "finished", "confirmed"} {
		select {
		case got := <-order:
			if got != want {
				t.Fatalf("order[%d] = %q, want %q", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestBrokerLifecycleObservationsInheritFrozenToolTrace(t *testing.T) {
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor)
	requested := make(chan Observation, 1)
	finished := make(chan Observation, 1)
	broker.SubscribeLifecycle(func(observation Observation) {
		requested <- observation
	}, func(observation Observation, status Status, _ Resolution) {
		if status == StatusResolved {
			finished <- observation
		}
	})
	toolMeta := corelifecycle.NewMeta(corelifecycle.SourceFace).
		WithConversation("conversation-1").WithGroup("group-1").WithPrincipal("principal-1").
		WithRequest("request-1").WithNode("mind")
	request := approvalRequest()
	request.Meta = toolMeta
	resolutionResult := make(chan Resolution, 1)
	go func() { resolutionResult <- broker.Confirm(context.Background(), request) }()

	requestedObservation := <-requested
	if requestedObservation.Meta.TraceID != toolMeta.TraceID || requestedObservation.Meta.ParentSpanID != toolMeta.SpanID ||
		requestedObservation.Meta.SpanID == toolMeta.SpanID || requestedObservation.Meta.GroupID != "group-1" ||
		requestedObservation.Meta.PrincipalID != "principal-1" || requestedObservation.Meta.Source != corelifecycle.SourceMind {
		t.Fatalf("approval requested meta = %+v, tool meta = %+v", requestedObservation.Meta, toolMeta)
	}
	actor := Actor{ID: "face-1", Source: "face"}
	if _, err := broker.Resolve(requestedObservation.Request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "approved", ResolveHooks{}); err != nil {
		t.Fatal(err)
	}
	finishedObservation := <-finished
	if finishedObservation.Meta != requestedObservation.Meta {
		t.Fatalf("approval terminal meta changed: requested=%+v finished=%+v", requestedObservation.Meta, finishedObservation.Meta)
	}
	resolution := receiveResolution(t, resolutionResult)
	if !resolution.Allowed() {
		t.Fatalf("resolution = %+v", resolution)
	}
	record := auditor.record(requestedObservation.Request.ApprovalID)
	if record.Meta != requestedObservation.Meta {
		t.Fatalf("approval audit meta = %+v, observation meta = %+v", record.Meta, requestedObservation.Meta)
	}
}

func TestBrokerConcurrentResolutionAcceptsExactlyOneFace(t *testing.T) {
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor)
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	resolutions := make(chan Resolution, 1)
	go func() { resolutions <- broker.Confirm(context.Background(), approvalRequest()) }()
	request := receiveRequest(t, requests)

	var accepted atomic.Int32
	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("face-%d", i)
		go func() {
			<-start
			_, err := broker.Resolve(request.ApprovalID, Actor{ID: id, Source: "face"}, protocol.FaceApprovalAllowOnce, "", ResolveHooks{
				Accepted: func(protocol.ApprovalRequest) bool {
					accepted.Add(1)
					return true
				},
			})
			errorsSeen <- err
		}()
	}
	close(start)
	var succeeded, conflicted int
	for i := 0; i < 2; i++ {
		err := <-errorsSeen
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("unexpected resolution error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 || accepted.Load() != 1 {
		t.Fatalf("succeeded=%d conflicted=%d accepted=%d", succeeded, conflicted, accepted.Load())
	}
	if resolution := receiveResolution(t, resolutions); !resolution.Allowed() {
		t.Fatalf("Confirm returned %+v", resolution)
	}
}

func TestBrokerFaceResolutionCancelsLocalFallback(t *testing.T) {
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor)
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	fallbackStarted := make(chan struct{})
	fallbackCancelled := make(chan struct{})
	broker.SetFallbackResolver(func(ctx context.Context, _ protocol.ApprovalRequest) (Actor, protocol.FaceApprovalDecision, string, bool) {
		close(fallbackStarted)
		<-ctx.Done()
		close(fallbackCancelled)
		return Actor{}, "", "", false
	})
	resolutions := make(chan Resolution, 1)
	go func() { resolutions <- broker.Confirm(context.Background(), approvalRequest()) }()
	request := receiveRequest(t, requests)
	select {
	case <-fallbackStarted:
	case <-time.After(time.Second):
		t.Fatal("local fallback did not start")
	}
	if _, err := broker.Resolve(request.ApprovalID, Actor{ID: "face-1", Source: "face"}, protocol.FaceApprovalAllowOnce, "", ResolveHooks{}); err != nil {
		t.Fatal(err)
	}
	if resolution := receiveResolution(t, resolutions); !resolution.Allowed() {
		t.Fatalf("Face resolution = %+v", resolution)
	}
	select {
	case <-fallbackCancelled:
	case <-time.After(time.Second):
		t.Fatal("local fallback was not cancelled after Face resolution")
	}
}

func TestBrokerRejectsExpiredAndUnownedResolution(t *testing.T) {
	current := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor, func(config *Config) { config.Now = func() time.Time { return current } })
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	resolutions := make(chan Resolution, 1)
	go func() { resolutions <- broker.Confirm(context.Background(), approvalRequest()) }()
	request := receiveRequest(t, requests)
	actor := Actor{ID: "face-1", Source: "face"}

	_, err := broker.Resolve(request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "", ResolveHooks{
		Validate: func(protocol.ApprovalRequest) bool { return false },
	})
	if !errors.Is(err, ErrNotOwned) {
		t.Fatalf("unowned Resolve error = %v", err)
	}
	current = request.ExpiresAt.Add(time.Nanosecond)
	_, err = broker.Resolve(request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "", ResolveHooks{})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expired Resolve error = %v", err)
	}
	if resolution := receiveResolution(t, resolutions); resolution.Decision != "" {
		t.Fatalf("expired approval returned %+v", resolution)
	}
	if record := auditor.record(request.ApprovalID); record.Status != StatusExpired || !record.ResolvedAt.Equal(current) {
		t.Fatalf("expired audit = %+v", record)
	}
	if _, err := broker.Resolve(request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "", ResolveHooks{}); !errors.Is(err, ErrExpired) {
		t.Fatalf("repeated expired Resolve error = %v", err)
	}
}

func TestBrokerResolvedStatusRemainsConflictAfterExpiry(t *testing.T) {
	current := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor, func(config *Config) { config.Now = func() time.Time { return current } })
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	go broker.Confirm(context.Background(), approvalRequest())
	request := receiveRequest(t, requests)
	actor := Actor{ID: "face-1", Source: "face"}
	if _, err := broker.Resolve(request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "", ResolveHooks{}); err != nil {
		t.Fatal(err)
	}
	current = request.ExpiresAt.Add(time.Hour)
	if _, err := broker.Resolve(request.ApprovalID, actor, protocol.FaceApprovalAllowOnce, "", ResolveHooks{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resolved approval after expiry error = %v", err)
	}
}

func TestBrokerCancellationTerminatesLocallyWhenAuditFinishFails(t *testing.T) {
	auditor := newMemoryAuditor()
	auditor.finishErr = errors.New("disk unavailable")
	broker := newTestBroker(t, auditor)
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	resolutions := make(chan Resolution, 1)
	go func() { resolutions <- broker.Confirm(ctx, approvalRequest()) }()
	request := receiveRequest(t, requests)
	cancel()
	if resolution := receiveResolution(t, resolutions); resolution.Decision != "" {
		t.Fatalf("cancelled approval returned %+v", resolution)
	}
	if pending, err := broker.Pending(request.ConversationID); err != nil || len(pending) != 0 {
		t.Fatalf("Pending = %+v, %v", pending, err)
	}
	_, err := broker.Resolve(request.ApprovalID, Actor{ID: "face-1", Source: "face"}, protocol.FaceApprovalAllowOnce, "", ResolveHooks{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve after cancellation error = %v", err)
	}
}

func TestBrokerDoesNotAcceptWhenResolvedAuditFails(t *testing.T) {
	auditor := newMemoryAuditor()
	auditor.finishErr = errors.New("disk unavailable")
	broker := newTestBroker(t, auditor)
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go broker.Confirm(ctx, approvalRequest())
	request := receiveRequest(t, requests)
	var accepted atomic.Bool
	_, err := broker.Resolve(request.ApprovalID, Actor{ID: "face-1", Source: "face"}, protocol.FaceApprovalAllowOnce, "", ResolveHooks{
		Accepted: func(protocol.ApprovalRequest) bool {
			accepted.Store(true)
			return true
		},
	})
	if err == nil || accepted.Load() {
		t.Fatalf("Resolve error=%v accepted=%t", err, accepted.Load())
	}
}

func TestBrokerDoesNotAllowToolWhenAcceptedCannotBeQueued(t *testing.T) {
	auditor := newMemoryAuditor()
	broker := newTestBroker(t, auditor)
	requests := make(chan protocol.ApprovalRequest, 1)
	broker.Subscribe(func(request protocol.ApprovalRequest) { requests <- request }, nil)
	resolutions := make(chan Resolution, 1)
	go func() { resolutions <- broker.Confirm(context.Background(), approvalRequest()) }()
	request := receiveRequest(t, requests)

	resolved, err := broker.Resolve(
		request.ApprovalID,
		Actor{ID: "face-1", Source: "face"},
		protocol.FaceApprovalAllowOnce,
		"approved",
		ResolveHooks{Accepted: func(protocol.ApprovalRequest) bool { return false }},
	)
	if !errors.Is(err, ErrNotAccepted) || !resolved.Allowed() {
		t.Fatalf("Resolve = %+v, %v", resolved, err)
	}
	if result := receiveResolution(t, resolutions); result.Decision != "" {
		t.Fatalf("Confirm returned executable resolution %+v", result)
	}
	record := auditor.record(request.ApprovalID)
	if record.Status != StatusResolved || record.Decision != protocol.FaceApprovalAllowOnce {
		t.Fatalf("audit = %+v", record)
	}
}
