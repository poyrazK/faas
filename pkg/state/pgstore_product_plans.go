package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func productPlanViewPG(ctx context.Context, tx pgx.Tx, s ProductPlanScope) (productPlanView, error) {
	var v productPlanView
	if err := validateProductPlanScope(s); err != nil {
		return v, err
	}
	if _, err := onboardingPGOwner(ctx, tx, s.AccountID, s.AppID); err != nil {
		return v, err
	}
	if _, err := onboardingPGAuthApp(ctx, tx, s.AccountID, api.ProductOnboardingRequest{AuthApp: s.AuthApp, AuthAppID: s.AuthAppID}); err != nil {
		if errors.Is(err, ErrProductOnboardingStale) {
			return v, ErrProductAccess
		}
		return v, err
	}
	head, err := productCatalogTx(ctx, tx, s.AccountID, s.AppID, "")
	if err != nil {
		return v, err
	}
	c, err := productCustomerTx(ctx, tx, s.AccountID, s.AppID, s.TenantID)
	if err != nil {
		return v, err
	}
	tenant, err := sqlc.New().ProductOnboardingReadTenant(ctx, tx, sqlc.ProductOnboardingReadTenantParams{AccountID: mustPgUUID(s.AccountID), ID: mustPgUUID(s.TenantID)})
	if err != nil {
		return v, mapErr(err)
	}
	if err := productPlanAuthorized(s, head, c, onboardingPGTenant(tenant)); err != nil {
		return v, err
	}
	current, err := productCatalogTx(ctx, tx, s.AccountID, s.AppID, c.Revision)
	if err != nil {
		return v, err
	}
	v = productPlanView{head: head, current: current, customer: c, starts: map[string]int64{}, period: productcontrol.Period(time.Now())}
	rows, err := sqlc.New().ProductPlanStarts(ctx, tx, sqlc.ProductPlanStartsParams{AccountID: mustPgUUID(s.AccountID), AppID: mustPgUUID(s.AppID), TenantID: mustPgUUID(s.TenantID), Period: v.period, Features: productPlanFeatures(v)})
	if err != nil {
		return v, err
	}
	for _, r := range rows {
		v.starts[r.Feature] = r.Starts
	}
	return v, nil
}

func (s *PgStore) productPlanTx(ctx context.Context, scope ProductPlanScope) (pgx.Tx, productPlanView, error) {
	if err := validateProductPlanScope(scope); err != nil {
		return nil, productPlanView{}, err
	}
	tx, err := s.productBillingTx(ctx, ProductBillingScope{AccountID: scope.AccountID, AppID: scope.AppID, TenantID: scope.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return nil, productPlanView{}, err
	}
	v, err := productPlanViewPG(ctx, tx, scope)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, v, err
	}
	return tx, v, nil
}

