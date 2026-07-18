package llm

import (
	"context"
	"errors"
	"testing"
)

func TestScriptedProviderChecksSequenceAndCopiesResponses(t *testing.T) {
	provider := NewScriptedProvider(
		ScriptedStep{
			Check: func(request *LLMRequest) error {
				if len(request.Messages) != 1 || request.Messages[0].Content != "hello" {
					return errors.New("unexpected first request")
				}
				return nil
			},
			Response: LLMResponse{ToolCalls: []ToolCall{{ID: "call-1", Name: "tool", Args: `{}`}}},
		},
		ScriptedStep{Response: LLMResponse{Content: "done"}},
	)
	first, err := provider.Chat(context.Background(), &LLMRequest{Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil || len(first.ToolCalls) != 1 {
		t.Fatalf("first scripted response = %+v, %v", first, err)
	}
	first.ToolCalls[0].Name = "mutated"
	second, err := provider.Chat(context.Background(), &LLMRequest{})
	if err != nil || second.Content != "done" || provider.Calls() != 2 {
		t.Fatalf("second scripted response = %+v, calls %d, %v", second, provider.Calls(), err)
	}
	if _, err := provider.Chat(context.Background(), &LLMRequest{}); err == nil {
		t.Fatal("exhausted scripted provider accepted another call")
	}
}

func TestScriptedProviderWaitHonorsCancellation(t *testing.T) {
	provider := NewScriptedProvider(ScriptedStep{Wait: make(chan struct{})})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Chat(ctx, &LLMRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scripted call error = %v", err)
	}
	if provider.Calls() != 0 {
		t.Fatal("pre-cancelled scripted call consumed a step")
	}
}
