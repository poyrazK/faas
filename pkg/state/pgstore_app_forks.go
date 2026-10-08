package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CreateAppFork admits a fork under a per-account advisory lock, so two
// concurrent requests cannot both pass the active-fork limits (ADR-732).
func (s *PgStore) CreateAppFork(ctx context.Context, params CreateAppForkParams) (AppFork, error) {
	p, err := validateCreateAppFork(params)
	if err != nil {
		return AppFork{}, err
	}
	accountID, appID, deploymentID := mustPgUUID(p.AccountID), mustPgUUID(p.AppID), mustPgUUID(p.DeploymentID)
	createdAt := pgtype.Timestamptz{Time: p.CreatedAt, Valid: true}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppFork{}, fmt.Errorf("state: create app fork tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	q := sqlc.New()
	if err := q.LockAccountAppForks(ctx, tx, accountID); err != nil {
		return AppFork{}, fmt.Errorf("state: create app fork lock: %w", mapErr(err))
	}
	counts, err := q.CountActiveAppForks(ctx, tx, sqlc.CountActiveAppForksParams{
		AppID: appID, AccountID: accountID, Now: createdAt,
	})
	if err != nil {
		return AppFork{}, fmt.Errorf("state: create app fork count: %w", mapErr(err))
	}
	if err := appForkLimitExceeded(p, int(counts.AppActive), int(counts.AccountActive)); err != nil {
		return AppFork{}, err
	}
	row, err := q.InsertAppFork(ctx, tx, sqlc.InsertAppForkParams{
		RequestedBy: p.RequestedBy, TtlSeconds: int32(p.TTLSeconds), CreatedAt: createdAt, //nolint:gosec // validated 60..86400
		AppID: appID, AccountID: accountID, DeploymentID: deploymentID, AccessTokenHash: p.AccessTokenHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppFork{}, ErrAppForkDeploymentUnavailable
	}
	if err != nil {
		return AppFork{}, fmt.Errorf("state: create app fork insert: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return AppFork{}, fmt.Errorf("state: create app fork commit: %w", err)
	}
	return appForkFromSQLC(row), nil
}

func (s *PgStore) AppForkByID(ctx context.Context, accountID, appID, forkID string) (AppFork, error) {
	row, err := sqlc.New().GetAppFork(ctx, s.pool, sqlc.GetAppForkParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ForkID: mustPgUUID(forkID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppFork{}, ErrNotFound
	}
	if err != nil {
		return AppFork{}, mapErr(err)
	}
	return appForkFromSQLC(row), nil
}

func (s *PgStore) AppForkForApp(ctx context.Context, appID, forkID string) (AppFork, error) {
	row, err := sqlc.New().GetAppForkForApp(ctx, s.pool, sqlc.GetAppForkForAppParams{
		AppID: mustPgUUID(appID), ForkID: mustPgUUID(forkID),
	})
	return appForkRow(row, err, ErrNotFound)
}

func (s *PgStore) ListAppForks(ctx context.Context, accountID, appID string, limit int) ([]AppFork, error) {
	rows, err := sqlc.New().ListAppForks(ctx, s.pool, sqlc.ListAppForksParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID),
		RowLimit: int32(clampAppForkListLimit(limit)), //nolint:gosec // clamped to 1..100
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AppFork, 0, len(rows))
	for _, row := range rows {
		out = append(out, appForkFromSQLC(row))
	}
	return out, nil
}

func (s *PgStore) RequestAppForkCancellation(ctx context.Context, accountID, appID, forkID string, requestedAt time.Time) (AppFork, error) {
	if requestedAt.IsZero() {
		return AppFork{}, ErrAppForkInvalid
	}
	row, err := sqlc.New().CancelAppFork(ctx, s.pool, sqlc.CancelAppForkParams{
		Now:       pgtype.Timestamptz{Time: requestedAt.UTC(), Valid: true},
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ForkID: mustPgUUID(forkID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Missing, or already terminal: the read tells the two apart.
		return s.AppForkByID(ctx, accountID, appID, forkID)
	}
	if err != nil {
		return AppFork{}, mapErr(err)
	}
	return appForkFromSQLC(row), nil
}

func appForkFromSQLC(row sqlc.AppFork) AppFork {
	return AppFork{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
		DeploymentID: pgUUIDString(row.DeploymentID), RequestedBy: row.RequestedBy,
		Status: AppForkStatus(row.Status), TTLSeconds: int(row.TtlSeconds), ExpiresAt: row.ExpiresAt.Time.UTC(),
		SnapshotID: executionUUIDPtr(row.SnapshotID), InstanceID: executionUUIDPtr(row.InstanceID),
		LeaseToken: executionUUIDPtr(row.LeaseToken), LeaseOwner: executionStringPtr(row.LeaseOwner),
		LeaseExpiresAt: timestamptzToTimePtr(row.LeaseExpiresAt), CancelRequested: timestamptzToTimePtr(row.CancelRequestedAt),
		FailureCode: executionStringPtr(row.FailureCode), FailureMessage: executionStringPtr(row.FailureMessage),
		StartedAt: timestamptzToTimePtr(row.StartedAt), FinishedAt: timestamptzToTimePtr(row.FinishedAt),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		AccessTokenHash: row.AccessTokenHash,
	}
}
