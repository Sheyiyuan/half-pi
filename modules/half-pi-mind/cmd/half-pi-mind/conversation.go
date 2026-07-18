package main

import (
	"fmt"
	"os"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/skill"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func newConversationManager(env *setup.Env, cfg *config.Config, db *store.Store, bus *events.EventBus, approvals *approval.Broker, authority *remoteexec.Authority, tasks *remoteexec.TaskService) (*conversation.Manager, error) {
	modelID := cfg.LLM.DefaultModel
	if modelID == "" && len(cfg.LLM.Models) > 0 {
		modelID = cfg.LLM.Models[0].ID
	}
	model, err := cfg.ResolveModel(modelID)
	if err != nil {
		return nil, err
	}
	provider, err := llm.New(model.Adapter, model.Endpoint, model.APIKey, model.Name)
	if err != nil {
		return nil, err
	}
	skills, err := skill.LoadFromDir(env.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}
	local.SetSkillStore(skills)
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	group, err := db.UpsertGroup(cwd)
	if err != nil {
		return nil, fmt.Errorf("initialize conversation group: %w", err)
	}
	return conversation.NewManager(conversation.Config{
		GroupID: group.ID, Provider: provider, Store: db, Bus: bus,
		Skills: skills, Approvals: approvals, Authority: authority, Tasks: tasks,
	})
}