func (s *PgStore) GetProductPlanOptions(ctx context.Context, scope ProductPlanScope) (api.ProductPlanOptions, error) {
	tx, v, err := s.productPlanTx(ctx, scope)
	if err != nil {
		return api.ProductPlanOptions{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return productPlanOptions(v), tx.Commit(ctx)
}

func (s *PgStore) PreviewProductPlanChange(ctx context.Context, scope ProductPlanScope, selection api.ProductPlanSelection) (api.ProductPlanChangePreview, error) {
	tx, v, err := s.productPlanTx(ctx, scope)
	if err != nil {
		return api.ProductPlanChangePreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := productPlanPreview(scope, v, selection)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func productPlanGetPG(ctx context.Context, tx pgx.Tx, scope ProductPlanScope, id string) (string, api.ProductPlanChangeReceipt, error) {
	r, err := sqlc.New().ProductPlanGetChange(ctx, tx, sqlc.ProductPlanGetChangeParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), ID: mustPgUUID(id)})
	if err != nil {
		return "", api.ProductPlanChangeReceipt{}, mapErr(err)
	}
	var receipt api.ProductPlanChangeReceipt
	err = json.Unmarshal(r.Receipt, &receipt)
	return r.Fingerprint, receipt, err
}

func (s *PgStore) ApplyProductPlanChange(ctx context.Context, scope ProductPlanScope, c api.ProductPlanChangeConfirmation) (api.ProductPlanChangeReceipt, error) {
	if err := validateProductPlanConfirmation(c); err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	tx, v, err := s.productPlanTx(ctx, scope)
	if err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fingerprint := productPlanFingerprint(scope, c)
	oldFingerprint, old, err := productPlanGetPG(ctx, tx, scope, c.Request.ID)
	if err == nil {
		if oldFingerprint != fingerprint {
			return old, ErrConflict
		}
		return old, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return api.ProductPlanChangeReceipt{}, err
	}
	p, err := productPlanPreview(scope, v, c.Request)
	if err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	if p.Hash != c.ExpectedHash {
		return api.ProductPlanChangeReceipt{}, ErrProductPlanChangeStale
	}
	r := productPlanReceipt(p, scope.Role)
	after := p.AfterCustomer
	q := sqlc.New()
	err = q.ProductControlSetCustomer(ctx, tx, sqlc.ProductControlSetCustomerParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), Revision: after.Revision, Plan: after.Plan, Suspended: after.Suspended, AuthOrganizationID: mustPgUUID(after.AuthOrganizationID)})
	if err != nil {
		return r, mapErr(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	err = q.ProductPlanInsertChange(ctx, tx, sqlc.ProductPlanInsertChangeParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), Fingerprint: fingerprint, Receipt: raw, CreatedAt: pgtype.Timestamptz{Time: r.EffectiveAt, Valid: true}})
	if err != nil {
		return r, mapErr(err)
	}
	return r, tx.Commit(ctx)
}

func (s *PgStore) GetProductPlanChange(ctx context.Context, scope ProductPlanScope, id string) (api.ProductPlanChangeReceipt, error) {
	if !validProductUUID(id) {
		return api.ProductPlanChangeReceipt{}, ErrInvalidArgument
	}
	tx, _, err := s.productPlanTx(ctx, scope)
	if err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, receipt, err := productPlanGetPG(ctx, tx, scope, id)
	if err != nil {
		return receipt, err
	}
	return receipt, tx.Commit(ctx)
}

func (s *PgStore) ListProductPlanChanges(ctx context.Context, scope ProductPlanScope, raw string) (api.ProductPlanChanges, error) {
	c, err := productPlanCursor(scope, raw)
	if err != nil {
		return api.ProductPlanChanges{}, err
	}
	tx, _, err := s.productPlanTx(ctx, scope)
	if err != nil {
		return api.ProductPlanChanges{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id pgtype.UUID
	if c.ID != "" {
		id = mustPgUUID(c.ID)
	}
	rows, err := sqlc.New().ProductPlanListChanges(ctx, tx, sqlc.ProductPlanListChangesParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), AfterID: c.ID, AfterAt: pgtype.Timestamptz{Time: c.At, Valid: !c.At.IsZero()}, AfterUuid: id, PageLimit: api.ProductControlOwnerPageSize + 1})
	if err != nil {
		return api.ProductPlanChanges{}, err
	}
	out := api.ProductPlanChanges{Changes: []api.ProductPlanChangeReceipt{}}
	for _, r := range rows {
		var receipt api.ProductPlanChangeReceipt
		if err := json.Unmarshal(r.Receipt, &receipt); err != nil {
			return out, err
		}
		out.Changes = append(out.Changes, receipt)
	}
	if len(out.Changes) > api.ProductControlOwnerPageSize {
		out.Changes = out.Changes[:api.ProductControlOwnerPageSize]
		last := out.Changes[len(out.Changes)-1]
		c.ID, c.At = last.ID, last.EffectiveAt
		out.NextCursor = encodeProductOwnerCursor(c)
	}
	return out, tx.Commit(ctx)
}
