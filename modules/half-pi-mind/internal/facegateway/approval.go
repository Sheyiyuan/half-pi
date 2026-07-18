package facegateway

import (
	"errors"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

func (g *Gateway) handleApprovalResolve(state *connection, identity protocol.FaceIdentity, env protocol.Envelope) {
	request, _ := protocol.DecodePayload[protocol.FaceApprovalResolve](&env)
	meta := protocol.FaceCommandMeta{RequestID: request.RequestID}
	if !g.requireScope(state, identity, meta, protocol.FaceScopeApprove) {
		return
	}
	actor := approval.Actor{ID: identity.ID, Label: identity.Label, Source: "face"}
	resolution, err := g.approvals.Resolve(request.ApprovalID, actor, request.Decision, request.Reason,
		approval.ResolveHooks{
			Validate: func(pending protocol.ApprovalRequest) bool {
				session, lookupErr := g.store.GetSession(pending.ConversationID)
				return lookupErr == nil && session != nil && session.GroupID == g.conversations.GroupID()
			},
			Accepted: func(pending protocol.ApprovalRequest) bool {
				meta.ConversationID = pending.ConversationID
				return g.sendAccepted(state, meta, protocol.FaceOperationApprovalResolve, g.version.Load())
			},
		})
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrNotAccepted):
			return
		case errors.Is(err, approval.ErrNotFound), errors.Is(err, approval.ErrNotOwned):
			g.sendError(state, meta, protocol.FaceErrorApprovalNotFound, "approval was not found", false)
		case errors.Is(err, approval.ErrExpired):
			g.sendError(state, meta, protocol.FaceErrorApprovalExpired, "approval has expired", false)
		case errors.Is(err, approval.ErrConflict):
			g.sendError(state, meta, protocol.FaceErrorRequestConflict, "approval was already resolved", false)
		default:
			g.sendError(state, meta, protocol.FaceErrorInternal, "approval resolution failed", true)
		}
		return
	}
	if resolution.Decision == "" {
		g.sendFailedResult(state, meta, protocol.FaceResultFailed, protocol.FaceErrorInternal, "approval resolution failed")
		return
	}
	g.sendSuccessResult(state, meta, "Approval resolved")
}
