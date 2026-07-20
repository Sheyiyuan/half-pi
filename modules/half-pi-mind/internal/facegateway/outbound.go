package facegateway

import (
	"context"
	"sync"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

const (
	defaultOutboundQueueSize      = 256
	maxOutboundTransientItems     = 128
	maxOutboundTransientBytes     = 512 << 10
	maxOutboundQueuedPayloadBytes = 8 << 20
)

type outboundItem struct {
	envelope  protocol.Envelope
	bytes     int
	transient bool
}

type outboundScheduler struct {
	mu             sync.Mutex
	items          []outboundItem
	maxItems       int
	totalBytes     int
	transientItems int
	transientBytes int
	closed         bool
	notify         chan struct{}
}

func newOutboundScheduler(maxItems int) *outboundScheduler {
	return &outboundScheduler{maxItems: maxItems, notify: make(chan struct{}, 1)}
}

func (q *outboundScheduler) enqueue(envelope protocol.Envelope, transient bool) bool {
	item := outboundItem{envelope: envelope, bytes: len(envelope.Payload) + len(envelope.Type), transient: transient}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if transient {
		if len(q.items) >= q.maxItems || q.transientItems >= min(q.maxItems, maxOutboundTransientItems) ||
			q.transientBytes+item.bytes > maxOutboundTransientBytes || q.totalBytes+item.bytes > maxOutboundQueuedPayloadBytes {
			return false
		}
		q.pushLocked(item)
		return true
	}
	for !q.fitsReliableLocked(item) && q.dropOldestTransientLocked() {
	}
	if !q.fitsReliableLocked(item) {
		return false
	}
	q.pushLocked(item)
	return true
}

func (q *outboundScheduler) fitsReliableLocked(item outboundItem) bool {
	if len(q.items) >= q.maxItems {
		return false
	}
	if item.bytes > maxOutboundQueuedPayloadBytes {
		return len(q.items) == 0
	}
	return q.totalBytes+item.bytes <= maxOutboundQueuedPayloadBytes
}

func (q *outboundScheduler) pushLocked(item outboundItem) {
	q.items = append(q.items, item)
	q.totalBytes += item.bytes
	if item.transient {
		q.transientItems++
		q.transientBytes += item.bytes
	}
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *outboundScheduler) dropOldestTransientLocked() bool {
	for index, item := range q.items {
		if !item.transient {
			continue
		}
		q.removeLocked(index)
		return true
	}
	return false
}

func (q *outboundScheduler) removeLocked(index int) outboundItem {
	item := q.items[index]
	copy(q.items[index:], q.items[index+1:])
	q.items = q.items[:len(q.items)-1]
	q.totalBytes -= item.bytes
	if item.transient {
		q.transientItems--
		q.transientBytes -= item.bytes
	}
	return item
}

func (q *outboundScheduler) pop(ctx context.Context) (protocol.Envelope, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			item := q.removeLocked(0)
			q.mu.Unlock()
			return item.envelope, true
		}
		if q.closed {
			q.mu.Unlock()
			return protocol.Envelope{}, false
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return protocol.Envelope{}, false
		case <-q.notify:
		}
	}
}

func (q *outboundScheduler) close() {
	q.mu.Lock()
	q.closed = true
	q.items = nil
	q.totalBytes = 0
	q.transientItems = 0
	q.transientBytes = 0
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
