package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// HookKind 是四类 Hook 通道的类型。
type HookKind string

const (
	KindGuard       HookKind = "guard"
	KindTransformer HookKind = "transformer"
	KindObserver    HookKind = "observer"
	KindAuditor     HookKind = "auditor"
)

// FailureMode 定义 Hook 失败时的处理方式。
type FailureMode string

const (
	FailureFailClosed FailureMode = "fail_closed"
	FailureFailOpen   FailureMode = "fail_open"
	FailureDegrade    FailureMode = "degrade"
)

// Capability 是 Hook 需要的额外权限。
type Capability string

const (
	CapabilityReadRaw     Capability = "read_raw"
	CapabilityReadArgs    Capability = "read_args"
	CapabilityReadResults Capability = "read_results"
	CapabilityTransform   Capability = "transform"
	CapabilityDeny        Capability = "deny"
)

// ScopeFilter 定义 Hook 的适用范围。
type ScopeFilter struct {
	ConversationIDs []string
	GroupIDs        []string
	PrincipalIDs    []string
	Sources         []string
	NodeIDs         []string
}

// Matches 检查给定的 Meta 是否匹配此 Scope。
func (f ScopeFilter) Matches(meta Meta) bool {
	return matches(f.ConversationIDs, meta.ConversationID) &&
		matches(f.GroupIDs, meta.GroupID) &&
		matches(f.PrincipalIDs, meta.PrincipalID) &&
		matches(f.Sources, meta.Source) &&
		matches(f.NodeIDs, meta.NodeID)
}

func matches(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// Registration 描述一个 Hook 的注册信息。
type Registration struct {
	ID             string
	Kind           HookKind
	Phases         []Phase
	Order          int
	Timeout        time.Duration
	FailureMode    FailureMode
	Scope          ScopeFilter
	Capabilities   []Capability
	ConfigRevision uint64
}

// RegistrySnapshot 是注册表在一个 revision 上的不可变规范视图。
type RegistrySnapshot struct {
	Revision      uint64
	Registrations []Registration
	Digest        string
}

// HasCapability 检查注册信息是否包含指定权限。
func (r Registration) HasCapability(cap Capability) bool {
	for _, candidate := range r.Capabilities {
		if candidate == cap {
			return true
		}
	}
	return false
}

// GuardBinding 保留 Guard 及其运行策略。
type GuardBinding struct {
	Registration Registration
	Guard        Guard
}

// TransformerBinding 保留 Transformer 及其运行策略。
type TransformerBinding struct {
	Registration Registration
	Transformer  Transformer
}

// ObserverBinding 保留 Observer 及其运行策略。
type ObserverBinding struct {
	Registration Registration
	Observer     Observer
}

// AuditorBinding 保留 Auditor 及其运行策略。
type AuditorBinding struct {
	Registration Registration
	Auditor      Auditor
}

// LifecycleRegistry 管理 Hook 注册、确定性排序和 Scope 过滤。
type LifecycleRegistry struct {
	mu            sync.RWMutex
	registrations map[string]Registration
	guards        map[Phase][]GuardBinding
	transformers  map[Phase][]TransformerBinding
	observers     map[Phase][]ObserverBinding
	auditors      map[Phase][]AuditorBinding
	observerQueue chan observation
	observerDrops atomic.Uint64
	sequence      atomic.Int64
	revision      atomic.Uint64
	dispatchMu    sync.RWMutex
	observerStop  chan struct{}
	observerDone  chan struct{}
	closed        bool
}

// NextEventMeta 为此 Registry 管理的节点生成单调 sequence 和新的事件身份。
func (r *LifecycleRegistry) NextEventMeta(base Meta) Meta {
	return base.EventMeta(r.sequence.Add(1))
}

type observation struct {
	event RedactedEvent
	done  chan struct{}
}

const observerQueueSize = 256

// NewRegistry 创建空的 Hook 注册表。
func NewRegistry() *LifecycleRegistry {
	registry := &LifecycleRegistry{
		registrations: make(map[string]Registration),
		guards:        make(map[Phase][]GuardBinding),
		transformers:  make(map[Phase][]TransformerBinding),
		observers:     make(map[Phase][]ObserverBinding),
		auditors:      make(map[Phase][]AuditorBinding),
		observerQueue: make(chan observation, observerQueueSize),
		observerStop:  make(chan struct{}),
		observerDone:  make(chan struct{}),
	}
	go registry.runObservers()
	return registry
}

func (r *LifecycleRegistry) runObservers() {
	defer close(r.observerDone)
	for {
		select {
		case <-r.observerStop:
			return
		case item := <-r.observerQueue:
			if item.done != nil {
				close(item.done)
				continue
			}
			for _, binding := range r.ObserverBindingsForPhase(item.event.Phase, item.event.Meta) {
				_ = runObserver(context.Background(), binding, ObserverView(item.event, binding.Registration))
			}
		}
	}
}

// RegisterGuard 注册一个 Guard。
func (r *LifecycleRegistry) RegisterGuard(reg Registration, guard Guard) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg = normalizeRegistration(reg, KindGuard)
	if err := r.validateRegistrationLocked(reg, KindGuard, guard); err != nil {
		return err
	}
	r.registrations[reg.ID] = reg
	for _, phase := range reg.Phases {
		r.guards[phase] = append(r.guards[phase], GuardBinding{Registration: reg, Guard: guard})
		sort.SliceStable(r.guards[phase], func(i, j int) bool {
			return bindingLess(r.guards[phase][i].Registration, r.guards[phase][j].Registration)
		})
	}
	r.revision.Add(1)
	return nil
}

