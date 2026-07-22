package agentcore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestChatStreamsMultipleResponsesAndPersistsCompletedBatches(t *testing.T) {
	provider := llm.NewScriptedProvider(
		llm.ScriptedStep{
			Response: llm.LLMResponse{Content: "checking", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read", Args: `{}`}}},
			Deltas:   []string{"check", "ing"},
		},
		llm.ScriptedStep{Response: llm.LLMResponse{Content: "done"}, Deltas: []string{"do", "ne"}},
	)
	db, err := store.New(filepath.Join(t.TempDir(), "stream.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	group, _ := db.UpsertGroup(t.TempDir())
	if err := db.CreateSession(group.ID, "stream-session"); err != nil {
		t.Fatal(err)
	}
	core, _ := New(provider, &stubExecutor{})
	if err := core.SetStore(db, "stream-session"); err != nil {
		t.Fatal(err)
	}
	var lifecycle []string
	ctx := requestctx.WithRequestID(context.Background(), "request-stream")
	transport := ChatTransport{
		TextDelta: func(delta ChatTextDelta) error {
			lifecycle = append(lifecycle, "delta:"+delta.Delta)
			return nil
		},
		ResponseCompleted: func(response ChatResponse) error {
			lifecycle = append(lifecycle, "response")
			return nil
		},
		ToolCalled:    func(ChatToolCall) { lifecycle = append(lifecycle, "tool-called") },
		ToolCompleted: func(ChatToolResult) { lifecycle = append(lifecycle, "tool-completed") },
	}
	reply, err := core.ChatWithTransport(ctx, "start", transport)
	if err != nil || reply != "done" {
		t.Fatalf("reply = %q, err = %v", reply, err)
	}
	want := []string{"delta:check", "delta:ing", "response", "tool-called", "tool-completed", "delta:do", "delta:ne", "response"}
	if !reflect.DeepEqual(lifecycle, want) {
		t.Fatalf("lifecycle = %#v, want %#v", lifecycle, want)
	}
	messages, err := db.GetMessages("stream-session")
	if err != nil || len(messages) != 4 {
		t.Fatalf("messages = %#v, err = %v", messages, err)
	}
	for _, message := range messages {
		if message.RequestID != "request-stream" {
			t.Fatalf("message request binding = %#v", messages)
		}
	}
	if messages[1].Content != "checking" || messages[2].Role != "tool" || messages[3].Content != "done" {
		t.Fatalf("persisted batches = %#v", messages)
	}
}

func TestChatDoesNotCompleteOrPersistPartialProviderResponse(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "provider error", err: errors.New("provider failed")},
		{name: "context cancelled", err: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &partialFailureProvider{err: test.err}
			db, err := store.New(filepath.Join(t.TempDir(), "partial.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			group, _ := db.UpsertGroup(t.TempDir())
			_ = db.CreateSession(group.ID, "partial-session")
			core, _ := New(provider, &stubExecutor{})
			_ = core.SetStore(db, "partial-session")
			var deltas []string
			completed := false
			ctx := requestctx.WithRequestID(context.Background(), "partial-request")
			transport := ChatTransport{
				TextDelta: func(delta ChatTextDelta) error {
					deltas = append(deltas, delta.Delta)
					return nil
				},
				ResponseCompleted: func(ChatResponse) error {
					completed = true
					return nil
				},
			}
			if _, err := core.ChatWithTransport(ctx, "hello", transport); !errors.Is(err, test.err) {
				t.Fatalf("Chat error = %v, want %v", err, test.err)
			}
			if !reflect.DeepEqual(deltas, []string{"partial"}) || completed {
				t.Fatalf("partial lifecycle = deltas %v completed %t", deltas, completed)
			}
			messages, _ := db.GetMessages("partial-session")
			if len(messages) != 1 || messages[0].Role != "user" {
				t.Fatalf("partial assistant response was persisted: %#v", messages)
			}
		})
	}
}

type partialFailureProvider struct {
	err error
}

func (p *partialFailureProvider) Chat(context.Context, *llm.LLMRequest) (*llm.LLMResponse, error) {
	return nil, p.err
}

func (p *partialFailureProvider) ChatStream(_ context.Context, _ *llm.LLMRequest, onDelta llm.TextDeltaFunc) (*llm.LLMResponse, error) {
	_ = onDelta("partial")
	return nil, p.err
}
