package facegateway

import (
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
)

const (
	chatStreamFlushInterval = 30 * time.Millisecond
	chatStreamFlushBytes    = 1 << 10
)

type chatStreamWriter struct {
	g      *Gateway
	record *requestRecord

	mu            sync.Mutex
	responseIndex int
	pendingOffset int64
	pending       string
	timer         *time.Timer
	err           error
	closed        bool
}

func newChatStreamWriter(gateway *Gateway, record *requestRecord) *chatStreamWriter {
	return &chatStreamWriter{g: gateway, record: record}
}

func (w *chatStreamWriter) Write(delta agentcore.ChatTextDelta) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("Chat stream writer is closed")
	}
	if w.err != nil {
		return w.err
	}
	if !utf8.ValidString(delta.Delta) || delta.Delta == "" {
		return fmt.Errorf("Chat delta must be non-empty valid UTF-8")
	}
	if w.pending != "" && w.responseIndex != delta.ResponseIndex {
		if err := w.flushLocked(); err != nil {
			w.err = err
			return err
		}
	}
	offset, err := w.g.chats.appendStreamText(w.record, delta.ResponseIndex, delta.Delta)
	if err != nil {
		w.err = err
		return err
	}
	if w.pending == "" {
		w.responseIndex = delta.ResponseIndex
		w.pendingOffset = offset
	}
	w.pending += delta.Delta
	for len(w.pending) >= chatStreamFlushBytes {
		if err := w.flushChunkLocked(); err != nil {
			w.err = err
			return err
		}
	}
	w.armTimerLocked()
	return nil
}

func (w *chatStreamWriter) Complete(response agentcore.ChatResponse) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if err := w.flushLocked(); err != nil {
		w.err = err
		return err
	}
	if err := w.g.chats.completeStreamResponse(w.record, response.ResponseIndex, response.Content); err != nil {
		w.err = err
		return err
	}
	return nil
}

func (w *chatStreamWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.err != nil {
		return w.err
	}
	if err := w.flushLocked(); err != nil {
		w.err = err
		return err
	}
	return nil
}

func (w *chatStreamWriter) armTimerLocked() {
	if w.pending == "" || w.timer != nil {
		return
	}
	w.timer = time.AfterFunc(chatStreamFlushInterval, func() {
		w.mu.Lock()
		w.timer = nil
		if !w.closed && w.err == nil {
			w.err = w.flushLocked()
		}
		w.mu.Unlock()
	})
}

func (w *chatStreamWriter) flushLocked() error {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	for w.pending != "" {
		if err := w.flushChunkLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (w *chatStreamWriter) flushChunkLocked() error {
	length := min(len(w.pending), protocol.MaxFaceChatDeltaBytes)
	for length > 0 && !utf8.ValidString(w.pending[:length]) {
		length--
	}
	if length == 0 {
		return fmt.Errorf("cannot split Chat delta at a UTF-8 boundary")
	}
	chunk := w.pending[:length]
	payload, err := w.g.chats.emitStreamChunk(w.record, w.responseIndex, w.pendingOffset, chunk)
	if err != nil {
		return err
	}
	w.g.publishChatDelta(payload)
	w.pending = w.pending[length:]
	w.pendingOffset += int64(length)
	if w.pending == "" {
		w.responseIndex = 0
		w.pendingOffset = 0
	}
	return nil
}

func (g *Gateway) publishChatDelta(payload protocol.FaceChatDelta) {
	g.publishTransient(protocol.FaceTransientChatDelta, protocol.FaceScopeSessionsRead,
		payload.ConversationID, protocol.TypeFaceChatDelta, payload)
}

func (g *Gateway) publishChatStreamEnd(payload protocol.FaceChatStreamEnd) {
	g.publishReliableTransient(protocol.FaceTransientChatDelta, protocol.FaceScopeSessionsRead,
		payload.ConversationID, protocol.TypeFaceChatStreamEnd, payload)
}

func (g *Gateway) publishTransient(transient protocol.FaceTransientType, scope protocol.FaceScope, conversationID, typ string, payload any) {
	g.forEachTransientSubscriber(transient, scope, conversationID, func(state *connection) {
		g.sendTransientPayloadLocked(state, typ, payload)
	})
}

func (g *Gateway) publishReliableTransient(transient protocol.FaceTransientType, scope protocol.FaceScope, conversationID, typ string, payload any) {
	g.forEachTransientSubscriber(transient, scope, conversationID, func(state *connection) {
		envelope, err := protocol.NewEnvelope("", typ, payload)
		if err == nil && protocol.ValidateFacePayload(typ, envelope.Payload) == nil {
			g.enqueueLocked(state, *envelope)
		}
	})
}

func (g *Gateway) forEachTransientSubscriber(transient protocol.FaceTransientType, scope protocol.FaceScope, conversationID string, visit func(*connection)) {
	g.mu.Lock()
	connections := make([]*connection, 0, len(g.connections))
	for _, state := range g.connections {
		connections = append(connections, state)
	}
	g.mu.Unlock()
	for _, state := range connections {
		state.mu.Lock()
		if g.matchesTransientLocked(state, transient, scope, conversationID) {
			visit(state)
		}
		state.mu.Unlock()
	}
}

func (g *Gateway) matchesTransientLocked(state *connection, transient protocol.FaceTransientType, scope protocol.FaceScope, conversationID string) bool {
	if state.closed || state.slow || !state.subscribed || !hasScope(state.identity, scope) {
		return false
	}
	if _, ok := state.filter.transients[transient]; !ok {
		return false
	}
	if len(state.filter.conversations) > 0 {
		_, ok := state.filter.conversations[conversationID]
		return ok
	}
	return true
}
