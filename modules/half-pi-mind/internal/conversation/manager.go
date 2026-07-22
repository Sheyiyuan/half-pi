// Package conversation 管理每个持久化对话独立的 Agent Core 与远程执行桥。
package conversation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	mindlifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/observer"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// ErrNotFound 表示 conversation 不存在。
var ErrNotFound = errors.New("conversation not found")

// ErrBusy 表示 conversation 正由另一个 mutation operation 占用。
var ErrBusy = errors.New("conversation busy")

// Config 是 Conversation Manager 的进程级依赖。
type Config struct {
	GroupID    string
	Provider   llm.Provider
	ProviderID string
	ModelID    string
	Reviewer   mindlifecycle.Reviewer
	Store      *store.Store
	Bus        *events.EventBus
	Skills     *skill.Store
	Approvals  *approval.Broker
	Authority  *remoteexec.Authority
	Tasks      *remoteexec.TaskService
}

// Actor 持有一个 conversation 独占的可变运行时状态。
type Actor struct {
	core           *agentcore.Core
	bridge         *local.RemoteBridge
	compactor      compact.Compactor
	runtime        compact.RuntimeConfig
	onChange       func()
	onCompact      func(compact.Event)
	operationMu    sync.Mutex
	operation      OperationState
	requestID      string
	operationToken uint64
	workerMu       sync.Mutex
	workerLive     bool
	workerAgain    bool
	workerCtx      context.Context
	workerStop     context.CancelFunc
	workerWG       sync.WaitGroup
}

// Core 返回该 conversation 独占的 Agent Core。
func (a *Actor) Core() *agentcore.Core { return a.core }

// Bridge 返回该 conversation 独占的远程执行桥。
func (a *Actor) Bridge() *local.RemoteBridge { return a.bridge }

// Manager 按 conversation ID 惰性创建并缓存 Actor。
type Manager struct {
	config Config

	mu                 sync.Mutex
	actors             map[string]*Actor
	changes            observer.Hub[string]
	compactMu          sync.RWMutex
	compactNext        uint64
	compactSubscribers map[uint64]func(compact.Event)
	diagnosticMu       sync.Mutex
	diagnostics        map[string]struct{}
	compactor          compact.Compactor
	runtime            compact.RuntimeConfig
}

// SetCompactor 安装进程共享的 Compactor，并为既有 Actor 启用预算编排。
func (m *Manager) SetCompactor(compactor compact.Compactor, runtime compact.RuntimeConfig) {
	m.mu.Lock()
	m.compactor, m.runtime = compactor, runtime
	actors := make([]*Actor, 0, len(m.actors))
	for _, actor := range m.actors {
		actor.compactor, actor.runtime = compactor, runtime
		actor.core.SetCompactRuntime(runtime, actor)
		actors = append(actors, actor)
	}
	m.mu.Unlock()
	for _, actor := range actors {
		actor.schedulePendingWorker()
		go m.diagnoseUnsupportedCompact(actor)
	}
}

// Snapshot 实现 compact.EnvironmentSource，返回指定 Actor 的最终请求环境。
func (m *Manager) Snapshot(ctx context.Context, sessionID string) (compact.EnvironmentSnapshot, error) {
	actor := m.cachedActor(sessionID)
	if actor == nil {
		return compact.EnvironmentSnapshot{}, fmt.Errorf("conversation environment is unavailable")
	}
	snapshot, err := actor.core.CompactEnvironmentSnapshot(ctx)
	if err != nil {
		return compact.EnvironmentSnapshot{}, err
	}
	actor.operationMu.Lock()
	snapshot.ActiveRequestID = actor.requestID
	actor.operationMu.Unlock()
	return snapshot, nil
}

// Subscribe 注册 conversation 持久化状态变化观察者。
func (m *Manager) Subscribe(subscriber func(string)) func() {
	return m.changes.Subscribe(subscriber)
}

// SubscribeCompact 同步注册已提交 Compact domain fact 的观察者。
func (m *Manager) SubscribeCompact(subscriber func(compact.Event)) func() {
	if subscriber == nil {
		return func() {}
	}
	m.compactMu.Lock()
	m.compactNext++
	id := m.compactNext
	if m.compactSubscribers == nil {
		m.compactSubscribers = make(map[uint64]func(compact.Event))
	}
	m.compactSubscribers[id] = subscriber
	m.compactMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.compactMu.Lock()
			delete(m.compactSubscribers, id)
			m.compactMu.Unlock()
		})
	}
}

