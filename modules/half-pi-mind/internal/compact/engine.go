package compact

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

// Engine 是会话级 Compact 的默认实现。
type Engine struct {
	cfg         RuntimeConfig
	provider    llm.Provider
	store       Store
	environment EnvironmentSource
	protection  ProtectionSource
	estimator   TokenEstimator
	capacity    chan struct{}
}

// New 创建共享进程级摘要容量的 Engine。
func New(cfg RuntimeConfig, provider llm.Provider, storage Store, environment EnvironmentSource, protection ProtectionSource) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if storage == nil || environment == nil {
		return nil, fmt.Errorf("compact store and environment are required")
	}
	if cfg.Enabled && provider == nil {
		return nil, fmt.Errorf("summary provider is required")
	}
	return &Engine{
		cfg: cfg, provider: provider, store: storage, environment: environment, protection: protection,
		capacity: make(chan struct{}, cfg.MaxConcurrent),
	}, nil
}

// Compact 执行一次已经取得 conversation operation lease 的压缩。
func (e *Engine) Compact(ctx context.Context, request CompactRequest) (CompactResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCompactRequest(request); err != nil {
		return CompactResult{}, err
	}
	if !e.cfg.Enabled || e.cfg.MainContextWindow == 0 {
		return CompactResult{}, compactError(ErrUnavailable, nil)
	}
	if request.TraceID == "" {
		request.TraceID = newUUIDv7()
	}
	ctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	startedAt := time.Now().UTC()
	if request.Trigger == TriggerManual {
		if err := e.store.AppendLifecycleEvent(ctx, newLifecycleEventWithTrace("compact.requested", request.SessionID, request.TraceID,
			compactRequestedPayload{Trigger: request.Trigger})); err != nil {
			return CompactResult{}, compactError(ErrInternal, err)
		}
	}

	snapshot, err := e.store.GetCompactSnapshot(ctx, request.SessionID)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, rangePlan{}, compactError(ErrInternal, err))
	}
	environment, err := e.environment.Snapshot(ctx, request.SessionID)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, rangePlan{}, compactError(ErrInternal, err))
	}
	protection, err := e.protectionSnapshot(request.SessionID)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, rangePlan{}, compactError(ErrInternal, err))
	}
	active, degradedReason := validateActiveSummary(snapshot)
	currentRequest, err := e.mainRequest(ctx, snapshot.Messages, active, environment)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, rangePlan{}, compactError(ErrInternal, err))
	}
	beforeTokens := e.estimator.EstimateRequest(currentRequest)
	evaluator := e.candidateEvaluator(ctx, snapshot, active, request, environment)
	activeToSeq := 0
	if active != nil {
		activeToSeq = active.ToSeq
	}
	minimumToSeq := activeToSeq
	if degradedReason != "" {
		minimumToSeq = recoverableActiveToSeq(snapshot)
	}
	plan, err := selectRange(rangeRequest{
		Messages: snapshot.Messages, ActiveToSeq: activeToSeq, MinimumToSeq: minimumToSeq,
		AllowSameRange: degradedReason != "", CurrentEstimatedTokens: beforeTokens,
		Target: request.Target, DefaultTargetTokens: e.cfg.LowTarget(), InputBudget: e.cfg.InputBudget(),
		HighWatermark: e.cfg.HighWatermark, Protection: protection, ActiveRequestID: environment.ActiveRequestID,
		Evaluate: evaluator,
	})
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, err)
	}

	candidate, summaryRequest, err := e.prepareCandidate(snapshot, active, request, plan)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, err)
	}
	existing, err := e.store.FindContextSummaryByContract(ctx, request.SessionID, 1, plan.ToSeq, candidate.ContractDigest)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrInternal, err))
	}
	if existing != nil {
		if err := validateExistingSummary(*existing, candidate, snapshot.Messages); err != nil {
			return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrInternal, err))
		}
		candidate = *existing
	} else {
		if err := e.acquireCapacity(ctx); err != nil {
			return CompactResult{}, e.fail(ctx, request, startedAt, plan, err)
		}
		defer e.releaseCapacity()
		attempt := int64(1)
		if request.Trigger == TriggerAutomatic {
			attempt = request.Pending.Attempt + 1
		}
		startedEvent := newLifecycleEventWithTrace("compact.started", request.SessionID, request.TraceID, compactStartedPayload{
			Trigger: request.Trigger, FromSeq: 1, ToSeq: plan.ToSeq,
			GenerationMode: candidate.GenerationMode, SourceDigest: candidate.SourceDigest, Attempt: attempt,
		})
		if request.Trigger == TriggerAutomatic {
			runtime, admitErr := e.store.AdmitCompactAttemptWithEvent(ctx, request.SessionID,
				request.Pending.ID, request.Pending.Attempt, startedEvent)
			if admitErr != nil {
				return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrConflict, admitErr))
			}
			request.Pending.Attempt = runtime.PendingAttempt
			attempt = runtime.PendingAttempt
		} else if err := e.store.AppendLifecycleEvent(ctx, startedEvent); err != nil {
			return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrInternal, err))
		}
		summary, usage, providerErr := callSummaryProvider(ctx, e.provider, e.cfg, summaryRequest, e.estimator)
		if providerErr != nil {
			return CompactResult{}, e.fail(ctx, request, startedAt, plan, providerErr)
		}
		candidate.Summary = summary
		candidate.SummaryDigest = store.ContextSummaryDigest(summary)
		candidate.SummaryEstimatedTokens = e.estimator.EstimateText(summary)
		candidate.InputTokens, candidate.OutputTokens = int64(usage.InputTokens), int64(usage.OutputTokens)
	}

	afterTokens, err := e.estimateCandidateRequest(ctx, snapshot.Messages, candidate, environment)
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrInvalidResponse, err))
	}
	if afterTokens > e.cfg.InputBudget() {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrContextLimit, nil))
	}
	if _, rebase := request.Target.(RebaseTarget); !rebase && afterTokens >= beforeTokens {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrNothingToCompact, nil))
	}
	currentEnvironment, err := e.environment.Snapshot(ctx, request.SessionID)
	if err != nil || currentEnvironment.Revision != environment.Revision || currentEnvironment.Digest != environment.Digest {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrConflict, err))
	}
	currentProtection, err := e.protectionSnapshot(request.SessionID)
	if err != nil || currentProtection.Revision != protection.Revision || currentProtection.Digest != protection.Digest {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrConflict, err))
	}

	predictedContextVersion := snapshot.Runtime.HistoryViewGeneration + 1
	if existing != nil && snapshot.Runtime.ActiveSummaryID == existing.ID {
		predictedContextVersion = snapshot.Runtime.HistoryViewGeneration
	}
	completed := compactCompletedPayload{
		Trigger: request.Trigger, SummaryID: candidate.ID, FromSeq: 1, ToSeq: candidate.ToSeq,
		BeforeEstimatedTokens: beforeTokens, AfterEstimatedTokens: afterTokens,
		SummaryBytes: len(candidate.Summary), SourceDigest: candidate.SourceDigest,
		DurationMS: elapsedMillis(startedAt), ContextVersion: predictedContextVersion,
		Reused: existing != nil,
	}
	commitResult, err := e.store.CommitContextSummary(ctx, store.CompactCommit{
		Summary: candidate, ExpectedHistoryGeneration: snapshot.Runtime.HistoryGeneration,
		ExpectedActiveSummaryID:       snapshot.Runtime.ActiveSummaryID,
		ExpectedHistoryViewGeneration: snapshot.Runtime.HistoryViewGeneration,
		Pending:                       request.Pending, ConsumePending: request.Pending.Required,
		AllowSameRange:      isRebaseTarget(request.Target) || degradedReason != "",
		AllowDegradedRepair: degradedReason != "",
		CompletedEvent:      newLifecycleEventWithTrace("compact.completed", request.SessionID, request.TraceID, completed),
	})
	if err != nil {
		return CompactResult{}, e.fail(ctx, request, startedAt, plan, compactError(ErrConflict, err))
	}
	return e.resultForPlan(plan, commitResult, beforeTokens, afterTokens, request.Target), nil
}

