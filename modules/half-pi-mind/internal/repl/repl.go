// Package repl 实现 Mind 的交互式命令行界面。
package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type Repl struct {
	core        *agentcore.Core
	bus         *events.EventBus
	store       *store.Store
	groupID     string
	hub         *hub.Hub
	bridge      *local.RemoteBridge
	switchActor func(string) (*agentcore.Core, *local.RemoteBridge, error)
	approver    *approver
	scanner     *bufio.Scanner
}

// Run 启动交互式 REPL 循环。
func Run(core *agentcore.Core, bridge *local.RemoteBridge, switchActor func(string) (*agentcore.Core, *local.RemoteBridge, error), bus *events.EventBus, s *store.Store, groupID string, serverEnabled bool, wsHub *hub.Hub) {
	r := &Repl{
		core:        core,
		bus:         bus,
		store:       s,
		groupID:     groupID,
		hub:         wsHub,
		bridge:      bridge,
		switchActor: switchActor,
		scanner:     bufio.NewScanner(os.Stdin),
	}
	r.approver = &approver{scanner: r.scanner}
	core.SetApprover(r.approver)

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
	fmt.Println("/hand list            list Hand credentials")
	fmt.Println("/hand add <label>     create hand token")
	fmt.Println("/hand remove --id <id> | --label <label>")
	fmt.Println("/face add <label> --scopes <comma-separated-scopes>")
	fmt.Println("/face list            list Face credentials")
	fmt.Println("/face remove --id <id> | --label <label>")
	fmt.Println("/hand select <id>     select session default Hand")
	fmt.Println("/hand online          list online Hands")
	fmt.Println("/hand info <id>       show Hand tool schemas")
	fmt.Println("/hand exec <tool> <json>  start remote run")
	fmt.Println("/hand cancel <run_id> cancel remote run")
	fmt.Println("/hand run <run_id>    show remote run")
	fmt.Println("/hand task start <tool> <json> [--timeout-ms N]  start background task")
	fmt.Println("/hand task status [task_id]  show/list background tasks")
	fmt.Println("/hand task log <task_id> [offset] [limit]  read task log")
	fmt.Println("/hand task cancel <task_id>  cancel background task")
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
