// Package agentcore 是系统唯一的智能节点。
package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	mindlifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/observer"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// Core 是 agent core 的主体。
type Core struct {
	llm               llm.Provider
	providerID        string
	modelID           string
	exec              toolCatalog
	history           []llm.Message
	persistedMessages int
	persistedSeq      int
	Bus               *events.EventBus // Mind 的事件总线，nil 时不发事件
	skills            *skill.Store
	store             *store.Store
	sessionID         string
	Debug             bool
	Mode              string // "strict" | "normal" | "review" | "yolo"（trust/ai_review 为兼容别名）
	approver          Approver
	activeHand        string // 当前会话的默认 Hand ID
	groupID           string
	chatMu            sync.Mutex
	stateMu           sync.RWMutex
	policy            *security.Policy
	lifecycle         *corelifecycle.LifecycleRegistry
	authorizer        *mindlifecycle.MindAuthorizer
	toolRuntime       *executor.ToolRuntime
	sessionChanges    observer.Hub[struct{}]
	compactRuntime    compact.RuntimeConfig
	compaction        CompactionCoordinator
	usageAnchor       *compact.UsageAnchor
}

// BudgetObservation 是一次最终主模型请求的上下文预算事实。
type BudgetObservation struct {
	FirstRequest    bool
	EstimatedTokens int64
	HighLimit       int64
	HardLimit       int64
	Degraded        bool
}

// CompactionCoordinator 由 Conversation Actor 实现首次自动压缩和 durable pending 编排。
type CompactionCoordinator interface {
	ObserveModelBudget(context.Context, BudgetObservation) (bool, error)
}

// ToolBatchPending 是与完整 tool-result 批次同事务提交的自动 Compact hint。
type ToolBatchPending struct {
	ID    string
	Event store.LifecycleEvent
}

// ToolBatchCompactionCoordinator 可在工具批次持久化前生成 durable pending mutation。
type ToolBatchCompactionCoordinator interface {
	PrepareToolBatchPending(context.Context, BudgetObservation) (*ToolBatchPending, error)
}

// SubscribeSessionChanges 注册持久化 conversation 状态变化观察者。
func (c *Core) SubscribeSessionChanges(subscriber func()) func() {
	if subscriber == nil {
		return func() {}
	}
	return c.sessionChanges.Subscribe(func(struct{}) { subscriber() })
}

// Approver 通过 conversation 级审批对象处理用户确认。
type Approver interface {
	Confirm(context.Context, approval.Request) approval.Resolution
}

const maxToolCallSteps = 10

type toolCatalog interface {
	Tools() []executor.Tool
}

func New(llmProvider llm.Provider, exec toolCatalog) (*Core, error) {
	policy := security.New()
	registry := corelifecycle.NewRegistry()
	authorizer := mindlifecycle.NewMindAuthorizer("normal", policy, nil)
	core := &Core{
		llm:        llmProvider,
		exec:       exec,
		Mode:       "normal",
		policy:     policy,
		lifecycle:  registry,
		authorizer: authorizer,
	}
	if err := core.installLifecycleRegistry(registry); err != nil {
		return nil, err
	}
	core.toolRuntime = executor.NewToolRuntime(authorizer, registry)
	authorizer.SetReviewObserver(core.observeSecurityReview)
	return core, nil
}

// SetMode 切换并持久化安全模式。
func (c *Core) SetMode(mode string) error {
	if !validSecurityMode(mode) {
		return fmt.Errorf("invalid security mode %q", mode)
	}
	mode = canonicalSecurityMode(mode)
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	c.stateMu.RLock()
	store, sessionID := c.store, c.sessionID
	c.stateMu.RUnlock()
	if store != nil && sessionID != "" {
		if err := store.SetSessionMode(sessionID, mode); err != nil {
			return err
		}
	}
	c.stateMu.Lock()
	c.Mode = mode
	c.policy.Mode = modeToSecurityMode(mode)
	policy := c.policy.Clone()
	c.stateMu.Unlock()
	c.authorizer.SetMode(mode, policy)
	c.notifySessionChanged()
	return nil
}

