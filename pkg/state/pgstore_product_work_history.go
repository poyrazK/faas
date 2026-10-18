package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListProductWorkHistory(ctx context.Context, scope ProductWorkHistoryScope, f api.ProductWorkHistoryFilter, raw string) (api.ProductWorkHistoryPage, error) {
	if err := validateProductPlanScope(scope.ProductPlanScope); err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	f, err := productcontrol.NormalizeWorkHistoryFilter(f)
	if err != nil {
		return api.ProductWorkHistoryPage{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = onboardingPGOwner(ctx, tx, scope.AccountID, scope.AppID); err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	v, err := helpViewPG(ctx, tx, ProductHelpScope(scope))
	if err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	c, err := workHistoryCursor(scope, v.permissions, f, raw)
	if err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := workHistoryQuery(scope, f, c, now)
	rows, err := sqlc.New().ProductWorkHistory(ctx, tx, p)
	if err != nil {
		return api.ProductWorkHistoryPage{}, fmt.Errorf("read customer work history: %w", err)
	}
	entries := make([]api.ProductWorkHistoryEntry, 0, len(rows))
	for _, row := range rows {
		var at *time.Time
		if row.ResolvedAt.Valid {
			at = &row.ResolvedAt.Time
		}
		entries = append(entries, api.ProductWorkHistoryEntry{OperationID: pgUUIDString(row.OperationID), Feature: row.Feature, Plan: row.Plan, Period: row.Period, AdmittedAt: row.AdmittedAt.Time.UTC(), Outcome: productRunOutcome(api.OperationState(row.ResolvedState), at, row.LiveState, row.ResultAvailable)})
	}
	page, err := workHistoryPage(scope, v.permissions, now, f, c, entries)
	if err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	return page, tx.Commit(ctx)
}

func workHistoryQuery(s ProductWorkHistoryScope, f api.ProductWorkHistoryFilter, c productcontrol.WorkHistoryCursor, now time.Time) sqlc.ProductWorkHistoryParams {
	p := sqlc.ProductWorkHistoryParams{AccountID: mustPgUUID(s.AccountID), AppID: mustPgUUID(s.AppID), TenantID: mustPgUUID(s.TenantID), Feature: f.Feature, StateFilter: f.State, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true}, PageLimit: api.ProductControlWorkHistoryPageSize + 1}
	if f.OperationID != "" {
		p.OperationID = mustPgUUID(f.OperationID)
	}
	if f.From != "" {
		at, _ := time.Parse(time.DateOnly, f.From) // Validated before opening the snapshot.
		p.FromAt = pgtype.Timestamptz{Time: at, Valid: true}
	}
	if f.Through != "" {
		at, _ := time.Parse(time.DateOnly, f.Through)
		p.UntilAt = pgtype.Timestamptz{Time: at.AddDate(0, 0, 1), Valid: true}
	}
	if c.ID != "" {
		p.AfterUuid = mustPgUUID(c.ID)
		p.AfterAt = pgtype.Timestamptz{Time: c.At, Valid: true}
	}
	return p
}