// RegisterTransformer 注册一个 Transformer。
func (r *LifecycleRegistry) RegisterTransformer(reg Registration, transformer Transformer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg = normalizeRegistration(reg, KindTransformer)
	if err := r.validateRegistrationLocked(reg, KindTransformer, transformer); err != nil {
		return err
	}
	r.registrations[reg.ID] = reg
	for _, phase := range reg.Phases {
		r.transformers[phase] = append(r.transformers[phase], TransformerBinding{Registration: reg, Transformer: transformer})
		sort.SliceStable(r.transformers[phase], func(i, j int) bool {
			return bindingLess(r.transformers[phase][i].Registration, r.transformers[phase][j].Registration)
		})
	}
	r.revision.Add(1)
	return nil
}

// RegisterObserver 注册一个 Observer。
func (r *LifecycleRegistry) RegisterObserver(reg Registration, observer Observer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg = normalizeRegistration(reg, KindObserver)
	if err := r.validateRegistrationLocked(reg, KindObserver, observer); err != nil {
		return err
	}
	r.registrations[reg.ID] = reg
	for _, phase := range reg.Phases {
		r.observers[phase] = append(r.observers[phase], ObserverBinding{Registration: reg, Observer: observer})
		sort.SliceStable(r.observers[phase], func(i, j int) bool {
			return bindingLess(r.observers[phase][i].Registration, r.observers[phase][j].Registration)
		})
	}
	r.revision.Add(1)
	return nil
}

// RegisterAuditor 注册一个 Auditor。
func (r *LifecycleRegistry) RegisterAuditor(reg Registration, auditor Auditor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg = normalizeRegistration(reg, KindAuditor)
	if err := r.validateRegistrationLocked(reg, KindAuditor, auditor); err != nil {
		return err
	}
	r.registrations[reg.ID] = reg
	for _, phase := range reg.Phases {
		r.auditors[phase] = append(r.auditors[phase], AuditorBinding{Registration: reg, Auditor: auditor})
		sort.SliceStable(r.auditors[phase], func(i, j int) bool {
			return bindingLess(r.auditors[phase][i].Registration, r.auditors[phase][j].Registration)
		})
	}
	r.revision.Add(1)
	return nil
}

