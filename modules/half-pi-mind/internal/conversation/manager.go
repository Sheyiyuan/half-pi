// Package conversation 管理每个持久化对话独立的 Agent Core 与远程执行桥。
package conversation

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
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
	core   *agentcore.Core
	bridge *local.RemoteBridge
}

// Core 返回该 conversation 独占的 Agent Core。
func (a *Actor) Core() *agentcore.Core { return a.core }

// Bridge 返回该 conversation 独占的远程执行桥。
func (a *Actor) Bridge() *local.RemoteBridge { return a.bridge }

// Manager 按 conversation ID 惰性创建并缓存 Actor。
type Manager struct {
	config Config

	mu      sync.Mutex
	actors  map[string]*Actor
	changes observer.Hub[string]
}

// Subscribe 注册 conversation 持久化状态变化观察者。
func (m *Manager) Subscribe(subscriber func(string)) func() {
	return m.changes.Subscribe(subscriber)
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
	return &Actor{core: core, bridge: bridge}, nil
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
