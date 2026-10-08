package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AppOperationalStore = (*PgStore)(nil)
var _ AppOperationalRestartStore = (*PgStore)(nil)
var _ AppOperationalStore = (*MemStore)(nil)

func (s *PgStore) operationalAppOwner(ctx context.Context, accountID, appID string) error {
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return err
	}
	if app.AccountID != accountID || app.Status == AppDeleted {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAppPendingRollbacks(ctx context.Context, accountID, appID string) ([]api.RollbackOperation, error) {
	if err := s.operationalAppOwner(ctx, accountID, appID); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListAppPendingRollbacks(ctx, s.pool, sqlc.ListAppPendingRollbacksParams{
		AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID), RowLimit: api.AppOperationalRecoveryLimit + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("read app pending rollbacks: %w", err)
	}
	out := make([]api.RollbackOperation, 0, len(rows))
	for _, raw := range rows {
		operation, err := decodeCheckedRollback(raw, nil)
		if err != nil {
			return nil, fmt.Errorf("decode app pending rollback: %w", err)
		}
		out = append(out, operation)
	}
	return out, nil
}

func (s *PgStore) GetAppOpenMonitorIncident(ctx context.Context, accountID, appID string) (*api.AppOperationalIncident, error) {
	tx, err := s.monitorIncidentRead(ctx, accountID, appID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().AppOpenMonitorIncident(ctx, tx, sqlc.AppOpenMonitorIncidentParams{
		AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID),
	})
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read app open monitor incident: %w", err)
	}
	return &api.AppOperationalIncident{ID: pgUUIDString(row.ID), DeploymentID: pgUUIDString(row.DeploymentID), OpenedAt: row.OpenedAt.Time}, nil
}

func (s *PgStore) ListAppPendingRestarts(ctx context.Context, accountID, appID string) ([]api.RuntimeConfigRestartStatusResponse, error) {
	if err := s.operationalAppOwner(ctx, accountID, appID); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListAppPendingRestarts(ctx, s.pool, sqlc.ListAppPendingRestartsParams{
		AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID), RowLimit: api.AppOperationalRecoveryLimit + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("read app pending restarts: %w", err)
	}
	out := make([]api.RuntimeConfigRestartStatusResponse, 0, len(rows))
	for _, row := range rows {
		item := api.RuntimeConfigRestartStatusResponse{WakeID: row.WakeID, Status: row.Status,
			Attempts: int(row.Attempts), FailureReason: row.FailureReason, RequestedAt: row.RequestedAt.Time}
		if row.CompletedAt.Valid {
			at := row.CompletedAt.Time
			item.CompletedAt = &at
		}
		out = append(out, item)
	}
	return out, nil
}
