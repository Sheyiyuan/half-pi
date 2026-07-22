package facegateway

import (
	"encoding/json"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
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

// PublishApprovalRequested 发布可由具备审批 scope 的 Face 裁决的请求。
func (g *Gateway) PublishApprovalRequested(request protocol.ApprovalRequest) {
	if request.ApprovalID == "" || request.ConversationID == "" {
		return
	}
	g.domainMu.Lock()
	defer g.domainMu.Unlock()
	version := g.version.Add(1)
	g.publish(domainEvent{
		conversationID: request.ConversationID, requestID: request.RequestID,
		typ: protocol.FaceEventApprovalRequested, source: "approval", message: "Approval requested",
		data: request,
	})
	g.publishConversationVersion(request.ConversationID, version)
}

// PublishApprovalFinished 发布审批终态，并在无裁决终态时仅推进快照版本。
func (g *Gateway) PublishApprovalFinished(request protocol.ApprovalRequest, status approval.Status, resolution approval.Resolution) {
	if request.ApprovalID == "" || request.ConversationID == "" {
		return
	}
	g.domainMu.Lock()
	defer g.domainMu.Unlock()
	version := g.version.Add(1)
	if status == approval.StatusResolved && resolution.Decision != "" {
		g.publish(domainEvent{
			conversationID: request.ConversationID, requestID: request.RequestID,
			typ: protocol.FaceEventApprovalResolved, source: "approval", message: "Approval resolved",
			data: protocol.ApprovalResolvedEventData{
				ApprovalID: request.ApprovalID, Decision: resolution.Decision, Actor: resolution.Actor.ID,
			},
		})
	}
	g.publishConversationVersion(request.ConversationID, version)
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
		conversationID: run.SessionID, requestID: run.Metadata.RequestID,
		typ:    protocol.FaceEventRemoteRunChanged,
		source: "remoteexec", message: "Remote run changed",
		data: protocol.RemoteRunChangedEventData{
			RunID: run.ID, HandID: run.HandID, Tool: run.Tool, Status: run.Status, DurationMs: duration,
		},
	})
	g.publishConversationVersion(run.SessionID, version)
}

// PublishRunProgress 投影已接纳 foreground run 的 stdout/stderr 增量。
func (g *Gateway) PublishRunProgress(observation remoteexec.ProgressObservation) {
	if observation.Run.ID == "" || observation.Run.SessionID == "" || observation.Run.DurableTask ||
		protocol.IsTerminalRunStatus(observation.Run.Status) {
		return
	}
	payload := protocol.FaceRunProgress{
		ConversationID: observation.Run.SessionID,
		RequestID:      observation.Run.Metadata.RequestID,
		RunID:          observation.Progress.RunID,
		Seq:            observation.Progress.Seq,
		Kind:           observation.Progress.Kind,
		Data:           observation.Progress.Data,
		Gap:            observation.Gap,
	}
	g.publishTransient(protocol.FaceTransientRunProgress, protocol.FaceScopeRunsOutput,
		payload.ConversationID, protocol.TypeFaceRunProgress, payload)
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

// PublishCompactEvent 按 phase 投影不含摘要正文的 Compact domain fact。
func (g *Gateway) PublishCompactEvent(event compact.Event) {
	var domain domainEvent
	stateChanged := false
	domain.source = "compact"
	domain.level = protocol.FaceEventLevelInfo
	switch value := event.(type) {
	case compact.RequestedEvent:
		stateChanged = value.StateChanged
		domain.conversationID, domain.requestID = value.SessionID, value.RequestID
		domain.typ, domain.message = protocol.FaceEventCompactRequested, "Compact requested"
		domain.data = protocol.CompactRequestedEventData{Trigger: projectCompactTrigger(value.Trigger)}
	case compact.StartedEvent:
		stateChanged = value.Trigger == compact.TriggerAutomatic
		domain.conversationID, domain.requestID = value.SessionID, value.RequestID
		domain.typ, domain.message = protocol.FaceEventCompactStarted, "Compact started"
		domain.data = protocol.CompactStartedEventData{
			Trigger: projectCompactTrigger(value.Trigger), FromSeq: value.FromSeq, ToSeq: value.ToSeq,
			GenerationMode: protocol.FaceCompactGenerationMode(value.GenerationMode), SourceDigest: value.SourceDigest, Attempt: value.Attempt,
		}
	case compact.CompletedEvent:
		stateChanged = value.StateChanged
		domain.conversationID, domain.requestID = value.SessionID, value.RequestID
		domain.typ, domain.message = protocol.FaceEventCompactCompleted, "Compact completed"
		domain.data = protocol.CompactCompletedEventData{
			Trigger: projectCompactTrigger(value.Trigger), SummaryID: value.SummaryID,
			FromSeq: value.FromSeq, ToSeq: value.ToSeq, BeforeEstimatedTokens: value.BeforeEstimatedTokens,
			AfterEstimatedTokens: value.AfterEstimatedTokens, SummaryBytes: value.SummaryBytes,
			SourceDigest: value.SourceDigest, DurationMS: value.DurationMS, ContextVersion: value.ContextVersion, Reused: value.Reused,
		}
	case compact.FailedEvent:
		stateChanged = value.StateChanged
		domain.conversationID, domain.requestID = value.SessionID, value.RequestID
		domain.typ, domain.message, domain.level = protocol.FaceEventCompactFailed, "Compact failed", protocol.FaceEventLevelError
		data := protocol.CompactFailedEventData{
			Trigger: projectCompactTrigger(value.Trigger), Reason: protocol.FaceErrorCode(value.Reason),
			DurationMS: value.DurationMS, RetryScheduled: value.RetryScheduled, SourceDigest: value.SourceDigest,
		}
		if value.FromSeq > 0 {
			data.FromSeq, data.ToSeq = intPointer(value.FromSeq), intPointer(value.ToSeq)
		}
		if value.RetryScheduled {
			data.PendingAttempt, data.RetryNotBefore = int64Pointer(value.PendingAttempt), int64Pointer(value.RetryNotBefore)
		}
		domain.data = data
	default:
		return
	}
	if domain.conversationID == "" || domain.requestID == "" {
		return
	}
	g.domainMu.Lock()
	g.publish(domain)
	if stateChanged {
		g.publishConversationVersion(domain.conversationID, g.version.Add(1))
	}
	g.domainMu.Unlock()
}

func projectCompactTrigger(trigger compact.Trigger) protocol.FaceCompactTrigger {
	return protocol.FaceCompactTrigger(trigger)
}

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }

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
	if isCompactEvent(event.typ) {
		if _, ok := state.features[protocol.FaceFeatureContextCompaction]; !ok {
			return false
		}
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
