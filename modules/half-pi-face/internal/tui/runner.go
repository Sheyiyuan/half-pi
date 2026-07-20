// Package tui 实现复用正式 Face 协议的全屏人类终端客户端。
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-face/internal/client"
)

// Run 在真实终端中启动全屏 Face 工作台。
func Run(ctx context.Context, connector client.Connector, input, output *os.File) error {
	if connector == nil || input == nil || output == nil {
		return fmt.Errorf("TUI connector and terminal files are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	model := NewModel(connector)
	model.ctx = ctx
	defer func() {
		if model.conn != nil {
			_ = model.conn.Close()
		}
	}()
	program := tea.NewProgram(model,
		tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output),
		tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus(), tea.WithFPS(30),
	)
	_, err := program.Run()
	if err != nil && !errorsIsContext(err, ctx) {
		return fmt.Errorf("run full-screen Face: %w", err)
	}
	return nil
}

func errorsIsContext(err error, ctx context.Context) bool {
	return ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}
