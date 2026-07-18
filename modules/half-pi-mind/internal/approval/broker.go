package approval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const (
	defaultTTL          = 2 * time.Minute
	terminalRetention   = 10 * time.Minute
	maxTerminal         = 256
	maxActorIDBytes     = 128
	maxActorLabelBytes  = 64
	maxActorSourceBytes = 32
)

// Config 配置进程级审批 Broker。
type Config struct {
	Auditor Auditor
	TTL     time.Duration
	Now     func() time.Time
}

type entry struct {
	request       protocol.ApprovalRequest
	status        Status
	resolution    Resolution
	confirmResult Resolution
	createdAt     time.Time
	completedAt   time.Time
	done          chan struct{}
}

// Broker 是审批请求、裁决、过期和审计的唯一进程级权威。
type Broker struct {
	mu       sync.Mutex
	entries  map[string]*entry
	auditor  Auditor
	ttl      time.Duration
	now      func() time.Time
	closed   bool
	fallback FallbackResolver

	observerMu sync.RWMutex
	requested  func(protocol.ApprovalRequest)
	finished   func(protocol.ApprovalRequest, Status, Resolution)
}

// New 创建审批 Broker。
func New(config Config) (*Broker, error) {
	if config.Auditor == nil {
		return nil, fmt.Errorf("approval auditor is required")
	}
	if config.TTL < 0 {
		return nil, fmt.Errorf("approval TTL must not be negative")
	}
	if config.TTL == 0 {
		config.TTL = defaultTTL
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Broker{
		entries: make(map[string]*entry), auditor: config.Auditor,
		ttl: config.TTL, now: config.Now,
	}, nil
}

// OnChange 设置审批请求和终态观察者。观察者在 Broker 锁外调用。
func (b *Broker) OnChange(requested func(protocol.ApprovalRequest), finished func(protocol.ApprovalRequest, Status, Resolution)) {
	b.observerMu.Lock()
	b.requested, b.finished = requested, finished
	b.observerMu.Unlock()
}

// SetFallbackResolver 设置 REPL 等本地裁决适配器。
func (b *Broker) SetFallbackResolver(resolver FallbackResolver) {
	b.mu.Lock()
	b.fallback = resolver
	b.mu.Unlock()
}

// Confirm 注册审批并等待 Face 或本地适配器给出首个合法裁决。
func (b *Broker) Confirm(ctx context.Context, request Request) Resolution {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return Resolution{}
	}
	if request.ConversationID == "" || request.Tool == "" || request.Reason == "" || request.ArgsDigest == "" ||
		len(request.Reason) > protocol.MaxFaceApprovalReasonBytes {
		return Resolution{}
	}
	now := b.now()
	faceRequest := protocol.ApprovalRequest{
		ApprovalID: protocol.MustNewMsgID(), ConversationID: request.ConversationID,
		RequestID: request.RequestID, RunID: request.RunID, Tool: request.Tool,
		Reason: request.Reason, ArgsDigest: request.ArgsDigest, ExpiresAt: now.Add(b.ttl),
	}
	record := AuditRecord{Request: faceRequest, Status: StatusPending, CreatedAt: now}
	if err := b.auditor.CreateApproval(record); err != nil {
		return Resolution{}
	}
	item := &entry{request: faceRequest, status: StatusPending, createdAt: now, done: make(chan struct{})}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = b.auditor.FinishApproval(faceRequest.ApprovalID, StatusCancelled, Resolution{ResolvedAt: now})
		return Resolution{}
	}
	b.pruneLocked(now)
	b.entries[faceRequest.ApprovalID] = item
	fallback := b.fallback
	b.mu.Unlock()
	b.notifyRequested(faceRequest)

	type fallbackResult struct {
		actor    Actor
		decision protocol.FaceApprovalDecision
		reason   string
		ok       bool
	}
	fallbackCtx, cancelFallback := context.WithCancel(ctx)
	defer cancelFallback()
	var fallbackResults <-chan fallbackResult
	if fallback != nil {
		results := make(chan fallbackResult, 1)
		fallbackResults = results
		go func() {
			actor, decision, reason, ok := fallback(fallbackCtx, faceRequest)
			results <- fallbackResult{actor: actor, decision: decision, reason: reason, ok: ok}
		}()
	}

	timer := time.NewTimer(b.ttl)
	defer timer.Stop()
	for {
		select {
		case <-item.done:
			cancelFallback()
			goto finished
		case <-ctx.Done():
			cancelFallback()
			b.finishPending(item, StatusCancelled)
			goto finished
		case <-timer.C:
			cancelFallback()
			b.finishPending(item, StatusExpired)
			goto finished
		case result := <-fallbackResults:
			fallbackResults = nil
			if ctx.Err() != nil {
				cancelFallback()
				b.finishPending(item, StatusCancelled)
				goto finished
			}
			if result.ok {
				_, _ = b.Resolve(faceRequest.ApprovalID, result.actor, result.decision, result.reason, ResolveHooks{})
			}
		}
	}

