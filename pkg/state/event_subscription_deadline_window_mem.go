package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetEventSubscriptionDeadlineWindow(ctx context.Context, account, app, sub string, within int64) (api.EventSubscriptionDeadlineWindow, error) {
	var zero api.EventSubscriptionDeadlineWindow
	if validSubscriptionControlIDs(account, app, sub) != nil || !api.ValidEventSubscriptionDeadlineWindow(within) {
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
	out := newSubscriptionDeadlineWindow(app, sub, within, m.subscriptionRatePreviewStateLocked(account, app, sub, now), now)
	control := m.eventSubscriptionWaitingReasonLocked(account, app, sub, now)
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if work.Delivered || !work.SnapshotCaptured || !sameMemUUID(eventRoutingAccount(work), account) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			if !sameMemUUID(recipient.AppID, app) || !sameMemUUID(recipient.ID, sub) || len(recipient.Workflow) != 0 || eventRecipientRoutingStateLocked(work, recipient.ID) != "pending" {
				continue
			}
			if out.Summary.PendingRecipients == api.EventSubscriptionPendingSummaryCountMax {
				return zero, errors.New("deadline window exceeds exact count representation")
			}
			out.Summary.PendingRecipients++
			accepted := work.CreatedAt.UTC().Truncate(time.Microsecond)
			invalid := accepted.IsZero() || accepted.After(now)
			deadline := EventDeliveryDeadline(recipient, accepted, work.RecipientProgress[recipient.ID]).UTC().Truncate(time.Microsecond)
			if !deadline.IsZero() {
				out.Summary.DeadlineRecipients++
				mergeSubscriptionDeadlineMinimum(&out.Summary.EarliestDeadlineAt, deadline)
			}
			backlog := m.backlogRecipientLocked(account, work, recipient, publishedEventIdentity{})
			backlog.NextAttemptAt = subscriptionDeadlineMetadataTime(backlog.NextAttemptAt)
			backlog.LeaseUntil = subscriptionDeadlineMetadataTime(backlog.LeaseUntil)
			leased := backlog.LeaseUntil != nil && backlog.LeaseUntil.After(now)
			scheduled := backlog.NextAttemptAt != nil && backlog.NextAttemptAt.After(now)
			finishSubscriptionDeadlineWindow(&out, invalid, leased, scheduled)
			if deadline.IsZero() || !deadline.Before(out.WindowEndAt) {
				continue
			}
			if backlog.RoutingMode != "event" || !leased {
				backlog.DeliveryControlReason = control
			}
			backlogObservation(&backlog.EventBacklogRecipient, now)
			if _, ok := out.UpcomingWaitingReasons[backlog.WaitingReason]; !ok {
				return zero, errors.New("unknown deadline waiting reason")
			}
			if !deadline.After(now) {
				out.Summary.AlreadyExpiredCount++
				out.ExpiredWaitingReasons[backlog.WaitingReason]++
			} else {
				out.UpcomingRecipients++
				out.UpcomingWaitingReasons[backlog.WaitingReason]++
				mergeSubscriptionDeadlineMinimum(&out.EarliestUpcomingDeadlineAt, deadline)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if out.Summary.Complete && !api.EventSubscriptionDeadlineWindowCountsComplete(out) {
		return zero, errors.New("inconsistent deadline window counts")
	}
	return out, nil
}
