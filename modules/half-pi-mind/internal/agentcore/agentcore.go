// Package agentcore is the sole intelligent node of the system.
// It maintains global memory, understands user intent, selects
// target devices, and orchestrates cross-device workflows.
package agentcore

import "fmt"

// Core represents the agent core.
type Core struct{}

// New creates and initialises the agent core.
func New() (*Core, error) {
	return &Core{}, nil
}

// Run starts the agent core's main loop.
func (c *Core) Run() {
	fmt.Println("half-pi mind ready")
}