// Status 返回当前已提交投影和预算诊断，不调用 provider。
func (e *Engine) Status(ctx context.Context, request CompactStatusRequest) (CompactStatus, error) {
	if request.SessionID == "" {
		return CompactStatus{}, compactError(ErrInternal, fmt.Errorf("session ID is required"))
	}
	snapshot, err := e.store.GetCompactSnapshot(ctx, request.SessionID)
	if err != nil {
		return CompactStatus{}, compactError(ErrInternal, err)
	}
	environment, err := e.environment.Snapshot(ctx, request.SessionID)
	if err != nil {
		return CompactStatus{}, compactError(ErrInternal, err)
	}
	active, degradedReason := validateActiveSummary(snapshot)
	view, err := e.mainRequest(ctx, snapshot.Messages, active, environment)
	if err != nil {
		return CompactStatus{}, compactError(ErrInternal, err)
	}
	status := CompactStatus{
		Enabled: e.cfg.Enabled && e.cfg.MainContextWindow > 0, Automatic: e.cfg.Automatic,
		OperationState: request.OperationState, ContextVersion: uint64(snapshot.Runtime.HistoryViewGeneration),
		HistoryGeneration: uint64(snapshot.Runtime.HistoryGeneration), CompactGeneration: uint64(snapshot.Runtime.CompactGeneration),
		CurrentEstimatedTokens: e.estimator.EstimateRequest(view), InputBudget: e.cfg.InputBudget(),
		HighLimit: e.cfg.HighLimit(), LowTarget: e.cfg.LowTarget(), HardLimit: e.cfg.InputBudget(),
		Pending: snapshot.Runtime.PendingCompact, PendingID: snapshot.Runtime.PendingCompactID,
		PendingAttempt: snapshot.Runtime.PendingAttempt, SummaryNodeCount: snapshot.SummaryNodeCount,
		SummaryStorageBytes: snapshot.SummaryStorageBytes, SummaryInputBudget: e.cfg.SummaryInputBudget(),
		Degraded: degradedReason != "", Blocker: degradedReason,
	}
	if snapshot.Runtime.PendingNotBefore > 0 {
		status.PendingNotBefore = time.UnixMilli(snapshot.Runtime.PendingNotBefore).UTC()
	}
	storedActive := snapshot.ActiveSummary
	if storedActive != nil {
		status.ActiveSummaryID, status.ActiveFromSeq, status.ActiveToSeq = storedActive.ID, storedActive.FromSeq, storedActive.ToSeq
		status.ActiveProviderID, status.ActiveModelID = storedActive.ProviderID, storedActive.ModelID
		if compactVersionSyntax.MatchString(storedActive.ProjectionVersion) && compactVersionSyntax.MatchString(storedActive.PolicyVersion) &&
			compactVersionSyntax.MatchString(storedActive.Profile) {
			status.ActiveProjectionVersion, status.ActivePolicyVersion, status.ActiveProfile =
				storedActive.ProjectionVersion, storedActive.PolicyVersion, storedActive.Profile
		}
		status.ActiveGenerationMode = storedActive.GenerationMode
		status.SourceEstimatedTokens, status.SummaryEstimatedTokens = storedActive.SourceEstimatedTokens, storedActive.SummaryEstimatedTokens
		status.CompressionRatio = float64(storedActive.SourceEstimatedTokens) / float64(max64(storedActive.SummaryEstimatedTokens, 1))
	}
	if e.cfg.SummaryWarningNodes > 0 && snapshot.SummaryNodeCount >= e.cfg.SummaryWarningNodes {
		status.Warnings = append(status.Warnings, "summary_node_count_high")
	}
	if e.cfg.SummaryWarningBytes > 0 && snapshot.SummaryStorageBytes >= e.cfg.SummaryWarningBytes {
		status.Warnings = append(status.Warnings, "summary_storage_high")
	}
	if e.cfg.MainProviderID == e.cfg.SummaryProviderID && e.cfg.MainModelID == e.cfg.SummaryModelID {
		status.Warnings = append(status.Warnings, "shared_summary_model")
	}
	if status.Enabled {
		protection, protectionErr := e.protectionSnapshot(request.SessionID)
		if protectionErr != nil {
			if status.Blocker == "" {
				status.Blocker = ErrInternal
			}
		} else {
			activeToSeq := 0
			if active != nil {
				activeToSeq = active.ToSeq
			}
			minimumToSeq := activeToSeq
			if degradedReason != "" {
				minimumToSeq = recoverableActiveToSeq(snapshot)
			}
			plan, planErr := selectRange(rangeRequest{
				Messages: snapshot.Messages, ActiveToSeq: activeToSeq, MinimumToSeq: minimumToSeq,
				AllowSameRange:         degradedReason != "",
				CurrentEstimatedTokens: status.CurrentEstimatedTokens, Target: DefaultTarget{},
				DefaultTargetTokens: e.cfg.LowTarget(), InputBudget: e.cfg.InputBudget(),
				HighWatermark: e.cfg.HighWatermark, Protection: protection,
				ActiveRequestID: environment.ActiveRequestID,
				Evaluate:        e.candidateEvaluator(ctx, snapshot, active, CompactRequest{Target: DefaultTarget{}}, environment),
			})
			status.RequiredSummaryInputEstimatedTokens = plan.RequiredSummaryInput
			status.CandidateGenerationMode = plan.Cost.GenerationMode
			if planErr != nil && status.Blocker == "" {
				status.Blocker = errorCodeOf(planErr)
			}
			if status.CandidateGenerationMode == "" && plan.Blocker != "" {
				status.Blocker = plan.Blocker
			}
		}
	}
	return status, nil
}

