// Package lifecycle 提供 Mind 侧的 lifecycle 协调器、范围过滤和 EventBus 适配器。
package lifecycle

import (
	"context"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
)

// Coordinator 是 Mind 侧的生命周期协调器。
// 它持有 core lifecycle Registry 并提供 Mind 特有的适配逻辑。
type Coordinator struct {
	Registry *corelifecycle.LifecycleRegistry
	bus      *events.EventBus
}

// Config 是 Coordinator 的配置。
type Config struct {
	Bus      *events.EventBus
	Registry *corelifecycle.LifecycleRegistry
	Auditor  corelifecycle.Auditor
}

// NewCoordinator 创建生命周期协调器。
func NewCoordinator(cfg Config) (*Coordinator, error) {
	reg := cfg.Registry
	if reg == nil {
		reg = corelifecycle.NewRegistry()
	}
	c := &Coordinator{
		Registry: reg,
		bus:      cfg.Bus,
	}

	// 注册内置 EventBus 适配 Observer
	if cfg.Bus != nil {
		adapter := &eventBusAdapter{bus: cfg.Bus}
		if err := reg.RegisterObserver(corelifecycle.Registration{
			ID:          "builtin.eventbus-adapter",
			Kind:        corelifecycle.KindObserver,
			Phases:      allObservationPhases(),
			Order:       1000, // 最后执行
			FailureMode: corelifecycle.FailureFailOpen,
		}, adapter); err != nil {
			return nil, fmt.Errorf("register EventBus lifecycle adapter: %w", err)
		}
	}
	if cfg.Auditor != nil {
		if err := reg.RegisterAuditor(corelifecycle.Registration{
			ID: "builtin.security-auditor", Kind: corelifecycle.KindAuditor,
			Phases: []corelifecycle.Phase{
				corelifecycle.PhaseMessageAdmitted,
				corelifecycle.PhaseModelRequested,
				corelifecycle.PhaseAssistantDelivered,
				corelifecycle.PhaseToolAuthorized,
				corelifecycle.PhaseToolDenied,
				corelifecycle.PhaseToolFinished,
				corelifecycle.PhaseSecurityDecision,
			},
			FailureMode: corelifecycle.FailureFailClosed,
		}, cfg.Auditor); err != nil {
			return nil, fmt.Errorf("register lifecycle auditor: %w", err)
		}
	}

	return c, nil
}

// EventBusAdapter 返回将 lifecycle 事件投影到 EventBus 的适配器。
// 调用方可直接使用或自定义。
func (c *Coordinator) EventBusAdapter(sessionID string) *eventBusAdapter {
	return &eventBusAdapter{bus: c.bus, sessionID: sessionID}
}

// eventBusAdapter 将 RedactedEvent 转为旧 EventBus Event。
type eventBusAdapter struct {
	bus       *events.EventBus
	sessionID string
}

func (a *eventBusAdapter) ID() string { return "eventbus-adapter" }

func (a *eventBusAdapter) Observe(ctx context.Context, event corelifecycle.RedactedEvent) {
	if a.bus == nil {
		return
	}
	sessionID := a.sessionID
	if sessionID == "" {
		sessionID = event.ConversationID
	}

	var eventType, message string
	switch event.Phase {
	case corelifecycle.PhaseToolFrozen:
		eventType = events.TypeToolCall
		message = fmt.Sprintf("工具调用: %s", event.ResourceName)
	case corelifecycle.PhaseToolDenied:
		eventType = events.TypeToolBlock
		message = fmt.Sprintf("工具被拒绝: %s (%s)", event.ResourceName, event.ReasonCode)
	case corelifecycle.PhaseToolStarted:
		eventType = events.TypeToolCall
		message = fmt.Sprintf("工具开始: %s", event.ResourceName)
	case corelifecycle.PhaseToolFinished:
		eventType = events.TypeToolResult
		message = fmt.Sprintf("工具完成: %s (outcome=%s)", event.ResourceName, event.Outcome)
	case corelifecycle.PhaseModelRequested:
		eventType = events.TypeLLMRequest
		message = fmt.Sprintf("模型请求: %s/%s", event.ProviderID, event.ModelID)
	case corelifecycle.PhaseModelResponseReceived:
		eventType = events.TypeLLMResponse
		message = "模型响应已接收"
	default:
		return // 不转发未映射的阶段
	}

	evt := events.New(sessionID, "lifecycle-coordinator", events.LevelInfo, eventType, message)
	a.bus.Publish(evt)
}

func allObservationPhases() []corelifecycle.Phase {
	return []corelifecycle.Phase{
		corelifecycle.PhaseToolFrozen,
		corelifecycle.PhaseToolDenied,
		corelifecycle.PhaseToolAuthorized,
		corelifecycle.PhaseToolStarted,
		corelifecycle.PhaseToolFinished,
		corelifecycle.PhaseModelRequested,
		corelifecycle.PhaseModelResponseReceived,
	}
}
