package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/events"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/approval"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/conversation"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/executor/local"
	mindlifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/lifecycle"
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
	var reviewer mindlifecycle.Reviewer
	if cfg.Security.Review.Enabled {
		reviewModelID := cfg.Security.Review.Model
		if reviewModelID == "" {
			return nil, fmt.Errorf("security.review.model is required when review is enabled")
		}
		reviewModel, resolveErr := cfg.ResolveModel(reviewModelID)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve security review model: %w", resolveErr)
		}
		if configuredProvider := cfg.Security.Review.Provider; configuredProvider != "" && configuredProvider != reviewModel.Provider {
			return nil, fmt.Errorf("security.review provider %q does not own model %q", configuredProvider, reviewModelID)
		}
		var reviewProvider llm.Provider
		if reviewModel.Adapter == "scripted" {
			reviewProvider, err = llm.NewScriptedProviderFromFile(reviewModel.ScriptPath)
		} else {
			reviewProvider, err = llm.New(reviewModel.Adapter, reviewModel.Endpoint, reviewModel.APIKey, reviewModel.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("create security review provider: %w", err)
		}
		reviewer, err = mindlifecycle.NewAIReviewer(reviewProvider, mindlifecycle.ReviewerConfig{
			Timeout:       time.Duration(cfg.Security.Review.TimeoutMS) * time.Millisecond,
			MaxTokens:     cfg.Security.Review.MaxTokens,
			PolicyVersion: cfg.Security.Review.PolicyVersion,
			Profile:       cfg.Security.Review.Profile,
			ProviderID:    reviewModel.Provider,
			ModelID:       reviewModel.ID,
		})
		if err != nil {
			return nil, err
		}
	}
	var provider llm.Provider
	if model.Adapter == "scripted" {
		provider, err = llm.NewScriptedProviderFromFile(model.ScriptPath)
	} else {
		provider, err = llm.New(model.Adapter, model.Endpoint, model.APIKey, model.Name)
	}
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
		GroupID: group.ID, Provider: provider, ProviderID: model.Provider, ModelID: model.ID,
		Reviewer: reviewer, Store: db, Bus: bus,
		Skills: skills, Approvals: approvals, Authority: authority, Tasks: tasks,
	})
}