// NewManager 创建可由服务模式和 REPL 共用的 Conversation Manager。
func NewManager(config Config) (*Manager, error) {
	if config.GroupID == "" {
		return nil, fmt.Errorf("conversation group ID is required")
	}
	if config.Provider == nil {
		return nil, fmt.Errorf("LLM provider is required")
	}
	if config.Store == nil {
		return nil, fmt.Errorf("conversation store is required")
	}
	if config.Approvals == nil || config.Authority == nil || config.Tasks == nil {
		return nil, fmt.Errorf("remote execution services are required")
	}
	manager := &Manager{config: config, actors: make(map[string]*Actor)}
	config.Approvals.SubscribeLifecycle(manager.publishApprovalRequested, manager.publishApprovalFinished)
	return manager, nil
}

// GroupID 返回新 conversation 所属的默认工作区。
func (m *Manager) GroupID() string { return m.config.GroupID }

// Close 排空并停止所有已缓存 conversation 的生命周期 Observer。
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	actors := make([]*Actor, 0, len(m.actors))
	for _, actor := range m.actors {
		actors = append(actors, actor)
	}
	m.mu.Unlock()

	var result error
	for _, actor := range actors {
		if actor == nil || actor.core == nil {
			continue
		}
		actor.stopPendingWorker()
		if err := actor.waitPendingWorker(ctx); err != nil {
			result = errors.Join(result, err)
		}
		if err := actor.core.CloseLifecycle(ctx); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

// Create 创建持久化 conversation 并返回其 Actor。
func (m *Manager) Create(name string) (*Actor, error) {
	conversationID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate conversation ID: %w", err)
	}
	id := conversationID.String()
	if err := m.config.Store.CreateSessionNamed(m.config.GroupID, id, name); err != nil {
		return nil, err
	}
	actor, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	m.notify(id)
	return actor, nil
}

// Rename 重命名当前工作区内的 conversation。
func (m *Manager) Rename(id, name string) error {
	session, err := m.config.Store.GetSession(id)
	if err != nil {
		return fmt.Errorf("load conversation metadata: %w", err)
	}
	if session == nil || session.GroupID != m.config.GroupID {
		return ErrNotFound
	}
	if err := m.config.Store.UpdateSessionName(id, name); err != nil {
		return err
	}
	m.notify(id)
	return nil
}

// Get 返回指定 conversation 的唯一 Actor，并在首次访问时恢复持久化状态。
func (m *Manager) Get(id string) (*Actor, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if actor := m.actors[id]; actor != nil {
		return actor, nil
	}
	session, err := m.config.Store.GetSession(id)
	if err != nil {
		return nil, fmt.Errorf("load conversation metadata: %w", err)
	}
	if session == nil || session.GroupID != m.config.GroupID {
		return nil, ErrNotFound
	}
	actor, err := m.newActor(id)
	if err != nil {
		return nil, err
	}
	m.actors[id] = actor
	actor.schedulePendingWorker()
	if actor.compactor != nil {
		go m.diagnoseUnsupportedCompact(actor)
	}
	return actor, nil
}

func (m *Manager) newActor(id string) (*Actor, error) {
	bridge := &local.RemoteBridge{
		Hub:         m.config.Authority.Hub,
		Authority:   m.config.Authority,
		Runs:        m.config.Authority.Registry,
		Tasks:       m.config.Tasks,
		PendingCall: m.config.Authority.PendingCall,
	}
	core, err := agentcore.New(m.config.Provider, local.New(bridge))
	if err != nil {
		return nil, fmt.Errorf("create Agent Core: %w", err)
	}
	core.Bus = m.config.Bus
	core.SetModelIdentity(m.config.ProviderID, m.config.ModelID)
	coordinator, err := mindlifecycle.NewCoordinator(mindlifecycle.Config{
		Bus: m.config.Bus, Auditor: mindlifecycle.NewStoreAuditor(m.config.Store),
	})
	if err != nil {
		return nil, fmt.Errorf("create conversation lifecycle: %w", err)
	}
	if err := core.SetLifecycleRegistry(coordinator.Registry); err != nil {
		return nil, fmt.Errorf("install conversation lifecycle: %w", err)
	}
	core.SetReviewer(m.config.Reviewer)
	core.SetSkills(m.config.Skills)
	core.SetApprover(m.config.Approvals)
	core.SubscribeSessionChanges(func() { m.notify(id) })
	if err := core.SetStore(m.config.Store, id); err != nil {
		return nil, err
	}
	bridge.ActiveHand = core.ActiveHand
	bridge.SessionID = core.SessionID
	bridge.Mode = core.SecurityMode
	bridge.SetActiveHand = core.SetActiveHand
	bridge.PrepareRemote = core.PrepareRemoteTool
	workerCtx, workerStop := context.WithCancel(context.Background())
	actor := &Actor{
		core: core, bridge: bridge, compactor: m.compactor, runtime: m.runtime,
		operation: OperationIdle, onChange: func() { m.notify(id) },
		onCompact: m.publishCompact,
		workerCtx: workerCtx, workerStop: workerStop,
	}
	core.SetCompactRuntime(m.runtime, actor)
	return actor, nil
}

