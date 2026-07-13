package main

import (
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
)

func main() {
	// Step 1: use deepseek via OpenAI-compatible adapter
	provider := llm.NewOpenAI(
		"https://api.deepseek.com/v1",
		os.Getenv("DEEPSEEK_API_KEY"),
		"deepseek-chat",
	)

	core, err := agentcore.New(provider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	core.Run()
}
