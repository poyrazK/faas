package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEventReplayGenerationChanged = errors.New("event replay generation changed")

// EventFanoutGenerationGuardedReplayStore atomically checks acceptance, routing generation and failed state.
type EventFanoutGenerationGuardedReplayStore interface {
	ReplayFailedPublishedEventRecipientWithReplayGuards(context.Context, string, string, string, string, string, bool, time.Time, int64) error
}

var ErrEventReplayAcceptanceChanged = errors.New("event replay acceptance changed")

// EventFanoutGuardedReplayStore atomically checks the original acceptance before replay.
// Guarded replay targets the acceptance snapshot, excluding historical backfill.
type EventFanoutGuardedReplayStore interface {
	ReplayFailedPublishedEventRecipientWithAcceptanceGuard(context.Context, string, string, string, string, string, bool, time.Time) error
}

func (s *PgStore) ReplayFailedPublishedEventRecipientWithAcceptanceGuard(ctx context.Context, account, app, source, event, subscription string, allowExpired bool, expected time.Time) error {
	return s.replayRecipientWithGuards(ctx, account, app, source, event, subscription, allowExpired, expected, nil)
}

func (s *PgStore) ReplayFailedPublishedEventRecipientWithReplayGuards(ctx context.Context, account, app, source, event, subscription string, allowExpired bool, expected time.Time, generation int64) error {
	return s.replayRecipientWithGuards(ctx, account, app, source, event, subscription, allowExpired, expected, &generation)
}

func (s *PgStore) replayRecipientWithGuards(ctx context.Context, account, app, source, event, subscription string, allowExpired bool, expected time.Time, generation *int64) error {
	if expected.IsZero() || (generation != nil && *generation < 0) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EventRecipientGuardedReplayReceipt(ctx, tx, sqlc.EventRecipientGuardedReplayReceiptParams{AccountID: mustPgUUID(account), AppID: app, EventSource: source, EventID: event, SubscriptionID: subscription})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// The receipt row lock also prevents retention/replacement between this check and commit.
	if !timeFromPgtype(row.CreatedAt).Equal(expected) {
		return ErrEventReplayAcceptanceChanged
	}
	if !row.RecipientFound {
		return ErrNotFound
	}
	if generation != nil {
		if !row.RecipientClaims {
			return ErrInvalidArgument
		}
		recipient, lockErr := q.EventRecipientReplayGenerationGuard(ctx, tx, sqlc.EventRecipientReplayGenerationGuardParams{OutboxID: row.ID, AppID: mustPgUUID(app), SubscriptionID: subscription})
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		if recipient.State == "enqueued" {
			return ErrNotFound
		}
		if recipient.Generation != *generation {
			return ErrEventReplayGenerationChanged
		}
		if recipient.State != "failed" {
			return ErrConflict
		}
	}
	if !row.RecipientClaims && row.State != "delivered" {
		return ErrConflict
	}
	if err = replayEventRecipientTx(ctx, q, tx, row.ID, app, subscription, row.Progress, row.RecipientClaims, time.Now().UTC(), allowExpired); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *MemStore) ReplayFailedPublishedEventRecipientWithAcceptanceGuard(ctx context.Context, account, app, source, event, subscription string, allowExpired bool, expected time.Time) error {
	if expected.IsZero() {
		return ErrInvalidArgument
	}
	return m.replayFailedPublishedEventRecipient(ctx, account, app, source, event, subscription, allowExpired, &expected, nil)
}
func (m *MemStore) ReplayFailedPublishedEventRecipientWithReplayGuards(ctx context.Context, account, app, source, event, subscription string, allowExpired bool, expected time.Time, generation int64) error {
	if expected.IsZero() || generation < 0 {
		return ErrInvalidArgument
	}
	return m.replayFailedPublishedEventRecipient(ctx, account, app, source, event, subscription, allowExpired, &expected, &generation)
}
