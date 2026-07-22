// Package management 实现 Mind 本地管理权威。
package management

import (
	"fmt"
	"os"
	"os/user"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

const (
	SourceREPL       = "repl"
	SourceIPC        = "ipc"
	SourceOfflineCLI = "offline_cli"

	ProfileObserver = "observer"
	ProfileOperator = "operator"
)

// RequestMeta 标识一次本地管理请求。
type RequestMeta struct {
	RequestID string `json:"request_id"`
	Source    string `json:"source"`
	Actor     string `json:"actor,omitempty"`
}

// Runtime 描述正在运行的 Mind runtime。
type Runtime struct {
	PID        int
	StartedAt  time.Time
	Mode       string
	HubEnabled bool
	WSURL      string
}

// Service 是本地管理操作的共享入口。
type Service struct {
	store *store.Store
	hub   *hub.Hub
	rt    Runtime
	mu    sync.Mutex
	rtMu  sync.RWMutex
}

// New 创建管理服务。
func New(s *store.Store, wsHub *hub.Hub, rt Runtime) *Service {
	if rt.PID == 0 {
		rt.PID = os.Getpid()
	}
	if rt.StartedAt.IsZero() {
		rt.StartedAt = time.Now().UTC()
	}
	return &Service{store: s, hub: wsHub, rt: rt}
}

// UpdateRuntime 更新可由管理 IPC 查询的 runtime 快照。
func (s *Service) UpdateRuntime(rt Runtime) {
	s.rtMu.Lock()
	s.rt = rt
	s.rtMu.Unlock()
}

// CredentialDTO 是不含秘密的凭据摘要。
type CredentialDTO struct {
	ID        int64     `json:"id"`
	Label     string    `json:"label"`
	Scopes    []string  `json:"scopes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SecretCredentialDTO 只在 add 成功响应中返回。
type SecretCredentialDTO struct {
	ID             int64     `json:"id"`
	Label          string    `json:"label"`
	Token          string    `json:"token"`
	ApplicationKey string    `json:"application_key"`
	Scopes         []string  `json:"scopes,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// RemoveResult 是凭据撤销结果。
type RemoveResult struct {
	ID           int64  `json:"id"`
	Label        string `json:"label"`
	Type         string `json:"type"`
	Disconnected bool   `json:"disconnected"`
}

// StatusResult 是 Mind runtime 状态。
type StatusResult struct {
	State      string    `json:"state"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
	Mode       string    `json:"mode"`
	HubEnabled bool      `json:"hub_enabled"`
	WSURL      string    `json:"ws_url"`
	PeerCount  int       `json:"peer_count"`
}

// PeerDTO 是在线 peer 的无秘密摘要。
type PeerDTO struct {
	Type        string    `json:"type"`
	Label       string    `json:"label"`
	ConnectedAt time.Time `json:"connected_at"`
}

// ExpandProfile 返回 profile 对应的规范 Face scope 集合。
func ExpandProfile(profile string) ([]protocol.FaceScope, error) {
	switch profile {
	case ProfileObserver:
		return store.CanonicalFaceScopes([]protocol.FaceScope{
			protocol.FaceScopeSessionsRead,
			protocol.FaceScopeRunsRead,
			protocol.FaceScopeHandsRead,
			protocol.FaceScopeTasksRead,
		})
	case ProfileOperator:
		return store.CanonicalFaceScopes([]protocol.FaceScope{
			protocol.FaceScopeChat,
			protocol.FaceScopeSessionsRead,
			protocol.FaceScopeSessionsWrite,
			protocol.FaceScopeRunsRead,
			protocol.FaceScopeRunsCancel,
			protocol.FaceScopeRunsOutput,
			protocol.FaceScopeApprove,
			protocol.FaceScopeHandsRead,
			protocol.FaceScopeTasksRead,
			protocol.FaceScopeTasksCancel,
		})
	default:
		return nil, errorf("invalid_argument", "unknown profile %q", profile)
	}
}

// ParseScopes parses comma-separated Face scopes into canonical protocol scopes.
func ParseScopes(raw string) ([]protocol.FaceScope, error) {
	if raw == "" {
		return nil, errorf("invalid_argument", "face scopes must not be empty")
	}
	parts := strings.Split(raw, ",")
	scopes := make([]protocol.FaceScope, 0, len(parts))
	for _, part := range parts {
		scope := strings.TrimSpace(part)
		if scope == "" {
			return nil, errorf("invalid_argument", "face scopes must not contain empty values")
		}
		scopes = append(scopes, protocol.FaceScope(scope))
	}
	canonical, err := store.CanonicalFaceScopes(scopes)
	if err != nil {
		return nil, wrap("invalid_argument", err.Error(), err)
	}
	return canonical, nil
}

// AddHand 创建 Hand 凭据。
func (s *Service) AddHand(meta RequestMeta, label string) (SecretCredentialDTO, error) {
	return s.addHand(meta, label)
}

// ListHands 返回 Hand 凭据摘要。
func (s *Service) ListHands() ([]CredentialDTO, error) {
	credentials, err := s.store.ListHandCredentials()
	if err != nil {
		return nil, wrap("internal", "list hand credentials", err)
	}
	out := make([]CredentialDTO, len(credentials))
	for i, credential := range credentials {
		out[i] = credentialDTO(credential, nil)
	}
	return out, nil
}

// RemoveHand 撤销 Hand 凭据。
func (s *Service) RemoveHand(meta RequestMeta, selector, value string) (RemoveResult, error) {
	return s.removeCredential(meta, hub.PeerHand, selector, value)
}

// AddFace 创建 Face 凭据。
func (s *Service) AddFace(meta RequestMeta, label string, scopes []protocol.FaceScope) (SecretCredentialDTO, error) {
	return s.addFace(meta, label, scopes)
}

// ListFaces 返回 Face 凭据摘要。
func (s *Service) ListFaces() ([]CredentialDTO, error) {
	credentials, err := s.store.ListFaceTokens()
	if err != nil {
		return nil, wrap("internal", "list face credentials", err)
	}
	out := make([]CredentialDTO, len(credentials))
	for i, credential := range credentials {
		out[i] = credentialDTO(credential.Credential, scopeStrings(credential.Scopes))
	}
	return out, nil
}

// RemoveFace 撤销 Face 凭据。
func (s *Service) RemoveFace(meta RequestMeta, selector, value string) (RemoveResult, error) {
	return s.removeCredential(meta, hub.PeerFace, selector, value)
}

// Status 返回当前 Mind runtime 状态。
func (s *Service) Status() StatusResult {
	s.rtMu.RLock()
	rt := s.rt
	s.rtMu.RUnlock()
	count := 0
	if s.hub != nil && rt.HubEnabled {
		count = s.hub.Count()
	}
	return StatusResult{
		State: "running", PID: rt.PID, StartedAt: rt.StartedAt, Mode: rt.Mode,
		HubEnabled: rt.HubEnabled, WSURL: rt.WSURL, PeerCount: count,
	}
}

// Peers 返回在线 peer 摘要。
func (s *Service) Peers() ([]PeerDTO, error) {
	s.rtMu.RLock()
	hubEnabled := s.rt.HubEnabled
	s.rtMu.RUnlock()
	if !hubEnabled || s.hub == nil {
		return nil, errorf("hub_disabled", "Hub is disabled")
	}
	peers := make([]PeerDTO, 0)
	for _, peer := range s.hub.PeersByType(hub.PeerFace) {
		peers = append(peers, PeerDTO{Type: string(peer.Type), Label: peer.ID, ConnectedAt: peer.JoinedAt})
	}
	for _, peer := range s.hub.PeersByType(hub.PeerHand) {
		peers = append(peers, PeerDTO{Type: string(peer.Type), Label: peer.ID, ConnectedAt: peer.JoinedAt})
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Type != peers[j].Type {
			return peers[i].Type < peers[j].Type
		}
		return peers[i].Label < peers[j].Label
	})
	return peers, nil
}

func (s *Service) addHand(meta RequestMeta, label string) (SecretCredentialDTO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	audit := s.audit(meta, "hand.add", "hand", "", label)
	credential, err := s.store.AddHandCredentialAudited(label, audit)
	if err != nil {
		if auditErr := s.auditFailure(audit, codeForMutationError(err), err); auditErr != nil {
			return SecretCredentialDTO{}, wrap("internal", "write failure audit", auditErr)
		}
		return SecretCredentialDTO{}, wrap(codeForMutationError(err), err.Error(), err)
	}
	return secretCredentialDTO(*credential, nil), nil
}

func (s *Service) addFace(meta RequestMeta, label string, scopes []protocol.FaceScope) (SecretCredentialDTO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	audit := s.audit(meta, "face.add", "face", "", label)
	credential, err := s.store.AddFaceTokenAudited(label, scopes, audit)
	if err != nil {
		if auditErr := s.auditFailure(audit, codeForMutationError(err), err); auditErr != nil {
			return SecretCredentialDTO{}, wrap("internal", "write failure audit", auditErr)
		}
		return SecretCredentialDTO{}, wrap(codeForMutationError(err), err.Error(), err)
	}
	return secretCredentialDTO(credential.Credential, scopeStrings(credential.Scopes)), nil
}

func (s *Service) removeCredential(meta RequestMeta, peerType hub.PeerType, selector, value string) (RemoveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	selector = strings.TrimPrefix(selector, "--")
	targetType := string(peerType)
	audit := s.audit(meta, targetType+".remove", targetType, "", value)
	var (
		removed store.RemovedCredential
		err     error
	)
	switch peerType {
	case hub.PeerHand:
		removed, err = s.store.RemoveHandCredentialAudited(selector, value, audit)
	case hub.PeerFace:
		removed, err = s.store.RemoveFaceTokenAudited(selector, value, audit)
	default:
		err = fmt.Errorf("invalid credential type")
	}
	if err != nil {
		if auditErr := s.auditFailure(audit, codeForMutationError(err), err); auditErr != nil {
			return RemoveResult{}, wrap("internal", "write failure audit", auditErr)
		}
		return RemoveResult{}, wrap(codeForMutationError(err), err.Error(), err)
	}
	disconnected := false
	if s.hub != nil {
		disconnected = s.hub.PeerByType(peerType, removed.Label) != nil
		s.hub.RemoveByType(peerType, removed.Label)
	}
	return RemoveResult{ID: removed.ID, Label: removed.Label, Type: targetType, Disconnected: disconnected}, nil
}

func (s *Service) audit(meta RequestMeta, operation, targetType, targetID, targetLabel string) store.ManagementAudit {
	actor := meta.Actor
	if actor == "" {
		actor = localActor()
	}
	return store.ManagementAudit{
		RequestID: meta.RequestID, Source: meta.Source, Actor: actor, Operation: operation,
		TargetType: targetType, TargetID: targetID, TargetLabel: targetLabel, CreatedAt: time.Now().UTC(),
	}
}

func localActor() string {
	current, err := user.Current()
	if err != nil || current.Uid == "" {
		return "local-user"
	}
	return "uid:" + current.Uid
}

func (s *Service) auditFailure(audit store.ManagementAudit, code string, err error) error {
	audit.Status = "failed"
	audit.Code = code
	audit.Message = err.Error()
	return s.store.AddManagementAudit(audit)
}

func codeForMutationError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unique"):
		return "conflict"
	case strings.Contains(msg, "not found"):
		return "not_found"
	case strings.Contains(msg, "invalid") || strings.Contains(msg, "unknown") || strings.Contains(msg, "empty"):
		return "invalid_argument"
	default:
		return "internal"
	}
}

func credentialDTO(credential store.Credential, scopes []string) CredentialDTO {
	return CredentialDTO{ID: credential.ID, Label: credential.Label, Scopes: scopes, CreatedAt: credential.CreatedAt}
}

func secretCredentialDTO(credential store.Credential, scopes []string) SecretCredentialDTO {
	return SecretCredentialDTO{
		ID: credential.ID, Label: credential.Label, Token: credential.Token,
		ApplicationKey: credential.ApplicationKey, Scopes: scopes, CreatedAt: credential.CreatedAt,
	}
}

func scopeStrings(scopes []protocol.FaceScope) []string {
	values := make([]string, len(scopes))
	for i, scope := range scopes {
		values[i] = string(scope)
	}
	return values
}

// ValidateConfig performs local config validation without network access.
func ValidateConfig(cfg *config.Config) error {
	if cfg.Server.Host == "" {
		return errorf("invalid_config", "server.host is required")
	}
	if cfg.Server.Port < 0 || cfg.Server.Port > 65535 {
		return errorf("invalid_config", "server.port must be between 0 and 65535")
	}
	if cfg.LLM.DefaultProvider == "" || cfg.LLM.DefaultModel == "" {
		return errorf("invalid_config", "llm.default_provider and llm.default_model are required")
	}
	providers := make(map[string]config.ProviderCfg, len(cfg.LLM.Providers))
	for _, provider := range cfg.LLM.Providers {
		if provider.Name == "" || provider.Adapter == "" {
			return errorf("invalid_config", "llm providers require name and adapter")
		}
		if _, exists := providers[provider.Name]; exists {
			return errorf("invalid_config", "duplicate provider %q", provider.Name)
		}
		switch provider.Adapter {
		case "openai", "gemini", "anthropic":
			if provider.BaseURL == "" {
				return errorf("invalid_config", "provider %q requires base_url", provider.Name)
			}
		case "scripted":
			if provider.ScriptPath == "" {
				return errorf("invalid_config", "provider %q requires script_path", provider.Name)
			}
		default:
			return errorf("invalid_config", "provider %q uses unknown adapter %q", provider.Name, provider.Adapter)
		}
		providers[provider.Name] = provider
	}
	if _, ok := providers[cfg.LLM.DefaultProvider]; !ok {
		return errorf("invalid_config", "default provider %q is not defined", cfg.LLM.DefaultProvider)
	}
	models := make(map[string]config.ModelCfg, len(cfg.LLM.Models))
	for _, model := range cfg.LLM.Models {
		if model.ID == "" || model.Provider == "" {
			return errorf("invalid_config", "llm models require id and provider")
		}
		if _, exists := models[model.ID]; exists {
			return errorf("invalid_config", "duplicate model %q", model.ID)
		}
		if _, ok := providers[model.Provider]; !ok {
			return errorf("invalid_config", "model %q references missing provider %q", model.ID, model.Provider)
		}
		if model.ContextWindow < 0 || model.MaxTokens < 0 {
			return errorf("invalid_config", "model %q token limits must not be negative", model.ID)
		}
		models[model.ID] = model
	}
	if _, ok := models[cfg.LLM.DefaultModel]; !ok {
		return errorf("invalid_config", "default model %q is not defined", cfg.LLM.DefaultModel)
	}
	if models[cfg.LLM.DefaultModel].Provider != cfg.LLM.DefaultProvider {
		return errorf("invalid_config", "default model %q does not use default provider %q", cfg.LLM.DefaultModel, cfg.LLM.DefaultProvider)
	}
	compact := cfg.Compact
	if compact == (config.CompactCfg{}) {
		compact = config.DefaultCompactCfg()
	}
	if compact.ProviderMarginTokens < 0 || compact.ReservedOutputTokens < 0 ||
		compact.SummaryWarningNodes < 0 || compact.SummaryWarningBytes < 0 {
		return errorf("invalid_config", "compact token margins and warning thresholds must not be negative")
	}
	if compact.LowWatermark < 0.20 || compact.HighWatermark > 0.95 || compact.LowWatermark >= compact.HighWatermark {
		return errorf("invalid_config", "compact watermarks must satisfy 0.20 <= low < high <= 0.95")
	}
	if compact.MaxConcurrent < 1 || compact.MaxConcurrent > 16 {
		return errorf("invalid_config", "compact.max_concurrent must be between 1 and 16")
	}
	if compact.RateLimitInitialBackoffMS < 1000 || compact.RateLimitInitialBackoffMS > 60_000 ||
		compact.RateLimitMaxBackoffMS < 10_000 || compact.RateLimitMaxBackoffMS > 3_600_000 ||
		compact.RateLimitInitialBackoffMS > compact.RateLimitMaxBackoffMS {
		return errorf("invalid_config", "compact rate limit backoff is invalid")
	}
	mainModel := models[cfg.LLM.DefaultModel]
	if mainModel.ContextWindow > 0 {
		reserved := compact.ReservedOutputTokens
		if reserved == 0 {
			reserved = mainModel.MaxTokens
		}
		if reserved < 1 || reserved > mainModel.MaxTokens {
			return errorf("invalid_config", "compact.reserved_output_tokens must resolve within the main model output limit")
		}
		if mainModel.ContextWindow <= reserved+compact.ProviderMarginTokens {
			return errorf("invalid_config", "main model context_window leaves no input budget")
		}
		inputBudget := mainModel.ContextWindow - reserved - compact.ProviderMarginTokens
		if int(float64(inputBudget)*compact.LowWatermark) >= int(float64(inputBudget)*compact.HighWatermark) ||
			int(float64(inputBudget)*compact.HighWatermark) >= inputBudget {
			return errorf("invalid_config", "compact watermarks do not produce strictly increasing limits")
		}
	}
	if compact.Enabled {
		if compact.Provider == "" || compact.Model == "" {
			return errorf("invalid_config", "compact provider and model are required when enabled")
		}
		summaryModel, ok := models[compact.Model]
		if !ok {
			return errorf("invalid_config", "compact model %q is not defined", compact.Model)
		}
		if summaryModel.Provider != compact.Provider {
			return errorf("invalid_config", "compact model %q does not use provider %q", compact.Model, compact.Provider)
		}
		if compact.TimeoutMS < 1000 || compact.TimeoutMS > 120_000 {
			return errorf("invalid_config", "compact.timeout_ms must be between 1000 and 120000")
		}
		if compact.MaxTokens < 128 || compact.MaxTokens > summaryModel.MaxTokens {
			return errorf("invalid_config", "compact.max_tokens must be between 128 and the summary model output limit")
		}
		if summaryModel.ContextWindow <= compact.MaxTokens+compact.ProviderMarginTokens {
			return errorf("invalid_config", "compact model context_window leaves no input budget")
		}
		if compact.PolicyVersion != "compact-v1" || compact.Profile != "default" {
			return errorf("invalid_config", "compact policy_version/profile is not supported")
		}
	}
	if compact.Automatic && (!compact.Enabled || mainModel.ContextWindow == 0) {
		return errorf("invalid_config", "compact.automatic requires enabled compact and a main model context_window")
	}
	if cfg.Security.Review.Enabled {
		review := cfg.Security.Review
		if review.Model == "" {
			return errorf("invalid_config", "security.review.model is required when review is enabled")
		}
		model, ok := models[review.Model]
		if !ok {
			return errorf("invalid_config", "security review model %q is not defined", review.Model)
		}
		if review.Provider != "" && review.Provider != model.Provider {
			return errorf("invalid_config", "security review model %q does not use provider %q", review.Model, review.Provider)
		}
		if review.TimeoutMS < 100 || review.TimeoutMS > 30_000 {
			return errorf("invalid_config", "security.review.timeout_ms must be between 100 and 30000")
		}
		if review.MaxTokens < 1 || review.MaxTokens > 4096 {
			return errorf("invalid_config", "security.review.max_tokens must be between 1 and 4096")
		}
		if review.PolicyVersion == "" {
			return errorf("invalid_config", "security.review.policy_version is required")
		}
		if review.Profile == "" {
			return errorf("invalid_config", "security.review.profile is required")
		}
	}
	return nil
}
