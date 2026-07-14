package main

import (
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/repl"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
)

func main() {
	env, err := setup.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(env.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}

	modelID := cfg.LLM.DefaultModel
	if modelID == "" && len(cfg.LLM.Models) > 0 {
		modelID = cfg.LLM.Models[0].ID
	}
	rm, err := cfg.ResolveModel(modelID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model resolve error: %v\n", err)
		os.Exit(1)
	}

	provider := llm.NewOpenAI(rm.Endpoint, rm.APIKey, rm.Name)
	exec := local.New()

	skillStore, err := skill.LoadFromDir(env.SkillsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill load failed: %v\n", err)
	}
	local.SetSkillStore(skillStore)

	bus := events.NewEventBus()
	defer bus.Close()
	bus.Subscribe(events.NewConsoleWriter())

	core, err := agentcore.New(provider, exec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "core init failed: %v\n", err)
		os.Exit(1)
	}
	core.Bus = bus
	core.SetSkills(skillStore)

	repl.Run(core, bus)
}
