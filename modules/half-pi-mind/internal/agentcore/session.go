package agentcore

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// SaveSession 将当前对话历史持久化到数据库。
func (c *Core) SaveSession() error {
	c.chatMu.Lock()
	defer c.chatMu.Unlock()
	return c.saveSessionLocked()
}

func (c *Core) saveSessionLocked() error {
	c.stateMu.RLock()
	store, sessionID := c.store, c.sessionID
	c.stateMu.RUnlock()
	if store == nil || sessionID == "" {
		return nil
	}
	// 自动命名只处理仍未命名的会话，显式名称始终优先。
	sess, err := store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("save session: get session: %w", err)
	}
	if sess != nil && sess.Name == "" && len(c.history) > 0 {
		for _, m := range c.history {
			if m.Role == llm.RoleUser {
				if err := store.UpdateSessionName(sessionID, automaticConversationName(m.Content)); err != nil {
					return fmt.Errorf("save session: auto-name: %w", err)
				}
				break
			}
		}
	}
	if c.persistedMessages > len(c.history) {
		return fmt.Errorf("save session: persisted message count exceeds history")
	}
	newMessages := llmMsgToStore(c.history[c.persistedMessages:])
	for i := range newMessages {
		newMessages[i].Seq = c.persistedSeq + i + 1
	}
	if err := store.AppendMessages(sessionID, c.persistedSeq, newMessages); err != nil {
		return fmt.Errorf("save session: append messages: %w", err)
	}
	c.persistedMessages = len(c.history)
	c.persistedSeq += len(newMessages)
	c.notifySessionChanged()
	return nil
}

func automaticConversationName(content string) string {
	name := []rune(strings.Join(strings.Fields(content), " "))
	if len(name) <= 48 {
		return string(name)
	}
	return string(name[:48]) + "..."
}

// llmMsgToStore 将 LLM 消息转为持久化格式，ToolCalls 序列化为 JSON。
func llmMsgToStore(msgs []llm.Message) []store.Message {
	result := make([]store.Message, len(msgs))
	for i, m := range msgs {
		tcJSON, _ := json.Marshal(m.ToolCalls)
		result[i] = store.Message{
			Role:              string(m.Role),
			Content:           m.Content,
			RequestID:         m.RequestID,
			ToolID:            m.ToolID,
			ToolCalls:         string(tcJSON),
			CompactProjection: m.CompactProjection,
			Seq:               i + 1,
		}
	}
	return result
}

// storeMsgToLLM 将持久化消息还原为 LLM 消息，ToolCalls 反序列化。
func storeMsgToLLM(msgs []store.Message) []llm.Message {
	result := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		var toolCalls []llm.ToolCall
		if m.ToolCalls != "" {
			json.Unmarshal([]byte(m.ToolCalls), &toolCalls)
		}
		result[i] = llm.Message{
			Role:              llm.Role(m.Role),
			Content:           m.Content,
			RequestID:         m.RequestID,
			ToolID:            m.ToolID,
			ToolCalls:         toolCalls,
			CompactProjection: m.CompactProjection,
		}
	}
	return result
}