// EnsureAutomaticPending 原子建立 durable 自动压缩需求；重复调用只合并。
func (e *Engine) EnsureAutomaticPending(ctx context.Context, sessionID string) (store.CompactPendingResult, error) {
	if sessionID == "" || !e.cfg.Enabled || !e.cfg.Automatic || e.cfg.MainContextWindow == 0 {
		return store.CompactPendingResult{}, compactError(ErrUnavailable, nil)
	}
	pendingID := newUUIDv7()
	return e.store.EnsureCompactPending(ctx, sessionID, pendingID,
		newLifecycleEventWithTrace("compact.requested", sessionID, pendingID,
			compactRequestedPayload{Trigger: TriggerAutomatic}))
}

// NewAutomaticPendingMutation 构造可与工具消息批次同事务提交的 durable pending 身份和事件。
func NewAutomaticPendingMutation(sessionID string) (string, store.LifecycleEvent) {
	pendingID := newUUIDv7()
	return pendingID, newLifecycleEventWithTrace("compact.requested", sessionID, pendingID,
		compactRequestedPayload{Trigger: TriggerAutomatic})
}

// ClearAutomaticPending 按 durable ID/attempt 清除重新评估后已不需要的 hint。
func (e *Engine) ClearAutomaticPending(ctx context.Context, sessionID string, pending store.PendingExpectation) (store.SessionRuntime, error) {
	if sessionID == "" || !pending.Required || pending.ID == "" || pending.Attempt < 0 {
		return store.SessionRuntime{}, compactError(ErrInternal, nil)
	}
	return e.store.ClearCompactPending(ctx, sessionID, pending.ID, pending.Attempt)
}

