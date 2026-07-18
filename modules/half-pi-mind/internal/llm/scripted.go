package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ScriptedStep 定义 ScriptedProvider 的一次确定性响应。
type ScriptedStep struct {
	Response LLMResponse
	Deltas   []string
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
	step, err := p.next(ctx, request)
	if err != nil {
		return nil, err
	}
	return copyScriptedResponse(step.Response), nil
}

// ChatStream 按 Deltas 顺序产生确定性增量；未设置时将完整 Content 作为一个增量。
func (p *ScriptedProvider) ChatStream(ctx context.Context, request *LLMRequest, onDelta TextDeltaFunc) (*LLMResponse, error) {
	step, err := p.next(ctx, request)
	if err != nil {
		return nil, err
	}
	deltas := step.Deltas
	if deltas == nil && step.Response.Content != "" {
		deltas = []string{step.Response.Content}
	}
	if strings.Join(deltas, "") != step.Response.Content {
		return nil, fmt.Errorf("scripted LLM deltas do not reconstruct response content")
	}
	for _, delta := range deltas {
		if delta == "" {
			return nil, fmt.Errorf("scripted LLM delta must not be empty")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if onDelta != nil {
			if err := onDelta(delta); err != nil {
				return nil, err
			}
		}
	}
	return copyScriptedResponse(step.Response), nil
}

func (p *ScriptedProvider) next(ctx context.Context, request *LLMRequest) (ScriptedStep, error) {
	if err := ctx.Err(); err != nil {
		return ScriptedStep{}, err
	}
	p.mu.Lock()
	if p.calls >= len(p.steps) {
		p.mu.Unlock()
		return ScriptedStep{}, fmt.Errorf("scripted LLM steps exhausted")
	}
	step := p.steps[p.calls]
	p.calls++
	p.mu.Unlock()

	if step.Check != nil {
		if err := step.Check(request); err != nil {
			return ScriptedStep{}, fmt.Errorf("scripted LLM request check: %w", err)
		}
	}
	if step.Wait != nil {
		select {
		case <-ctx.Done():
			return ScriptedStep{}, ctx.Err()
		case <-step.Wait:
		}
	}
	if step.Err != nil {
		return ScriptedStep{}, step.Err
	}
	step.Deltas = append([]string(nil), step.Deltas...)
	return step, nil
}

func copyScriptedResponse(source LLMResponse) *LLMResponse {
	response := source
	response.ToolCalls = append([]ToolCall(nil), source.ToolCalls...)
	return &response
}
