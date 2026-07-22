package lifecycle

import (
	"context"
	"fmt"
	"reflect"
)

// Transform 按注册顺序执行一个阶段的 Transformer。
func (r *LifecycleRegistry) Transform(ctx context.Context, phase Phase, action MutableAction) (MutableAction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	current := cloneMutableAction(action)
	for _, binding := range r.TransformerBindingsForPhase(phase, action.Meta) {
		result, err := runTransformer(ctx, binding, cloneMutableAction(current))
		if err != nil {
			if binding.Registration.FailureMode == FailureFailOpen {
				continue
			}
			return MutableAction{}, err
		}
		if result.Meta != current.Meta || result.Kind != current.Kind {
			return MutableAction{}, fmt.Errorf("transformer %s changed action identity", binding.Registration.ID)
		}
		current = result
	}
	return current, nil
}

// Guard 按单调收紧规则执行一个阶段的 Guard。
func (r *LifecycleRegistry) Guard(ctx context.Context, phase Phase, action FrozenAction) (Verdict, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bindings := r.GuardBindingsForPhase(phase, action.Meta)
	verdicts := make([]Verdict, 0, len(bindings))
	for _, binding := range bindings {
		view := CloneFrozenAction(action)
		if !binding.Registration.HasCapability(CapabilityReadRaw) && !binding.Registration.HasCapability(CapabilityReadArgs) {
			view.Sensitive = nil
		}
		verdict, err := runGuard(ctx, binding, view)
		if err != nil {
			return VerdictDeny, err
		}
		verdicts = append(verdicts, verdict)
	}
	return MergeVerdicts(verdicts...), nil
}

// Publish 将脱敏事件加入有界 Observer 队列；Observer 失败永不改变业务事实。
func (r *LifecycleRegistry) Publish(ctx context.Context, event RedactedEvent) {
	r.dispatchMu.RLock()
	defer r.dispatchMu.RUnlock()
	if r.closed {
		return
	}
	event.Sensitive = cloneStringMap(event.Sensitive)
	select {
	case r.observerQueue <- observation{event: event}:
	default:
		r.observerDrops.Add(1)
	}
}

// FlushObservers 等待此前成功入队的 Observer 事件处理完成。
// 它用于测试和优雅停机，不应放在业务状态锁内调用。
func (r *LifecycleRegistry) FlushObservers(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.dispatchMu.RLock()
	defer r.dispatchMu.RUnlock()
	if r.closed {
		return nil
	}
	return r.flushObserversLocked(ctx)
}