func (e *Engine) candidateEvaluator(ctx context.Context, snapshot store.CompactSnapshot, active *store.ContextSummary, request CompactRequest, environment EnvironmentSnapshot) candidateEvaluator {
	return func(toSeq int) (candidateCost, error) {
		mode, parent, summaryRequest, required, blocker, err := e.summaryInputForRange(
			snapshot.Messages, active, snapshot.Runtime.ActiveSummaryID != "", request.Target, toSeq)
		if err != nil {
			return candidateCost{}, err
		}
		cost := candidateCost{
			SummaryInputTokens: required, GenerationMode: mode,
			FitsSummaryInput: required <= e.cfg.SummaryInputBudget(), Blocker: blocker,
		}
		if summaryRequest.Messages == nil {
			cost.FitsSummaryInput = false
		}
		candidate := &store.ContextSummary{
			FromSeq: 1, ToSeq: toSeq, Summary: "", ProjectionVersion: ProjectionVersion,
			PolicyVersion: e.cfg.PolicyVersion, Profile: e.cfg.Profile, GenerationMode: mode,
		}
		if parent != nil {
			candidate.ParentSummaryID = parent.ID
		}
		cost.WorstAfterTokens, err = e.estimateWorstCandidate(ctx, snapshot.Messages, candidate, environment)
		return cost, err
	}
}

