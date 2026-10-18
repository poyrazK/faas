package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListProductWorkspaceActivity(ctx context.Context, scope ProductWorkspaceActivityScope, filter, raw string) (api.ProductWorkspaceActivityPage, error) {
	if err := validateProductPlanScope(scope.ProductPlanScope); err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = onboardingPGOwner(ctx, tx, scope.AccountID, scope.AppID); err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	v, err := helpViewPG(ctx, tx, ProductHelpScope(scope))
	if err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	c, err := workspaceActivityCursor(scope, v.permissions, filter, raw)
	if err != nil {
		return api.ProductWorkspaceActivityPage{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := sqlc.ProductWorkspaceActivityParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), AuthAppID: mustPgUUID(scope.AuthAppID), OrganizationID: mustPgUUID(scope.OrganizationID), CanViewWork: v.permissions.ViewWork, CanViewBilling: v.permissions.ViewBilling, CanManagePlan: v.permissions.ManagePlan, CanViewFeature: v.permissions.RunWork || v.permissions.ViewBilling, CategoryFilter: filter, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true}, AfterID: c.ID, AfterKind: c.Kind, AfterAt: pgtype.Timestamptz{Time: c.At, Valid: !c.At.IsZero()}, PageLimit: api.ProductControlWorkspaceActivityPageSize + 1}
	if c.ID != "" {
		p.AfterUuid = mustPgUUID(c.ID)
	}
	rows, err := sqlc.New().ProductWorkspaceActivity(ctx, tx, p)
	if err != nil {
		return api.ProductWorkspaceActivityPage{}, fmt.Errorf("read customer workspace activity: %w", err)
	}
	events := make([]api.ProductWorkspaceActivityEvent, 0, len(rows))
	for _, row := range rows {
		e := workspaceActivityEvent(row.Kind, pgUUIDString(row.SourceID), row.OccurredAt.Time)
		if err = json.Unmarshal(row.Details, &e); err != nil {
			return api.ProductWorkspaceActivityPage{}, fmt.Errorf("decode customer workspace activity: %w", err)
		}
		e.Feature, e.HelpKind = row.Feature, row.HelpKind
		events = append(events, e)
	}
	return workspaceActivityPage(scope, now, filter, c, events), tx.Commit(ctx)
}
