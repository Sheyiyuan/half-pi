package lifecycle

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

const (
	defaultOutboxBatch = 64
	maxOutboxAttempts  = 8
)

// ReliableConsumer 消费至少投递一次的脱敏 lifecycle outbox 事实。
// 实现必须按 EventID 去重。
type ReliableConsumer interface {
	Deliver(context.Context, store.OutboxRecord) error
}

type outboxStore interface {
	DueOutbox(context.Context, time.Time, int) ([]store.OutboxRecord, error)
	FinishOutbox(context.Context, string, time.Time) error
	RetryOutbox(context.Context, string, int, time.Time, string, bool) error
}

// OutboxDispatcher 以有界退避方式投递可靠低频事件。
type OutboxDispatcher struct {
	store    outboxStore
	consumer ReliableConsumer
	now      func() time.Time
	batch    int
	onError  func(error)
}

// SetErrorObserver 设置 dispatcher 的运维错误观察器。
// 回调只接收错误摘要，不参与投递裁决。
func (d *OutboxDispatcher) SetErrorObserver(observer func(error)) {
	d.onError = observer
}

// NewOutboxDispatcher 创建可靠事件 dispatcher。
func NewOutboxDispatcher(outbox outboxStore, consumer ReliableConsumer) (*OutboxDispatcher, error) {
	if outbox == nil || consumer == nil {
		return nil, fmt.Errorf("outbox store and consumer are required")
	}
	return &OutboxDispatcher{store: outbox, consumer: consumer, now: func() time.Time { return time.Now().UTC() }, batch: defaultOutboxBatch}, nil
}

// DispatchOnce 投递当前到期批次，返回成功投递数量。
func (d *OutboxDispatcher) DispatchOnce(ctx context.Context) (int, error) {
	now := d.now()
	records, err := d.store.DueOutbox(ctx, now, d.batch)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, record := range records {
		if ctx.Err() != nil {
			return delivered, ctx.Err()
		}
		if err := d.consumer.Deliver(ctx, record); err != nil {
			attempts := record.Attempts + 1
			dead := attempts >= maxOutboxAttempts
			backoff := time.Duration(math.Pow(2, float64(min(attempts, 8)))) * time.Second
			if retryErr := d.store.RetryOutbox(ctx, record.ID, attempts, now.Add(backoff), err.Error(), dead); retryErr != nil {
				return delivered, fmt.Errorf("record outbox retry: %w", retryErr)
			}
			continue
		}
		if err := d.store.FinishOutbox(ctx, record.ID, d.now()); err != nil {
			return delivered, fmt.Errorf("finish outbox delivery: %w", err)
		}
		delivered++
	}
	return delivered, nil
}

// Run 周期性投递，直到 context 取消。
func (d *OutboxDispatcher) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := d.DispatchOnce(ctx); err != nil && ctx.Err() == nil && d.onError != nil {
			d.onError(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