func (e *Engine) summaryInputForRange(messages []store.Message, active *store.ContextSummary, hasActive bool, target Target, toSeq int) (string, *store.ContextSummary, llm.LLMRequest, int64, ErrorCode, error) {
	_, explicitRebase := target.(RebaseTarget)
	compatible := active != nil && active.ProjectionVersion == ProjectionVersion && active.PolicyVersion == e.cfg.PolicyVersion &&
		active.Profile == e.cfg.Profile && active.ToSeq < toSeq && !explicitRebase
	if compatible {
		incremental, err := buildIncrementalSummaryRequest(e.cfg, messages, *active, toSeq)
		if err != nil {
			return "", nil, llm.LLMRequest{}, 0, ErrInternal, compactError(ErrInternal, err)
		}
		incrementalTokens := e.estimator.EstimateRequest(incremental)
		if incrementalTokens <= e.cfg.SummaryInputBudget() {
			return "incremental", active, incremental, incrementalTokens, "", nil
		}
		candidate := store.ContextSummary{FromSeq: 1, ToSeq: toSeq, ProjectionVersion: ProjectionVersion,
			PolicyVersion: e.cfg.PolicyVersion, Profile: e.cfg.Profile, GenerationMode: "rebase"}
		full, err := buildSummaryRequest(e.cfg, messages, candidate)
		if err != nil {
			return "", nil, llm.LLMRequest{}, 0, ErrInternal, compactError(ErrInternal, err)
		}
		fullTokens := e.estimator.EstimateRequest(full)
		if fullTokens <= e.cfg.SummaryInputBudget() {
			return "rebase", nil, full, fullTokens, "", nil
		}
		return "incremental", active, llm.LLMRequest{}, min64(incrementalTokens, fullTokens), ErrIncrementTooLarge, nil
	}
	mode := "full"
	if hasActive || explicitRebase {
		mode = "rebase"
	}
	candidate := store.ContextSummary{FromSeq: 1, ToSeq: toSeq, ProjectionVersion: ProjectionVersion,
		PolicyVersion: e.cfg.PolicyVersion, Profile: e.cfg.Profile, GenerationMode: mode}
	full, err := buildSummaryRequest(e.cfg, messages, candidate)
	if err != nil {
		return "", nil, llm.LLMRequest{}, 0, ErrInternal, compactError(ErrInternal, err)
	}
	tokens := e.estimator.EstimateRequest(full)
	if tokens > e.cfg.SummaryInputBudget() {
		return mode, nil, llm.LLMRequest{}, tokens, ErrRebaseTooLarge, nil
	}
	return mode, nil, full, tokens, "", nil
}

func (e *Engine) prepareCandidate(snapshot store.CompactSnapshot, active *store.ContextSummary, request CompactRequest, plan rangePlan) (store.ContextSummary, llm.LLMRequest, error) {
	mode, parent, summaryRequest, _, blocker, err := e.summaryInputForRange(
		snapshot.Messages, active, snapshot.Runtime.ActiveSummaryID != "", request.Target, plan.ToSeq)
	if err != nil {
		return store.ContextSummary{}, llm.LLMRequest{}, err
	}
	if blocker != "" || summaryRequest.Messages == nil {
		return store.ContextSummary{}, llm.LLMRequest{}, compactError(blocker, nil)
	}
	sourceDigest, err := store.ContextSourceDigest(request.SessionID, 1, plan.ToSeq, snapshot.Messages[:plan.ToSeq])
	if err != nil {
		return store.ContextSummary{}, llm.LLMRequest{}, compactError(ErrInternal, err)
	}
	parentID := ""
	if parent != nil {
		parentID = parent.ID
	}
	generationKey := ""
	if _, explicit := request.Target.(RebaseTarget); explicit {
		generationKey = manualRebaseKey(request.Principal, request.RequestID)
	}
	candidate := store.ContextSummary{
		ID: newUUIDv7(), SessionID: request.SessionID, ParentSummaryID: parentID,
		FromSeq: 1, ToSeq: plan.ToSeq, SourceDigest: sourceDigest,
		ProviderID: e.cfg.SummaryProviderID, ModelID: e.cfg.SummaryModelID,
		Profile: e.cfg.Profile, PolicyVersion: e.cfg.PolicyVersion, ProjectionVersion: ProjectionVersion,
		GenerationMode: mode, GenerationKey: generationKey,
	}
	if snapshot.ActiveSummary != nil && snapshot.ActiveReferenceUsable {
		candidate.SupersedesSummaryID = snapshot.ActiveSummary.ID
	}
	candidate.ContractDigest = store.ContextContractDigest(sourceDigest, ProjectionVersion,
		e.cfg.PolicyVersion, e.cfg.Profile, mode, parentID, generationKey)
	fullProjection, err := projectSource(snapshot.Messages, 1, plan.ToSeq)
	if err != nil {
		return store.ContextSummary{}, llm.LLMRequest{}, compactError(ErrInternal, err)
	}
	encoded, _ := encodeJSONNoHTML(fullProjection)
	candidate.SourceEstimatedTokens = e.estimator.EstimateText(encoded)
	return candidate, summaryRequest, nil
}

