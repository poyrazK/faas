package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

var ErrEventReplayAcceptanceChanged = errors.New("event replay acceptance changed")

// EventFanoutGuardedReplayStore atomically checks the original acceptance before replay.
// Guarded replay targets the acceptance snapshot, excluding historical backfill.
type EventFanoutGuardedReplayStore interface {
	ReplayFailedPublishedEventRecipientWithAcceptanceGuard(context.Context, string, string, string, string, string, bool, time.Time) error
}

func (s *PgStore) ReplayFailedPublishedEventRecipientWithAcceptanceGuard(ctx context.Context, account, app, source, event, subscription string, allowExpired bool, expected time.Time) error {
	if expected.IsZero() {
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
	return m.replayFailedPublishedEventRecipient(ctx, account, app, source, event, subscription, allowExpired, &expected)
}
