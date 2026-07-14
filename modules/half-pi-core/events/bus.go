package events

import (
	"fmt"
	"os"
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
	wg      sync.WaitGroup
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
// 每个 Writer 在独立 goroutine 中执行，互不阻塞。
func (b *EventBus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.writers {
		b.wg.Add(1)
		go func(w Writer) {
			defer b.wg.Done()
			if err := w.WriteEvent(event); err != nil {
				fmt.Fprintf(os.Stderr, "events: WriteEvent 失败: %v\n", err)
			}
		}(w)
	}
}

// PublishSync 同步发送事件，等所有 Writer 写入完成才返回。
// 用于需要保证输出顺序的场景（如 REPL 提示符不会提前出现）。
func (b *EventBus) PublishSync(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, w := range b.writers {
		if err := w.WriteEvent(event); err != nil {
			fmt.Fprintf(os.Stderr, "events: WriteEvent 失败: %v\n", err)
		}
	}
}

// Close 等待所有进行中的写入完成，然后关闭所有 Writer。
func (b *EventBus) Close() {
	b.wg.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, w := range b.writers {
		w.Close()
	}
	b.writers = nil
}
