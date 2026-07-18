package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
)

// approver 将终端交互裁决提交给统一 Approval Broker。
type approver struct {
	input *inputReader
}

type inputLine struct {
	text string
	ok   bool
}

type inputReader struct {
	lines <-chan inputLine
}

func newInputReader(scanner *bufio.Scanner) *inputReader {
	lines := make(chan inputLine)
	go func() {
		for scanner.Scan() {
			lines <- inputLine{text: scanner.Text(), ok: true}
		}
		lines <- inputLine{}
		close(lines)
	}()
	return &inputReader{lines: lines}
}

func (r *inputReader) read(ctx context.Context) (string, bool) {
	select {
	case line, open := <-r.lines:
		return line.text, open && line.ok
	case <-ctx.Done():
		return "", false
	}
}

func (a *approver) Resolve(ctx context.Context, request protocol.ApprovalRequest) (approval.Actor, protocol.FaceApprovalDecision, string, bool) {
	if err := ctx.Err(); err != nil {
		return approval.Actor{}, "", "", false
	}
	fmt.Fprintf(os.Stderr, "\n⚠️  Confirm [%s] %s\n", request.Tool, request.Reason)
	fmt.Fprint(os.Stderr, "  [y] once  [n] deny  [Y] always allow  [N] always deny: ")

	line, ok := a.input.read(ctx)
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
