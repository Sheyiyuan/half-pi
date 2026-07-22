package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
)

func loadSoul(mode string) string {
	return fmt.Sprintf(`你是 half-pi，一个远程设备操控系统的智能核心。
你运行在用户的中心服务器上，通过工具调用在本地执行任务。

当前安全模式: %s

模式说明：
- strict：仅允许白名单操作，所有敏感操作都会被拒绝
- normal：敏感操作需用户确认后才能执行，系统会自动弹出确认
- review：由隔离的安全审核模型判断自动通过或交给用户审批
- yolo：除硬拒绝外直接执行；工具或模型要求的人工确认仍然生效

每个工具都有可选的 confirm 参数。当设为 true 时，系统会在执行前请求用户确认。
你不需要自己用文字询问用户，系统会处理确认流程。

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
	for _, tool := range tools {
		schema := tool.SchemaParameters()
		if props, ok := schema["properties"].(map[string]any); ok && !tool.OwnsConfirm {
			props["confirm"] = map[string]any{
				"type": "boolean", "description": "设为 true 会在执行前请求用户确认，用于有风险的操作",
			}
		}
		defs = append(defs, llm.ToolDef{Name: tool.Name, Description: tool.Description, Parameters: schema})
	}
	return defs
}

// Chat 处理一轮用户输入，并发布完整的 message/model/tool 生命周期。
func (c *Core) Chat(ctx context.Context, input string) (reply string, err error) {
	return c.ChatWithTransport(ctx, input, ChatTransport{})
}

// ChatWithTransport 处理一轮用户输入，并把可见流显式交给 transport。
// transport 不是 lifecycle Hook；安全 Guard、Transformer 和 Auditor 只通过 Registry 注册。
func (c *Core) ChatWithTransport(ctx context.Context, input string, transport ChatTransport) (reply string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if corelifecycle.IsHookContext(ctx) {
		return "", fmt.Errorf("synchronous lifecycle Hook reentrancy is not allowed")
	}
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	requestID := requestctx.RequestID(ctx)
	traceMeta := c.newChatMeta(ctx)
	c.publishLifecycle(ctx, traceMeta, corelifecycle.PhaseChatReceived, corelifecycle.RedactedEvent{})
	defer func() {
		c.publishLifecycle(ctx, traceMeta, corelifecycle.PhaseChatFinished, corelifecycle.RedactedEvent{
			Outcome: lifecycleOutcome(ctx, err),
		})
	}()

	input, err = c.admitMessage(ctx, traceMeta, input)
	if err != nil {
		return "", err
	}
	c.history = append(c.history, llm.Message{Role: llm.RoleUser, Content: input, RequestID: requestID})
	if err = c.saveSessionLocked(); err != nil {
		return "", err
	}
	c.publishLifecycle(ctx, traceMeta, corelifecycle.PhaseMessagePersisted, corelifecycle.RedactedEvent{
		InputLength: len(input), InputDigest: corelifecycle.HashString(input),
	})

	for step := 0; step < maxToolCallSteps; step++ {
		responseIndex := step + 1
		modelMeta := traceMeta.ChildMeta(corelifecycle.SourceMind).WithNode("mind")
		req, prepareErr := c.prepareModelRequest(ctx, modelMeta)
		if prepareErr != nil {
			err = prepareErr
			return "", err
		}
		registry := c.lifecycleRegistry()
		buffered := registry != nil && registry.RequiresBufferedDelivery(modelMeta)
		c.publishLifecycle(ctx, modelMeta, corelifecycle.PhaseModelRequested, corelifecycle.RedactedEvent{
			InputLength: len(req.Messages), ProviderID: c.modelProviderID(), ModelID: c.modelName(),
		})

		var streamed strings.Builder
		resp, callErr := llm.ChatWithStreaming(ctx, c.llm, req, func(delta string) error {
			streamed.WriteString(delta)
			c.publishLifecycle(ctx, modelMeta, corelifecycle.PhaseModelDelta, corelifecycle.RedactedEvent{
				OutputLength: len(delta), ProviderID: c.modelProviderID(), ModelID: c.modelName(),
				Sensitive: map[string]any{"delta": delta},
			})
			if !buffered && transport.TextDelta != nil {
				return transport.TextDelta(ChatTextDelta{ResponseIndex: responseIndex, Delta: delta})
			}
			return nil
		})
		if callErr != nil {
			c.publishLifecycle(ctx, modelMeta, corelifecycle.PhaseModelFailed, corelifecycle.RedactedEvent{
				Outcome: lifecycleOutcome(ctx, callErr), ReasonCode: "provider_error",
			})
			err = fmt.Errorf("LLM call failed: %w", callErr)
			return "", err
		}
		if resp == nil || streamed.String() != resp.Content {
			err = fmt.Errorf("LLM stream content mismatch")
			c.publishLifecycle(ctx, modelMeta, corelifecycle.PhaseModelFailed, corelifecycle.RedactedEvent{
				Outcome: corelifecycle.OutcomeFailed, ReasonCode: "stream_mismatch",
			})
			return "", err
		}
		c.publishLifecycle(ctx, modelMeta, corelifecycle.PhaseModelResponseReceived, corelifecycle.RedactedEvent{
			OutputLength: len(resp.Content), InputDigest: corelifecycle.HashString(resp.Content),
			ProviderID: c.modelProviderID(), ModelID: c.modelName(),
			Sensitive: map[string]any{"content": resp.Content},
		})
		content, deliveryErr := c.prepareAssistantDelivery(ctx, modelMeta, resp.Content)
		if deliveryErr != nil {
			err = deliveryErr
			return "", err
		}
		resp.Content = content
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: content, RequestID: requestID}
		for _, call := range resp.ToolCalls {
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, llm.ToolCall{ID: call.ID, Name: call.Name, Args: call.Args})
		}
		c.history = append(c.history, assistantMsg)
		if err = c.saveSessionLocked(); err != nil {
			return "", err
		}
		if buffered && transport.TextDelta != nil && content != "" {
			if callbackErr := transport.TextDelta(ChatTextDelta{ResponseIndex: responseIndex, Delta: content}); callbackErr != nil {
				err = callbackErr
				return "", err
			}
		}
		if transport.ResponseCompleted != nil {
			if callbackErr := transport.ResponseCompleted(ChatResponse{
				ResponseIndex: responseIndex, Content: content, HasToolCalls: len(resp.ToolCalls) > 0,
			}); callbackErr != nil {
				err = fmt.Errorf("complete LLM stream response: %w", callbackErr)
				return "", err
			}
		}
		c.publishLifecycle(ctx, modelMeta, corelifecycle.PhaseAssistantDelivered, corelifecycle.RedactedEvent{
			OutputLength: len(content), InputDigest: corelifecycle.HashString(content),
			ProviderID: c.modelProviderID(), ModelID: c.modelName(),
			Sensitive: map[string]any{"content": content},
		})
		if len(resp.ToolCalls) == 0 {
			return content, nil
		}
		for _, call := range resp.ToolCalls {
			cleanArgs, forceApproval := prepareToolArgs(call.Name, call.Args)
			if c.Debug {
				c.publish(events.LevelDebug, events.TypeToolCall, fmt.Sprintf("%s(%s)", call.Name, corelifecycle.HashDigest(cleanArgs)))
			}
			if transport.ToolCalled != nil {
				transport.ToolCalled(ChatToolCall{Tool: call.Name, ArgsDigest: corelifecycle.HashDigest(cleanArgs)})
			}
			toolMeta := modelMeta.ChildMeta(corelifecycle.SourceMind).WithNode("mind")
			result := c.executeToolWithMeta(ctx, call.Name, cleanArgs, forceApproval, input, toolMeta)
			if transport.ToolCompleted != nil {
				transport.ToolCompleted(ChatToolResult{Tool: call.Name, Success: result.Success})
			}
			output := result.Output
			if !result.Success {
				output = result.Error
			}
			if c.Debug {
				c.publish(events.LevelDebug, events.TypeToolResult, fmt.Sprintf("%s success=%t", call.Name, result.Success))
			}
			c.history = append(c.history, llm.Message{
				Role: llm.RoleTool, ToolID: call.ID, Content: output, RequestID: requestID,
			})
		}
		if err = c.saveSessionLocked(); err != nil {
			return "", err
		}
	}
	err = fmt.Errorf("tool call loop exceeded max steps")
	return "", err
}

func (c *Core) admitMessage(ctx context.Context, meta corelifecycle.Meta, input string) (string, error) {
	registry := c.lifecycleRegistry()
	if registry != nil {
		action, err := registry.Transform(ctx, corelifecycle.PhaseMessageBeforeAccept, corelifecycle.MutableAction{
			Meta: meta, Kind: "message", Fields: map[string]any{"content": input},
		})
		if err != nil {
			return "", fmt.Errorf("message transform failed: %w", err)
		}
		var ok bool
		input, ok = action.Fields["content"].(string)
		if !ok {
			return "", fmt.Errorf("message transformer returned invalid content")
		}
		verdict, guardErr := registry.Guard(ctx, corelifecycle.PhaseMessageBeforeAccept, corelifecycle.FrozenAction{
			Meta: meta, Kind: "message", Resource: meta.ConversationID,
			ArgsDigest: corelifecycle.HashString(input), Sensitive: map[string]any{"content": input},
		})
		if guardErr != nil || verdict == corelifecycle.VerdictDeny || verdict == corelifecycle.VerdictRequireApproval {
			return "", fmt.Errorf("message denied by input policy")
		}
	}
	if !utf8.ValidString(input) || len(input) > maxChatMessageBytes {
		return "", fmt.Errorf("message is invalid or exceeds %d bytes", maxChatMessageBytes)
	}
	if err := c.commitLifecycleAudit(ctx, meta, corelifecycle.PhaseMessageAdmitted, corelifecycle.AuditMutation{
		ActionKind: "message", ResourceName: meta.ConversationID,
		InputDigest: corelifecycle.HashString(input), Decision: "allow", ReasonCode: "message_admitted",
	}); err != nil {
		return "", fmt.Errorf("message admission audit failed: %w", err)
	}
	c.publishLifecycle(ctx, meta, corelifecycle.PhaseMessageAdmitted, corelifecycle.RedactedEvent{
		InputLength: len(input), InputDigest: corelifecycle.HashString(input),
	})
	return input, nil
}

func (c *Core) prepareModelRequest(ctx context.Context, meta corelifecycle.Meta) (*llm.LLMRequest, error) {
	req := &llm.LLMRequest{Messages: cloneMessages(c.history), Tools: toolsToDefs(c.exec.Tools())}
	registry := c.lifecycleRegistry()
	if registry == nil {
		return nil, fmt.Errorf("model request lifecycle is unavailable")
	}
	action, err := registry.Transform(ctx, corelifecycle.PhaseModelBeforeRequest, corelifecycle.MutableAction{
		Meta: meta, Kind: "model_request", Fields: map[string]any{
			"system": req.System, "messages": cloneMessages(req.Messages), "tools": req.Tools,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("model request transform failed: %w", err)
	}
	var ok bool
	if req.System, ok = action.Fields["system"].(string); !ok || !utf8.ValidString(req.System) {
		return nil, fmt.Errorf("model request transformer returned invalid system prompt")
	}
	if req.Messages, ok = action.Fields["messages"].([]llm.Message); !ok {
		return nil, fmt.Errorf("model request transformer returned invalid messages")
	}
	req.Messages = cloneMessages(req.Messages)
	if req.Tools, ok = action.Fields["tools"].([]llm.ToolDef); !ok {
		return nil, fmt.Errorf("model request transformer returned invalid tools")
	}
	if req.Tools, err = cloneToolDefs(req.Tools); err != nil {
		return nil, err
	}
	if err := validateModelRequest(req); err != nil {
		return nil, err
	}
	verdict, err := registry.Guard(ctx, corelifecycle.PhaseModelBeforeRequest, corelifecycle.FrozenAction{
		Meta: meta, Kind: "model_request", Resource: "llm", ArgsDigest: corelifecycle.HashString(req.System),
	})
	if err != nil || verdict == corelifecycle.VerdictDeny || verdict == corelifecycle.VerdictRequireApproval {
		return nil, fmt.Errorf("model request denied")
	}
	if err := c.commitLifecycleAudit(ctx, meta, corelifecycle.PhaseModelRequested, corelifecycle.AuditMutation{
		ActionKind: "model_request", ResourceName: "llm",
		InputDigest: corelifecycle.HashString(req.System), Decision: "allow", ReasonCode: "model_request_admitted",
		Details: map[string]any{"provider_id": c.modelProviderID(), "model_id": c.modelName()},
	}); err != nil {
		return nil, fmt.Errorf("model request admission audit failed: %w", err)
	}
	return req, nil
}

func (c *Core) modelProviderID() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.providerID
}

func (c *Core) modelName() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.modelID
}

type coreModelContextTransformer struct{ core *Core }

func (coreModelContextTransformer) ID() string { return "builtin.agentcore-model-context" }

func (t coreModelContextTransformer) Transform(_ context.Context, action corelifecycle.MutableAction) (corelifecycle.MutableAction, error) {
	if t.core == nil || action.Kind != "model_request" {
		return corelifecycle.MutableAction{}, fmt.Errorf("invalid model context action")
	}
	t.core.stateMu.RLock()
	mode, skills, groupID := t.core.Mode, t.core.skills, t.core.groupID
	t.core.stateMu.RUnlock()
	system := loadSoul(mode)
	if skills != nil {
		if index := skills.IndexForGroup(groupID); index != "" {
			system += "\n\n" + index
		}
	}
	action.Fields["system"] = system
	return action, nil
}

func cloneMessages(source []llm.Message) []llm.Message {
	clone := append([]llm.Message(nil), source...)
	for i := range clone {
		clone[i].ToolCalls = append([]llm.ToolCall(nil), source[i].ToolCalls...)
	}
	return clone
}

func cloneToolDefs(source []llm.ToolDef) ([]llm.ToolDef, error) {
	clone := append([]llm.ToolDef(nil), source...)
	for i := range clone {
		encoded, err := json.Marshal(source[i].Parameters)
		if err != nil {
			return nil, fmt.Errorf("clone tool schema %q: %w", source[i].Name, err)
		}
		var parameters any
		if err := json.Unmarshal(encoded, &parameters); err != nil {
			return nil, fmt.Errorf("clone tool schema %q: %w", source[i].Name, err)
		}
		clone[i].Parameters = parameters
	}
	return clone, nil
}

func validateModelRequest(request *llm.LLMRequest) error {
	const (
		maxMessages          = 10_000
		maxTools             = 512
		maxModelRequestBytes = 8 << 20
	)
	if request == nil || !utf8.ValidString(request.System) || len(request.System) > maxModelRequestBytes {
		return fmt.Errorf("model request has invalid system prompt")
	}
	total := len(request.System)
	if len(request.Messages) > maxMessages || len(request.Tools) > maxTools {
		return fmt.Errorf("model request exceeds item limits")
	}
	for _, message := range request.Messages {
		switch message.Role {
		case llm.RoleSystem, llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
		default:
			return fmt.Errorf("model request contains invalid message role %q", message.Role)
		}
		if !utf8.ValidString(message.Content) || len(message.Content) > maxChatMessageBytes {
			return fmt.Errorf("model request contains invalid message content")
		}
		total += len(message.Content)
	}
	for _, tool := range request.Tools {
		if tool.Name == "" || !utf8.ValidString(tool.Name) || !utf8.ValidString(tool.Description) {
			return fmt.Errorf("model request contains invalid tool definition")
		}
		encoded, err := json.Marshal(tool.Parameters)
		if err != nil {
			return fmt.Errorf("model request contains invalid tool schema: %w", err)
		}
		total += len(tool.Name) + len(tool.Description) + len(encoded)
	}
	if total > maxModelRequestBytes {
		return fmt.Errorf("model request exceeds %d bytes", maxModelRequestBytes)
	}
	return nil
}

func (c *Core) prepareAssistantDelivery(ctx context.Context, meta corelifecycle.Meta, content string) (string, error) {
	registry := c.lifecycleRegistry()
	if registry == nil {
		return content, nil
	}
	action, err := registry.Transform(ctx, corelifecycle.PhaseAssistantBeforeDeliver, corelifecycle.MutableAction{
		Meta: meta, Kind: "assistant_response", Fields: map[string]any{"content": content},
	})
	if err != nil {
		return "", fmt.Errorf("assistant response transform failed: %w", err)
	}
	transformed, ok := action.Fields["content"].(string)
	if !ok || !utf8.ValidString(transformed) {
		return "", fmt.Errorf("assistant response transformer returned invalid content")
	}
	verdict, err := registry.Guard(ctx, corelifecycle.PhaseAssistantBeforeDeliver, corelifecycle.FrozenAction{
		Meta: meta, Kind: "assistant_response", Resource: "llm",
		ArgsDigest: corelifecycle.HashString(transformed), Sensitive: map[string]any{"content": transformed},
	})
	if err != nil || verdict == corelifecycle.VerdictDeny || verdict == corelifecycle.VerdictRequireApproval {
		return "", fmt.Errorf("assistant response denied")
	}
	if err := c.commitLifecycleAudit(ctx, meta, corelifecycle.PhaseAssistantDelivered, corelifecycle.AuditMutation{
		ActionKind: "assistant_response", ResourceName: "llm",
		InputDigest: corelifecycle.HashString(transformed), Decision: "allow", ReasonCode: "assistant_delivery_admitted",
	}); err != nil {
		return "", fmt.Errorf("assistant delivery audit failed: %w", err)
	}
	return transformed, nil
}

func prepareToolArgs(toolName, encoded string) (json.RawMessage, bool) {
	var rawArgs map[string]any
	if err := json.Unmarshal([]byte(encoded), &rawArgs); err != nil || rawArgs == nil {
		return json.RawMessage(encoded), false
	}
	forceApproval, _ := rawArgs["confirm"].(bool)
	if tool, ok := executor.FindTool(toolName); !ok || !tool.OwnsConfirm {
		delete(rawArgs, "confirm")
	} else {
		forceApproval = false
	}
	cleanArgs, _ := json.Marshal(rawArgs)
	return cleanArgs, forceApproval
}