finished:
	<-item.done
	b.mu.Lock()
	resolution := item.confirmResult
	b.mu.Unlock()
	return resolution
}

// Resolve 原子校验并完成审批。Accepted 在持久化成功后、终态事件发布前调用。
func (b *Broker) Resolve(approvalID string, actor Actor, decision protocol.FaceApprovalDecision, reason string, hooks ResolveHooks) (Resolution, error) {
	if approvalID == "" || actor.ID == "" || actor.Source == "" || !validDecision(decision) ||
		len(actor.ID) > maxActorIDBytes || len(actor.Label) > maxActorLabelBytes || len(actor.Source) > maxActorSourceBytes ||
		len(reason) > protocol.MaxFaceApprovalReasonBytes {
		return Resolution{}, fmt.Errorf("invalid approval resolution")
	}
	now := b.now()
	b.mu.Lock()
	b.pruneLocked(now)
	item := b.entries[approvalID]
	if item == nil {
		b.mu.Unlock()
		return b.resolveMissing(approvalID)
	}
	if item.status == StatusResolved {
		b.mu.Unlock()
		return Resolution{}, ErrConflict
	}
	if item.status == StatusExpired {
		b.mu.Unlock()
		return Resolution{}, ErrExpired
	}
	if item.status != StatusPending {
		b.mu.Unlock()
		return Resolution{}, ErrNotFound
	}
	if !now.Before(item.request.ExpiresAt) {
		var finishErr error
		finishErr = b.forceFinishLocked(item, StatusExpired, Resolution{ResolvedAt: now})
		request := item.request
		b.mu.Unlock()
		b.notifyFinished(request, StatusExpired, Resolution{ResolvedAt: now})
		close(item.done)
		return Resolution{}, errors.Join(ErrExpired, finishErr)
	}
	if hooks.Validate != nil && !hooks.Validate(item.request) {
		b.mu.Unlock()
		return Resolution{}, ErrNotOwned
	}
	resolution := Resolution{
		ApprovalID: approvalID, ConversationID: item.request.ConversationID,
		Decision: decision, Actor: actor,
		Reason: reason, ResolvedAt: now,
	}
	if err := b.finishLocked(item, StatusResolved, resolution); err != nil {
		b.mu.Unlock()
		return Resolution{}, err
	}
	request := item.request
	accepted := hooks.Accepted == nil || hooks.Accepted(request)
	if !accepted {
		item.confirmResult = Resolution{}
	}
	b.mu.Unlock()
	b.notifyFinished(request, StatusResolved, resolution)
	close(item.done)
	if !accepted {
		return resolution, ErrNotAccepted
	}
	return resolution, nil
}

// Pending 返回指定 conversation 尚可裁决的审批摘要。
func (b *Broker) Pending(conversationID string) ([]protocol.ApprovalSummary, error) {
	now := b.now()
	b.mu.Lock()
	b.pruneLocked(now)
	result := make([]protocol.ApprovalSummary, 0)
	expired := make([]*entry, 0)
	var resultErr error
	for _, item := range b.entries {
		if item.status != StatusPending || item.request.ConversationID != conversationID {
			continue
		}
		if !now.Before(item.request.ExpiresAt) {
			resultErr = errors.Join(resultErr, b.forceFinishLocked(item, StatusExpired, Resolution{ResolvedAt: now}))
			expired = append(expired, item)
			continue
		}
		result = append(result, item.request)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ExpiresAt.Equal(result[j].ExpiresAt) {
			return result[i].ApprovalID < result[j].ApprovalID
		}
		return result[i].ExpiresAt.Before(result[j].ExpiresAt)
	})
	b.mu.Unlock()
	for _, item := range expired {
		b.notifyFinished(item.request, StatusExpired, item.resolution)
		close(item.done)
	}
	return result, resultErr
}

