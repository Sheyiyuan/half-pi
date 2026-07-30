package main

import (
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/repl"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// runREPL 创建初始 conversation 并进入交互式 REPL。
func runREPL(conversations *conversation.Manager, approvals *approval.Broker, bus *events.EventBus, db *store.Store, serverEnabled bool, wsHub *hub.Hub, managementService *management.Service) error {
	input, err := repl.NewInput(os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return fmt.Errorf("initialize REPL input: %w", err)
	}
	defer input.Close()
	bus.Subscribe(events.NewConsoleWriterWithOutput(input.Stderr()))

	actor, err := conversations.Create("")
	if err != nil {
		return fmt.Errorf("initialize REPL conversation: %w", err)
	}
	switchActor := func(id string) (*conversation.Actor, error) {
		return conversations.Get(id)
	}
	repl.Run(actor, switchActor, conversations.Create, approvals, bus, db, conversations.GroupID(), serverEnabled, wsHub, managementService, input)
	return nil
}
