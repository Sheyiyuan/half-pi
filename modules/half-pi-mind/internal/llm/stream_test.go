package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeSSESupportsCRLFCommentsMultilineAndEOF(t *testing.T) {
	input := ": keepalive\r\nevent: update\r\ndata: first\r\ndata: second\r\n\r\ndata: final"
	var events []sseEvent
	err := decodeSSE(strings.NewReader(input), func(event sseEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "update" || events[0].Data != "first\nsecond" || events[1].Data != "final" {
		t.Fatalf("events = %#v", events)
	}
}

func TestDecodeSSERejectsOversizedEvent(t *testing.T) {
	input := "data: " + strings.Repeat("x", maxSSEEventBytes+1) + "\n\n"
	if err := decodeSSE(strings.NewReader(input), func(sseEvent) error { return nil }); err == nil {
		t.Fatal("oversized SSE event accepted")
	}
}

func TestOpenAIChatStreamTextToolsAndUsage(t *testing.T) {
	server := streamServer(t, "/chat/completions", func(t *testing.T, requestBody string) string {
		if !strings.Contains(requestBody, `"stream":true`) || !strings.Contains(requestBody, `"include_usage":true`) {
			t.Fatalf("stream request flags missing: %s", requestBody)
		}
		return strings.Join([]string{
			`sse:{"choices":[{"delta":{"content":"你"}}]}`,
			`sse:{"choices":[{"delta":{"content":"好","tool_calls":[{"index":0,"id":"call-1","function":{"name":"read_","arguments":"{\"path\":"}}]}}]}`,
			`sse:{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"file","arguments":"\"a.txt\"}"}}]}}]}`,
			`sse:{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`,
			`sse:[DONE]`,
		}, "\n")
	})
	defer server.Close()

	var deltas []string
	response, err := ChatWithStreaming(context.Background(), NewOpenAI(server.URL, "secret", "model"), &LLMRequest{}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "你好" || strings.Join(deltas, "") != response.Content {
		t.Fatalf("content = %q, deltas = %#v", response.Content, deltas)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0] != (ToolCall{ID: "call-1", Name: "read_file", Args: `{"path":"a.txt"}`}) {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.Usage != (Usage{InputTokens: 7, OutputTokens: 3}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestAnthropicChatStreamTextToolsAndUsage(t *testing.T) {
	server := streamServer(t, "/v1/messages", func(t *testing.T, requestBody string) string {
		if !strings.Contains(requestBody, `"stream":true`) {
			t.Fatalf("stream request flag missing: %s", requestBody)
		}
		return strings.Join([]string{
			`sse:{"type":"message_start","message":{"usage":{"input_tokens":9,"output_tokens":0}}}`,
			`sse:{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}}`,
			`sse:{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool-1","name":"read_file","input":{}}}`,
			`sse:{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			`sse:{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"b.txt\"}"}}`,
			`sse:{"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"hidden"}}`,
			`sse:{"type":"message_delta","usage":{"output_tokens":4}}`,
			`sse:{"type":"message_stop"}`,
		}, "\n")
	})
	defer server.Close()

	var visible string
	response, err := ChatWithStreaming(context.Background(), NewAnthropic(server.URL, "secret", "model"), &LLMRequest{}, func(delta string) error {
		visible += delta
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visible != "hello " || response.Content != visible {
		t.Fatalf("visible = %q response = %q", visible, response.Content)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Args != `{"path":"b.txt"}` {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.Usage != (Usage{InputTokens: 9, OutputTokens: 4}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestGeminiChatStreamTextFunctionAndUsage(t *testing.T) {
	server := streamServer(t, "/models/model:streamGenerateContent", func(t *testing.T, requestBody string) string {
		return strings.Join([]string{
			`sse:{"candidates":[{"content":{"parts":[{"text":"a"}]}}]}`,
			`sse:{"candidates":[{"content":{"parts":[{"text":"b"},{"functionCall":{"id":"call-2","name":"write_file","args":{"path":"c.txt"}}}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
		}, "\n")
	})
	defer server.Close()

	var visible string
	response, err := ChatWithStreaming(context.Background(), NewGemini(server.URL, "secret", "model"), &LLMRequest{}, func(delta string) error {
		visible += delta
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visible != "ab" || response.Content != visible || len(response.ToolCalls) != 1 || response.ToolCalls[0].ID != "call-2" {
		t.Fatalf("response = %#v visible = %q", response, visible)
	}
	if response.Usage != (Usage{InputTokens: 5, OutputTokens: 2}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestStreamingCallbackErrorStopsProvider(t *testing.T) {
	server := streamServer(t, "/chat/completions", func(t *testing.T, requestBody string) string {
		return "sse:{\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\nsse:[DONE]\n"
	})
	defer server.Close()
	want := errors.New("stop")
	_, err := ChatWithStreaming(context.Background(), NewOpenAI(server.URL, "secret", "model"), &LLMRequest{}, func(string) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestChatWithStreamingSyncFallback(t *testing.T) {
	provider := &syncOnlyProvider{response: LLMResponse{Content: "complete"}}
	var deltas []string
	response, err := ChatWithStreaming(context.Background(), provider, &LLMRequest{}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil || response.Content != "complete" || len(deltas) != 1 || deltas[0] != "complete" {
		t.Fatalf("response = %#v deltas = %#v err = %v", response, deltas, err)
	}
}

type syncOnlyProvider struct {
	response LLMResponse
}

func (p *syncOnlyProvider) Chat(context.Context, *LLMRequest) (*LLMResponse, error) {
	response := p.response
	return &response, nil
}

func streamServer(t *testing.T, path string, response func(*testing.T, string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != path {
			t.Errorf("path = %q, want %q", request.URL.Path, path)
			http.NotFound(writer, request)
			return
		}
		if strings.Contains(path, "streamGenerateContent") && request.URL.Query().Get("alt") != "sse" {
			t.Errorf("Gemini alt = %q", request.URL.Query().Get("alt"))
		}
		var builder strings.Builder
		_, _ = io.Copy(&builder, request.Body)
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, line := range strings.Split(response(t, builder.String()), "\n") {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "sse:") {
				t.Fatalf("invalid test SSE line %q", line)
			}
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", strings.TrimPrefix(line, "sse:"))
		}
	}))
}
