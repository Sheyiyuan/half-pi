package main

import (
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
)

func main() {
	core, err := agentcore.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	core.Run()
}
