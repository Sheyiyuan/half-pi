// Package agentcore 是系统唯一的智能节点。
// 维护全局记忆、理解用户意图、选择目标设备、编排跨设备流程。
// 通过 import llm 包与 LLM provider 通信。
package agentcore

import (
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

// Core 是 agent core 的主体。
type Core struct {
	llm llm.Provider
}

// New 创建并初始化 agent core。
func New(llmProvider llm.Provider) (*Core, error) {
	return &Core{llm: llmProvider}, nil
}

// Run 启动 agent core 的主循环。
func (c *Core) Run() {
	fmt.Println("half-pi mind ready")
}
