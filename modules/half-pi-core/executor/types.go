// Package executor 定义工具类型、工具注册表和工具执行器接口。
package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/security"
)

// ToolResult 是工具执行结果。Output 是 LLM 可读的文本，Data 是供 Face 渲染的结构化数据。
type ToolResult struct {
	Success           bool
	Output            string
	Data              any // 结构化输出，用于 Face 渲染（JSON 序列化）
	Error             string
	CompactReason     string
	CompactFacts      []CompactFact
	CompactProjection CompactProjection
}

// CompactFact 是工具可提交给中央 Compact projector 的强类型候选事实。
type CompactFact struct {
	Kind      string
	Path      string
	HandID    string
	RunID     string
	TaskID    string
	Tool      string
	Status    string
	ToolNames []string
}

// CompactProjection 绑定最终工具输出，并携带尚未由中央策略放行的候选事实。
type CompactProjection struct {
	OutputBytes    int
	OutputDigest   string
	ReasonCategory string
	CandidateFacts []CompactFact
}

// Progress 是工具执行期间产生的可选增量信息。
type Progress struct {
	Kind string
	Data string
}

// ProgressFunc 接收工具执行期间的增量信息。调用方应快速返回，并允许并发调用。
type ProgressFunc func(Progress)

type progressContextKey struct{}
type finalOutputLimitContextKey struct{}

// WithProgress 返回携带可选进度回调的 context。
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, progressContextKey{}, fn)
}

// ReportProgress 向 context 中的回调报告进度；未配置回调时无操作。
func ReportProgress(ctx context.Context, progress Progress) {
	if fn := progressFunc(ctx); fn != nil {
		fn(progress)
	}
}

func progressFunc(ctx context.Context) ProgressFunc {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(progressContextKey{}).(ProgressFunc)
	return fn
}

// WithFinalOutputLimit 限制工具为最终结果保留的输出字节数；流式进度不受影响。
func WithFinalOutputLimit(ctx context.Context, limit int64) context.Context {
	if limit <= 0 {
		return ctx
	}
	return context.WithValue(ctx, finalOutputLimitContextKey{}, limit)
}

// FinalOutputLimit 返回最终结果输出的可选保留上限。
func FinalOutputLimit(ctx context.Context) int64 {
	limit, _ := ctx.Value(finalOutputLimitContextKey{}).(int64)
	return limit
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

// ToolPolicyCheck 使用调用方提供的安全策略执行检查。
type ToolPolicyCheck func(args json.RawMessage, policy *security.Policy) (Decision, string)

// Tool 定义一个可被 LLM 调用的工具。
type Tool struct {
	Name           string
	Description    string
	Parameters     *ObjectSchema
	DefaultConfirm bool      // true 时每次调用都需用户确认
	OwnsConfirm    bool      // true 时 confirm 是工具参数，由工具自行完成审批
	Check          ToolCheck // 执行前安全检查，nil 表示不检查
	PolicyCheck    ToolPolicyCheck
	Execute        func(ctx context.Context, args json.RawMessage) *ToolResult
}

// ObjectSchema 描述 JSON Schema object 类型。
type ObjectSchema struct {
	Properties []PropertySchema
	Required   []string
}

// ReviewExposure 定义字段进入隔离 AI Reviewer 证书时的投影视图。
type ReviewExposure string

const (
	// ReviewInclude 传递字段值；这是非敏感字段的默认值。
	ReviewInclude ReviewExposure = "include"
	// ReviewRedact 只传递固定占位符，不传原值。
	ReviewRedact ReviewExposure = "redact"
	// ReviewRequireUser 不把字段交给 Reviewer，并强制升级到用户审批。
	ReviewRequireUser ReviewExposure = "require_user"
)

// PropertySchema 描述一个属性的 JSON Schema。
type PropertySchema struct {
	Name        string
	Type        string
	Description string
	Review      ReviewExposure
}

// ProjectReviewArgs 按工具 schema 生成 Reviewer 可见参数投影。
// complete=false 表示存在必须由用户查看、不能交给 Reviewer 的字段。
func ProjectReviewArgs(tool Tool, args json.RawMessage) (projected json.RawMessage, complete bool, err error) {
	var values map[string]any
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return nil, false, fmt.Errorf("decode review args: %w", err)
	}
	if values == nil || decoder.Decode(&struct{}{}) == nil {
		return nil, false, fmt.Errorf("decode review args: expected one object")
	}
	if tool.Parameters == nil {
		if len(values) != 0 {
			return json.RawMessage(`{}`), false, nil
		}
		return json.RawMessage(`{}`), true, nil
	}
	complete = true
	for _, property := range tool.Parameters.Properties {
		if _, exists := values[property.Name]; !exists {
			continue
		}
		switch property.Review {
		case ReviewRedact:
			values[property.Name] = "[redacted]"
		case ReviewRequireUser:
			delete(values, property.Name)
			complete = false
		}
	}
	projected, err = json.Marshal(values)
	if err != nil {
		return nil, false, fmt.Errorf("encode review args: %w", err)
	}
	return projected, complete, nil
}