func (e *Engine) mainRequest(ctx context.Context, messages []store.Message, active *store.ContextSummary, environment EnvironmentSnapshot) (llm.LLMRequest, error) {
	if environment.BuildRequest != nil {
		return environment.BuildRequest(ctx, messages, active)
	}
	view, err := buildProviderMessages(messages, active)
	if err != nil {
		return llm.LLMRequest{}, err
	}
	return llm.LLMRequest{
		System: environment.System, Messages: view, Tools: append([]llm.ToolDef(nil), environment.Tools...),
		MaxTokens: minInt(e.cfg.MainMaxTokens, int(e.cfg.ReservedOutputTokens)),
	}, nil
}

func (e *Engine) estimateCandidateRequest(ctx context.Context, messages []store.Message, candidate store.ContextSummary, environment EnvironmentSnapshot) (int64, error) {
	request, err := e.mainRequest(ctx, messages, &candidate, environment)
	if err != nil {
		return 0, err
	}
	return e.estimator.EstimateRequest(request), nil
}

func (e *Engine) estimateWorstCandidate(ctx context.Context, messages []store.Message, candidate *store.ContextSummary, environment EnvironmentSnapshot) (int64, error) {
	candidate.Summary = "x"
	request, err := e.mainRequest(ctx, messages, candidate, environment)
	if err != nil {
		return 0, err
	}
	base := e.estimator.EstimateRequest(request)
	return base + ceilDiv(int64(2*e.cfg.MaxSummaryBytes())*110, 100), nil
}

func (e *Engine) protectionSnapshot(sessionID string) (remoteexec.ProtectionSnapshot, error) {
	if e.protection == nil {
		return remoteexec.NewProtectionIndex().Snapshot(sessionID), nil
	}
	return e.protection.ProtectionSnapshot(sessionID)
}

func (e *Engine) acquireCapacity(ctx context.Context) error {
	select {
	case e.capacity <- struct{}{}:
		return nil
	case <-ctx.Done():
		return compactError(ErrTimeout, ctx.Err())
	}
}

func (e *Engine) releaseCapacity() { <-e.capacity }

func (e *Engine) fail(ctx context.Context, request CompactRequest, startedAt time.Time, plan rangePlan, failure error) error {
	code := errorCodeOf(failure)
	if code == "" {
		code = ErrInternal
	}
	rateLimited := code == ErrRateLimited
	retryAt := time.Time{}
	if rateLimited {
		var compactErr *Error
		if errors.As(failure, &compactErr) {
			retryAt = compactErr.RetryNotBefore
		}
		retryAt = e.clampOrBackoff(retryAt, request.Pending.Attempt)
	}
	payload := compactFailedPayload{
		Trigger: request.Trigger, Reason: code, DurationMS: elapsedMillis(startedAt),
		RetryScheduled: rateLimited && request.Pending.Required,
	}
	if plan.ToSeq > 0 {
		payload.FromSeq, payload.ToSeq = 1, plan.ToSeq
	}
	if payload.RetryScheduled {
		payload.PendingAttempt = request.Pending.Attempt
		payload.RetryNotBefore = retryAt.UnixMilli()
	}
	terminalCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		terminalCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
	}
	_, err := e.store.FinishCompactFailure(terminalCtx, store.CompactFailure{
		SessionID: request.SessionID, ExpectedPending: request.Pending,
		Automatic: request.Trigger == TriggerAutomatic, RateLimited: payload.RetryScheduled,
		RetryNotBefore: payload.RetryNotBefore,
		FailedEvent:    newLifecycleEventWithTrace("compact.failed", request.SessionID, request.TraceID, payload),
	})
	if err != nil {
		return compactError(ErrInternal, err)
	}
	if rateLimited {
		return &Error{Code: ErrRateLimited, RetryNotBefore: retryAt, cause: failure}
	}
	return &Error{Code: code, cause: failure}
}

