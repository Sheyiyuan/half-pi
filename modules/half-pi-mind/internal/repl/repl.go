package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type Repl struct {
	core    *agentcore.Core
	bus     *events.EventBus
	store   *store.Store
	groupID string
	scanner *bufio.Scanner
}

func Run(core *agentcore.Core, bus *events.EventBus, s *store.Store, groupID string) {
	r := &Repl{
		core:    core,
		bus:     bus,
		store:   s,
		groupID: groupID,
		scanner: bufio.NewScanner(os.Stdin),
	}
	core.SetApprover(&approver{scanner: r.scanner})

	r.printBanner()
	for r.loop() {
	}
}

func (r *Repl) printBanner() {
	fmt.Println("half-pi mind ready")
	fmt.Printf("group: %s\n", r.groupID)
	fmt.Println("/session              list sessions")
	fmt.Println("/session <prefix>     switch session")
	fmt.Println("/session name <name>  rename session")
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
