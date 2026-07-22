// Package compact 实现会话上下文压缩的预算、范围、投影和摘要编排。
package compact

import (
	"context"
	"fmt"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/llm"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

const (
	ProjectionVersion     = "compact-source-v1"
	ToolProjectionVersion = "compact-tool-v1"
)

// Trigger 表示 Compact 是人工请求还是自动需求。
type Trigger string

const (
	TriggerManual    Trigger = "manual"
	TriggerAutomatic Trigger = "automatic"
)

// ErrorCode 是不会携带 provider 原始错误的稳定失败分类。
type ErrorCode string

const (
	ErrUnavailable        ErrorCode = "compact_unavailable"
	ErrNothingToCompact   ErrorCode = "nothing_to_compact"
	ErrTargetUnreachable  ErrorCode = "compact_target_unreachable"
	ErrIncrementTooLarge  ErrorCode = "compact_increment_too_large"
	ErrRebaseTooLarge     ErrorCode = "compact_rebase_too_large"
	ErrTimeout            ErrorCode = "compact_timeout"
	ErrRateLimited        ErrorCode = "compact_rate_limited"
	ErrProvider           ErrorCode = "compact_provider_error"
	ErrInvalidResponse    ErrorCode = "compact_invalid_response"
	ErrConflict           ErrorCode = "compact_conflict"
	ErrContextLimit       ErrorCode = "context_limit"
	ErrRepairRequired     ErrorCode = "compact_repair_required"
	ErrIntegrity          ErrorCode = "compact_integrity_error"
	ErrUnsupportedVersion ErrorCode = "compact_unsupported_version"
	ErrInternal           ErrorCode = "internal_error"
)

// Error 是 Compact 对上层暴露的脱敏 typed error。
type Error struct {
	Code           ErrorCode
	RetryNotBefore time.Time
	cause          error
}

func (e *Error) Error() string {
	if e == nil {
		return "compact failed"
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.cause }

func compactError(code ErrorCode, cause error) error {
	return &Error{Code: code, cause: cause}
}

// Target 是 Compact 目标的内部 sealed union。
type Target interface {
	compactTarget()
}

// DefaultTarget 使用配置低水位。
type DefaultTarget struct{}

func (DefaultTarget) compactTarget() {}

// RatioTarget 使用主模型输入预算的指定比例。
type RatioTarget struct{ Ratio float64 }

func (RatioTarget) compactTarget() {}

// KeepTarget 至少保留最近 N 条有效原始消息。
type KeepTarget struct{ Messages int }

func (KeepTarget) compactTarget() {}

// RebaseTarget 忽略兼容 parent 并重建累计摘要。
type RebaseTarget struct{}

func (RebaseTarget) compactTarget() {}

// TargetResult 是 Compact 成功目标结果的 sealed union。
type TargetResult interface {
	compactTargetResult()
}

// TokenTargetResult 描述 default、ratio 和 rebase 的 token 目标。
type TokenTargetResult struct {
	TargetTokens int64
	TargetMet    bool
}

func (TokenTargetResult) compactTargetResult() {}

// KeepTargetResult 描述 keep 的保留消息结果。
type KeepTargetResult struct {
	RequestedKeepMessages int
	RetainedMessageCount  int
	SafetyRetainedExtra   int
	CapacityRetainedExtra int
}

func (KeepTargetResult) compactTargetResult() {}

// RuntimeConfig 是一次 Compactor 使用的已解析、无秘密配置。
type RuntimeConfig struct {
	Enabled                 bool
	Automatic               bool
	MainProviderID          string
	MainModelID             string
	MainContextWindow       int64
	MainMaxTokens           int
	ReservedOutputTokens    int64
	SummaryProviderID       string
	SummaryModelID          string
	SummaryContextWindow    int64
	SummaryModelMaxTokens   int
	Timeout                 time.Duration
	MaxTokens               int
	HighWatermark           float64
	LowWatermark            float64
	ProviderMarginTokens    int64
	MaxConcurrent           int
	RateLimitInitialBackoff time.Duration
	RateLimitMaxBackoff     time.Duration
	SummaryWarningNodes     int
	SummaryWarningBytes     int64
	PolicyVersion           string
	Profile                 string
}

// ResolveRuntimeConfig 从已解析模型和 Compact 配置构造运行时预算。
func ResolveRuntimeConfig(compact config.CompactCfg, main, summary *config.ResolvedModel) (RuntimeConfig, error) {
	if main == nil {
		return RuntimeConfig{}, fmt.Errorf("main model is required")
	}
	resolved := RuntimeConfig{
		Enabled: compact.Enabled, Automatic: compact.Automatic,
		MainProviderID: main.Provider, MainModelID: main.ID,
		MainContextWindow: int64(main.ContextWindow), MainMaxTokens: main.MaxTokens,
		ReservedOutputTokens: int64(compact.ReservedOutputTokens),
		Timeout:              time.Duration(compact.TimeoutMS) * time.Millisecond, MaxTokens: compact.MaxTokens,
		HighWatermark: compact.HighWatermark, LowWatermark: compact.LowWatermark,
		ProviderMarginTokens: int64(compact.ProviderMarginTokens), MaxConcurrent: compact.MaxConcurrent,
		RateLimitInitialBackoff: time.Duration(compact.RateLimitInitialBackoffMS) * time.Millisecond,
		RateLimitMaxBackoff:     time.Duration(compact.RateLimitMaxBackoffMS) * time.Millisecond,
		SummaryWarningNodes:     compact.SummaryWarningNodes, SummaryWarningBytes: compact.SummaryWarningBytes,
		PolicyVersion: compact.PolicyVersion, Profile: compact.Profile,
	}
	if resolved.ReservedOutputTokens == 0 {
		resolved.ReservedOutputTokens = int64(main.MaxTokens)
	}
	if summary != nil {
		if compact.Enabled && (summary.ID != compact.Model || summary.Provider != compact.Provider) {
			return RuntimeConfig{}, fmt.Errorf("summary model does not match compact configuration")
		}
		resolved.SummaryProviderID = summary.Provider
		resolved.SummaryModelID = summary.ID
		resolved.SummaryContextWindow = int64(summary.ContextWindow)
		resolved.SummaryModelMaxTokens = summary.MaxTokens
	}
	if err := resolved.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return resolved, nil
}

// Validate 检查运行时预算不变量。
func (c RuntimeConfig) Validate() error {
	if c.MainProviderID == "" || c.MainModelID == "" || c.MainMaxTokens < 0 || c.MainContextWindow < 0 {
		return fmt.Errorf("invalid main model configuration")
	}
	if c.ProviderMarginTokens < 0 || c.ReservedOutputTokens < 0 {
		return fmt.Errorf("invalid compact token margins")
	}
	if c.MainContextWindow > 0 && (c.ReservedOutputTokens < 1 || c.ReservedOutputTokens > int64(c.MainMaxTokens) || c.InputBudget() <= 0) {
		return fmt.Errorf("invalid main model input budget")
	}
	if c.LowWatermark < .20 || c.HighWatermark > .95 || c.LowWatermark >= c.HighWatermark {
		return fmt.Errorf("invalid compact watermarks")
	}
	if c.Enabled {
		if c.SummaryProviderID == "" || c.SummaryModelID == "" || c.SummaryContextWindow <= 0 {
			return fmt.Errorf("summary model is required")
		}
		if c.Timeout < time.Second || c.Timeout > 120*time.Second || c.MaxTokens < 128 || c.MaxTokens > c.SummaryModelMaxTokens {
			return fmt.Errorf("invalid summary limits")
		}
		if c.SummaryInputBudget() <= 0 || c.PolicyVersion != "compact-v1" || c.Profile != "default" {
			return fmt.Errorf("invalid summary contract")
		}
	}
	if c.Automatic && (!c.Enabled || c.MainContextWindow == 0) {
		return fmt.Errorf("automatic compact is unavailable")
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 16 {
		return fmt.Errorf("invalid compact concurrency")
	}
	if c.RateLimitInitialBackoff < time.Second || c.RateLimitInitialBackoff > time.Minute ||
		c.RateLimitMaxBackoff < 10*time.Second || c.RateLimitMaxBackoff > time.Hour ||
		c.RateLimitInitialBackoff > c.RateLimitMaxBackoff {
		return fmt.Errorf("invalid compact rate limit backoff")
	}
	if c.SummaryWarningNodes < 0 || c.SummaryWarningBytes < 0 {
		return fmt.Errorf("invalid compact warning thresholds")
	}
	if c.MainContextWindow > 0 && !(c.LowTarget() < c.HighLimit() && c.HighLimit() < c.InputBudget()) {
		return fmt.Errorf("compact watermarks do not produce strict limits")
	}
	return nil
}

func (c RuntimeConfig) InputBudget() int64 {
	if c.MainContextWindow <= 0 {
		return 0
	}
	return c.MainContextWindow - c.ReservedOutputTokens - c.ProviderMarginTokens
}

func (c RuntimeConfig) SummaryInputBudget() int64 {
	return c.SummaryContextWindow - int64(c.MaxTokens) - c.ProviderMarginTokens
}

func (c RuntimeConfig) HighLimit() int64         { return int64(float64(c.InputBudget()) * c.HighWatermark) }
func (c RuntimeConfig) LowTarget() int64         { return int64(float64(c.InputBudget()) * c.LowWatermark) }
func (c RuntimeConfig) MaxSummaryBytes() int     { return c.MaxTokens * 4 }
func (c RuntimeConfig) ResponseByteLimit() int64 { return int64(6*c.MaxSummaryBytes() + 4096) }

// EnvironmentSnapshot 是构造主模型上下文所需的动态、版本化环境。
type EnvironmentSnapshot struct {
	System          string
	Tools           []llm.ToolDef
	Revision        uint64
	Digest          string
	ActiveRequestID string
}

// EnvironmentSource 提供 Compact 开始与提交前可重读的环境。
type EnvironmentSource interface {
	Snapshot(context.Context, string) (EnvironmentSnapshot, error)
}

// ProtectionSource 提供 session-scoped 远程工作保护集合。
type ProtectionSource interface {
	ProtectionSnapshot(string) (remoteexec.ProtectionSnapshot, error)
}

// Store 是 Engine 需要的持久化事务接口。
type Store interface {
	GetCompactSnapshot(context.Context, string) (store.CompactSnapshot, error)
	FindContextSummaryByContract(context.Context, string, int, int, string) (*store.ContextSummary, error)
	AdmitCompactAttemptWithEvent(context.Context, string, string, int64, store.LifecycleEvent) (store.SessionRuntime, error)
	FinishCompactFailure(context.Context, store.CompactFailure) (store.CompactFailureResult, error)
	CommitContextSummary(context.Context, store.CompactCommit) (store.CompactCommitResult, error)
	AppendLifecycleEvent(context.Context, store.LifecycleEvent) error
}

// CompactRequest 描述一次已通过 Actor admission 的 Compact。
type CompactRequest struct {
	SessionID string
	RequestID string
	TraceID   string
	Principal string
	Target    Target
	Trigger   Trigger
	Pending   store.PendingExpectation
}

// CompactResult 是不暴露摘要正文的成功结果。
type CompactResult struct {
	SummaryID             string
	FromSeq               int
	ToSeq                 int
	BeforeEstimatedTokens int64
	AfterEstimatedTokens  int64
	RetainedFromSeq       int
	RetainedToSeq         int
	GenerationMode        string
	ContextVersion        uint64
	Reused                bool
	TargetResult          TargetResult
}

// CompactStatusRequest 描述只读状态查询。
type CompactStatusRequest struct {
	SessionID      string
	OperationState string
}

// CompactStatus 是 Compact 的权威只读诊断。
type CompactStatus struct {
	Enabled                             bool
	Automatic                           bool
	OperationState                      string
	ContextVersion                      uint64
	HistoryGeneration                   uint64
	CompactGeneration                   uint64
	CurrentEstimatedTokens              int64
	InputBudget                         int64
	HighLimit                           int64
	LowTarget                           int64
	HardLimit                           int64
	ActiveSummaryID                     string
	ActiveFromSeq                       int
	ActiveToSeq                         int
	ActiveProviderID                    string
	ActiveModelID                       string
	ActiveProjectionVersion             string
	ActivePolicyVersion                 string
	ActiveProfile                       string
	ActiveGenerationMode                string
	SourceEstimatedTokens               int64
	SummaryEstimatedTokens              int64
	CompressionRatio                    float64
	Pending                             bool
	PendingID                           string
	PendingAttempt                      int64
	PendingNotBefore                    time.Time
	SummaryNodeCount                    int
	SummaryStorageBytes                 int64
	SummaryInputBudget                  int64
	RequiredSummaryInputEstimatedTokens int64
	CandidateGenerationMode             string
	Blocker                             ErrorCode
	Degraded                            bool
	Warnings                            []string
}

// Compactor 是 Actor、REPL 和 Face 共用的上下文压缩接口。
type Compactor interface {
	Compact(context.Context, CompactRequest) (CompactResult, error)
	Status(context.Context, CompactStatusRequest) (CompactStatus, error)
}