func (e *Engine) clampOrBackoff(providerTime time.Time, attempt int64) time.Time {
	now := time.Now().UTC()
	if providerTime.After(now) {
		delay := providerTime.Sub(now)
		if delay > e.cfg.RateLimitMaxBackoff {
			delay = e.cfg.RateLimitMaxBackoff
		}
		return now.Add(delay)
	}
	if attempt < 1 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 30 {
		exponent = 30
	}
	maximum := e.cfg.RateLimitInitialBackoff * time.Duration(1<<exponent)
	if maximum <= 0 || maximum > e.cfg.RateLimitMaxBackoff {
		maximum = e.cfg.RateLimitMaxBackoff
	}
	var random [8]byte
	_, _ = cryptorand.Read(random[:])
	jitter := time.Duration(binary.BigEndian.Uint64(random[:]) % uint64(maximum))
	if jitter < time.Second {
		jitter = time.Second
	}
	return now.Add(jitter)
}

func (e *Engine) resultForPlan(plan rangePlan, committed store.CompactCommitResult, before, after int64, target Target) CompactResult {
	result := CompactResult{
		SummaryID: committed.Summary.ID, FromSeq: committed.Summary.FromSeq, ToSeq: committed.Summary.ToSeq,
		BeforeEstimatedTokens: before, AfterEstimatedTokens: after,
		RetainedFromSeq: plan.RetainedFromSeq, RetainedToSeq: plan.RetainedToSeq,
		GenerationMode: committed.Summary.GenerationMode,
		ContextVersion: uint64(committed.Runtime.HistoryViewGeneration), Reused: committed.Reused,
	}
	switch typed := target.(type) {
	case KeepTarget:
		result.TargetResult = KeepTargetResult{
			RequestedKeepMessages: typed.Messages, RetainedMessageCount: plan.RetainedMessageCount,
			SafetyRetainedExtra: plan.SafetyRetainedExtra, CapacityRetainedExtra: plan.CapacityRetainedExtra,
		}
	default:
		result.TargetResult = TokenTargetResult{TargetTokens: plan.TargetTokens, TargetMet: after <= plan.TargetTokens}
	}
	return result
}

type compactRequestedPayload struct {
	Trigger Trigger `json:"trigger"`
}
type compactStartedPayload struct {
	Trigger        Trigger `json:"trigger"`
	FromSeq        int     `json:"from_seq"`
	ToSeq          int     `json:"to_seq"`
	GenerationMode string  `json:"generation_mode"`
	SourceDigest   string  `json:"source_digest"`
	Attempt        int64   `json:"attempt"`
}
type compactCompletedPayload struct {
	Trigger               Trigger `json:"trigger"`
	SummaryID             string  `json:"summary_id"`
	FromSeq               int     `json:"from_seq"`
	ToSeq                 int     `json:"to_seq"`
	BeforeEstimatedTokens int64   `json:"before_estimated_tokens"`
	AfterEstimatedTokens  int64   `json:"after_estimated_tokens"`
	SummaryBytes          int     `json:"summary_bytes"`
	SourceDigest          string  `json:"source_digest"`
	DurationMS            int64   `json:"duration_ms"`
	ContextVersion        int64   `json:"context_version"`
	Reused                bool    `json:"reused"`
}
type compactFailedPayload struct {
	Trigger        Trigger   `json:"trigger"`
	Reason         ErrorCode `json:"reason"`
	DurationMS     int64     `json:"duration_ms"`
	RetryScheduled bool      `json:"retry_scheduled"`
	FromSeq        int       `json:"from_seq,omitempty"`
	ToSeq          int       `json:"to_seq,omitempty"`
	PendingAttempt int64     `json:"pending_attempt,omitempty"`
	RetryNotBefore int64     `json:"retry_not_before,omitempty"`
}