func (r *LifecycleRegistry) flushObserversLocked(ctx context.Context) error {
	done := make(chan struct{})
	select {
	case r.observerQueue <- observation{done: done}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseObservers 排空已接纳的观察事件并停止 Observer worker。
// 关闭后新的观察事件会被忽略，Guard、Transformer 和 Auditor 注册快照仍可读取。
func (r *LifecycleRegistry) CloseObservers(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.dispatchMu.Lock()
	if r.closed {
		r.dispatchMu.Unlock()
		select {
		case <-r.observerDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.closed = true
	flushErr := r.flushObserversLocked(ctx)
	close(r.observerStop)
	r.dispatchMu.Unlock()
	if flushErr != nil {
		return flushErr
	}
	select {
	case <-r.observerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DroppedObserverEvents 返回因有界队列满而丢弃的观察事件数。
func (r *LifecycleRegistry) DroppedObserverEvents() uint64 {
	return r.observerDrops.Load()
}

// Commit 将权威 mutation 交给 Auditor，并执行其失败策略。
func (r *LifecycleRegistry) Commit(ctx context.Context, phase Phase, mutation AuditMutation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, binding := range r.AuditorBindingsForPhase(phase, mutation.Meta) {
		if err := runAuditor(ctx, binding, CloneAuditMutation(mutation)); err != nil && binding.Registration.FailureMode == FailureFailClosed {
			return err
		}
	}
	return nil
}

// RequiresBufferedDelivery 判断完整响应 hook 是否要求在审核前缓冲输出。
func (r *LifecycleRegistry) RequiresBufferedDelivery(meta Meta) bool {
	return len(r.TransformerBindingsForPhase(PhaseAssistantBeforeDeliver, meta)) > 0 ||
		len(r.GuardBindingsForPhase(PhaseAssistantBeforeDeliver, meta)) > 0
}

func runGuard(ctx context.Context, binding GuardBinding, action FrozenAction) (Verdict, error) {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = WithHookContext(hookCtx)
	type response struct {
		verdict Verdict
		panic   any
	}
	responses := make(chan response, 1)
	go func() {
		result := response{}
		defer func() {
			result.panic = recover()
			responses <- result
		}()
		result.verdict = binding.Guard.Evaluate(hookCtx, action)
	}()
	select {
	case result := <-responses:
		if result.panic != nil {
			return VerdictDeny, fmt.Errorf("guard %s panicked: %v", binding.Registration.ID, result.panic)
		}
		return result.verdict, nil
	case <-hookCtx.Done():
		return VerdictDeny, fmt.Errorf("guard %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func runTransformer(ctx context.Context, binding TransformerBinding, action MutableAction) (MutableAction, error) {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = WithHookContext(hookCtx)
	type response struct {
		action MutableAction
		err    error
		panic  any
	}
	responses := make(chan response, 1)
	go func() {
		result := response{}
		defer func() {
			result.panic = recover()
			responses <- result
		}()
		result.action, result.err = binding.Transformer.Transform(hookCtx, action)
	}()
	select {
	case result := <-responses:
		if result.panic != nil {
			return MutableAction{}, fmt.Errorf("transformer %s panicked: %v", binding.Registration.ID, result.panic)
		}
		return result.action, result.err
	case <-hookCtx.Done():
		return MutableAction{}, fmt.Errorf("transformer %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func runObserver(ctx context.Context, binding ObserverBinding, event RedactedEvent) error {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = WithHookContext(hookCtx)
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		binding.Observer.Observe(hookCtx, event)
	}()
	select {
	case recovered := <-done:
		if recovered != nil {
			return fmt.Errorf("observer %s panicked: %v", binding.Registration.ID, recovered)
		}
		return nil
	case <-hookCtx.Done():
		return fmt.Errorf("observer %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func runAuditor(ctx context.Context, binding AuditorBinding, mutation AuditMutation) error {
	hookCtx, cancel := context.WithTimeout(ctx, binding.Registration.Timeout)
	defer cancel()
	hookCtx = WithHookContext(hookCtx)
	responses := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("auditor %s panicked: %v", binding.Registration.ID, recovered)
			}
			responses <- err
		}()
		err = binding.Auditor.Commit(hookCtx, mutation)
	}()
	select {
	case err := <-responses:
		return err
	case <-hookCtx.Done():
		return fmt.Errorf("auditor %s: %w", binding.Registration.ID, hookCtx.Err())
	}
}

func cloneMutableAction(action MutableAction) MutableAction {
	fields := cloneStringMap(action.Fields)
	return MutableAction{Meta: action.Meta, Kind: action.Kind, Fields: fields}
}

// CloneMutableAction 为每个 Hook 创建保留具体 Go 类型的递归副本。
// MutableAction 字段必须是无环的结构化数据；Hook 不得通过共享切片、map 或指针修改其他 Hook 的视图。
func CloneMutableAction(action MutableAction) MutableAction {
	return cloneMutableAction(action)
}

func cloneStringMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = cloneValue(value)
	}
	return clone
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	return cloneReflectValue(reflect.ValueOf(value)).Interface()
}

func cloneReflectValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		clone := reflect.New(value.Type()).Elem()
		if !value.IsNil() {
			clone.Set(cloneReflectValue(value.Elem()))
		}
		return clone
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.New(value.Type().Elem())
		clone.Elem().Set(cloneReflectValue(value.Elem()))
		return clone
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			clone.SetMapIndex(iterator.Key(), cloneReflectValue(iterator.Value()))
		}
		return clone
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := range value.Len() {
			clone.Index(i).Set(cloneReflectValue(value.Index(i)))
		}
		return clone
	case reflect.Array:
		clone := reflect.New(value.Type()).Elem()
		for i := range value.Len() {
			clone.Index(i).Set(cloneReflectValue(value.Index(i)))
		}
		return clone
	case reflect.Struct:
		clone := reflect.New(value.Type()).Elem()
		clone.Set(value)
		for i := range value.NumField() {
			if clone.Field(i).CanSet() && value.Field(i).CanInterface() {
				clone.Field(i).Set(cloneReflectValue(value.Field(i)))
			}
		}
		return clone
	default:
		return value
	}
}
