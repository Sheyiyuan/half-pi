// Package events 提供统一的事件总线，Mind 各组件通过它输出日志、调试信息、
// 工具调用记录等。ConsoleWriter 渲染到终端，FileWriter 持久化，后续
// TCPWriter 可推送到远程 Face。
//
// Agent Core 只发事件，不直接 fmt.Printf，保持核心纯净。
package events

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Event 是 Mind 系统的结构化事件。
type Event struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Source    string    `json:"source"`
	Level     string    `json:"level"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Data      any       `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ── 事件级别 ──

const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// ── 事件类型 ──

const (
	TypeSystem     = "system"
	TypeUserInput  = "user_input"
	TypeLLMRequest = "llm_request"
	TypeLLMResponse = "llm_response"
	TypeToolCall   = "tool_call"
	TypeToolResult = "tool_result"
	TypeToolBlock  = "tool_blocked"
	TypeSecurity   = "security"
	TypeModeChange = "mode_change"
)

// New 创建一条事件。会话 ID 和来源由调用方传入，其余字段自动填充。
func New(sessionID, source, level, eventType, message string) Event {
	return Event{
		ID:        newID(),
		SessionID: sessionID,
		Source:    source,
		Level:     level,
		Type:      eventType,
		Message:   message,
		Timestamp: time.Now(),
	}
}

// WithData 给事件附加结构化数据，返回副本。
func (e Event) WithData(data any) Event {
	e.Data = data
	return e
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%016x", b)
}