func (m *Manager) publishCompact(event compact.Event) {
	m.compactMu.RLock()
	subscribers := make([]func(compact.Event), 0, len(m.compactSubscribers))
	for _, subscriber := range m.compactSubscribers {
		subscribers = append(subscribers, subscriber)
	}
	m.compactMu.RUnlock()
	for _, subscriber := range subscribers {
		func() {
			defer func() { _ = recover() }()
			subscriber(event)
		}()
	}
}

type unsupportedCompactDiagnostic struct {
	SummaryID         string   `json:"summary_id"`
	FromSeq           int      `json:"from_seq"`
	ToSeq             int      `json:"to_seq"`
	Unsupported       []string `json:"unsupported"`
	ProjectionVersion string   `json:"projection_version,omitempty"`
	PolicyVersion     string   `json:"policy_version,omitempty"`
	Profile           string   `json:"profile,omitempty"`
	Blocker           string   `json:"blocker"`
}

func (m *Manager) diagnoseUnsupportedCompact(actor *Actor) {
	if actor == nil || actor.compactor == nil || m.config.Bus == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := actor.CompactStatus(ctx)
	if err != nil || status.Blocker != compact.ErrUnsupportedVersion || status.ActiveSummaryID == "" {
		return
	}
	key := actor.core.SessionID() + "\x00" + status.ActiveSummaryID + "\x00" + status.ActiveContractDigest
	m.diagnosticMu.Lock()
	if m.diagnostics == nil {
		m.diagnostics = make(map[string]struct{})
	}
	if _, exists := m.diagnostics[key]; exists {
		m.diagnosticMu.Unlock()
		return
	}
	m.diagnostics[key] = struct{}{}
	m.diagnosticMu.Unlock()
	diagnostic := unsupportedCompactDiagnostic{
		SummaryID: status.ActiveSummaryID, FromSeq: status.ActiveFromSeq, ToSeq: status.ActiveToSeq,
		Blocker: string(compact.ErrUnsupportedVersion),
	}
	if status.ActiveProjectionVersion != compact.ProjectionVersion {
		diagnostic.Unsupported = append(diagnostic.Unsupported, "projection_version")
		diagnostic.ProjectionVersion = status.ActiveProjectionVersion
	}
	if status.ActivePolicyVersion != status.PolicyVersion {
		diagnostic.Unsupported = append(diagnostic.Unsupported, "policy_version")
		diagnostic.PolicyVersion = status.ActivePolicyVersion
	}
	if status.ActiveProfile != status.Profile {
		diagnostic.Unsupported = append(diagnostic.Unsupported, "profile")
		diagnostic.Profile = status.ActiveProfile
	}
	m.config.Bus.PublishSync(events.New(actor.core.SessionID(), "compact", events.LevelWarn,
		events.TypeCompactUnsupported, "Active context summary version is unsupported").WithData(diagnostic))
}

func (m *Manager) notify(id string) {
	m.changes.Publish(id)
}

func (m *Manager) publishApprovalRequested(observation approval.Observation) {
	if actor := m.cachedActor(observation.Request.ConversationID); actor != nil {
		actor.core.ObserveApprovalRequested(observation)
	}
}

func (m *Manager) publishApprovalFinished(observation approval.Observation, status approval.Status, resolution approval.Resolution) {
	if actor := m.cachedActor(observation.Request.ConversationID); actor != nil {
		actor.core.ObserveApprovalFinished(observation, status, resolution)
	}
}

func (m *Manager) cachedActor(id string) *Actor {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.actors[id]
}
