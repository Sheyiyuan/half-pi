package facegateway

import (
	"encoding/json"
	"errors"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/requestctx"
)

func (g *Gateway) handleConversationCompact(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceConversationCompact](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeSessionsWrite) || !g.requireConversation(state, meta) ||
		!g.requireFeature(state, meta, protocol.FaceFeatureContextCompaction) {
		return
	}
	actor, err := g.conversations.Get(request.ConversationID)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "conversation runtime is unavailable", true)
		return
	}
	status, err := actor.CompactStatus(state.ctx)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "Compact status is unavailable", true)
		return
	}
	if !status.Enabled {
		g.sendError(state, meta, protocol.FaceErrorCompactUnavailable, "Context compaction is unavailable", false)
		return
	}
	if request.Target.Mode == protocol.FaceCompactTargetRatio &&
		(status.InputBudget <= 0 || *request.Target.Ratio*float64(status.InputBudget) >= float64(status.HighLimit)) {
		g.sendError(state, meta, protocol.FaceErrorInvalidRequest, "Compact ratio must be below the high watermark", false)
		return
	}
	digest, err := faceCommandDigest(protocol.FaceOperationConversationCompact, request)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "Compact request registration failed", true)
		return
	}
	admission := g.chats.beginCompact(identity, request, digest, state, actor)
	if g.sendRequestAdmission(state, meta, admission) {
		return
	}
	if !g.sendPayload(state, protocol.TypeFaceAccepted, admission.record.accepted) {
		g.chats.abortCompact(admission.record)
		return
	}
	go g.runCompact(admission.record, actor, identity.ID, projectCompactTarget(request.Target))
}

func (g *Gateway) runCompact(record *requestRecord, actor *conversation.Actor, principal string, target compact.Target) {
	ctx := requestctx.WithRequestID(record.ctx, record.key.requestID)
	ctx = requestctx.WithPrincipalID(ctx, principal)
	ctx = requestctx.WithSource(ctx, "face")
	result, err := actor.CompactWithLease(ctx, compact.CompactRequest{
		RequestID: record.key.requestID, Principal: principal, Target: target,
	}, record.lease)
	protocolResult := projectCompactTerminal(record, result, err)
	origin, completed := g.chats.complete(record, protocolResult)
	if completed && origin != nil {
		g.sendProtocolResult(origin, protocolResult)
	}
}

func (g *Gateway) handleCompactStatus(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceCompactStatusGet](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeSessionsRead) || !g.requireConversation(state, meta) ||
		!g.requireFeature(state, meta, protocol.FaceFeatureContextCompaction) {
		return
	}
	actor, err := g.conversations.Get(request.ConversationID)
	if err != nil {
		g.sendError(state, meta, protocol.FaceErrorInternal, "conversation runtime is unavailable", true)
		return
	}
	if !g.sendAccepted(state, meta, protocol.FaceOperationCompactStatus, 0) {
		return
	}
	status, err := actor.CompactStatus(state.ctx)
	if err != nil {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "Compact status failed")
		return
	}
	g.sendResult(state, meta, protocol.FaceOperationCompactStatus, projectCompactStatus(status))
}

func projectCompactTarget(target protocol.FaceCompactTarget) compact.Target {
	switch target.Mode {
	case protocol.FaceCompactTargetRatio:
		return compact.RatioTarget{Ratio: *target.Ratio}
	case protocol.FaceCompactTargetKeep:
		return compact.KeepTarget{Messages: *target.KeepMessages}
	case protocol.FaceCompactTargetRebase:
		return compact.RebaseTarget{}
	default:
		return compact.DefaultTarget{}
	}
}

func projectCompactTerminal(record *requestRecord, result compact.CompactResult, err error) protocol.FaceResult {
	terminal := protocol.FaceResult{
		RequestID: record.key.requestID, ConversationID: record.conversationID, Status: protocol.FaceResultSucceeded,
	}
	if err != nil {
		code, message, ok := compactFaceError(err)
		if !ok {
			code, message = protocol.FaceErrorInternal, "Compact failed"
		}
		terminal.Status, terminal.ErrorCode, terminal.Error = compactResultStatus(code), code, message
		return terminal
	}
	data, projectionErr := projectCompactResult(result)
	if projectionErr != nil {
		terminal.Status, terminal.ErrorCode, terminal.Error = protocol.FaceResultFailed, protocol.FaceErrorInternal, "Compact result projection failed"
		return terminal
	}
	terminal.Data, projectionErr = json.Marshal(data)
	if projectionErr != nil || protocol.ValidateFaceResultData(protocol.FaceOperationConversationCompact, terminal.Data) != nil {
		terminal.Data = nil
		terminal.Status, terminal.ErrorCode, terminal.Error = protocol.FaceResultFailed, protocol.FaceErrorInternal, "Compact result projection failed"
	}
	return terminal
}

