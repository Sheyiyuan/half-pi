package events

import (
	"sync"
)

// Writer 是事件订阅者，EventBus 将事件广播给所有注册的 Writer。
type Writer interface {
	WriteEvent(event Event) error
	Close() error
}

// EventBus 管理事件发布和订阅。
type EventBus struct {
	writers []Writer
	mu      sync.RWMutex
}

// NewEventBus 创建事件总线。
func NewEventBus() *EventBus {
	return &EventBus{}
}

// Subscribe 注册一个 Writer，所有事件都会广播给它。
func (b *EventBus) Subscribe(w Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writers = append(b.writers, w)
}

// Publish 将事件广播给所有订阅者。
// Writer 的 WriteEvent 在各自 goroutine 中执行，互不阻塞。
func (b *EventBus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.writers {
		w := w
		go func() {
			if err := w.WriteEvent(event); err != nil {
				// Writer 自身不应返回错误，若返回则说明内部故障，
				// 最简单的处理是忽略——Writer 自己负责错误处理。
			}
		}()
	}
}

// Close 关闭所有 Writer。
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, w := range b.writers {
		w.Close()
	}
	b.writers = nil
}