// Snapshot 返回注册信息的深拷贝、单调 revision 和规范摘要。
func (r *LifecycleRegistry) Snapshot() RegistrySnapshot {
	r.mu.RLock()
	registrations := make([]Registration, 0, len(r.registrations))
	for _, registration := range r.registrations {
		registrations = append(registrations, cloneRegistration(registration))
	}
	revision := r.revision.Load()
	r.mu.RUnlock()
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].ID < registrations[j].ID })
	encoded, _ := json.Marshal(registrations)
	digest := sha256.Sum256(append([]byte("half-pi:lifecycle-registry:v1\x00"), encoded...))
	return RegistrySnapshot{Revision: revision, Registrations: registrations, Digest: hex.EncodeToString(digest[:])}
}

func cloneRegistration(registration Registration) Registration {
	registration.Phases = append([]Phase(nil), registration.Phases...)
	registration.Capabilities = append([]Capability(nil), registration.Capabilities...)
	registration.Scope.ConversationIDs = append([]string(nil), registration.Scope.ConversationIDs...)
	registration.Scope.GroupIDs = append([]string(nil), registration.Scope.GroupIDs...)
	registration.Scope.PrincipalIDs = append([]string(nil), registration.Scope.PrincipalIDs...)
	registration.Scope.Sources = append([]string(nil), registration.Scope.Sources...)
	registration.Scope.NodeIDs = append([]string(nil), registration.Scope.NodeIDs...)
	return registration
}

// GuardBindingsForPhase 返回带运行策略的 Guard 快照。
func (r *LifecycleRegistry) GuardBindingsForPhase(phase Phase, meta Meta) []GuardBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return filterBindings(r.guards[phase], meta, func(binding GuardBinding) Registration { return binding.Registration })
}

// TransformerBindingsForPhase 返回带运行策略的 Transformer 快照。
func (r *LifecycleRegistry) TransformerBindingsForPhase(phase Phase, meta Meta) []TransformerBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return filterBindings(r.transformers[phase], meta, func(binding TransformerBinding) Registration { return binding.Registration })
}

// ObserverBindingsForPhase 返回带运行策略的 Observer 快照。
func (r *LifecycleRegistry) ObserverBindingsForPhase(phase Phase, meta Meta) []ObserverBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return filterBindings(r.observers[phase], meta, func(binding ObserverBinding) Registration { return binding.Registration })
}

// AuditorBindingsForPhase 返回带运行策略的 Auditor 快照。
func (r *LifecycleRegistry) AuditorBindingsForPhase(phase Phase, meta Meta) []AuditorBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return filterBindings(r.auditors[phase], meta, func(binding AuditorBinding) Registration { return binding.Registration })
}

// GuardsForPhase 返回兼容旧调用方的 Guard 列表。
func (r *LifecycleRegistry) GuardsForPhase(phase Phase, meta Meta) []Guard {
	bindings := r.GuardBindingsForPhase(phase, meta)
	result := make([]Guard, len(bindings))
	for i := range bindings {
		result[i] = bindings[i].Guard
	}
	return result
}

// TransformersForPhase 返回兼容旧调用方的 Transformer 列表。
func (r *LifecycleRegistry) TransformersForPhase(phase Phase, meta Meta) []Transformer {
	bindings := r.TransformerBindingsForPhase(phase, meta)
	result := make([]Transformer, len(bindings))
	for i := range bindings {
		result[i] = bindings[i].Transformer
	}
	return result
}

// ObserversForPhase 返回兼容旧调用方的 Observer 列表。
func (r *LifecycleRegistry) ObserversForPhase(phase Phase, meta Meta) []Observer {
	bindings := r.ObserverBindingsForPhase(phase, meta)
	result := make([]Observer, len(bindings))
	for i := range bindings {
		result[i] = bindings[i].Observer
	}
	return result
}

// AuditorsForPhase 返回兼容旧调用方的 Auditor 列表。
func (r *LifecycleRegistry) AuditorsForPhase(phase Phase, meta Meta) []Auditor {
	bindings := r.AuditorBindingsForPhase(phase, meta)
	result := make([]Auditor, len(bindings))
	for i := range bindings {
		result[i] = bindings[i].Auditor
	}
	return result
}

