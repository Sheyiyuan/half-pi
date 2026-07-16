// Package executor 定义工具类型、工具注册表和工具执行器接口。
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ToolResult 是工具执行结果。Output 是 LLM 可读的文本，Data 是供 Face 渲染的结构化数据。
type ToolResult struct {
	Success bool
	Output  string
	Data    any    // 结构化输出，用于 Face 渲染（JSON 序列化）
	Error   string
}

// Decision 是安全检查的决策结果。
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
	DecisionConfirm
)

// ToolCheck 是工具执行前的安全检查函数。
// 返回 Decision 和原因说明。DecisionAllow 时执行，否则拦截。
type ToolCheck func(args json.RawMessage) (Decision, string)

// Tool 定义一个可被 LLM 调用的工具。
type Tool struct {
	Name           string
	Description    string
	Parameters     *ObjectSchema
	DefaultConfirm bool      // true 时每次调用都需用户确认
	Check          ToolCheck // 执行前安全检查，nil 表示不检查
	Execute        func(ctx context.Context, args json.RawMessage) *ToolResult
}

// ObjectSchema 描述 JSON Schema object 类型。
type ObjectSchema struct {
	Properties []PropertySchema
	Required   []string
}

// PropertySchema 描述一个属性的 JSON Schema。
type PropertySchema struct {
	Name        string
	Type        string
	Description string
}

// ToolExecutor 是工具执行器的接口。
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, name string, args json.RawMessage) *ToolResult
	Tools() []Tool
}

// ConfirmDecision 表示需要确认时调用方的决策。
type ConfirmDecision int

const (
	ConfirmDeny ConfirmDecision = iota
	ConfirmAllow
)

// ConfirmFunc 在工具需要确认时被调用。返回 ConfirmAllow 才会继续执行。
type ConfirmFunc func(tool Tool, args json.RawMessage, reason string) ConfirmDecision

// ExecutionPolicy 控制 Runner 的安全检查行为。
type ExecutionPolicy struct {
	// Confirm 为空时，任何需要确认的工具都会被拒绝。
	Confirm ConfirmFunc
	// SkipChecks 为 true 时只查找并执行工具，不运行 Tool.Check 或 DefaultConfirm。
	SkipChecks bool
}

// Runner 是可复用的工具执行器，统一处理查找、Check、DefaultConfirm 与执行。
type Runner struct {
	policy ExecutionPolicy
}

// NewRunner 创建工具执行器。
func NewRunner(policy ExecutionPolicy) *Runner {
	return &Runner{policy: policy}
}

// Tools 返回已注册工具快照。
func (r *Runner) Tools() []Tool {
	return RegisteredTools()
}

// ExecuteTool 按策略安全地执行工具。
func (r *Runner) ExecuteTool(ctx context.Context, name string, args json.RawMessage) *ToolResult {
	tool, ok := FindTool(name)
	if !ok {
		return &ToolResult{Error: fmt.Sprintf("unknown tool: %s", name)}
	}

	if !r.policy.SkipChecks {
		if decision, reason := toolDecision(tool, args); decision != DecisionAllow {
			return r.handleDecision(ctx, tool, args, decision, reason)
		}
	}

	if tool.Execute == nil {
		return &ToolResult{Error: fmt.Sprintf("tool %s has no execute function", name)}
	}
	return tool.Execute(ctx, args)
}

func (r *Runner) handleDecision(ctx context.Context, tool Tool, args json.RawMessage, decision Decision, reason string) *ToolResult {
	switch decision {
	case DecisionDeny:
		if reason == "" {
			reason = "blocked by tool policy"
		}
		return &ToolResult{Success: true, Output: fmt.Sprintf("⚠️ 操作被拒绝: %s", reason)}
	case DecisionConfirm:
		if reason == "" {
			reason = "该操作需要确认"
		}
		if r.policy.Confirm == nil || r.policy.Confirm(tool, args, reason) != ConfirmAllow {
			return &ToolResult{Success: true, Output: fmt.Sprintf("⚠️ 操作被拒绝: %s", reason)}
		}
		if tool.Execute == nil {
			return &ToolResult{Error: fmt.Sprintf("tool %s has no execute function", tool.Name)}
		}
		return tool.Execute(ctx, args)
	default:
		return &ToolResult{Error: "unknown tool decision"}
	}
}

// CheckTool 对工具参数执行 Tool.Check 和 DefaultConfirm，供需要自定义审批流的调用方复用。
func CheckTool(name string, args json.RawMessage) (Tool, Decision, string, bool) {
	tool, ok := FindTool(name)
	if !ok {
		return Tool{}, DecisionDeny, fmt.Sprintf("unknown tool: %s", name), false
	}
	decision, reason := toolDecision(tool, args)
	return tool, decision, reason, true
}

func toolDecision(tool Tool, args json.RawMessage) (Decision, string) {
	if tool.Check != nil {
		decision, reason := tool.Check(args)
		if decision != DecisionAllow {
			return decision, reason
		}
	}
	if tool.DefaultConfirm {
		return DecisionConfirm, "该操作默认需用户确认"
	}
	return DecisionAllow, ""
}

// ── 全局注册表 ──

var (
	registryMu      sync.RWMutex
	registeredTools []Tool
)

// Register 在 init() 中调用，注册工具到全局列表。
func Register(t Tool) {
	if t.Name == "" {
		panic("executor: tool name cannot be empty")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, existing := range registeredTools {
		if existing.Name == t.Name {
			panic(fmt.Sprintf("executor: duplicate tool registration: %s", t.Name))
		}
	}
	registeredTools = append(registeredTools, t)
}

// RegisteredTools 返回所有已注册的工具。
func RegisteredTools() []Tool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	tools := make([]Tool, len(registeredTools))
	copy(tools, registeredTools)
	return tools
}

// FindTool 按名称查找已注册的工具。
func FindTool(name string) (Tool, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for i := range registeredTools {
		if registeredTools[i].Name == name {
			return registeredTools[i], true
		}
	}
	return Tool{}, false
}

// SchemaParameters 返回参数的 JSON Schema。
func (t *Tool) SchemaParameters() map[string]any {
	props := make(map[string]any)
	for _, p := range t.Parameters.Properties {
		props[p.Name] = map[string]any{
			"type":        p.Type,
			"description": p.Description,
		}
	}
	params := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(t.Parameters.Required) > 0 {
		params["required"] = t.Parameters.Required
	}
	return params
}
