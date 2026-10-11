package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type EventConsumerDeliveryHealthStore interface {
	GetEventConsumerDeliveryHealth(context.Context, string, string, string, time.Duration) (api.EventConsumerDeliveryHealth, error)
}

type eventConsumerDeliveryHealthReads interface {
	EventConsumerHealthStore
	EventConsumerExecutionHealthStore
	EventBacklogStore
}

func (s *PgStore) GetEventConsumerDeliveryHealth(ctx context.Context, account, app, sub string, window time.Duration) (api.EventConsumerDeliveryHealth, error) {
	return getEventConsumerDeliveryHealth(ctx, s, account, app, sub, window)
}

func (m *MemStore) GetEventConsumerDeliveryHealth(ctx context.Context, account, app, sub string, window time.Duration) (api.EventConsumerDeliveryHealth, error) {
	return getEventConsumerDeliveryHealth(ctx, m, account, app, sub, window)
}

func getEventConsumerDeliveryHealth(ctx context.Context, store eventConsumerDeliveryHealthReads, account, app, sub string, window time.Duration) (api.EventConsumerDeliveryHealth, error) {
	out := api.EventConsumerDeliveryHealth{StartedAt: time.Now().UTC()}
	var err error
	now := out.StartedAt
	out.Routing, err = store.GetEventConsumerHealth(ctx, account, app, sub, now.Add(-window), now)
	if err != nil {
		return out, err
	}
	now = time.Now().UTC()
	out.Execution, err = store.GetEventConsumerExecutionHealth(ctx, account, app, sub, now.Add(-window), now)
	if err != nil {
		return out, err
	}
	backlog, err := store.EventBacklog(ctx, account, EventBacklogQuery{AppID: app, Filters: api.EventBacklogFilters{SubscriptionID: canonicalMemUUID(sub), ConsumerKind: "application", State: "pending"}, Limit: api.EventConsumerDeliveryHealthSampleMax, ConsumerLimit: 1})
	if err != nil {
		return out, err
	}
	sample := api.EventConsumerPendingSample{ObservedAt: backlog.ObservedAt, Coverage: "oldest_pending_recipients", Truncated: backlog.Next.OutboxID != 0, WaitingReasons: map[string]int64{}, Recipients: []api.EventBacklogRecipient{}}
	for _, entry := range backlog.Recipients {
		sample.Recipients = append(sample.Recipients, entry.EventBacklogRecipient)
		sample.WaitingReasons[entry.WaitingReason]++
	}
	out.PendingSample = sample
	out.ObservedAt = time.Now().UTC()
	return out, nil
}
