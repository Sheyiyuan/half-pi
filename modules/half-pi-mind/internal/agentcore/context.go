package agentcore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
	corelifecycle "github.com/Sheyiyuan/half-pi/modules/half-pi-core/lifecycle"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/compact"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type modelEnvironmentToken struct {
	ContextVersion    int64  `json:"context_version"`
	Mode              string `json:"mode"`
	LifecycleRevision uint64 `json:"lifecycle_revision"`
	LifecycleDigest   string `json:"lifecycle_digest"`
	SkillRevision     uint64 `json:"skill_revision"`
	SkillDigest       string `json:"skill_digest"`
	ToolRevision      uint64 `json:"tool_revision"`
	ToolDigest        string `json:"tool_digest"`
}

type modelPreflight struct {
	request     *llm.LLMRequest
	fingerprint compact.RequestFingerprint
	environment modelEnvironmentToken
	estimated   int64
	degraded    compact.ErrorCode
}

func (c *Core) rawContextView(ctx context.Context) ([]llm.Message, int64, compact.ErrorCode, error) {
	c.stateMu.RLock()
	storage, sessionID := c.store, c.sessionID
	c.stateMu.RUnlock()
	if storage == nil || sessionID == "" {
		return cloneMessages(c.history), int64(len(c.history)), "", nil
	}
	snapshot, err := storage.GetCompactSnapshot(ctx, sessionID)
	if err != nil {
		return nil, 0, "", fmt.Errorf("load compact context view: %w", err)
	}
	active, degraded := compact.ValidateActiveSummary(snapshot)
	view, err := compact.BuildProviderMessages(snapshot.Messages, active)
	if err != nil {
		return nil, 0, "", fmt.Errorf("build compact context view: %w", err)
	}
	return view, snapshot.Runtime.HistoryViewGeneration, degraded, nil
}

func (c *Core) captureModelEnvironment(contextVersion int64) modelEnvironmentToken {
	c.stateMu.RLock()
	mode, registry, skills := c.Mode, c.lifecycle, c.skills
	c.stateMu.RUnlock()
	token := modelEnvironmentToken{ContextVersion: contextVersion, Mode: mode}
	if registry != nil {
		snapshot := registry.Snapshot()
		token.LifecycleRevision, token.LifecycleDigest = snapshot.Revision, snapshot.Digest
	}
	if skills != nil {
		snapshot := skills.Snapshot()
		token.SkillRevision, token.SkillDigest = snapshot.Revision, snapshot.Digest
	}
	tools := executor.RegisteredToolsSnapshot()
	token.ToolRevision, token.ToolDigest = tools.Revision, tools.Digest
	return token
}

func (c *Core) currentContextVersion() (int64, error) {
	c.stateMu.RLock()
	storage, sessionID := c.store, c.sessionID
	c.stateMu.RUnlock()
	if storage == nil || sessionID == "" {
		return int64(len(c.history)), nil
	}
	runtime, err := storage.GetSessionRuntime(context.Background(), sessionID)
	if err != nil {
		return 0, err
	}
	return runtime.HistoryViewGeneration, nil
}

func (c *Core) modelEnvironmentUnchanged(expected modelEnvironmentToken) bool {
	version, err := c.currentContextVersion()
	return err == nil && c.captureModelEnvironment(version) == expected
}

// CompactEnvironmentSnapshot 返回摘要引擎构造候选主请求所需的最终 system/tools 环境。
func (c *Core) CompactEnvironmentSnapshot(ctx context.Context) (compact.EnvironmentSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	messages, contextVersion, _, err := c.rawContextView(ctx)
	if err != nil {
		return compact.EnvironmentSnapshot{}, err
	}
	meta := c.newChatMeta(ctx).ChildMeta(corelifecycle.SourceMind).WithNode("mind")
	request, err := c.transformModelRequest(ctx, meta, messages)
	if err != nil {
		return compact.EnvironmentSnapshot{}, err
	}
	token := c.captureModelEnvironment(contextVersion)
	encoded, err := json.Marshal(struct {
		Token  modelEnvironmentToken `json:"token"`
		System string                `json:"system"`
		Tools  []llm.ToolDef         `json:"tools"`
	}{Token: token, System: request.System, Tools: request.Tools})
	if err != nil {
		return compact.EnvironmentSnapshot{}, fmt.Errorf("encode compact request environment: %w", err)
	}
	hash := sha256.New()
	writeContextDigestField(hash, "half-pi:compact-request-environment:v1")
	writeContextDigestField(hash, string(encoded))
	return compact.EnvironmentSnapshot{
		System: request.System, Tools: request.Tools,
		Revision: uint64(contextVersion), Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		BuildRequest: c.CompactContextRequest,
	}, nil
}

// CompactContextRequest 对摘要候选视图运行与主 Chat 相同的 Transformer、结构校验和 Guard。
func (c *Core) CompactContextRequest(ctx context.Context, messages []store.Message, active *store.ContextSummary) (llm.LLMRequest, error) {
	view, err := compact.BuildProviderMessages(messages, active)
	if err != nil {
		return llm.LLMRequest{}, err
	}
	meta := c.newChatMeta(ctx).ChildMeta(corelifecycle.SourceMind).WithNode("mind")
	request, err := c.transformModelRequest(ctx, meta, view)
	if err != nil {
		return llm.LLMRequest{}, err
	}
	return *request, nil
}

func (c *Core) prepareToolBatchPending(ctx context.Context) *ToolBatchPending {
	c.stateMu.RLock()
	runtime, coordinator := c.compactRuntime, c.compaction
	c.stateMu.RUnlock()
	preparer, ok := coordinator.(ToolBatchCompactionCoordinator)
	if !ok || !runtime.Automatic || runtime.MainContextWindow == 0 {
		return nil
	}
	view, _, _, err := c.rawContextView(ctx)
	if err != nil {
		return nil
	}
	if c.persistedMessages < 0 || c.persistedMessages > len(c.history) {
		return nil
	}
	view = append(view, cloneMessages(c.history[c.persistedMessages:])...)
	request, err := c.transformModelRequest(ctx, c.newChatMeta(ctx), view)
	if err != nil {
		return nil
	}
	estimated := (compact.TokenEstimator{}).EstimateRequest(*request)
	if estimated < runtime.HighLimit() {
		return nil
	}
	pending, err := preparer.PrepareToolBatchPending(ctx, BudgetObservation{
		EstimatedTokens: estimated, HighLimit: runtime.HighLimit(), HardLimit: runtime.InputBudget(),
	})
	if err != nil {
		return nil
	}
	return pending
}

func writeContextDigestField(writer interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
