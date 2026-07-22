// Package lifecycle 定义统一的生命周期阶段、身份标识和 Hook 通道接口。
// 它不依赖任何 Mind internal 类型，可被所有模块安全导入。
package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Meta 是每次生命周期事件携带的统一身份标识。
// 新的事件不直接复用 Face connection event_seq、Gateway Envelope Seq 或 RemoteRun progress seq。
type Meta struct {
	SchemaVersion  int       `json:"schema_version"`
	EventID        string    `json:"event_id"`
	TraceID        string    `json:"trace_id"`
	SpanID         string    `json:"span_id"`
	ParentSpanID   string    `json:"parent_span_id,omitempty"`
	RequestID      string    `json:"request_id"`
	ConversationID string    `json:"conversation_id"`
	GroupID        string    `json:"group_id,omitempty"`
	PrincipalID    string    `json:"principal_id"`
	Source         string    `json:"source"`
	NodeID         string    `json:"node_id"`
	Sequence       int64     `json:"sequence"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// Source 常量。
const (
	SourceFace   = "face"
	SourceRepl   = "repl"
	SourceMind   = "mind"
	SourceHand   = "hand"
	SourcePlugin = "plugin"
)

// NewMeta 创建带有随机 ID 的基础身份标识。
// 调用方应随后设置 SpanID、ParentSpanID、RequestID、ConversationID 等字段。
func NewMeta(source string) Meta {
	now := time.Now().UTC()
	return Meta{
		SchemaVersion: 1,
		EventID:       newID(),
		TraceID:       newID(),
		SpanID:        newID(),
		Source:        source,
		OccurredAt:    now,
	}
}

// ChildMeta 创建当前 span 的子 span 身份标识。
func (m Meta) ChildMeta(source string) Meta {
	child := NewMeta(source)
	child.TraceID = m.TraceID
	child.ParentSpanID = m.SpanID
	child.SpanID = newID()
	child.RequestID = m.RequestID
	child.ConversationID = m.ConversationID
	child.GroupID = m.GroupID
	child.PrincipalID = m.PrincipalID
	child.NodeID = m.NodeID
	return child
}

// EventMeta 为同一 span 创建一个新的事件身份。
func (m Meta) EventMeta(sequence int64) Meta {
	m.EventID = newID()
	m.Sequence = sequence
	m.OccurredAt = time.Now().UTC()
	return m
}

// WithNode 设置执行节点标识并返回副本。
func (m Meta) WithNode(nodeID string) Meta {
	m.NodeID = nodeID
	return m
}

// WithConversation 设置对话 ID 并返回副本。
func (m Meta) WithConversation(conversationID string) Meta {
	m.ConversationID = conversationID
	return m
}

// WithGroup 设置 SessionGroup ID 并返回副本。
func (m Meta) WithGroup(groupID string) Meta {
	m.GroupID = groupID
	return m
}

// WithPrincipal 设置主体 ID 并返回副本。
func (m Meta) WithPrincipal(principalID string) Meta {
	m.PrincipalID = principalID
	return m
}

// WithRequest 设置幂等请求 ID 并返回副本。
func (m Meta) WithRequest(requestID string) Meta {
	m.RequestID = requestID
	return m
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
