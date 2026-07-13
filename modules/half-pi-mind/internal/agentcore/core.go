// Package agentcore 是系统唯一的智能节点。
package agentcore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

// Core 是 agent core 的主体。
// TODO: 接入 WSS server 后需为 Mode、history、autoAllow/autoDeny 加并发保护。
type Core struct {
	llm        llm.Provider
	exec       executor.ToolExecutor
	history    []llm.Message
	Bus        *events.EventBus // Mind 的事件总线，nil 时不发事件
	Debug      bool
	Mode       string // "normal" | "trust" | "yolo"
	approver   Approver
	autoAllow  map[string]bool // 本会话自动放行的工具
	autoDeny   map[string]bool // 本会话自动拒绝的工具
}

// Approver 由 REPL 实现，处理用户确认交互。
type Approver interface {
	Confirm(toolName, reason string) ConfirmResult
}

// ConfirmResult 是用户确认的结果。
type ConfirmResult int

const (
	ConfirmDeny       ConfirmResult = iota
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

// SetMode 切换安全模式，同时在对话历史中记录，让 LLM 感知到变化。
// TODO: 被非 REPL 调用方（如 WSS server）调用时，调用方需负责发送事件。
func (c *Core) SetMode(mode string) {
	c.Mode = mode
	c.history = append(c.history, llm.Message{
		Role:    llm.RoleSystem,
		Content: fmt.Sprintf("安全模式已切换为: %s", mode),
	})
}

// publish 向事件总线发送一条事件，Bus 为 nil 时静默跳过。
// 同步发送，确保调试输出顺序（[TOOL] → [RESULT] → LLM 回复）不乱。
func (c *Core) publish(level, typ, msg string) {
	if c.Bus != nil {
		c.Bus.PublishSync(events.New("", "agentcore", level, typ, msg))
	}
}

// SetApprover 设置审批交互器，用于 normal 模式下用户确认流程。
func (c *Core) SetApprover(a Approver) {
	c.approver = a
}

func toolsToDefs(tools []executor.Tool) []llm.ToolDef {
	var defs []llm.ToolDef
	for _, t := range tools {
		schema := t.SchemaParameters()
		// 为每个工具注入通用可选参数 confirm
		if props, ok := schema["properties"].(map[string]any); ok {
			props["confirm"] = map[string]any{
				"type":        "boolean",
				"description": "设为 true 会在执行前请求用户确认，用于有风险的操作",
			}
		}
		defs = append(defs, llm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schema,
		})
	}
	return defs
}

// Chat 处理一轮用户输入，走完整个 tool call 循环，返回最终回答。
func (c *Core) Chat(ctx context.Context, input string) (string, error) {
	c.history = append(c.history, llm.Message{
		Role:    llm.RoleUser,
		Content: input,
	})

	for step := 0; step < maxToolCallSteps; step++ {
		req := &llm.LLMRequest{
			System:   loadSoul(c.Mode),
			Messages: c.history,
			Tools:    toolsToDefs(c.exec.Tools()),
		}

		resp, err := c.llm.Chat(ctx, req)
		if err != nil {
			return "", fmt.Errorf("LLM 调用失败: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			c.history = append(c.history, llm.Message{
				Role:    llm.RoleAssistant,
				Content: resp.Content,
			})
			return resp.Content, nil
		}

		// 记录 LLM 的回复
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: resp.Content}
		for _, tc := range resp.ToolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, llm.ToolCall{
				ID: tc.ID, Name: tc.Name, Args: tc.Args,
			})
		}
		c.history = append(c.history, assistantMsg)

		// 逐个执行工具
		for _, tc := range resp.ToolCalls {
			if c.Debug {
				c.publish(events.LevelDebug, events.TypeToolCall, fmt.Sprintf("%s(%s)", tc.Name, tc.Args))
			}

			// 解析参数，提取 LLM 的 confirm 标记
			var rawArgs map[string]any
			_ = json.Unmarshal([]byte(tc.Args), &rawArgs)
			llmConfirm, _ := rawArgs["confirm"].(bool)
			delete(rawArgs, "confirm")
			cleanArgs, _ := json.Marshal(rawArgs)

			// 安全检查
			tool := executor.FindTool(tc.Name)
			shouldBlock := false
			var blockReason string

			if tool != nil && tool.Check != nil {
				decision, reason := tool.Check(json.RawMessage(tc.Args))
				switch decision {
				case executor.DecisionDeny:
					shouldBlock = true
					blockReason = reason
				case executor.DecisionConfirm:
					blockReason = reason
				}
			}
			if tool != nil && tool.DefaultConfirm && blockReason == "" {
				blockReason = "该操作默认需用户确认"
			}

			// 合并审批：LLM 主动标记 confirm 或安全检查触发
			needsConfirm := llmConfirm || blockReason != ""
			if blockReason == "" && llmConfirm {
				blockReason = "LLM 标记为需确认操作"
			}

			if needsConfirm {
				if c.autoDeny[tc.Name] {
					shouldBlock = true
				} else if c.autoAllow[tc.Name] {
					shouldBlock = false
				} else if c.Mode == "strict" {
					shouldBlock = true
				} else if llmConfirm || c.Mode == "normal" {
					// LLM 主动标记 confirm 时覆盖 trust/yolo，强制弹确认
					if c.approver != nil {
						r := c.approver.Confirm(tc.Name, blockReason)
						switch r {
						case ConfirmAllow:
							shouldBlock = false
						case ConfirmAllowAlways:
							shouldBlock = false
							c.autoAllow[tc.Name] = true
						case ConfirmDenyAlways:
							shouldBlock = true
							c.autoDeny[tc.Name] = true
						default:
							shouldBlock = true
						}
					}
				}
			}

			var result *executor.ToolResult
			if shouldBlock {
				c.publish(events.LevelDebug, events.TypeToolBlock, blockReason)
				result = &executor.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("⚠️ 操作被拒绝: %s", blockReason),
				}
			} else {
				result = c.exec.ExecuteTool(ctx, tc.Name, cleanArgs)
			}

			output := result.Output
			if !result.Success {
				output = result.Error
			}

			if c.Debug && !shouldBlock {
				truncated := output
				if len([]rune(truncated)) > 200 {
					truncated = string([]rune(truncated)[:200]) + "…"
				}
				c.publish(events.LevelDebug, events.TypeToolResult, truncated)
			}

			c.history = append(c.history, llm.Message{
				Role:    llm.RoleTool,
				ToolID:  tc.ID,
				Content: output,
			})
		}
		// continue loop → LLM sees tool results
	}

	return "", fmt.Errorf("tool call 循环超过最大轮数")
}
