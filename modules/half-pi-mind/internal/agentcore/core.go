// Package agentcore is the sole intelligent node of the system.
// It maintains global memory, understands user intent, selects
// target devices, and orchestrates cross-device workflows.
// It imports the llm package to send requests to LLM providers.
package agentcore

import (
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

// Core represents the agent core.
type Core struct {
	llm llm.Provider
}

// New creates and initialises the agent core.
func New(llmProvider llm.Provider) (*Core, error) {
	return &Core{llm: llmProvider}, nil
}

// Run starts the agent core's main loop.
func (c *Core) Run() {
	fmt.Println("half-pi mind ready")
}
