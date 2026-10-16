package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListEventSubscriptionDeadlines(ctx context.Context, account, app, sub string, query api.EventSubscriptionDeadlineQuery) (api.EventSubscriptionDeadlinePage, error) {
	var zero api.EventSubscriptionDeadlinePage
	if validSubscriptionControlIDs(account, app, sub) != nil || query.Validate() != nil {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionDeadlineReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return zero, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	query, cursor, err := subscriptionDeadlineQuery(account, app, sub, query, now)
	if err != nil {
		return zero, err
	}
	rows := make([]subscriptionDeadlineEntry, 0, query.Limit+1)
	boundary := subscriptionDeadlineEntry{outboxID: cursor.OutboxID, EventSubscriptionDeadlineRecipient: api.EventSubscriptionDeadlineRecipient{DeadlineAt: cursor.DeadlineAt, AcceptedAt: cursor.AcceptedAt}}
	control := m.eventSubscriptionWaitingReasonLocked(account, app, sub, now)
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if work.Delivered || !work.SnapshotCaptured || !sameMemUUID(eventRoutingAccount(work), account) || work.CreatedAt.UTC().Truncate(time.Microsecond).After(cursor.WindowAt) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			if !sameMemUUID(recipient.AppID, app) || !sameMemUUID(recipient.ID, sub) || len(recipient.Workflow) != 0 || eventRecipientRoutingStateLocked(work, recipient.ID) != "pending" {
				continue
			}
			accepted := work.CreatedAt.UTC().Truncate(time.Microsecond)
			deadline := EventDeliveryDeadline(recipient, accepted, work.RecipientProgress[recipient.ID]).UTC().Truncate(time.Microsecond)
			if !subscriptionDeadlineMatches(deadline, query, cursor.WindowAt) {
				continue
			}
			entry := subscriptionDeadlineEntry{outboxID: work.ID, EventSubscriptionDeadlineRecipient: api.EventSubscriptionDeadlineRecipient{AcceptedAt: accepted, DeadlineAt: deadline.UTC()}}
			if query.Cursor != "" && !subscriptionDeadlineEntryBefore(boundary, entry) {
				continue
			}
			index := sort.Search(len(rows), func(i int) bool { return !subscriptionDeadlineEntryBefore(rows[i], entry) })
			if index >= query.Limit+1 {
				continue
			}
			var identity publishedEventIdentity
			if err := json.Unmarshal(work.Payload, &identity); err != nil {
				return zero, err
			}
			backlog := m.backlogRecipientLocked(account, work, recipient, identity)
			backlog.NextAttemptAt = subscriptionDeadlineMetadataTime(backlog.NextAttemptAt)
			backlog.LeaseUntil = subscriptionDeadlineMetadataTime(backlog.LeaseUntil)
			if backlog.RoutingMode != "event" || backlog.LeaseUntil == nil || !backlog.LeaseUntil.After(now) {
				backlog.DeliveryControlReason = control
			}
			// Effective age deadlines exclude ordered work, so no ordering-blocker scan is needed.
			backlogObservation(&backlog.EventBacklogRecipient, now)
			if query.WaitingReason != "" && backlog.WaitingReason != query.WaitingReason {
				continue
			}
			entry.EventSource, entry.EventID, entry.WaitingReason = identity.Source, identity.ID, backlog.WaitingReason
			entry.NextAttemptAt, entry.LeaseUntil = cloneEventReceiptTime(backlog.NextAttemptAt), cloneEventReceiptTime(backlog.LeaseUntil)
			if !subscriptionDeadlineEntryValid(entry, now) {
				return zero, errors.New("invalid retained deadline metadata")
			}
			if len(rows) < query.Limit+1 {
				rows = append(rows, subscriptionDeadlineEntry{})
			}
			copy(rows[index+1:], rows[index:len(rows)-1])
			rows[index] = entry
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return subscriptionDeadlinePage(app, sub, now, query, cursor, rows), nil
}

func subscriptionDeadlineMetadataTime(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	copyTime := at.UTC().Truncate(time.Microsecond)
	return &copyTime
}
