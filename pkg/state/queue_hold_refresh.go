package state

// adr: 20261113180000 — messages a push consumer holds keep their retention.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// QueueHoldRefresher restarts the retention wait of a push queue's pending
// messages while its consumer holds them (R5-5). The push loop skips a queue
// whose app is at its spend cap, under a compute budget hold, or whose
// queues capability is off, which spends no attempts; but retention kept
// counting, so a hold longer than the plan's retention dead-lettered the
// queue as queue_message_expired. The drain's holds move each held row's
// due_at, which restarts the wait (ADR-20261112200000); this does the same
// for push queues, once a message has waited olderThan, so each message is
// written at most once per olderThan. due_at moves to now, so delivery is not
// delayed once the hold lifts.
type QueueHoldRefresher interface {
	RefreshHeldQueueMessages(ctx context.Context, appID, bindingID, queueName string, olderThan time.Duration, limit int) (int, error)
}

var (
	_ QueueHoldRefresher = (*PgStore)(nil)
	_ QueueHoldRefresher = (*MemStore)(nil)
)

// QueueHoldRefreshAge is how long a held message waits before its wait
// restarts: half the shortest plan retention, so no plan's message expires
// while held and each one is rewritten rarely.
func QueueHoldRefreshAge() time.Duration {
	shortest := time.Duration(0)
	for _, plan := range api.Plans {
		if retention, ok := queueMessageRetention(plan); ok && (shortest == 0 || retention < shortest) {
			shortest = retention
		}
	}
	if shortest == 0 {
		return 12 * time.Hour
	}
	return shortest / 2
}

func (s *PgStore) RefreshHeldQueueMessages(ctx context.Context, appID, bindingID, queueName string, olderThan time.Duration, limit int) (int, error) {
	app, err := parsePgUUID(appID)
	if err != nil {
		return 0, ErrInvalidArgument
	}
	if limit <= 0 {
		limit = 256
	}
	n, err := sqlc.New().RefreshHeldQueueMessages(ctx, s.pool, sqlc.RefreshHeldQueueMessagesParams{
		AppID: app, BindingID: mustPgUUID(bindingID), QueueName: queueName,
		OlderThanSeconds: int64(olderThan / time.Second), BatchLimit: int32(limit),
	})
	return int(n), err
}

func (m *MemStore) RefreshHeldQueueMessages(_ context.Context, appID, bindingID, queueName string, olderThan time.Duration, limit int) (int, error) {
	if limit <= 0 {
		limit = 256
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	refreshed := 0
	for id, inv := range m.invocations {
		if refreshed >= limit {
			break
		}
		if inv.Source != InvocationQueue || inv.State != InvocationPending || inv.EnvironmentID != "" || !sameMemUUID(inv.AppID, appID) {
			continue
		}
		mine := bindingID != "" && sameMemUUID(inv.QueueBindingID, bindingID) || inv.QueueBindingID == "" && inv.QueueName == queueName
		if !mine || inv.DueAt.After(now) || !queueMessageWaitStart(inv).Before(now.Add(-olderThan)) {
			continue
		}
		inv.DueAt = now
		m.setInvocationLocked(id, inv)
		refreshed++
	}
	return refreshed, nil
}
