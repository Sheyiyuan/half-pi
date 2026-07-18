package facegateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
)

func (g *Gateway) handleCommand(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeFaceConversationList:
		g.handleConversationList(state, identity, env)
	case protocol.TypeFaceConversationCreate:
		g.handleConversationCreate(state, identity, env)
	case protocol.TypeFaceConversationSnapshot:
		g.handleConversationSnapshot(state, identity, env)
	case protocol.TypeFaceConversationRename:
		g.handleConversationRename(state, identity, env)
	case protocol.TypeFaceSubscribe:
		g.handleSubscribe(state, identity, env)
	case protocol.TypeFaceHandList:
		g.handleHandList(state, identity, env)
	case protocol.TypeFaceHandGet:
		g.handleHandGet(state, identity, env)
	case protocol.TypeFaceRunGet:
		g.handleRunGet(state, identity, env)
	case protocol.TypeFaceTaskList:
		g.handleTaskList(state, identity, env)
	case protocol.TypeFaceTaskGet:
		g.handleTaskGet(state, identity, env)
	case protocol.TypeFaceTaskLog:
		g.handleTaskLog(state, identity, env)
	default:
		meta := decodeMeta(env.Payload)
		if scope := commandScope(env.Type); scope != "" && !g.requireScope(state, identity, meta, scope) {
			return
		}
		g.sendError(state, meta, protocol.FaceErrorInternal, "Face operation is not implemented", false)
	}
}

func (g *Gateway) handleConversationList(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceConversationList](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeSessionsRead) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationConversationList, 0) {
		return
	}
	sessions, err := g.store.ListSessions(g.conversations.GroupID())
	if err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation list failed")
		return
	}
	result := protocol.ConversationListResult{Conversations: make([]protocol.ConversationSummary, 0, len(sessions))}
	for _, session := range sessions {
		summary, err := g.conversationSummary(session)
		if err != nil {
			g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation list failed")
			return
		}
		result.Conversations = append(result.Conversations, summary)
	}
	g.sendResult(state, meta, protocol.FaceOperationConversationList, result)
}

func (g *Gateway) handleConversationCreate(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceConversationCreate](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeSessionsWrite) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationConversationCreate, 0) {
		return
	}
	actor, err := g.conversations.Create(strings.TrimSpace(request.Name))
	if err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation creation failed")
		return
	}
	session, err := g.store.GetSession(actor.Core().SessionID())
	if err != nil || session == nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation creation failed")
		return
	}
	summary, err := g.conversationSummary(*session)
	if err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation creation failed")
		return
	}
	g.sendResult(state, meta, protocol.FaceOperationConversationCreate, protocol.ConversationCreateResult{Conversation: summary})
}

func (g *Gateway) handleConversationRename(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceConversationRename](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeSessionsWrite) || !g.requireConversation(state, meta) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		g.sendError(state, meta, protocol.FaceErrorInvalidRequest, "conversation name is required", false)
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationConversationRename, 0) {
		return
	}
	if err := g.conversations.Rename(request.ConversationID, name); err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation rename failed")
		return
	}
	session, err := g.store.GetSession(request.ConversationID)
	if err != nil || session == nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation rename failed")
		return
	}
	summary, err := g.conversationSummary(*session)
	if err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation rename failed")
		return
	}
	g.sendResult(state, meta, protocol.FaceOperationConversationRename, protocol.ConversationRenameResult{Conversation: summary})
}

func (g *Gateway) handleConversationSnapshot(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceConversationSnapshot](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeSessionsRead) || !g.requireConversation(state, meta) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationConversationSnapshot, g.version.Load()) {
		return
	}
	snapshot, err := g.snapshot(identity, request.ConversationID)
	if err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation snapshot failed")
		return
	}
	payload := protocol.FaceSnapshot{RequestID: request.RequestID, Snapshot: snapshot}
	raw, err := json.Marshal(payload)
	if err != nil || protocol.ValidateFacePayload(protocol.TypeFaceSnapshot, raw) != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "conversation snapshot projection failed")
		return
	}
	g.sendPayload(state, protocol.TypeFaceSnapshot, payload)
}

func (g *Gateway) handleSubscribe(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceSubscribe](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID}
	filter, code, message := g.validateSubscription(identity, request)
	if code != "" {
		g.sendError(state, meta, code, message, false)
		return
	}
	state.mu.Lock()
	state.identity = identity
	state.filter = filter
	state.subscribed = true
	accepted, err := protocol.NewEnvelope("", protocol.TypeFaceAccepted, protocol.FaceAccepted{
		RequestID: request.RequestID, Operation: protocol.FaceOperationSubscribe, SnapshotVersion: g.version.Load(),
	})
	if err == nil {
		g.enqueueLocked(state, *accepted)
	}
	state.mu.Unlock()
}

