package main

import (
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/repl"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// runREPL 创建初始 conversation 并进入交互式 REPL。
func runREPL(conversations *conversation.Manager, approvals *approval.Broker, bus *events.EventBus, db *store.Store, serverEnabled bool, wsHub *hub.Hub) {
	actor, err := conversations.Create("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "core init failed: %v\n", err)
		os.Exit(1)
	}
	switchActor := func(id string) (*conversation.Actor, error) {
		return conversations.Get(id)
	}
	repl.Run(actor, switchActor, approvals, bus, db, conversations.GroupID(), serverEnabled, wsHub)
}
