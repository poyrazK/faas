package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteHealthHistoryStore = (*PgStore)(nil)

func pgRecordRouteHealthHistory(ctx context.Context, tx pgx.Tx, accountID string, report api.RouteHealthReport, d Deployment, params CanaryAdvanceParams) (api.RouteHealthHistoryEntry, error) {
	entry, record, err := buildRouteHealthHistory(report, d, params)
	if err != nil || len(record.Body) == 0 {
		return entry, err
	}
	return pgPersistRouteHealthHistory(ctx, tx, accountID, d, entry, record)
}

func pgPersistRouteHealthHistory(ctx context.Context, tx pgx.Tx, accountID string, d Deployment, entry api.RouteHealthHistoryEntry, record routeHealthStoredDecision) (api.RouteHealthHistoryEntry, error) {
	q := sqlc.New()
	id, err := q.InsertRouteHealthHistory(ctx, tx, sqlc.InsertRouteHealthHistoryParams{ID: entry.ID, DeploymentID: d.ID, AppID: d.AppID,
		AccountID: accountID, DecisionKey: record.Key, CheckedAt: NewPgtypeTime(entry.CheckedAt), EncodedBytes: int32(len(record.Body)), Entry: record.Body})
	if err != nil {
		return entry, fmt.Errorf("save route health decision evidence: %w", err)
	}
	if err := q.PruneRouteHealthHistory(ctx, tx, sqlc.PruneRouteHealthHistoryParams{DeploymentID: d.ID, MaxEntries: api.RouteHealthHistoryMaxEntries, MaxBytes: api.RouteHealthHistoryMaxBytes}); err != nil {
		return entry, fmt.Errorf("prune route health decision history: %w", err)
	}
	if id != entry.ID {
		body, err := q.ReadRouteHealthHistoryEntry(ctx, tx, sqlc.ReadRouteHealthHistoryEntryParams{ID: id, AppID: d.AppID, AccountID: accountID, DeploymentID: d.ID})
		if err != nil {
			return entry, fmt.Errorf("read reused route health decision: %w", err)
		}
		return decodeRouteHealthHistory(body, d.AppID, d.ID)
	}
	return entry, nil
}

func pgRouteHealthHistoryTarget(ctx context.Context, tx pgx.Tx, accountID, appID, deploymentID string) error {
	if _, err := pgRouteHealthGate(ctx, tx, accountID, appID); err != nil {
		return err
	}
	_, err := pgRouteHealthDeployment(ctx, tx, appID, deploymentID)
	return err
}

func (s *PgStore) GetRouteHealthHistoryEntry(ctx context.Context, accountID, appID, deploymentID, id string) (api.RouteHealthHistoryEntry, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RouteHealthHistoryEntry{}, fmt.Errorf("begin route health history lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := pgRouteHealthHistoryTarget(ctx, tx, accountID, appID, deploymentID); err != nil {
		return api.RouteHealthHistoryEntry{}, err
	}
	body, err := sqlc.New().ReadRouteHealthHistoryEntry(ctx, tx, sqlc.ReadRouteHealthHistoryEntryParams{ID: id, AppID: appID, AccountID: accountID, DeploymentID: deploymentID})
	if err != nil {
		return api.RouteHealthHistoryEntry{}, routePolicyReadError(err)
	}
	entry, err := decodeRouteHealthHistory(body, appID, deploymentID)
	if err != nil {
		return entry, err
	}
	return entry, tx.Commit(ctx)
}

func (s *PgStore) ListRouteHealthHistory(ctx context.Context, accountID, appID, deploymentID string, limit int, before string) (api.RouteHealthHistoryPage, error) {
	page := api.RouteHealthHistoryPage{AppID: appID, DeploymentID: deploymentID, Entries: []api.RouteHealthHistoryEntry{}}
	if err := validateRouteHealthHistoryPage(limit, before); err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, fmt.Errorf("begin route health history page: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := pgRouteHealthHistoryTarget(ctx, tx, accountID, appID, deploymentID); err != nil {
		return page, err
	}
	q := sqlc.New()
	if before != "" {
		if _, err := q.ReadRouteHealthHistoryEntry(ctx, tx, sqlc.ReadRouteHealthHistoryEntryParams{ID: before, AppID: appID, AccountID: accountID, DeploymentID: deploymentID}); err != nil {
			return page, routePolicyReadError(err)
		}
	}
	rows, err := q.ListRouteHealthHistory(ctx, tx, sqlc.ListRouteHealthHistoryParams{AppID: appID, AccountID: accountID, DeploymentID: deploymentID, BeforeID: before, PageLimit: int32(limit + 1)})
	if err != nil {
		return page, fmt.Errorf("read route health history page: %w", err)
	}
	for i, body := range rows {
		if i == limit {
			page.NextCursor = page.Entries[i-1].ID
			break
		}
		entry, err := decodeRouteHealthHistory(body, appID, deploymentID)
		if err != nil {
			return page, err
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, tx.Commit(ctx)
}
