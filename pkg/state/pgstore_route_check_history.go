package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgRouteHistoryTarget(ctx context.Context, tx pgx.Tx, accountID, appID, deploymentID string) error {
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, false)
	if err != nil {
		return err
	}
	if snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0 {
		return ErrAutomaticRouteCheckPlan
	}
	_, err = sqlc.New().ReadRoutePolicyDeployment(ctx, tx, sqlc.ReadRoutePolicyDeploymentParams{AppID: appID, DeploymentID: deploymentID})
	return routePolicyReadError(err)
}

func (s *PgStore) GetRouteCheckHistoryEntry(ctx context.Context, accountID, appID, deploymentID, id string) (api.RouteCheckHistoryEntry, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RouteCheckHistoryEntry{}, fmt.Errorf("begin route history lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := pgRouteHistoryTarget(ctx, tx, accountID, appID, deploymentID); err != nil {
		return api.RouteCheckHistoryEntry{}, err
	}
	body, err := sqlc.New().ReadRouteCheckHistoryEntry(ctx, tx, sqlc.ReadRouteCheckHistoryEntryParams{ID: id, AppID: appID, AccountID: accountID, DeploymentID: deploymentID})
	if err != nil {
		return api.RouteCheckHistoryEntry{}, routePolicyReadError(err)
	}
	entry, err := decodeRouteHistoryEntry(body, appID, deploymentID)
	if err != nil {
		return entry, err
	}
	return entry, tx.Commit(ctx)
}

func (s *PgStore) ListRouteCheckHistory(ctx context.Context, accountID, appID, deploymentID string, limit int, before string) (api.RouteCheckHistoryPage, error) {
	page := api.RouteCheckHistoryPage{AppID: appID, DeploymentID: deploymentID, Entries: []api.RouteCheckHistorySummary{}}
	if err := validateRouteHistoryPage(limit, before); err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, fmt.Errorf("begin route history page: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := pgRouteHistoryTarget(ctx, tx, accountID, appID, deploymentID); err != nil {
		return page, err
	}
	q := sqlc.New()
	if before != "" {
		if _, err := q.ReadRouteCheckHistoryEntry(ctx, tx, sqlc.ReadRouteCheckHistoryEntryParams{ID: before, AppID: appID, AccountID: accountID, DeploymentID: deploymentID}); err != nil {
			return page, routePolicyReadError(err)
		}
	}
	rows, err := q.ListRouteCheckHistory(ctx, tx, sqlc.ListRouteCheckHistoryParams{AppID: appID, AccountID: accountID, DeploymentID: deploymentID, BeforeID: before, PageLimit: int32(limit + 1)})
	if err != nil {
		return page, fmt.Errorf("read route history page: %w", err)
	}
	for i, body := range rows {
		if i == limit {
			page.NextCursor = page.Entries[i-1].ID
			break
		}
		var entry api.RouteCheckHistorySummary
		if err := json.Unmarshal(body, &entry); err != nil {
			return page, fmt.Errorf("decode route history summary: %w", err)
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, tx.Commit(ctx)
}
