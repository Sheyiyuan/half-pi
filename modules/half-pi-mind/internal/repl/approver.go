package repl

import (
	"context"
	"fmt"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

// approver 将终端交互裁决提交给统一 Approval Broker。
type approver struct {
	input *InputReader
}

func (a *approver) Resolve(ctx context.Context, request protocol.ApprovalRequest) (approval.Actor, protocol.FaceApprovalDecision, string, bool) {
	if err := ctx.Err(); err != nil {
		return approval.Actor{}, "", "", false
	}
	fmt.Fprintf(a.input.Stderr(), "\n⚠️  Confirm [%s] %s\n", request.Tool, request.Reason)

	line, ok := a.input.ReadLine(ctx, "  [y] once  [n] deny  [Y] always allow  [N] always deny: ", false)
	if !ok {
		if ctx.Err() != nil {
			return approval.Actor{}, "", "", false
		}
		return approval.Actor{ID: "repl", Label: "REPL", Source: "repl"}, protocol.FaceApprovalDenyOnce, "input closed", true
	}
	actor := approval.Actor{ID: "repl", Label: "REPL", Source: "repl"}
	switch strings.TrimSpace(line) {
	case "y":
		return actor, protocol.FaceApprovalAllowOnce, "approved in REPL", true
	case "Y":
		return actor, protocol.FaceApprovalAllowSession, "approved for conversation in REPL", true
	case "N":
		return actor, protocol.FaceApprovalDenySession, "denied for conversation in REPL", true
	default:
		return actor, protocol.FaceApprovalDenyOnce, "denied in REPL", true
	}
}
