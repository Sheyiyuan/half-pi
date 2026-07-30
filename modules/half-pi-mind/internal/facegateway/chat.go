package facegateway

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	coreexec "github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
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
	admission := g.chats.beginChat(identity, request, digest, state, actor,
		g.connectionHasFeature(state, protocol.FaceFeatureContextCompaction))
	if admission.record != nil {
		admission.record.detailMode = g.connectionDetailMode(state)
	}
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
	ctx = requestctx.WithPrincipalID(ctx, record.key.identityID)
	ctx = requestctx.WithSource(ctx, "face")
	ctx = requestctx.WithToolDetailMode(ctx, string(record.detailMode))
	transport := agentcore.ChatTransport{
		TextDelta:         stream.Write,
		ResponseCompleted: stream.Complete,
		ToolCalled: func(call agentcore.ChatToolCall) {
			ordinal := record.toolOrdinal.Add(1)
			record.currentTool.Store(ordinal)
			record.toolProgressSeq.Store(0)
			record.toolProgressLen.Store(0)
			argsView := (*protocol.ToolArgsView)(nil)
			projectionVersion := ""
			warnings := []string(nil)
			argsBytes := len(call.Args)
			argsTruncated := argsBytes > protocol.MaxFaceToolArgsBytes
			tool, _ := actor.Core().Catalog().Find(call.Tool)
			if view, projectionErr := coreexec.ProjectDisplayArgs(tool, call.Args); projectionErr == nil {
				argsBytes, argsTruncated = view.Bytes, view.Truncated
				warnings = append([]string(nil), view.Warnings...)
				if record.detailMode == protocol.FaceDetailModeTransparent {
					argsView = protocolToolArgsView(view)
					projectionVersion = view.ProjectionVersion
				}
			}
			_ = g.store.CreateToolDisplayProjection(store.ToolDisplayProjection{
				ConversationID: record.conversationID, RequestID: requestID, Ordinal: ordinal,
				Tool: call.Tool, DetailMode: record.detailMode, ArgsDigest: call.ArgsDigest, Args: argsView,
				ArgsBytes: argsBytes, ArgsTruncated: argsTruncated,
				ProjectionVersion: projectionVersion, ScanWarnings: append([]string(nil), warnings...), CreatedAt: time.Now().UTC(),
			})
			g.publishChatEvent(record.conversationID, requestID, protocol.FaceEventChatToolCalled,
				"Chat called a tool", protocol.FaceEventLevelInfo, protocol.ChatToolCalledEventData{
					RequestID: requestID, Tool: call.Tool, ArgsDigest: call.ArgsDigest,
					Args: argsView, ProjectionVersion: projectionVersion, ScanWarnings: warnings,
					ArgsBytes: argsBytes, ArgsTruncated: argsTruncated,
				})
		},
		ToolProgress: func(progress agentcore.ChatToolProgress) {
			if record.detailMode != protocol.FaceDetailModeTransparent || progress.Data == "" {
				return
			}
			g.publishToolProgress(record, progress)
		},
		ToolCompleted: func(result agentcore.ChatToolResult) {
			outputView := (*protocol.ToolOutputView)(nil)
			view := coreexec.ProjectDisplayOutput(result.Stdout, result.Stderr)
			projectionVersion := ""
			warnings := view.Warnings
			if record.detailMode == protocol.FaceDetailModeTransparent {
				outputView = protocolToolOutputView(view)
				projectionVersion = coreexec.DisplayProjectionVersion
			}
			errorCategory := ""
			if !result.Success {
				errorCategory = "tool_error"
			}
			_ = g.store.CompleteToolDisplayProjection(record.conversationID, requestID, record.currentTool.Load(),
				outputView, result.Success, projectionVersion, warnings, view.OutputBytes, view.Digest, view.Truncated,
				errorCategory, time.Now().UTC())
			g.publishChatEvent(record.conversationID, requestID, protocol.FaceEventChatToolCompleted,
				"Chat tool completed", protocol.FaceEventLevelInfo, protocol.ChatToolCompletedEventData{
					RequestID: requestID, Tool: result.Tool, Success: result.Success,
					Result: outputView, ProjectionVersion: projectionVersion, ScanWarnings: warnings,
					OutputBytes: view.OutputBytes, OutputDigest: view.Digest, Truncated: view.Truncated,
					ErrorCategory: errorCategory,
				})
		},
	}
	reply, err := actor.ChatWithLease(ctx, content, transport, record.lease)
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
	if code, message, ok := compactFaceError(err); ok {
		if record.compactErrors {
			result.Status, result.ErrorCode, result.Error = compactResultStatus(code), code, message
		} else {
			result.Status, result.ErrorCode, result.Error = protocol.FaceResultFailed, protocol.FaceErrorInternal,
				"Chat could not continue within the model context budget"
		}
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

func protocolToolArgsView(view coreexec.DisplayArgs) *protocol.ToolArgsView {
	fields := make(map[string]protocol.ToolFieldView, len(view.Fields))
	for name, field := range view.Fields {
		fields[name] = protocol.ToolFieldView{
			State: protocol.ToolDisplayState(field.State), Value: field.Value, Preview: field.Preview,
			Bytes: field.Bytes, Truncated: field.Truncated,
		}
	}
	return &protocol.ToolArgsView{
		ProjectionVersion: view.ProjectionVersion, Fields: fields, Bytes: view.Bytes,
		Truncated: view.Truncated, Warnings: append([]string(nil), view.Warnings...),
	}
}

func protocolToolOutputView(view coreexec.DisplayOutput) *protocol.ToolOutputView {
	return &protocol.ToolOutputView{
		Stdout: view.Stdout, Stderr: view.Stderr, StdoutBytes: view.StdoutBytes, StderrBytes: view.StderrBytes,
		OutputBytes: view.OutputBytes, Digest: view.Digest, Truncated: view.Truncated,
		Warnings: append([]string(nil), view.Warnings...),
	}
}

func (g *Gateway) publishToolProgress(record *requestRecord, progress agentcore.ChatToolProgress) {
	remaining := progress.Data
	for remaining != "" {
		requested := min(len(remaining), protocol.MaxFaceChatDeltaBytes)
		allowed := reserveProgressBytes(&record.toolProgressLen, int64(requested), protocol.MaxFaceToolOutputBytes)
		if allowed == 0 {
			return
		}
		length := allowed
		for length > 0 && !utf8.ValidString(remaining[:length]) {
			length--
		}
		if length == 0 {
			return
		}
		if length < allowed {
			record.toolProgressLen.Add(int64(length - allowed))
		}
		chunk := remaining[:length]
		remaining = remaining[length:]
		seq := record.toolProgressSeq.Add(1)
		g.publishTransient(protocol.FaceTransientChatToolProgress, protocol.FaceScopeChat, record.conversationID,
			protocol.TypeFaceChatToolProgress, protocol.FaceChatToolProgress{
				ConversationID: record.conversationID, RequestID: record.key.requestID, Tool: progress.Tool,
				Seq: seq, Kind: progress.Kind, Data: chunk,
			})
	}
}

func reserveProgressBytes(used *atomic.Int64, requested int64, limit int) int {
	for {
		current := used.Load()
		if current >= int64(limit) {
			return 0
		}
		allowed := min(requested, int64(limit)-current)
		if used.CompareAndSwap(current, current+allowed) {
			return int(allowed)
		}
	}
}
