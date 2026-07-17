package main

import (
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/repl"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// runREPL 初始化 Agent Core 并进入交互式 REPL。
func runREPL(env *setup.Env, cfg *config.Config, db *store.Store, bus *events.EventBus, wsHub *hub.Hub) {
	modelID := cfg.LLM.DefaultModel
	if modelID == "" && len(cfg.LLM.Models) > 0 {
		modelID = cfg.LLM.Models[0].ID
	}
	rm, err := cfg.ResolveModel(modelID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model resolve error: %v\n", err)
		os.Exit(1)
	}

	provider, err := llm.New(rm.Adapter, rm.Endpoint, rm.APIKey, rm.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "adapter init failed: %v\n", err)
		os.Exit(1)
	}
	exec := local.New()

	skillStore, err := skill.LoadFromDir(env.SkillsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill load failed: %v\n", err)
	}
	local.SetSkillStore(skillStore)

	cwd, _ := os.Getwd()
	group, err := db.UpsertGroup(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "group init failed: %v\n", err)
		os.Exit(1)
	}

	sessionID := uuid.Must(uuid.NewV7()).String()
	if err := db.CreateSession(group.ID, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "session create failed: %v\n", err)
		os.Exit(1)
	}

	core, err := agentcore.New(provider, exec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "core init failed: %v\n", err)
		os.Exit(1)
	}
	core.Bus = bus
	core.SetSkills(skillStore)
	if err := core.SetStore(db, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "session load failed: %v\n", err)
		os.Exit(1)
	}

	core.SetHub(wsHub)

	local.SetRemoteBridge(&local.RemoteBridge{
		Hub:             wsHub,
		Runs:            core.RemoteRuns(),
		ActiveHand:      core.ActiveHand,
		SessionID:       core.SessionID,
		SetActiveHand:   core.SetActiveHand,
		PendingCall:     core.PendingCall,
		CheckAndConfirm: core.CheckAndConfirm,
	})

	repl.Run(core, bus, db, group.ID, cfg.Server.Enabled)
}