func projectCompactResult(result compact.CompactResult) (protocol.FaceCompactResult, error) {
	projected := protocol.FaceCompactResult{
		SummaryID: result.SummaryID, FromSeq: result.FromSeq, ToSeq: result.ToSeq,
		BeforeEstimatedTokens: result.BeforeEstimatedTokens, AfterEstimatedTokens: result.AfterEstimatedTokens,
		RetainedFromSeq: result.RetainedFromSeq, RetainedToSeq: result.RetainedToSeq,
		GenerationMode: protocol.FaceCompactGenerationMode(result.GenerationMode), ContextVersion: result.ContextVersion, Reused: result.Reused,
	}
	switch target := result.TargetResult.(type) {
	case compact.TokenTargetResult:
		projected.TargetTokens, projected.TargetMet = int64Pointer(target.TargetTokens), boolPointer(target.TargetMet)
	case compact.KeepTargetResult:
		projected.RequestedKeepMessages = intPointer(target.RequestedKeepMessages)
		projected.RetainedMessageCount = intPointer(target.RetainedMessageCount)
		projected.SafetyRetainedExtra = intPointer(target.SafetyRetainedExtra)
		projected.CapacityRetainedExtra = intPointer(target.CapacityRetainedExtra)
	default:
		return protocol.FaceCompactResult{}, errors.New("unknown Compact target result")
	}
	return projected, nil
}

func projectCompactStatus(status compact.CompactStatus) protocol.FaceCompactStatus {
	warnings := append([]string(nil), status.Warnings...)
	if warnings == nil {
		warnings = []string{}
	}
	pendingNotBefore := int64(0)
	if !status.PendingNotBefore.IsZero() {
		pendingNotBefore = status.PendingNotBefore.UnixMilli()
	}
	return protocol.FaceCompactStatus{
		Enabled: status.Enabled, Automatic: status.Automatic, OperationState: status.OperationState,
		SummaryID: status.ActiveSummaryID, CoveredFromSeq: status.ActiveFromSeq, CoveredToSeq: status.ActiveToSeq,
		LastSeq: status.LastSeq, MessageCount: status.MessageCount, ContextMessageCount: status.ContextMessageCount,
		SummaryNodeCount: status.SummaryNodeCount, SummaryStorageBytes: status.SummaryStorageBytes,
		SummaryBytes: status.ActiveSummaryBytes, SourceEstimatedTokens: status.SourceEstimatedTokens,
		SummaryEstimatedTokens: status.SummaryEstimatedTokens, CompressionRatio: status.CompressionRatio,
		GenerationMode:              protocol.FaceCompactGenerationMode(status.ActiveGenerationMode),
		CandidateGenerationMode:     protocol.FaceCompactGenerationMode(status.CandidateGenerationMode),
		ConfiguredSummaryProviderID: status.ConfiguredSummaryProviderID, ConfiguredSummaryModelID: status.ConfiguredSummaryModelID,
		ActiveSummaryProviderID: status.ActiveProviderID, ActiveSummaryModelID: status.ActiveModelID,
		EstimatedTokens: status.CurrentEstimatedTokens, InputBudget: status.InputBudget,
		ReservedOutputTokens: status.ReservedOutputTokens, HighLimit: status.HighLimit, LowTarget: status.LowTarget,
		CompressibleFromSeq: status.CompressibleFromSeq, CompressibleToSeq: status.CompressibleToSeq,
		RetainedFromSeq: status.RetainedFromSeq, RetainedToSeq: status.RetainedToSeq,
		Pending: status.Pending, PendingAttempt: status.PendingAttempt, PendingNotBefore: pendingNotBefore,
		SummaryInputBudget:                  status.SummaryInputBudget,
		RequiredSummaryInputEstimatedTokens: status.RequiredSummaryInputEstimatedTokens,
		ContextVersion:                      status.ContextVersion, ProjectionVersion: status.ProjectionVersion,
		PolicyVersion: status.PolicyVersion, Profile: status.Profile,
		ActiveProjectionVersion: status.ActiveProjectionVersion, ActivePolicyVersion: status.ActivePolicyVersion,
		ActiveProfile: status.ActiveProfile, CompactDegraded: status.Degraded,
		Blocker: protocol.FaceErrorCode(status.Blocker), Warnings: warnings,
	}
}

func compactFaceError(err error) (protocol.FaceErrorCode, string, bool) {
	code := compact.ErrorCodeOf(err)
	if code == "" {
		return "", "", false
	}
	faceCode := protocol.FaceErrorCode(code)
	messages := map[compact.ErrorCode]string{
		compact.ErrUnavailable: "Context compaction is unavailable", compact.ErrNothingToCompact: "There is nothing to compact",
		compact.ErrTargetUnreachable: "The Compact target cannot be reached", compact.ErrIncrementTooLarge: "The Compact increment is too large",
		compact.ErrRebaseTooLarge: "The Compact rebase input is too large", compact.ErrTimeout: "Compact timed out",
		compact.ErrRateLimited: "The Compact provider is rate limited", compact.ErrProvider: "The Compact provider failed",
		compact.ErrInvalidResponse: "The Compact provider returned an invalid summary", compact.ErrConflict: "Conversation context changed during Compact",
		compact.ErrContextLimit: "The model context limit was reached", compact.ErrRepairRequired: "Context compaction repair is required",
		compact.ErrIntegrity: "The active context summary failed integrity validation", compact.ErrUnsupportedVersion: "The active context summary version is unsupported",
		compact.ErrInternal: "Compact failed",
	}
	message, ok := messages[code]
	return faceCode, message, ok
}

func compactResultStatus(code protocol.FaceErrorCode) protocol.FaceResultStatus {
	if code == protocol.FaceErrorCompactTimeout {
		return protocol.FaceResultTimedOut
	}
	return protocol.FaceResultFailed
}

func boolPointer(value bool) *bool { return &value }