func (g *Gateway) handleHandList(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceHandList](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeHandsRead) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationHandList, 0) {
		return
	}
	peers := g.hub.PeersByType(hub.PeerHand)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	result := protocol.HandListResult{Hands: make([]protocol.HandSummary, 0, len(peers))}
	for _, peer := range peers {
		result.Hands = append(result.Hands, projectStaticHand(peer))
	}
	g.sendResult(state, meta, protocol.FaceOperationHandList, result)
}

func (g *Gateway) handleHandGet(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceHandGet](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeHandsRead) {
		return
	}
	peer := g.hub.PeerByType(hub.PeerHand, request.HandID)
	if peer == nil {
		g.sendError(state, meta, protocol.FaceErrorHandNotFound, "Hand is not online", false)
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationHandGet, 0) {
		return
	}
	ctx, cancel := context.WithTimeout(state.ctx, handQueryTimeout)
	info, err := g.authority.GetHandInfo(ctx, request.HandID)
	cancel()
	if err != nil {
		g.sendCommandFailure(state, meta, err, protocol.FaceErrorHandOffline, "Hand information is unavailable")
		return
	}
	hand := projectStaticHand(peer)
	hand.Hostname, hand.OS, hand.Tools = info.Host, info.OS, info.Tools
	if hand.Tools == nil {
		hand.Tools = []protocol.ToolInfo{}
	}
	g.sendResult(state, meta, protocol.FaceOperationHandGet, protocol.HandGetResult{Hand: hand})
}

func (g *Gateway) handleRunGet(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceRunGet](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeRunsRead) || !g.requireConversation(state, meta) {
		return
	}
	run, found, err := g.lookupRun(request.RunID)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "run lookup failed", true)
		return
	}
	if !found || run.conversationID != request.ConversationID {
		g.sendError(state, meta, protocol.FaceErrorRunNotFound, "run was not found", false)
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationRunGet, 0) {
		return
	}
	g.sendResult(state, meta, protocol.FaceOperationRunGet, protocol.RunGetResult{Run: run.summary})
}

func (g *Gateway) handleTaskList(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceTaskList](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeTasksRead) || !g.requireConversation(state, meta) {
		return
	}
	result, err := g.listTasks(request)
	if err != nil {
		if errors.Is(err, errInvalidTaskCursor) {
			g.sendError(state, meta, protocol.FaceErrorInvalidRequest, "invalid task cursor", false)
			return
		}
		if !g.sendAccepted(state, meta, protocol.FaceOperationTaskList, 0) {
			return
		}
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "task list failed")
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationTaskList, 0) {
		return
	}
	g.sendResult(state, meta, protocol.FaceOperationTaskList, result)
}

func (g *Gateway) handleTaskGet(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceTaskGet](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeTasksRead) || !g.requireConversation(state, meta) {
		return
	}
	if !g.requireTask(state, meta, request.TaskID) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationTaskGet, 0) {
		return
	}
	ctx, cancel := context.WithTimeout(state.ctx, taskQueryTimeout)
	task, err := g.tasks.Get(ctx, request.ConversationID, request.TaskID)
	cancel()
	if err != nil {
		g.sendCommandFailure(state, meta, err, protocol.FaceErrorTaskStale, "task status is unavailable")
		return
	}
	g.sendResult(state, meta, protocol.FaceOperationTaskGet, protocol.TaskGetResult{Task: projectTask(task)})
}

func (g *Gateway) handleTaskLog(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceTaskLog](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeTasksRead) || !g.requireConversation(state, meta) {
		return
	}
	if !g.requireTask(state, meta, request.TaskID) {
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationTaskLog, 0) {
		return
	}
	ctx, cancel := context.WithTimeout(state.ctx, taskQueryTimeout)
	log, err := g.tasks.ReadLog(ctx, request.ConversationID, request.TaskID, request.Offset, request.Limit)
	cancel()
	if err != nil {
		code := protocol.FaceErrorLogUnavailable
		if errors.Is(err, remoteexec.ErrHandOffline) {
			code = protocol.FaceErrorHandOffline
		}
		g.sendCommandFailure(state, meta, err, code, "task log is unavailable")
		return
	}
	data := log.Data
	if data == nil {
		data = []byte{}
	}
	g.sendResult(state, meta, protocol.FaceOperationTaskLog, protocol.TaskLogResult{
		TaskID: log.TaskID, Offset: log.Offset, NextOffset: log.NextOffset,
		Data: data, EOF: log.EOF, Truncated: log.Truncated,
	})
}

func (g *Gateway) requireScope(state *connection, identity protocol.FaceIdentity, meta protocol.FaceCommandMeta, scope protocol.FaceScope) bool {
	if hasScope(identity, scope) {
		return true
	}
	g.sendError(state, meta, protocol.FaceErrorForbidden, "Face scope is required", false)
	return false
}

