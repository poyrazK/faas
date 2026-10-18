package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetEventSubscriptionPendingSummary(ctx context.Context, account, app, sub string) (api.EventSubscriptionPendingSummary, error) {
	var zero api.EventSubscriptionPendingSummary
	if validSubscriptionControlIDs(account, app, sub) != nil {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionPendingSummaryTimeout)
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
	out := newSubscriptionPendingSummary(app, sub, m.subscriptionRatePreviewStateLocked(account, app, sub, now), now)
	invalid, leased, scheduled := false, false, false
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
			if out.PendingRecipients == api.EventSubscriptionPendingSummaryCountMax {
				return zero, errors.New("pending summary exceeds exact count representation")
			}
			out.PendingRecipients++
			invalid = invalid || work.CreatedAt.IsZero() || work.CreatedAt.After(now)
			progress := work.RecipientProgress[recipient.ID]
			deadline := EventDeliveryDeadline(recipient, work.CreatedAt, progress)
			if !deadline.IsZero() {
				out.DeadlineRecipients++
				if !deadline.After(now) {
					out.AlreadyExpiredCount++
				}
				if out.EarliestDeadlineAt == nil || deadline.Before(*out.EarliestDeadlineAt) {
					copyDeadline := deadline
					out.EarliestDeadlineAt = &copyDeadline
				}
			}
			at := work.AvailableAt
			if progress.NextAttemptAt != nil {
				at = *progress.NextAttemptAt
			}
			activeLease := work.LeaseUntil.After(now)
			if row := work.routingRecipients[recipient.ID]; row != nil {
				if work.RecipientClaims || row.LeaseUntil.After(now) {
					activeLease = row.LeaseUntil.After(now)
				}
				at = row.AvailableAt
			}
			leased = leased || activeLease
			scheduled = scheduled || at.After(now)
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	finishSubscriptionPendingSummary(&out, invalid, leased, scheduled)
	return out, nil
}
