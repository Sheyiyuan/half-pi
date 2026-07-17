package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/agentcore"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/repl"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// runREPL 初始化 Agent Core 并进入交互式 REPL。
func runREPL(env *setup.Env, cfg *config.Config, db *store.Store, bus *events.EventBus, authority *remoteexec.Authority) {
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

	type actor struct {
		core   *agentcore.Core
		bridge *local.RemoteBridge
	}
	var actorsMu sync.Mutex
	actors := make(map[string]actor)
	getActor := func(id string) (*agentcore.Core, *local.RemoteBridge, error) {
		actorsMu.Lock()
		defer actorsMu.Unlock()
		if existing, ok := actors[id]; ok {
			return existing.core, existing.bridge, nil
		}
		bridge := &local.RemoteBridge{Hub: authority.Hub, Runs: authority.Registry, PendingCall: authority.PendingCall}
		exec := local.New(bridge)
		core, err := agentcore.New(provider, exec)
		if err != nil {
			return nil, nil, err
		}
		core.Bus = bus
		core.SetSkills(skillStore)
		if err := core.SetStore(db, id); err != nil {
			return nil, nil, err
		}
		bridge.ActiveHand = core.ActiveHand
		bridge.SessionID = core.SessionID
		bridge.Mode = core.SecurityMode
		bridge.SetActiveHand = core.SetActiveHand
		bridge.CheckAndConfirm = core.CheckAndConfirm
		actors[id] = actor{core: core, bridge: bridge}
		return core, bridge, nil
	}

	core, bridge, err := getActor(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "core init failed: %v\n", err)
		os.Exit(1)
	}
	repl.Run(core, bridge, getActor, bus, db, group.ID, cfg.Server.Enabled, authority.Hub)
}