func (g *Gateway) requireConversation(state *connection, meta protocol.FaceCommandMeta) bool {
	session, err := g.store.GetSession(meta.ConversationID)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "conversation lookup failed", true)
		return false
	}
	if session == nil || session.GroupID != g.conversations.GroupID() {
		g.sendError(state, meta, protocol.FaceErrorConversationNotFound, "conversation was not found", false)
		return false
	}
	return true
}

func (g *Gateway) requireTask(state *connection, meta protocol.FaceCommandMeta, taskID string) bool {
	task, err := g.store.GetRemoteTask(taskID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		g.sendError(state, meta, protocol.FaceErrorInternal, "task lookup failed", true)
		return false
	}
	if err != nil || task.SessionID != meta.ConversationID {
		g.sendError(state, meta, protocol.FaceErrorTaskNotFound, "task was not found", false)
		return false
	}
	return true
}

func (g *Gateway) sendCommandFailure(state *connection, meta protocol.FaceCommandMeta, err error, code protocol.FaceErrorCode, message string) {
	status := protocol.FaceResultFailed
	if errors.Is(err, context.Canceled) {
		status, code = protocol.FaceResultCancelled, protocol.FaceErrorCancelled
	} else if errors.Is(err, context.DeadlineExceeded) {
		status, code = protocol.FaceResultTimedOut, protocol.FaceErrorTimeout
	}
	g.sendFailedResult(state, meta, status, code, message)
}

func (g *Gateway) validateSubscription(identity protocol.FaceIdentity, request protocol.FaceSubscribe) (subscription, protocol.FaceErrorCode, string) {
	filter := subscription{
		conversations: make(map[string]struct{}, len(request.ConversationIDs)),
		events:        make(map[protocol.FaceEventType]struct{}, len(request.EventTypes)),
	}
	for _, conversationID := range request.ConversationIDs {
		session, err := g.store.GetSession(conversationID)
		if err != nil {
			return subscription{}, protocol.FaceErrorInternal, "conversation lookup failed"
		}
		if session == nil || session.GroupID != g.conversations.GroupID() {
			return subscription{}, protocol.FaceErrorConversationNotFound, "conversation was not found"
		}
		filter.conversations[conversationID] = struct{}{}
	}
	for _, eventType := range request.EventTypes {
		scope := eventScope(eventType)
		if scope != "" && !hasScope(identity, scope) {
			return subscription{}, protocol.FaceErrorForbidden, "event scope is required"
		}
		filter.events[eventType] = struct{}{}
	}
	return filter, "", ""
}

func eventScope(eventType protocol.FaceEventType) protocol.FaceScope {
	switch eventType {
	case protocol.FaceEventChatStarted, protocol.FaceEventChatToolCalled, protocol.FaceEventChatToolCompleted,
		protocol.FaceEventChatCompleted, protocol.FaceEventChatFailed, protocol.FaceEventChatCancelled:
		return protocol.FaceScopeChat
	case protocol.FaceEventApprovalRequested, protocol.FaceEventApprovalResolved:
		return protocol.FaceScopeApprove
	case protocol.FaceEventRemoteRunChanged:
		return protocol.FaceScopeRunsRead
	case protocol.FaceEventHandConnected, protocol.FaceEventHandDisconnected:
		return protocol.FaceScopeHandsRead
	case protocol.FaceEventConversationChanged:
		return protocol.FaceScopeSessionsRead
	case protocol.FaceEventTaskChanged:
		return protocol.FaceScopeTasksRead
	default:
		return ""
	}
}

func commandScope(messageType string) protocol.FaceScope {
	switch messageType {
	case protocol.TypeFaceChat, protocol.TypeFaceChatCancel:
		return protocol.FaceScopeChat
	case protocol.TypeFaceConversationList, protocol.TypeFaceConversationSnapshot:
		return protocol.FaceScopeSessionsRead
	case protocol.TypeFaceConversationCreate, protocol.TypeFaceConversationRename:
		return protocol.FaceScopeSessionsWrite
	case protocol.TypeFaceApprovalResolve:
		return protocol.FaceScopeApprove
	case protocol.TypeFaceRunGet:
		return protocol.FaceScopeRunsRead
	case protocol.TypeFaceRunCancel:
		return protocol.FaceScopeRunsCancel
	case protocol.TypeFaceHandList, protocol.TypeFaceHandGet:
		return protocol.FaceScopeHandsRead
	case protocol.TypeFaceTaskList, protocol.TypeFaceTaskGet, protocol.TypeFaceTaskLog:
		return protocol.FaceScopeTasksRead
	case protocol.TypeFaceTaskCancel:
		return protocol.FaceScopeTasksCancel
	default:
		return ""
	}
}

func projectStaticHand(peer *hub.Peer) protocol.HandSummary {
	hand := protocol.HandSummary{HandID: peer.ID, Connected: true, Tools: []protocol.ToolInfo{}}
	if peer.Info != nil {
		hand.Hostname, hand.OS, hand.Arch = peer.Info.Hostname, peer.Info.OS, peer.Info.Arch
	}
	return hand
}
