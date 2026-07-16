//go:build windows

package hand

import (
	"context"
	"os/exec"
)

func monitorShellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/c", command)
}
