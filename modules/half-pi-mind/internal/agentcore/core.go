// Package agentcore 是系统唯一的智能节点。
package agentcore

import (
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/security"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// Core 是 agent core 的主体。
// TODO: 接入 WSS server 后需为 Mode、history、autoAllow/autoDeny 加并发保护。
type Core struct {
	llm       llm.Provider
	exec      executor.ToolExecutor
	history   []llm.Message
	Bus       *events.EventBus // Mind 的事件总线，nil 时不发事件
	skills    *skill.Store
	store     *store.Store
	sessionID string
	Debug     bool
	Mode      string // "normal" | "trust" | "yolo"
	approver  Approver
	autoAllow map[string]bool // 本会话自动放行的工具
	autoDeny  map[string]bool // 本会话自动拒绝的工具
}

// Approver 由 REPL 实现，处理用户确认交互。
type Approver interface {
	Confirm(toolName, reason string) ConfirmResult
}

type ConfirmResult int

const (
	ConfirmDeny ConfirmResult = iota
	ConfirmAllow
	ConfirmAllowAlways
	ConfirmDenyAlways
)

const maxToolCallSteps = 10

func New(llmProvider llm.Provider, exec executor.ToolExecutor) (*Core, error) {
	return &Core{
		llm:       llmProvider,
		exec:      exec,
		Mode:      "normal",
		autoAllow: make(map[string]bool),
		autoDeny:  make(map[string]bool),
	}, nil
}

// SetMode 切换安全模式，同时在对话历史中记录。
// TODO: 被非 REPL 调用方（如 WSS server）调用时，调用方需负责发送事件。
func (c *Core) SetMode(mode string) {
	c.Mode = mode
	security.SetMode(modeToSecurityMode(mode))
	c.history = append(c.history, llm.Message{
		Role:    llm.RoleSystem,
		Content: fmt.Sprintf("安全模式已切换为: %s", mode),
	})
}

func modeToSecurityMode(mode string) security.Mode {
	switch mode {
	case "strict":
		return security.ModeStrict
	case "trust":
		return security.ModeTrust
	case "yolo":
		return security.ModeYOLO
	default:
		return security.ModeNormal
	}
}

// publish 向事件总线同步发送事件，Bus 为 nil 时静默跳过。
func (c *Core) publish(level, typ, msg string) {
	if c.Bus != nil {
		c.Bus.PublishSync(events.New("", "agentcore", level, typ, msg))
	}
}

func (c *Core) SetApprover(a Approver) { c.approver = a }

func (c *Core) SetSkills(s *skill.Store) { c.skills = s }

// SetStore 注入持久化存储并加载已有会话历史。
func (c *Core) SetStore(s *store.Store, sessionID string) error {
	c.store = s
	c.sessionID = sessionID
	msgs, err := s.GetMessages(sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	c.history = storeMsgToLLM(msgs)
	return nil
}

func (c *Core) SessionID() string { return c.sessionID }
