// Package repl 实现 Mind 的交互式命令行界面。
package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type Repl struct {
	core    *agentcore.Core
	bus     *events.EventBus
	store   *store.Store
	groupID string
	scanner *bufio.Scanner
}

// Run 启动交互式 REPL 循环。
func Run(core *agentcore.Core, bus *events.EventBus, s *store.Store, groupID string, serverEnabled bool) {
	r := &Repl{
		core:    core,
		bus:     bus,
		store:   s,
		groupID: groupID,
		scanner: bufio.NewScanner(os.Stdin),
	}
	core.SetApprover(&approver{scanner: r.scanner})

	r.printBanner(serverEnabled)
	for r.loop() {
	}
}

func (r *Repl) printBanner(serverEnabled bool) {
	fmt.Println("half-pi mind ready")
	fmt.Printf("group: %s\n", r.groupID)
	if serverEnabled {
		fmt.Println("hub:   ws://127.0.0.1:15707/ws")
	}
	fmt.Println("/session              list sessions")
	fmt.Println("/session <prefix>     switch session")
	fmt.Println("/session name <name>  rename session")
	fmt.Println("/hand                 list hand tokens")
	fmt.Println("/hand add <label>     create hand token")
	fmt.Println("/hand remove <id>     revoke hand token")
	fmt.Println("/peers                list connected peers")
	fmt.Println("/mode <normal|trust|yolo>  switch mode")
	fmt.Println("/debug                toggle debug")
	fmt.Println("exit / quit           exit")
	fmt.Println()
}

func (r *Repl) loop() bool {
	fmt.Print("> ")
	if !r.scanner.Scan() {
		return false
	}
	input := strings.TrimSpace(r.scanner.Text())
	if input == "" {
		return true
	}
	if input == "exit" || input == "quit" {
		if err := r.core.SaveSession(); err != nil {
			r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("save session: %v", err))
		}
		fmt.Println("bye")
		return false
	}
	if r.handleCommand(input) {
		return true
	}

	response, err := r.core.Chat(context.Background(), input)
	if err != nil {
		r.emit(events.LevelError, events.TypeSystem, fmt.Sprintf("error: %v", err))
		return true
	}
	fmt.Println(response)
	fmt.Println()
	return true
}
