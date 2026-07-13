// Package executor 定义工具类型、工具注册表和工具执行器接口。
package executor

import (
	"context"
	"encoding/json"
)

// ToolResult 是工具执行结果。
type ToolResult struct {
	Success bool
	Output  string
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
	Name            string
	Description     string
	Parameters      *ObjectSchema
	DefaultConfirm  bool      // true 时每次调用都需用户确认
	Check           ToolCheck // 执行前安全检查，nil 表示不检查
	Execute         func(ctx context.Context, args json.RawMessage) *ToolResult
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

// ── 全局注册表 ──

var registeredTools []Tool

// Register 在 init() 中调用，注册工具到全局列表。
func Register(t Tool) {
	registeredTools = append(registeredTools, t)
}

// RegisteredTools 返回所有已注册的工具。
func RegisteredTools() []Tool {
	return registeredTools
}

// FindTool 按名称查找已注册的工具。
func FindTool(name string) *Tool {
	for i := range registeredTools {
		if registeredTools[i].Name == name {
			return &registeredTools[i]
		}
	}
	return nil
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
