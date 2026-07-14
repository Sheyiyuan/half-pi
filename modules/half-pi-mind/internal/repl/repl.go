// Package repl 提供 REPL 交互终端。
package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
)

// Repl 是 REPL 交互终端。
type Repl struct {
	core    *agentcore.Core
	bus     *events.EventBus
	scanner *bufio.Scanner
}

// Run 启动 REPL 主循环，阻塞直到用户输入 exit 或 EOF。
func Run(core *agentcore.Core, bus *events.EventBus) {
	r := &Repl{
		core:    core,
		bus:     bus,
		scanner: bufio.NewScanner(os.Stdin),
	}
	core.SetApprover(&approver{scanner: r.scanner})

	fmt.Println("half-pi mind ready")
	fmt.Println("/mode <normal|trust|yolo>  switch mode")
	fmt.Println("/debug                 toggle debug")
	fmt.Println("exit / quit            exit")
	fmt.Println()

	for {
		fmt.Print("> ")
		if !r.scanner.Scan() {
			break
		}
		input := strings.TrimSpace(r.scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("bye")
			break
		}
		if r.handleCommand(input) {
			continue
		}

		response, err := r.core.Chat(context.Background(), input)
		if err != nil {
			r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("error: %v", err))
			continue
		}
		fmt.Println(response)
		fmt.Println()
	}
}

func (r *Repl) handleCommand(input string) bool {
	switch {
	case input == "/debug":
		r.core.Debug = !r.core.Debug
		r.emit(events.LevelInfo, events.TypeSystem, fmt.Sprintf("debug mode: %v", r.core.Debug))
		return true

	case input == "/mode":
		r.emit(events.LevelInfo, events.TypeSystem, fmt.Sprintf("current mode: %s", r.core.Mode))
		return true

	case strings.HasPrefix(input, "/mode "):
		mode := strings.TrimSpace(strings.TrimPrefix(input, "/mode "))
		switch mode {
		case "strict", "normal", "trust", "yolo":
			r.core.SetMode(mode)
			r.emit(events.LevelInfo, events.TypeModeChange, fmt.Sprintf("mode switched to: %s", mode))
		default:
			r.emit(events.LevelWarn, events.TypeSystem, fmt.Sprintf("unknown mode: %s (strict/normal/trust/yolo)", mode))
		}
		return true
	}
	return false
}

func (r *Repl) emit(level, typ, msg string) {
	r.bus.PublishSync(events.New("", "repl", level, typ, msg))
}
