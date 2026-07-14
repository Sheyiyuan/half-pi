package events

import (
	"fmt"
	"os"
	"strings"
)

// ConsoleWriter 将事件格式化输出到终端（stderr）。
// 风格与当前的 [TOOL] / [RESULT] / [BLOCKED] 一致。
type ConsoleWriter struct{}

func NewConsoleWriter() *ConsoleWriter {
	return &ConsoleWriter{}
}

func (w *ConsoleWriter) WriteEvent(e Event) error {
	prefix := formatPrefix(e)
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return nil
	}
	fmt.Fprintln(os.Stderr, prefix+msg)
	if e.Type == TypeToolResult {
		fmt.Fprintln(os.Stderr) // 结果后空一行，和旧格式一致
	}
	return nil
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
