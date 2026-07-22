package hand

import (
	"context"
	"errors"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

type handAuthorizer struct {
	hand      *Hand
	rpc       protocol.RPC
	now       time.Time
	rejection protocol.RejectCode
}

func (a *handAuthorizer) Authorize(_ context.Context, frozen executor.FrozenInvocation) executor.Authorization {
	if code := a.hand.toolPermissionRejection(frozen.Tool); code != "" {
		a.rejection = code
		return handDeny("tool is not allowed by Hand policy", string(code))
	}
	_, decision, reason, found := executor.CheckTool(frozen.Tool, frozen.Args)
	if !found {
		a.rejection = protocol.RejectUnknownTool
		return handDeny(reason, string(a.rejection))
	}
	if decision == executor.DecisionDeny {
		a.rejection = protocol.RejectCheckFailed
		return handDeny(reason, string(a.rejection))
	}
	if a.rpc.Background != nil {
		if err := protocol.ValidateRPCApproval(a.rpc.Approval, a.rpc, a.hand.nodeID(), a.now); err != nil {
			a.rejection = approvalRejectCode(err)
			return handDeny(err.Error(), string(a.rejection))
		}
	} else if decision == executor.DecisionConfirm {
		if err := protocol.ValidateApproval(a.rpc.Approval, a.rpc.RunID, a.hand.nodeID(), a.rpc.Tool, a.rpc.Args, a.now); err != nil {
			a.rejection = approvalRejectCode(err)
			return handDeny(err.Error(), string(a.rejection))
		}
	}
	return executor.Authorization{Allowed: true, Decision: "allow", ReasonCode: "hand_policy_allow"}
}

func (h *Hand) nodeID() string {
	if h.cfg != nil && h.cfg.Hand.ID != "" {
		return h.cfg.Hand.ID
	}
	if h.conn != nil {
		return h.conn.Session.LocalID
	}
	return "hand"
}

func handDeny(reason, code string) executor.Authorization {
	return executor.Authorization{Allowed: false, Reason: reason, ReasonCode: code, Decision: "deny"}
}

func approvalRejectCode(err error) protocol.RejectCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, protocol.ErrApprovalExpired):
		return protocol.RejectApprovalExpired
	case errors.Is(err, protocol.ErrApprovalDigestMismatch):
		return protocol.RejectApprovalDigestMismatch
	default:
		return protocol.RejectApprovalRequired
	}
}