// ToolExecutor 是工具执行器的接口。
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, name string, args json.RawMessage) *ToolResult
	Tools() []Tool
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

// CheckToolWithPolicy 使用显式策略检查工具，避免不同会话共享全局安全模式。
func CheckToolWithPolicy(name string, args json.RawMessage, policy *security.Policy) (Tool, Decision, string, bool) {
	tool, ok := FindTool(name)
	if !ok {
		return Tool{}, DecisionDeny, "unknown tool: " + name, false
	}
	if tool.PolicyCheck == nil {
		decision, reason := toolDecision(tool, args)
		return tool, decision, reason, true
	}
	decision, reason := tool.PolicyCheck(args, policy)
	if decision == DecisionAllow && tool.DefaultConfirm {
		return tool, DecisionConfirm, "该操作默认需用户确认", true
	}
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
	registryMu       sync.RWMutex
	registeredTools  []Tool
	registryRevision atomic.Uint64
)

// ToolRegistrySnapshot 是工具定义在一个 revision 上的不可变规范视图。
type ToolRegistrySnapshot struct {
	Revision uint64
	Tools    []Tool
	Digest   string
}

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
	registryRevision.Add(1)
}

// RegisteredTools 返回所有已注册的工具。
func RegisteredTools() []Tool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	tools := make([]Tool, len(registeredTools))
	for i := range registeredTools {
		tools[i] = cloneTool(registeredTools[i])
	}
	return tools
}

// RegisteredToolsSnapshot 返回深拷贝工具定义、单调 revision 和规范摘要。
func RegisteredToolsSnapshot() ToolRegistrySnapshot {
	registryMu.RLock()
	tools := make([]Tool, len(registeredTools))
	for i := range registeredTools {
		tools[i] = cloneTool(registeredTools[i])
	}
	revision := registryRevision.Load()
	registryMu.RUnlock()

	type digestTool struct {
		Name           string
		Description    string
		Parameters     map[string]any
		DefaultConfirm bool
		OwnsConfirm    bool
	}
	digestTools := make([]digestTool, len(tools))
	for i := range tools {
		digestTools[i] = digestTool{
			Name: tools[i].Name, Description: tools[i].Description,
			Parameters:     tools[i].SchemaParameters(),
			DefaultConfirm: tools[i].DefaultConfirm, OwnsConfirm: tools[i].OwnsConfirm,
		}
	}
	sort.Slice(digestTools, func(i, j int) bool { return digestTools[i].Name < digestTools[j].Name })
	encoded, _ := json.Marshal(digestTools)
	digest := sha256.Sum256(append([]byte("half-pi:tool-registry:v1\x00"), encoded...))
	return ToolRegistrySnapshot{Revision: revision, Tools: tools, Digest: fmt.Sprintf("%x", digest[:])}
}

func cloneTool(tool Tool) Tool {
	if tool.Parameters != nil {
		parameters := *tool.Parameters
		parameters.Properties = append([]PropertySchema(nil), tool.Parameters.Properties...)
		parameters.Required = append([]string(nil), tool.Parameters.Required...)
		tool.Parameters = &parameters
	}
	return tool
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
	if t.Parameters == nil {
		return map[string]any{"type": "object", "properties": props}
	}
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
