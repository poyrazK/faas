package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetProductRunDetails(ctx context.Context, scope ProductRunDetailsScope, id string) (api.ProductRunDetails, error) {
	if !validProductUUID(id) {
		return api.ProductRunDetails{}, ErrInvalidArgument
	}
	if err := validateProductPlanScope(scope.ProductPlanScope); err != nil {
		return api.ProductRunDetails{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ProductRunDetails{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = onboardingPGOwner(ctx, tx, scope.AccountID, scope.AppID); err != nil {
		return api.ProductRunDetails{}, err
	}
	v, err := helpViewPG(ctx, tx, ProductHelpScope(scope))
	if err != nil {
		return api.ProductRunDetails{}, err
	}
	if !v.permissions.ViewWork {
		return api.ProductRunDetails{}, ErrProductAccess
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := sqlc.ProductRunDetailsParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), OperationID: mustPgUUID(id), CanViewBilling: v.permissions.ViewBilling, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	r, err := sqlc.New().ProductRunDetails(ctx, tx, p)
	if err != nil {
		return api.ProductRunDetails{}, mapErr(err)
	}
	receipt := api.ProductOperationReceipt{Feature: r.Feature, Plan: r.Plan, Period: r.Period, AdmittedAt: r.AdmittedAt.Time, Currency: r.Currency, UnitPriceMillicents: r.UnitPriceMillicents}
	if len(r.Allowance) > 0 {
		if err = json.Unmarshal(r.Allowance, &receipt.Admission); err != nil {
			return api.ProductRunDetails{}, err
		}
	}
	var resolvedAt *time.Time
	if r.ResolvedAt.Valid {
		resolvedAt = &r.ResolvedAt.Time
	}
	d := productRunDetails(scope, id, receipt, api.OperationState(r.ResolvedState), resolvedAt, r.LiveState, r.ResultAvailable, r.Waived, v.permissions, now)
	if c := d.Charge; c != nil {
		b, err := sqlc.New().ProductRunDetailsBilling(ctx, tx, sqlc.ProductRunDetailsBillingParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, OperationID: p.OperationID, Period: r.Period})
		if err != nil {
			return api.ProductRunDetails{}, err
		}
		c.CreditMillicents, c.CreditCount = b.CreditMillicents, b.CreditCount
		if b.StatementID.Valid {
			c.Statement = &api.ProductRunStatement{ID: pgUUIDString(b.StatementID), CreatedAt: b.StatementCreatedAt.Time.UTC()}
		}
	}
	if err = productRunFinalCharge(&d); err != nil {
		return api.ProductRunDetails{}, err
	}
	return d, tx.Commit(ctx)
}
