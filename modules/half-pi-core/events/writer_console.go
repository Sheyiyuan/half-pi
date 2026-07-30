package events

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// ConsoleWriter 将事件格式化输出到终端（stderr）。
// 风格与当前的 [TOOL] / [RESULT] / [BLOCKED] 一致。
type ConsoleWriter struct {
	output io.Writer
	mu     sync.Mutex
}

// NewConsoleWriter 创建写入标准错误的终端事件 writer。
func NewConsoleWriter() *ConsoleWriter {
	return NewConsoleWriterWithOutput(os.Stderr)
}

// NewConsoleWriterWithOutput 创建写入指定目标的终端事件 writer。
func NewConsoleWriterWithOutput(output io.Writer) *ConsoleWriter {
	if output == nil {
		output = os.Stderr
	}
	return &ConsoleWriter{output: output}
}

func (w *ConsoleWriter) WriteEvent(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	output := w.output
	if output == nil {
		output = os.Stderr
	}
	prefix := formatPrefix(e)
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return nil
	}
	var rendered strings.Builder
	fmt.Fprintln(&rendered, prefix+msg)
	if detail := consoleDetail(e); detail != "" {
		fmt.Fprintln(&rendered, detail)
	}
	if e.Type == TypeToolResult {
		fmt.Fprintln(&rendered) // 结果后空一行，和旧格式一致
	}
	_, err := io.WriteString(output, rendered.String())
	return err
}

func consoleDetail(event Event) string {
	data, ok := event.Data.(map[string]any)
	if !ok {
		return ""
	}
	var value any
	switch event.Type {
	case TypeToolCall:
		value = data["args"]
	case TypeToolProgress:
		value = data["data"]
	case TypeToolResult:
		value = data["result"]
	}
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (w *ConsoleWriter) Close() error { return nil }

func formatPrefix(e Event) string {
	switch e.Type {
	case TypeToolCall:
		return "── [TOOL] "
	case TypeToolResult:
		return "── [RESULT] "
	case TypeToolBlock:
		return "── [BLOCKED] "
	case TypeLLMRequest:
		return "── [LLM >>] "
	case TypeLLMResponse:
		return "── [LLM <<] "
	case TypeSecurity:
		return "── [SEC] "
	case TypeModeChange:
		return "── [MODE] "
	default:
		return ""
	}
}
