package agentcore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

// loadSoul 返回系统提示词，mode 会被注入以便 LLM 了解当前安全模式。
func loadSoul(mode string) string {
	return fmt.Sprintf(`你是 half-pi，一个远程设备操控系统的智能核心。
你运行在用户的中心服务器上，通过工具调用在本地执行任务。

当前安全模式: %s

模式说明：
- strict：仅允许白名单操作，所有敏感操作都会被拒绝
- normal：敏感操作需用户确认后才能执行，系统会自动弹出确认
- trust：你自行判断风险，大部分操作直接执行
- yolo：无条件执行所有操作

每个工具都有可选的 confirm 参数。当设为 true 时，系统会在执行前请求用户确认。
你不需要自己用文字询问用户——系统会处理确认流程。

远程执行时：
1. 先用 list_hands 查看在线设备。
2. 用 get_hand_info 获取目标 Hand 的准确工具 schema。
3. 需要会话默认设备时用 select_hand。
4. 用 use_hand 按 schema 传参执行。
5. 长时间任务给 use_hand 传 background=true 和 task_timeout_ms；之后用 get_hand_task、read_hand_task_log、cancel_hand_task 管理。
不要假设 Hand 上存在未查询到的工具。`, mode)
}

func toolsToDefs(tools []executor.Tool) []llm.ToolDef {
	var defs []llm.ToolDef
	for _, t := range tools {
		schema := t.SchemaParameters()
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
func (c *Core) Chat(ctx context.Context, input string) (reply string, err error) {
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	defer func() {
		if saveErr := c.saveSessionLocked(); saveErr != nil {
			err = saveErr
		}
	}()

	c.history = append(c.history, llm.Message{
		Role:    llm.RoleUser,
		Content: input,
	})
	hooks := chatHooksFromContext(ctx)

	for step := 0; step < maxToolCallSteps; step++ {
		c.stateMu.RLock()
		mode, skills, debug := c.Mode, c.skills, c.Debug
		c.stateMu.RUnlock()
		system := loadSoul(mode)
		if skills != nil {
			if idx := skills.Index(); idx != "" {
				system += "\n\n" + idx
			}
		}
		req := &llm.LLMRequest{
			System:   system,
			Messages: c.history,
			Tools:    toolsToDefs(c.exec.Tools()),
		}

		resp, err := c.llm.Chat(ctx, req)
		if err != nil {
			return "", fmt.Errorf("LLM call failed: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			c.history = append(c.history, llm.Message{
				Role:    llm.RoleAssistant,
				Content: resp.Content,
			})
			return resp.Content, nil
		}

		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: resp.Content}
		for _, tc := range resp.ToolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, llm.ToolCall{
				ID: tc.ID, Name: tc.Name, Args: tc.Args,
			})
		}
		c.history = append(c.history, assistantMsg)

		for _, tc := range resp.ToolCalls {
			if debug {
				c.publish(events.LevelDebug, events.TypeToolCall, fmt.Sprintf("%s(%s)", tc.Name, tc.Args))
			}

			var rawArgs map[string]any
			_ = json.Unmarshal([]byte(tc.Args), &rawArgs)
			llmConfirm, _ := rawArgs["confirm"].(bool)
			delete(rawArgs, "confirm")
			cleanArgs, _ := json.Marshal(rawArgs)
			if hooks.ToolCalled != nil {
				hooks.ToolCalled(ChatToolCall{Tool: tc.Name, ArgsDigest: toolArgsDigest(cleanArgs)})
			}

			shouldBlock, blockReason := c.CheckAndConfirm(ctx, tc.Name, cleanArgs, llmConfirm)

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
			if hooks.ToolCompleted != nil {
				hooks.ToolCompleted(ChatToolResult{Tool: tc.Name, Success: !shouldBlock && result.Success})
			}

			output := result.Output
			if !result.Success {
				output = result.Error
			}

			if debug && !shouldBlock {
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
	}

	return "", fmt.Errorf("tool call loop exceeded max steps")
}