func (c *Core) notifySessionChanged() {
	c.sessionChanges.Publish(struct{}{})
}

func modeToSecurityMode(mode string) security.Mode {
	switch canonicalSecurityMode(mode) {
	case "strict":
		return security.ModeStrict
	case "review":
		return security.ModeTrust
	case "yolo":
		return security.ModeYOLO
	default:
		return security.ModeNormal
	}
}

func canonicalSecurityMode(mode string) string {
	switch mode {
	case "trust", "ai_review":
		return "review"
	default:
		return mode
	}
}

func validSecurityMode(mode string) bool {
	switch mode {
	case "strict", "normal", "review", "ai_review", "trust", "yolo":
		return true
	default:
		return false
	}
}

// publish 向事件总线同步发送事件，Bus 为 nil 时静默跳过。
func (c *Core) publish(level, typ, msg string) {
	c.stateMu.RLock()
	bus, sessionID := c.Bus, c.sessionID
	c.stateMu.RUnlock()
	if bus != nil {
		bus.PublishSync(events.New(sessionID, "agentcore", level, typ, msg))
	}
}

func (c *Core) SetApprover(a Approver) {
	c.stateMu.Lock()
	c.approver = a
	c.stateMu.Unlock()
	c.authorizer.SetApprover(a)
}

// SetReviewer 设置与会话模型隔离的安全 Reviewer。
func (c *Core) SetReviewer(reviewer mindlifecycle.Reviewer) {
	c.authorizer.SetReviewer(reviewer)
}

// SetModelIdentity 设置只用于脱敏生命周期和审计关联的 provider/model 标识。
func (c *Core) SetModelIdentity(providerID, modelID string) {
	c.stateMu.Lock()
	c.providerID = providerID
	c.modelID = modelID
	c.stateMu.Unlock()
}

// SetCompactRuntime 设置主模型预算以及 Actor 自动压缩编排入口。
func (c *Core) SetCompactRuntime(runtime compact.RuntimeConfig, coordinator CompactionCoordinator) {
	c.stateMu.Lock()
	c.compactRuntime = runtime
	c.compaction = coordinator
	c.stateMu.Unlock()
}

// SetLifecycleRegistry 替换会话生命周期注册表、安装内置 Transformer 并重建 ToolRuntime。
func (c *Core) SetLifecycleRegistry(registry *corelifecycle.LifecycleRegistry) error {
	if registry == nil {
		registry = corelifecycle.NewRegistry()
	}
	c.stateMu.RLock()
	current := c.lifecycle
	c.stateMu.RUnlock()
	if current == registry {
		return nil
	}
	if err := c.installLifecycleRegistry(registry); err != nil {
		return err
	}
	c.stateMu.Lock()
	c.lifecycle = registry
	c.toolRuntime = executor.NewToolRuntime(c.authorizer, registry)
	c.stateMu.Unlock()
	if current != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = current.CloseObservers(closeCtx)
		cancel()
	}
	return nil
}

// CloseLifecycle 排空并停止当前会话的生命周期 Observer worker。
func (c *Core) CloseLifecycle(ctx context.Context) error {
	c.stateMu.RLock()
	registry := c.lifecycle
	c.stateMu.RUnlock()
	if registry == nil {
		return nil
	}
	return registry.CloseObservers(ctx)
}

func (c *Core) installLifecycleRegistry(registry *corelifecycle.LifecycleRegistry) error {
	return registry.RegisterTransformer(corelifecycle.Registration{
		ID: "builtin.agentcore-model-context", Kind: corelifecycle.KindTransformer,
		Phases: []corelifecycle.Phase{corelifecycle.PhaseModelBeforeRequest}, Order: -1000,
		FailureMode:  corelifecycle.FailureFailClosed,
		Capabilities: []corelifecycle.Capability{corelifecycle.CapabilityTransform},
	}, coreModelContextTransformer{core: c})
}

func (c *Core) SetSkills(s *skill.Store) {
	c.stateMu.Lock()
	c.skills = s
	c.stateMu.Unlock()
}

