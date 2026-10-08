package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func pgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// appForkRow maps a lease-guarded single-row result: no row means the
// caller's view of the fork is stale.
func appForkRow(row sqlc.AppFork, err error, noRow error) (AppFork, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return AppFork{}, noRow
	}
	if err != nil {
		return AppFork{}, mapErr(err)
	}
	return appForkFromSQLC(row), nil
}

func appForkRows(rows []sqlc.AppFork, err error) ([]AppFork, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AppFork, 0, len(rows))
	for _, row := range rows {
		out = append(out, appForkFromSQLC(row))
	}
	return out, nil
}

func (s *PgStore) ClaimNextAppFork(ctx context.Context, owner string, now time.Time, lease time.Duration) (AppFork, error) {
	if err := validLease(owner, now, lease); err != nil {
		return AppFork{}, err
	}
	row, err := sqlc.New().ClaimNextAppFork(ctx, s.pool, sqlc.ClaimNextAppForkParams{
		Owner: owner, LeaseExpiresAt: pgTime(now.Add(lease)), Now: pgTime(now),
	})
	return appForkRow(row, err, ErrNotFound)
}

func (s *PgStore) RenewAppForkLease(ctx context.Context, forkID, leaseToken string, now time.Time, lease time.Duration) (AppFork, error) {
	if forkID == "" || leaseToken == "" || now.IsZero() || lease <= 0 {
		return AppFork{}, ErrAppForkInvalid
	}
	row, err := sqlc.New().RenewAppForkLease(ctx, s.pool, sqlc.RenewAppForkLeaseParams{
		LeaseExpiresAt: pgTime(now.Add(lease)), Now: pgTime(now),
		ForkID: mustPgUUID(forkID), LeaseToken: mustPgUUID(leaseToken),
	})
	return appForkRow(row, err, ErrAppForkLeaseLost)
}

func (s *PgStore) MarkAppForkRunning(ctx context.Context, forkID, leaseToken, snapshotID, instanceID string, now time.Time) (AppFork, error) {
	if forkID == "" || leaseToken == "" || snapshotID == "" || instanceID == "" || now.IsZero() {
		return AppFork{}, ErrAppForkInvalid
	}
	row, err := sqlc.New().MarkAppForkRunning(ctx, s.pool, sqlc.MarkAppForkRunningParams{
		SnapshotID: mustPgUUID(snapshotID), InstanceID: mustPgUUID(instanceID), Now: pgTime(now),
		ForkID: mustPgUUID(forkID), LeaseToken: mustPgUUID(leaseToken),
	})
	return appForkRow(row, err, ErrAppForkLeaseLost)
}

func (s *PgStore) FinishAppFork(ctx context.Context, p FinishAppForkParams) (AppFork, error) {
	if err := p.validate(); err != nil {
		return AppFork{}, err
	}
	row, err := sqlc.New().FinishAppFork(ctx, s.pool, sqlc.FinishAppForkParams{
		Status: string(p.Status), FailureCode: pgText(p.FailureCode), FailureMessage: pgText(p.FailureMessage),
		Now: pgTime(p.FinishedAt), ForkID: mustPgUUID(p.ForkID), LeaseToken: mustPgUUID(p.LeaseToken),
	})
	return appForkRow(row, err, ErrAppForkLeaseLost)
}

func (s *PgStore) ExpireUnclaimedAppForks(ctx context.Context, now time.Time) ([]AppFork, error) {
	if now.IsZero() {
		return nil, ErrAppForkInvalid
	}
	return appForkRows(sqlc.New().ExpireUnclaimedAppForks(ctx, s.pool, pgTime(now)))
}

func (s *PgStore) AppForksDueForTeardown(ctx context.Context, owner string, now time.Time, limit int) ([]AppFork, error) {
	if owner == "" || now.IsZero() {
		return nil, ErrAppForkInvalid
	}
	return appForkRows(sqlc.New().ListAppForksDueForTeardown(ctx, s.pool, sqlc.ListAppForksDueForTeardownParams{
		Owner: owner, Now: pgTime(now), RowLimit: int32(clampAppForkListLimit(limit)), //nolint:gosec // clamped to 1..100
	}))
}

func (s *PgStore) TakeOverAbandonedAppFork(ctx context.Context, owner string, now time.Time, lease time.Duration) (AppFork, error) {
	if err := validLease(owner, now, lease); err != nil {
		return AppFork{}, err
	}
	row, err := sqlc.New().TakeOverAbandonedAppFork(ctx, s.pool, sqlc.TakeOverAbandonedAppForkParams{
		Owner: owner, LeaseExpiresAt: pgTime(now.Add(lease)), Now: pgTime(now),
	})
	return appForkRow(row, err, ErrNotFound)
}
