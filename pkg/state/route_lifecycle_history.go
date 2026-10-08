package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type RouteLifecycleHistoryStore interface {
	ListRouteLifecycleHistory(context.Context, string, string, int, string) (api.RouteLifecycleHistoryPage, error)
}

func lifecycleHistoryCursor(limit int, before string) (int64, error) {
	if limit < 1 || limit > api.RouteLifecycleHistoryMaxPage {
		return 0, ErrInvalidArgument
	}
	if before == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(before, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != before {
		return 0, ErrInvalidArgument
	}
	return id, nil
}
func (s *PgStore) ListRouteLifecycleHistory(ctx context.Context, accountID, appID string, limit int, before string) (api.RouteLifecycleHistoryPage, error) {
	page := api.RouteLifecycleHistoryPage{AppID: appID, Entries: []api.RouteLifecycleHistoryEntry{}}
	cursor, err := lifecycleHistoryCursor(limit, before)
	if err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(ctx)
	if _, err = pgRoutePolicySnapshot(ctx, tx, accountID, appID, false); err != nil {
		return page, err
	}
	q := sqlc.New()
	if cursor != 0 {
		found, e := q.LifecycleHistoryCursorExists(ctx, tx, sqlc.LifecycleHistoryCursorExistsParams{BeforeID: cursor, AppID: appID, AccountID: accountID})
		if e != nil {
			return page, e
		}
		if !found {
			return page, ErrNotFound
		}
	}
	rows, err := q.ListProductionLifecycleHistory(ctx, tx, sqlc.ListProductionLifecycleHistoryParams{AppID: appID, AccountID: accountID, BeforeID: cursor, PageLimit: int32(limit + 1)})
	if err != nil {
		return page, err
	}
	for i, body := range rows {
		if i == limit {
			page.NextCursor = page.Entries[i-1].ID
			break
		}
		var entry api.RouteLifecycleHistoryEntry
		if err = json.Unmarshal(body, &entry); err != nil {
			return page, err
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, tx.Commit(ctx)
}
func pgRecordBlockedLifecycle(ctx context.Context, tx pgx.Tx, appID, deploymentID string, decision api.RouteGateDecision) error {
	body, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	q := sqlc.New()
	evidence, err := q.ReadLifecycleHistoryEvidence(ctx, tx, sqlc.ReadLifecycleHistoryEvidenceParams{AppID: appID, DeploymentID: deploymentID, Decision: body})
	if err != nil {
		return err
	}
	scope, err := q.ReadLifecycleDeploymentScope(ctx, tx, sqlc.ReadLifecycleDeploymentScopeParams{AppID: appID, DeploymentID: deploymentID})
	if err != nil {
		return err
	}
	return q.InsertBlockedLifecycleHistory(ctx, tx, sqlc.InsertBlockedLifecycleHistoryParams{AppID: appID, DeploymentID: deploymentID, Decision: body, Scope: normalizedDeploymentScope(scope), Evidence: evidence})
}

// Roll back all business writes before persisting the denied review separately.
func (s *PgStore) authorizeProductionLifecycle(ctx context.Context, tx pgx.Tx, id string, recovery bool) error {
	err := pgAuthorizeProductionLifecycle(ctx, tx, id, recovery)
	var blocked *RouteGateBlockedError
	if !errors.As(err, &blocked) {
		return err
	}
	q := sqlc.New()
	body, _ := json.Marshal(blocked.Decision)
	appID := ""
	d, e := q.ReadLifecycleHistoryDeployment(ctx, tx, id)
	if e != nil {
		return e
	}
	appID = d.AppID
	evidence, e := q.ReadLifecycleHistoryEvidence(ctx, tx, sqlc.ReadLifecycleHistoryEvidenceParams{AppID: appID, DeploymentID: id, Decision: body})
	if e != nil {
		return e
	}
	if e = tx.Rollback(ctx); e != nil {
		return e
	}
	if e = q.InsertBlockedLifecycleHistory(ctx, s.pool, sqlc.InsertBlockedLifecycleHistoryParams{AppID: appID, DeploymentID: id, Scope: normalizedDeploymentScope(d.Scope), Decision: body, Evidence: evidence}); e != nil {
		return e
	}
	return err
}
func (s *PgStore) authorizeLifecycleActivation(ctx context.Context, tx pgx.Tx, id string) error {
	dark, err := sqlc.New().ReadLifecycleDarkActivation(ctx, tx, id)
	if err != nil {
		return routePolicyReadError(err)
	}
	if dark {
		return nil
	}
	return s.authorizeProductionLifecycle(ctx, tx, id, false)
}