// SetStore 注入持久化存储并加载已有会话历史。
func (c *Core) SetStore(s *store.Store, sessionID string) error {
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	session, err := s.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("load session metadata: %w", err)
	}
	if session == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}
	if !validSecurityMode(session.Mode) {
		return fmt.Errorf("session %q has invalid mode %q", sessionID, session.Mode)
	}
	mode := canonicalSecurityMode(session.Mode)
	if mode != session.Mode {
		if err := s.SetSessionMode(sessionID, mode); err != nil {
			return fmt.Errorf("migrate session security mode: %w", err)
		}
	}
	msgs, err := s.GetMessages(sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.store = s
	c.sessionID = sessionID
	c.Mode = mode
	c.policy.Mode = modeToSecurityMode(mode)
	policy := c.policy.Clone()
	c.groupID = session.GroupID
	c.activeHand = session.ActiveHand
	c.history = storeMsgToLLM(msgs)
	c.persistedMessages = len(msgs)
	if len(msgs) > 0 {
		c.persistedSeq = msgs[len(msgs)-1].Seq
	} else {
		c.persistedSeq = 0
	}
	c.authorizer.SetMode(mode, policy)
	return nil
}

func (c *Core) SessionID() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.sessionID
}

// SecurityMode 返回当前会话安全模式。
func (c *Core) SecurityMode() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.Mode
}

// ToggleDebug 切换调试输出并返回新值。
func (c *Core) ToggleDebug() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.Debug = !c.Debug
	return c.Debug
}

// ExecuteTool 通过当前会话执行器调用工具，供非 LLM 入口复用。
func (c *Core) ExecuteTool(ctx context.Context, name string, args json.RawMessage) *executor.ToolResult {
	return c.executeTool(ctx, name, args, false, "")
}

type toolContextPreparer interface {
	PrepareToolContext(context.Context) context.Context
}

func (c *Core) executeTool(ctx context.Context, name string, args json.RawMessage, forceApproval bool, purpose string) *executor.ToolResult {
	return c.executeToolWithMeta(ctx, name, args, forceApproval, purpose, corelifecycle.Meta{})
}

func (c *Core) executeToolWithMeta(ctx context.Context, name string, args json.RawMessage, forceApproval bool, purpose string, meta corelifecycle.Meta) *executor.ToolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if preparer, ok := c.exec.(toolContextPreparer); ok {
		ctx = preparer.PrepareToolContext(ctx)
	}
	c.stateMu.RLock()
	runtime := c.toolRuntime
	sessionID, groupID := c.sessionID, c.groupID
	c.stateMu.RUnlock()
	if meta.TraceID == "" {
		meta = corelifecycle.NewMeta(corelifecycle.SourceMind).
			WithConversation(sessionID).
			WithGroup(groupID).
			WithRequest(requestctx.RequestID(ctx)).
			WithNode("mind")
	}
	result := runtime.Execute(ctx, executor.Invocation{
		Meta: meta, Tool: name, Args: args, ForceUserApproval: forceApproval, Purpose: purpose,
	})
	toolResult := &executor.ToolResult{
		Data: result.Data, CompactReason: result.CompactProjection.ReasonCategory,
		CompactFacts:      append([]executor.CompactFact(nil), result.CompactProjection.CandidateFacts...),
		CompactProjection: result.CompactProjection,
	}
	if result.ExecutionOutcome == executor.ExecutionSucceeded && result.DeliveryOutcome == corelifecycle.OutcomeSucceeded {
		toolResult.Success = true
		toolResult.Output = result.Output
		return toolResult
	}
	if result.DeliveryOutcome == corelifecycle.OutcomeDenied {
		toolResult.Output = result.Output
		toolResult.Error = result.Output
		return toolResult
	}
	toolResult.Error = result.Output
	if toolResult.Error == "" {
		toolResult.Error = result.ErrorCode
	}
	return toolResult
}

func truncate(s string) string {
	runes := []rune(s)
	if len(runes) <= 100 {
		return s
	}
	return string(runes[:100]) + "…"
}
