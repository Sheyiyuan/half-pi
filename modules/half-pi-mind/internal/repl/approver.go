package repl

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
)

// approver 实现 agentcore.Approver，在终端交互确认。
type approver struct {
	scanner *bufio.Scanner
}

func (a *approver) Confirm(toolName, reason string) agentcore.ConfirmResult {
	fmt.Fprintf(os.Stderr, "\n⚠️  Confirm [%s] %s\n", toolName, reason)
	fmt.Fprint(os.Stderr, "  [y] once  [n] deny  [Y] always allow  [N] always deny: ")

	if !a.scanner.Scan() {
		return agentcore.ConfirmDeny
	}
	switch strings.TrimSpace(a.scanner.Text()) {
	case "y":
		return agentcore.ConfirmAllow
	case "Y":
		return agentcore.ConfirmAllowAlways
	case "N":
		return agentcore.ConfirmDenyAlways
	default:
		return agentcore.ConfirmDeny
	}
}
