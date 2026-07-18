package facegateway

import (
	"encoding/json"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

// HandleHandConnect 投影已认证 Hand 上线事件。
func (g *Gateway) HandleHandConnect(peer *hub.Peer) {
	if peer == nil || peer.Type != hub.PeerHand || peer.Info == nil {
		return
	}
	g.publish(domainEvent{
		typ: protocol.FaceEventHandConnected, source: "hand", message: "Hand connected",
		data: protocol.HandConnectedEventData{
			HandID: peer.ID, Hostname: peer.Info.Hostname, OS: peer.Info.OS, Arch: peer.Info.Arch,
		},
	})
}

// HandleHandDisconnect 投影 Hand 下线事件。
func (g *Gateway) HandleHandDisconnect(peer *hub.Peer) {
	if peer == nil || peer.Type != hub.PeerHand {
		return
	}
	g.publish(domainEvent{
		typ: protocol.FaceEventHandDisconnected, source: "hand", message: "Hand disconnected",
		data: protocol.HandDisconnectedEventData{HandID: peer.ID},
	})
}

// PublishConversationChanged 发布 conversation 权威状态变化。
func (g *Gateway) PublishConversationChanged(conversationID string) {
	if conversationID == "" {
		return
	}
	g.domainMu.Lock()
	defer g.domainMu.Unlock()
	version := g.version.Add(1)
	g.publish(domainEvent{
		conversationID: conversationID, typ: protocol.FaceEventConversationChanged,
		source: "conversation", message: "Conversation changed",
		data: protocol.ConversationChangedEventData{ConversationID: conversationID, SnapshotVersion: version},
	})
}

// PublishRemoteRunChanged 发布 run 状态变化。
func (g *Gateway) PublishRemoteRunChanged(run remoteexec.Run) {
	if run.ID == "" || run.SessionID == "" {
		return
	}
	g.domainMu.Lock()
	defer g.domainMu.Unlock()
	version := g.version.Add(1)
	duration := time.Since(run.CreatedAt).Milliseconds()
	if !run.FinishedAt.IsZero() {
		duration = run.FinishedAt.Sub(run.CreatedAt).Milliseconds()
	}
	if duration < 0 {
		duration = 0
	}
	g.publish(domainEvent{
		conversationID: run.SessionID, typ: protocol.FaceEventRemoteRunChanged,
		source: "remoteexec", message: "Remote run changed",
		data: protocol.RemoteRunChangedEventData{
			RunID: run.ID, HandID: run.HandID, Tool: run.Tool, Status: run.Status, DurationMs: duration,
		},
	})
	g.publishConversationVersion(run.SessionID, version)
}

// PublishTaskChanged 发布后台任务 best-known 状态变化。
func (g *Gateway) PublishTaskChanged(task remoteexec.Task) {
	if task.TaskID == "" || task.SessionID == "" {
		return
	}
	g.domainMu.Lock()
	defer g.domainMu.Unlock()
	version := g.version.Add(1)
	g.publish(domainEvent{
		conversationID: task.SessionID, typ: protocol.FaceEventTaskChanged,
		source: "task", message: "Task changed", data: projectTask(task),
	})
	g.publishConversationVersion(task.SessionID, version)
}

func (g *Gateway) publishConversationVersion(conversationID string, version int64) {
	g.publish(domainEvent{
		conversationID: conversationID, typ: protocol.FaceEventConversationChanged,
		source: "conversation", message: "Conversation changed",
		data: protocol.ConversationChangedEventData{ConversationID: conversationID, SnapshotVersion: version},
	})
}

type domainEvent struct {
	conversationID string
	requestID      string
	typ            protocol.FaceEventType
	source         string
	level          protocol.FaceEventLevel
	message        string
	data           any
}

func (g *Gateway) publish(event domainEvent) {
	data, err := json.Marshal(event.data)
	if err != nil {
		return
	}
	if event.level == "" {
		event.level = protocol.FaceEventLevelInfo
	}
	g.mu.Lock()
	connections := make([]*connection, 0, len(g.connections))
	for _, state := range g.connections {
		connections = append(connections, state)
	}
	g.mu.Unlock()
	for _, state := range connections {
		state.mu.Lock()
		if !g.matchesLocked(state, event) {
			state.mu.Unlock()
			continue
		}
		sequence := state.eventSeq + 1
		payload := protocol.FaceEvent{
			EventSeq: sequence, ConversationID: event.conversationID, RequestID: event.requestID,
			Type: event.typ, Source: event.source, Level: event.level, Message: event.message,
			Data: data, Timestamp: time.Now().UTC(),
		}
		env, err := protocol.NewEnvelope("", protocol.TypeFaceEvent, payload)
		if err == nil && protocol.ValidateFacePayload(protocol.TypeFaceEvent, env.Payload) == nil && g.enqueueLocked(state, *env) {
			state.eventSeq = sequence
		}
		state.mu.Unlock()
	}
}

func (g *Gateway) matchesLocked(state *connection, event domainEvent) bool {
	if state.closed || state.slow || !state.subscribed {
		return false
	}
	scope := eventScope(event.typ)
	if scope != "" && !hasScope(state.identity, scope) {
		return false
	}
	if len(state.filter.events) > 0 {
		if _, ok := state.filter.events[event.typ]; !ok {
			return false
		}
	}
	if event.conversationID != "" && len(state.filter.conversations) > 0 {
		if _, ok := state.filter.conversations[event.conversationID]; !ok {
			return false
		}
	}
	return true
}
