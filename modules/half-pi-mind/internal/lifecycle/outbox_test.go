package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestOutboxDispatcherRetriesThenDelivers(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	outbox := &fakeOutboxStore{records: []store.OutboxRecord{{ID: "event-1", Attempts: 0, AvailableAt: now}}}
	consumer := &deduplicatingConsumer{failures: 1}
	dispatcher, err := NewOutboxDispatcher(outbox, consumer)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return now }
	if delivered, err := dispatcher.DispatchOnce(context.Background()); err != nil || delivered != 0 {
		t.Fatalf("first dispatch delivered=%d err=%v", delivered, err)
	}
	if outbox.records[0].Attempts != 1 || outbox.records[0].AvailableAt != now.Add(2*time.Second) {
		t.Fatalf("retry state = %#v", outbox.records[0])
	}
	now = now.Add(2 * time.Second)
	if delivered, err := dispatcher.DispatchOnce(context.Background()); err != nil || delivered != 1 {
		t.Fatalf("second dispatch delivered=%d err=%v", delivered, err)
	}
	if !outbox.delivered["event-1"] || consumer.effects != 1 {
		t.Fatalf("delivery state=%v effects=%d", outbox.delivered, consumer.effects)
	}
}

func TestOutboxDispatcherDeadLettersAfterBoundedAttempts(t *testing.T) {
	now := time.Unix(2000, 0).UTC()
	outbox := &fakeOutboxStore{records: []store.OutboxRecord{{ID: "event-dead", Attempts: maxOutboxAttempts - 1, AvailableAt: now}}}
	consumer := &deduplicatingConsumer{failures: 100}
	dispatcher, _ := NewOutboxDispatcher(outbox, consumer)
	dispatcher.now = func() time.Time { return now }
	if delivered, err := dispatcher.DispatchOnce(context.Background()); err != nil || delivered != 0 {
		t.Fatalf("dispatch delivered=%d err=%v", delivered, err)
	}
	if !outbox.dead["event-dead"] || outbox.records[0].Attempts != maxOutboxAttempts {
		t.Fatalf("record was not dead-lettered: %#v", outbox.records[0])
	}
}

func TestReliableConsumerDeduplicatesReplayedEvent(t *testing.T) {
	consumer := &deduplicatingConsumer{}
	record := store.OutboxRecord{ID: "event-replayed"}
	if err := consumer.Deliver(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Deliver(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if consumer.effects != 1 {
		t.Fatalf("replay produced %d business effects", consumer.effects)
	}
}

type fakeOutboxStore struct {
	records   []store.OutboxRecord
	delivered map[string]bool
	dead      map[string]bool
}

func (s *fakeOutboxStore) DueOutbox(_ context.Context, now time.Time, _ int) ([]store.OutboxRecord, error) {
	var due []store.OutboxRecord
	for _, record := range s.records {
		if !s.delivered[record.ID] && !s.dead[record.ID] && !record.AvailableAt.After(now) {
			due = append(due, record)
		}
	}
	return due, nil
}

func (s *fakeOutboxStore) FinishOutbox(_ context.Context, id string, _ time.Time) error {
	if s.delivered == nil {
		s.delivered = make(map[string]bool)
	}
	s.delivered[id] = true
	return nil
}

func (s *fakeOutboxStore) RetryOutbox(_ context.Context, id string, attempts int, availableAt time.Time, _ string, dead bool) error {
	for i := range s.records {
		if s.records[i].ID == id {
			s.records[i].Attempts = attempts
			s.records[i].AvailableAt = availableAt
		}
	}
	if dead {
		if s.dead == nil {
			s.dead = make(map[string]bool)
		}
		s.dead[id] = true
	}
	return nil
}

type deduplicatingConsumer struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	failures int
	effects  int
}

func (c *deduplicatingConsumer) Deliver(_ context.Context, record store.OutboxRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failures > 0 {
		c.failures--
		return errors.New("temporary consumer failure")
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	if _, exists := c.seen[record.ID]; exists {
		return nil
	}
	c.seen[record.ID] = struct{}{}
	c.effects++
	return nil
}
