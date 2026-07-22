package local

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

const taskQueryTimeout = 10 * time.Second

func init() {
	executor.Register(executor.Tool{
		Name: "get_hand_task", Description: "查询当前会话拥有的 Hand 后台任务；省略 task_id 时列出任务。",
		Parameters: &executor.ObjectSchema{Properties: []executor.PropertySchema{
			{Name: "task_id", Type: "string", Description: "后台任务 ID；省略时列出当前会话任务"},
		}},
		Execute: getHandTask,
	})
	executor.Register(executor.Tool{
		Name: "read_hand_task_log", Description: "按字节偏移读取当前会话拥有的 Hand 后台任务日志。",
		Parameters: &executor.ObjectSchema{Properties: []executor.PropertySchema{
			{Name: "task_id", Type: "string", Description: "后台任务 ID"},
			{Name: "offset", Type: "integer", Description: "起始字节偏移，默认 0"},
			{Name: "limit", Type: "integer", Description: "读取字节数，默认 4096，最大 65536"},
		}, Required: []string{"task_id"}}, Execute: readHandTaskLog,
	})
	executor.Register(executor.Tool{
		Name: "cancel_hand_task", Description: "取消当前会话拥有的 Hand 后台任务；不会触发第二次交互审批。",
		Parameters: &executor.ObjectSchema{Properties: []executor.PropertySchema{
			{Name: "task_id", Type: "string", Description: "后台任务 ID"},
		}, Required: []string{"task_id"}}, Execute: cancelHandTask,
	})
}

func taskDependencies(ctx context.Context) (*RemoteBridge, string, error) {
	bridge := remoteBridgeFromContext(ctx)
	if bridge == nil || bridge.Tasks == nil || bridge.SessionID == nil {
		return nil, "", fmt.Errorf("后台任务系统未初始化")
	}
	sessionID := bridge.SessionID()
	if sessionID == "" {
		return nil, "", fmt.Errorf("当前会话不可用")
	}
	return bridge, sessionID, nil
}

func getHandTask(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	bridge, sessionID, err := taskDependencies(ctx)
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &executor.ToolResult{Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if params.TaskID == "" {
		tasks, err := bridge.Tasks.List(sessionID)
		if err != nil {
			return &executor.ToolResult{Error: err.Error()}
		}
		for i := range tasks {
			tasks[i].ArgsDigest = ""
		}
		result := jsonToolResult(map[string]any{"tasks": tasks})
		for _, task := range tasks {
			result.CompactFacts = append(result.CompactFacts, taskCompactFact(task))
		}
		return result
	}
	queryCtx, cancel := context.WithTimeout(ctx, taskQueryTimeout)
	defer cancel()
	task, err := bridge.Tasks.Get(queryCtx, sessionID, params.TaskID)
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}
	return taskToolResult(task)
}

func readHandTaskLog(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	bridge, sessionID, err := taskDependencies(ctx)
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}
	var params struct {
		TaskID string `json:"task_id"`
		Offset int64  `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.TaskID == "" {
		return &executor.ToolResult{Error: "task_id 参数不能为空"}
	}
	if params.Limit == 0 {
		params.Limit = 4096
	}
	queryCtx, cancel := context.WithTimeout(ctx, taskQueryTimeout)
	defer cancel()
	page, err := bridge.Tasks.ReadLog(queryCtx, sessionID, params.TaskID, params.Offset, params.Limit)
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}
	result := jsonToolResult(map[string]any{
		"task_id": page.TaskID, "offset": page.Offset, "next_offset": page.NextOffset,
		"data": string(page.Data), "eof": page.EOF, "truncated": page.Truncated,
	})
	result.CompactFacts = []executor.CompactFact{{Kind: "task", TaskID: page.TaskID, Truncated: page.Truncated}}
	return result
}

func cancelHandTask(ctx context.Context, args json.RawMessage) *executor.ToolResult {
	bridge, sessionID, err := taskDependencies(ctx)
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil || params.TaskID == "" {
		return &executor.ToolResult{Error: "task_id 参数不能为空"}
	}
	queryCtx, cancel := context.WithTimeout(ctx, taskQueryTimeout)
	defer cancel()
	result, err := bridge.Tasks.Cancel(queryCtx, sessionID, params.TaskID, "user")
	if err != nil {
		return &executor.ToolResult{Error: err.Error()}
	}
	toolResult := jsonToolResult(result)
	toolResult.CompactFacts = []executor.CompactFact{{Kind: "task", TaskID: params.TaskID, Status: string(result.Status)}}
	return toolResult
}

func taskToolResult(task remoteexec.Task) *executor.ToolResult {
	task.ArgsDigest = ""
	result := jsonToolResult(task)
	result.CompactFacts = []executor.CompactFact{taskCompactFact(task)}
	return result
}

func taskCompactFact(task remoteexec.Task) executor.CompactFact {
	return executor.CompactFact{
		Kind: "task", HandID: task.HandID, TaskID: task.TaskID,
		Tool: task.Tool, Status: string(task.Status), Truncated: task.Truncated,
	}
}

func jsonToolResult(value any) *executor.ToolResult {
	data, _ := json.Marshal(value)
	return &executor.ToolResult{Success: true, Output: string(data), Data: value}
}
