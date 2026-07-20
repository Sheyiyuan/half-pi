package facegateway

import (
	"context"
	"errors"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
)

func (g *Gateway) handleChat(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceChat](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeChat) || !g.requireConversation(state, meta) {
		return
	}
	actor, err := g.conversations.Get(request.ConversationID)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "conversation runtime is unavailable", true)
		return
	}
	digest, err := faceCommandDigest(protocol.FaceOperationChat, request)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "Chat request registration failed", true)
		return
	}
	admission := g.chats.beginChat(identity, request, digest, state)
	if g.sendRequestAdmission(state, meta, admission) {
		return
	}
	if !g.sendPayload(state, protocol.TypeFaceAccepted, admission.record.accepted) {
		g.chats.abortChat(admission.record)
		return
	}
	g.publishChatEvent(request.ConversationID, request.RequestID, protocol.FaceEventChatStarted,
		"Chat started", protocol.FaceEventLevelInfo, protocol.ChatStartedEventData{RequestID: request.RequestID})
	go g.runChat(admission.record, actor, request.Content)
}

func (g *Gateway) runChat(record *requestRecord, actor *conversation.Actor, content string) {
	requestID := record.key.requestID
	stream := newChatStreamWriter(g, record)
	ctx := requestctx.WithRequestID(record.ctx, requestID)
	ctx = agentcore.WithChatHooks(ctx, agentcore.ChatHooks{
		RequestID:         requestID,
		TextDelta:         stream.Write,
		ResponseCompleted: stream.Complete,
		ToolCalled: func(call agentcore.ChatToolCall) {
			g.publishChatEvent(record.conversationID, requestID, protocol.FaceEventChatToolCalled,
				"Chat called a tool", protocol.FaceEventLevelInfo, protocol.ChatToolCalledEventData{
					RequestID: requestID, Tool: call.Tool, ArgsDigest: call.ArgsDigest,
				})
		},
		ToolCompleted: func(result agentcore.ChatToolResult) {
			g.publishChatEvent(record.conversationID, requestID, protocol.FaceEventChatToolCompleted,
				"Chat tool completed", protocol.FaceEventLevelInfo, protocol.ChatToolCompletedEventData{
					RequestID: requestID, Tool: result.Tool, Success: result.Success,
				})
		},
	})
	reply, err := actor.Core().Chat(ctx, content)
	if closeErr := stream.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	result := chatResult(record, reply, err)
	result, origin, completed := g.chats.completeChat(record, result, func(finalResult protocol.FaceResult) {
		g.publishChatStreamEnd(streamEndLocked(record, finalResult))
		eventType, message, level, data := chatTerminalEvent(finalResult)
		g.publishChatEvent(record.conversationID, requestID, eventType, message, level, data)
	})
	if !completed {
		return
	}
	if origin != nil {
		g.sendProtocolResult(origin, result)
	}
}

func chatResult(record *requestRecord, reply string, err error) protocol.FaceResult {
	requestID := record.key.requestID
	result := protocol.FaceResult{
		RequestID: requestID, ConversationID: record.conversationID,
		Status: protocol.FaceResultSucceeded, Content: reply,
	}
	if err == nil {
		return result
	}
	if errors.Is(err, context.Canceled) {
		result.Status, result.ErrorCode, result.Error = protocol.FaceResultCancelled, protocol.FaceErrorCancelled, "Chat was cancelled"
		result.Content = ""
		return result
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.Status, result.ErrorCode, result.Error = protocol.FaceResultTimedOut, protocol.FaceErrorTimeout, "Chat timed out"
		result.Content = ""
		return result
	}
	result.Status, result.ErrorCode, result.Error = protocol.FaceResultFailed, protocol.FaceErrorInternal, "Chat failed"
	result.Content = ""
	return result
}

func chatTerminalEvent(result protocol.FaceResult) (protocol.FaceEventType, string, protocol.FaceEventLevel, any) {
	switch result.Status {
	case protocol.FaceResultSucceeded:
		return protocol.FaceEventChatCompleted, "Chat completed", protocol.FaceEventLevelInfo,
			protocol.ChatCompletedEventData{RequestID: result.RequestID}
	case protocol.FaceResultCancelled:
		return protocol.FaceEventChatCancelled, "Chat cancelled", protocol.FaceEventLevelWarn,
			protocol.ChatCancelledEventData{RequestID: result.RequestID, Reason: "cancelled by Face"}
	case protocol.FaceResultTimedOut:
		return protocol.FaceEventChatFailed, "Chat timed out", protocol.FaceEventLevelError,
			protocol.ChatFailedEventData{RequestID: result.RequestID, Code: protocol.FaceErrorTimeout}
	default:
		return protocol.FaceEventChatFailed, "Chat failed", protocol.FaceEventLevelError,
			protocol.ChatFailedEventData{RequestID: result.RequestID, Code: protocol.FaceErrorInternal}
	}
}

func (g *Gateway) handleChatCancel(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceChatCancel](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeChat) || !g.requireConversation(state, meta) {
		return
	}
	digest, err := faceCommandDigest(protocol.FaceOperationChatCancel, request)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "Chat cancellation registration failed", true)
		return
	}
	admission := g.chats.beginCancel(identity, request, digest, state)
	if g.sendRequestAdmission(state, meta, admission.requestAdmission) {
		return
	}
	if !g.sendPayload(state, protocol.TypeFaceAccepted, admission.record.accepted) {
		g.chats.abortCancel(admission.record, admission.target)
		return
	}
	content := "Chat already completed"
	if !admission.alreadyTerminal {
		g.chats.cancelTarget(admission.target)
		content = "Chat cancellation requested"
	}
	result := protocol.FaceResult{
		RequestID: request.RequestID, ConversationID: request.ConversationID,
		Status: protocol.FaceResultSucceeded, Content: content,
	}
	origin, completed := g.chats.complete(admission.record, result)
	if completed && origin != nil {
		g.sendProtocolResult(origin, result)
	}
}

func (g *Gateway) sendRequestAdmission(state *connection, meta protocol.FaceCommandMeta, admission requestAdmission) bool {
	if admission.code != "" {
		g.sendError(state, meta, admission.code, admission.message, admission.retryable)
		return true
	}
	if admission.result != nil {
		g.sendProtocolResult(state, *admission.result)
		return true
	}
	if admission.accepted != nil {
		g.sendPayload(state, protocol.TypeFaceAccepted, *admission.accepted)
		return true
	}
	return false
}

func (g *Gateway) sendProtocolResult(state *connection, result protocol.FaceResult) bool {
	return g.sendPayload(state, protocol.TypeFaceResult, result)
}

func (g *Gateway) publishChatEvent(conversationID, requestID string, typ protocol.FaceEventType, message string, level protocol.FaceEventLevel, data any) {
	g.publish(domainEvent{
		conversationID: conversationID, requestID: requestID, typ: typ,
		source: "chat", message: message, level: level, data: data,
	})
}