// Close 取消全部仍在等待的审批。
func (b *Broker) Close() error {
	now := b.now()
	b.mu.Lock()
	b.closed = true
	var result error
	finished := make([]*entry, 0)
	for _, item := range b.entries {
		if item.status != StatusPending {
			continue
		}
		if err := b.forceFinishLocked(item, StatusCancelled, Resolution{ResolvedAt: now}); err != nil {
			result = errors.Join(result, err)
		}
		finished = append(finished, item)
	}
	b.mu.Unlock()
	for _, item := range finished {
		b.notifyFinished(item.request, StatusCancelled, item.resolution)
		close(item.done)
	}
	return result
}

func (b *Broker) finishPending(item *entry, status Status) {
	b.mu.Lock()
	finished := false
	if item.status == StatusPending {
		_ = b.forceFinishLocked(item, status, Resolution{ResolvedAt: b.now()})
		finished = true
	}
	b.mu.Unlock()
	if finished {
		b.notifyFinished(item.request, status, item.resolution)
		close(item.done)
	}
}

func (b *Broker) finishLocked(item *entry, status Status, resolution Resolution) error {
	if err := b.auditor.FinishApproval(item.request.ApprovalID, status, resolution); err != nil {
		return fmt.Errorf("finish approval audit: %w", err)
	}
	b.completeLocked(item, status, resolution)
	return nil
}

func (b *Broker) forceFinishLocked(item *entry, status Status, resolution Resolution) error {
	err := b.auditor.FinishApproval(item.request.ApprovalID, status, resolution)
	b.completeLocked(item, status, resolution)
	if err != nil {
		return fmt.Errorf("finish approval audit: %w", err)
	}
	return nil
}

func (b *Broker) completeLocked(item *entry, status Status, resolution Resolution) {
	item.status, item.resolution, item.confirmResult, item.completedAt = status, resolution, resolution, b.now()
}

func (b *Broker) resolveMissing(approvalID string) (Resolution, error) {
	record, found, err := b.auditor.LookupApproval(approvalID)
	if err != nil {
		return Resolution{}, fmt.Errorf("lookup approval audit: %w", err)
	}
	if !found || record.Status == StatusCancelled || record.Status == StatusPending {
		return Resolution{}, ErrNotFound
	}
	if record.Status == StatusExpired {
		return Resolution{}, ErrExpired
	}
	return Resolution{}, ErrConflict
}

func (b *Broker) pruneLocked(now time.Time) {
	terminal := make([]*entry, 0)
	for id, item := range b.entries {
		if item.status == StatusPending {
			continue
		}
		if now.Sub(item.completedAt) > terminalRetention {
			delete(b.entries, id)
			continue
		}
		terminal = append(terminal, item)
	}
	if len(terminal) <= maxTerminal {
		return
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].completedAt.Before(terminal[j].completedAt) })
	for _, item := range terminal[:len(terminal)-maxTerminal] {
		delete(b.entries, item.request.ApprovalID)
	}
}

func (b *Broker) notifyRequested(request protocol.ApprovalRequest) {
	b.observerMu.RLock()
	observer := b.requested
	b.observerMu.RUnlock()
	if observer != nil {
		observer(request)
	}
}

func (b *Broker) notifyFinished(request protocol.ApprovalRequest, status Status, resolution Resolution) {
	b.observerMu.RLock()
	observer := b.finished
	b.observerMu.RUnlock()
	if observer != nil {
		observer(request, status, resolution)
	}
}

func validDecision(decision protocol.FaceApprovalDecision) bool {
	switch decision {
	case protocol.FaceApprovalAllowOnce, protocol.FaceApprovalDenyOnce,
		protocol.FaceApprovalAllowSession, protocol.FaceApprovalDenySession:
		return true
	default:
		return false
	}
}