func filterBindings[T any](entries []T, meta Meta, registration func(T) Registration) []T {
	result := make([]T, 0, len(entries))
	for _, entry := range entries {
		if registration(entry).Scope.Matches(meta) {
			result = append(result, entry)
		}
	}
	return result
}

func normalizeRegistration(reg Registration, kind HookKind) Registration {
	if reg.Timeout <= 0 {
		switch kind {
		case KindObserver:
			reg.Timeout = 100 * time.Millisecond
		default:
			reg.Timeout = 2 * time.Second
		}
	}
	if reg.FailureMode == "" {
		if kind == KindObserver {
			reg.FailureMode = FailureFailOpen
		} else {
			reg.FailureMode = FailureFailClosed
		}
	}
	reg.Phases = append([]Phase(nil), reg.Phases...)
	reg.Capabilities = append([]Capability(nil), reg.Capabilities...)
	reg.Scope = cloneRegistration(reg).Scope
	return reg
}

func (r *LifecycleRegistry) validateRegistrationLocked(reg Registration, kind HookKind, impl any) error {
	if reg.ID == "" {
		return fmt.Errorf("hook id cannot be empty")
	}
	if reg.Kind != kind {
		return fmt.Errorf("hook kind mismatch: expected %s, got %s", kind, reg.Kind)
	}
	if isNilImplementation(impl) {
		return fmt.Errorf("hook %s implementation cannot be nil", reg.ID)
	}
	if _, exists := r.registrations[reg.ID]; exists {
		return fmt.Errorf("duplicate hook registration: %s", reg.ID)
	}
	if len(reg.Phases) == 0 {
		return fmt.Errorf("hook %s must specify at least one phase", reg.ID)
	}
	if reg.Timeout <= 0 {
		return fmt.Errorf("hook %s timeout must be positive", reg.ID)
	}
	if !validFailureMode(reg.FailureMode) {
		return fmt.Errorf("hook %s has invalid failure mode %q", reg.ID, reg.FailureMode)
	}
	for _, phase := range reg.Phases {
		if !IsSupportedPhase(phase) {
			return fmt.Errorf("hook %s has unsupported phase %q", reg.ID, phase)
		}
		if !phaseSupportsKind(phase, kind) {
			return fmt.Errorf("hook %s kind %s is not supported at phase %s", reg.ID, kind, phase)
		}
	}
	for _, capability := range reg.Capabilities {
		if !validCapability(capability) {
			return fmt.Errorf("hook %s has invalid capability %q", reg.ID, capability)
		}
	}
	if kind == KindTransformer && !reg.HasCapability(CapabilityTransform) && len(reg.Capabilities) > 0 {
		return fmt.Errorf("transformer %s requires transform capability", reg.ID)
	}
	if kind == KindGuard && reg.FailureMode != FailureFailClosed {
		return fmt.Errorf("guard %s must fail closed", reg.ID)
	}
	return nil
}

func bindingLess(a, b Registration) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return a.ID < b.ID
}

func isNilImplementation(impl any) bool {
	if impl == nil {
		return true
	}
	value := reflect.ValueOf(impl)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func validFailureMode(mode FailureMode) bool {
	return mode == FailureFailClosed || mode == FailureFailOpen || mode == FailureDegrade
}

func validCapability(capability Capability) bool {
	switch capability {
	case CapabilityReadRaw, CapabilityReadArgs, CapabilityReadResults, CapabilityTransform, CapabilityDeny:
		return true
	default:
		return false
	}
}

func phaseSupportsKind(phase Phase, kind HookKind) bool {
	switch kind {
	case KindTransformer:
		return phase == PhaseMessageBeforeAccept || phase == PhaseModelBeforeRequest ||
			phase == PhaseAssistantBeforeDeliver || phase == PhaseToolBeforeFreeze ||
			phase == PhaseToolResultBeforeCommit
	case KindGuard:
		return phase == PhaseMessageBeforeAccept || phase == PhaseModelBeforeRequest ||
			phase == PhaseAssistantBeforeDeliver || phase == PhaseToolBeforeExecute
	case KindObserver, KindAuditor:
		return true
	default:
		return false
	}
}
