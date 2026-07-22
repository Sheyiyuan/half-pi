// Package observer 提供进程内、多订阅者、有界且 fail-open 的领域事件分发。
package observer

import (
	"sync"
	"sync/atomic"
)

const defaultQueueSize = 256

// Hub 将领域事实异步投递给多个订阅者。
// 队列满时 Publish 返回 false；调用方不得因此回滚已经发生的业务事实。
type Hub[T any] struct {
	once sync.Once
	mu   sync.RWMutex
	next atomic.Uint64
	subs map[uint64]func(T)
	q    chan T
}

// Subscribe 注册订阅者并返回幂等取消函数。
func (h *Hub[T]) Subscribe(subscriber func(T)) func() {
	if subscriber == nil {
		return func() {}
	}
	h.start()
	id := h.next.Add(1)
	h.mu.Lock()
	h.subs[id] = subscriber
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
		})
	}
}

// Publish 将事实加入有界队列；无订阅者时直接成功。
func (h *Hub[T]) Publish(value T) bool {
	h.start()
	h.mu.RLock()
	hasSubscribers := len(h.subs) > 0
	h.mu.RUnlock()
	if !hasSubscribers {
		return true
	}
	select {
	case h.q <- value:
		return true
	default:
		return false
	}
}

func (h *Hub[T]) start() {
	h.once.Do(func() {
		h.subs = make(map[uint64]func(T))
		h.q = make(chan T, defaultQueueSize)
		go h.run()
	})
}

func (h *Hub[T]) run() {
	for value := range h.q {
		h.mu.RLock()
		subscribers := make([]func(T), 0, len(h.subs))
		for _, subscriber := range h.subs {
			subscribers = append(subscribers, subscriber)
		}
		h.mu.RUnlock()
		for _, subscriber := range subscribers {
			func() {
				defer func() { _ = recover() }()
				subscriber(value)
			}()
		}
	}
}
