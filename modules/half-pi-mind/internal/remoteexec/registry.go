// Package remoteexec 管理 Mind 侧远程执行生命周期。
package remoteexec

import (
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// Run 是一次远程执行的内存状态。
type Run struct {
	ID           string
	SessionID    string
	HandID       string
	ConnectionID string
	Tool         string
	Status       protocol.RunStatus

	CreatedAt  time.Time
	SentAt     time.Time
	AcceptedAt time.Time
	FinishedAt time.Time

	Result       *protocol.RPCResult
	Rejection    *protocol.RPCRejected
	Error        string
	CancelReason string
	Metadata     AuditMetadata

	done    chan struct{}
	waiters int
}

// Registry 按 run_id 原子管理远程执行状态。
type Registry struct {
	mu      sync.Mutex
	runs    map[string]*Run
	auditor Auditor
}

const (
	terminalRunRetention = 10 * time.Minute
	maxTerminalRuns      = 100
)

// NewRegistry 创建空的远程执行注册表。
func NewRegistry(auditor ...Auditor) *Registry {
	registry := &Registry{runs: make(map[string]*Run)}
	if len(auditor) > 0 {
		registry.auditor = auditor[0]
	}
	return registry
}

// Create 创建 run，初始状态为 created。
func (r *Registry) Create(id, sessionID, handID, tool string) error {
	return r.CreateWithMetadata(id, sessionID, handID, tool, AuditMetadata{})
}

// CreateWithMetadata 创建带脱敏审批元数据的 run。
func (r *Registry) CreateWithMetadata(id, sessionID, handID, tool string, metadata AuditMetadata) error {
	return r.CreateForPeer(id, sessionID, handID, "", tool, metadata)
}

// CreateForPeer 创建并绑定具体 Hand 连接的 run。
func (r *Registry) CreateForPeer(id, sessionID, handID, connectionID, tool string, metadata AuditMetadata) error {
	if id == "" || handID == "" || tool == "" {
		return fmt.Errorf("run id, hand id and tool are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneRunsLocked(time.Now())
	if _, exists := r.runs[id]; exists {
		return fmt.Errorf("run %q already exists", id)
	}
	run := &Run{
		ID: id, SessionID: sessionID, HandID: handID, ConnectionID: connectionID, Tool: tool,
		Status: protocol.RunCreated, CreatedAt: time.Now(), Metadata: metadata, done: make(chan struct{}),
	}
	if r.auditor != nil {
		if err := r.auditor.CreateRemoteRun(AuditRun{
			ID: id, SessionID: sessionID, HandID: handID, Tool: tool,
			Metadata: metadata, Status: run.Status, CreatedAt: run.CreatedAt,
		}); err != nil {
			return err
		}
	}
	r.runs[id] = run
	return nil
}

// Transition 执行一次合法状态迁移。
func (r *Registry) Transition(id string, to protocol.RunStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return fmt.Errorf("run %q not found", id)
	}
	return r.transitionLocked(run, to, time.Now(), AuditTransition{})
}

// Done 返回 run 进入终态时关闭的通道。
func (r *Registry) Done(id string) (<-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return nil, false
	}
	return run.done, true
}

// Wait 注册一个活跃等待者，release 后终态 run 才可被淘汰。
func (r *Registry) Wait(id string) (<-chan struct{}, func(), bool) {
	r.mu.Lock()
	run, ok := r.runs[id]
	if !ok {
		r.mu.Unlock()
		return nil, nil, false
	}
	run.waiters++
	done := run.done
	r.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			r.mu.Lock()
			if current := r.runs[id]; current != nil && current.waiters > 0 {
				current.waiters--
			}
			r.mu.Unlock()
		})
	}
	return done, release, true
}

// Snapshot 返回 run 的只读快照。
func (r *Registry) Snapshot(id string) (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return Run{}, false
	}
	copy := *run
	if run.Result != nil {
		result := *run.Result
		copy.Result = &result
	}
	if run.Rejection != nil {
		rejection := *run.Rejection
		copy.Rejection = &rejection
	}
	copy.done = nil
	return copy, true
}