func newLifecycleEvent(eventType, sessionID string, payload any) store.LifecycleEvent {
	return newLifecycleEventWithTrace(eventType, sessionID, newUUIDv7(), payload)
}

func newLifecycleEventWithTrace(eventType, sessionID, traceID string, payload any) store.LifecycleEvent {
	encoded, _ := json.Marshal(payload)
	return store.LifecycleEvent{
		ID: newUUIDv7(), EventType: eventType, SchemaVersion: 1,
		TraceID: traceID, SpanID: newUUIDv7(), SubjectID: sessionID,
		Payload: encoded, OccurredAt: time.Now().UTC(),
	}
}

func recoverableActiveToSeq(snapshot store.CompactSnapshot) int {
	if snapshot.ActiveSummary == nil || snapshot.ActiveSummary.FromSeq != 1 ||
		snapshot.ActiveSummary.ToSeq < 1 || snapshot.ActiveSummary.ToSeq > len(snapshot.Messages) {
		return 0
	}
	return snapshot.ActiveSummary.ToSeq
}

func validateCompactRequest(request CompactRequest) error {
	if request.SessionID == "" || request.RequestID == "" || request.Target == nil ||
		(request.Trigger != TriggerManual && request.Trigger != TriggerAutomatic) {
		return compactError(ErrInternal, fmt.Errorf("invalid compact request"))
	}
	if request.Trigger == TriggerManual && request.Principal == "" {
		return compactError(ErrInternal, fmt.Errorf("manual compact principal is required"))
	}
	if request.Trigger == TriggerAutomatic && (!request.Pending.Required || request.Pending.ID != request.RequestID) {
		return compactError(ErrInternal, fmt.Errorf("automatic compact pending binding is invalid"))
	}
	return nil
}

func manualRebaseKey(principal, requestID string) string {
	hash := digestDomain("half-pi:compact-manual-rebase:v1", principal, requestID)
	return hash
}

func validateExistingSummary(existing, expected store.ContextSummary, messages []store.Message) error {
	if existing.SessionID != expected.SessionID || existing.FromSeq != expected.FromSeq || existing.ToSeq != expected.ToSeq ||
		existing.ContractDigest != expected.ContractDigest || existing.SourceDigest != expected.SourceDigest ||
		existing.ProjectionVersion != expected.ProjectionVersion || existing.PolicyVersion != expected.PolicyVersion ||
		existing.Profile != expected.Profile || existing.GenerationMode != expected.GenerationMode ||
		existing.GenerationKey != expected.GenerationKey || existing.ParentSummaryID != expected.ParentSummaryID ||
		existing.Summary == "" || existing.SummaryDigest != store.ContextSummaryDigest(existing.Summary) ||
		existing.ToSeq > len(messages) {
		return fmt.Errorf("existing compact contract is invalid")
	}
	sourceDigest, err := store.ContextSourceDigest(existing.SessionID, 1, existing.ToSeq, messages[:existing.ToSeq])
	if err != nil || sourceDigest != existing.SourceDigest {
		return fmt.Errorf("existing compact source is invalid")
	}
	contractDigest := store.ContextContractDigest(existing.SourceDigest, existing.ProjectionVersion,
		existing.PolicyVersion, existing.Profile, existing.GenerationMode, existing.ParentSummaryID, existing.GenerationKey)
	if contractDigest != existing.ContractDigest {
		return fmt.Errorf("existing compact contract digest is invalid")
	}
	return nil
}

func digestDomain(fields ...string) string {
	data := make([]byte, 0)
	for _, field := range fields {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		data = append(data, length[:]...)
		data = append(data, field...)
	}
	return digestBytes(data)
}

func newUUIDv7() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func isRebaseTarget(target Target) bool   { _, ok := target.(RebaseTarget); return ok }
func elapsedMillis(start time.Time) int64 { return max64(time.Since(start).Milliseconds(), 0) }
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ Compactor = (*Engine)(nil)
var _ AutomaticCompactor = (*Engine)(nil)
