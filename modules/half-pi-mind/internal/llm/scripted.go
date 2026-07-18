package llm

import (
	"context"
	"fmt"
	"sync"
)

// ScriptedStep 定义 ScriptedProvider 的一次确定性响应。
type ScriptedStep struct {
	Response LLMResponse
	Err      error
	Check    func(*LLMRequest) error
	Wait     <-chan struct{}
}

// ScriptedProvider 按注册顺序返回固定响应，用于确定性运行时和 E2E fixture。
type ScriptedProvider struct {
	mu    sync.Mutex
	steps []ScriptedStep
	calls int
}

// NewScriptedProvider 创建一个拥有独立脚本游标的 Provider。
func NewScriptedProvider(steps ...ScriptedStep) *ScriptedProvider {
	return &ScriptedProvider{steps: append([]ScriptedStep(nil), steps...)}
}

// Calls 返回已经领取的脚本步骤数。
func (p *ScriptedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Chat 返回下一条脚本响应，并尊重等待期间的 context 取消。
func (p *ScriptedProvider) Chat(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.calls >= len(p.steps) {
		p.mu.Unlock()
		return nil, fmt.Errorf("scripted LLM steps exhausted")
	}
	step := p.steps[p.calls]
	p.calls++
	p.mu.Unlock()

	if step.Check != nil {
		if err := step.Check(request); err != nil {
			return nil, fmt.Errorf("scripted LLM request check: %w", err)
		}
	}
	if step.Wait != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-step.Wait:
		}
	}
	if step.Err != nil {
		return nil, step.Err
	}
	response := step.Response
	response.ToolCalls = append([]ToolCall(nil), step.Response.ToolCalls...)
	return &response, nil
}
