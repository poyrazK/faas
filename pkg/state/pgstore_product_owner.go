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

func (s *PgStore) GetProductOwnerDocument(ctx context.Context, scope ProductBillingScope, id string) (api.ProductBillingDocument, error) {
	if !validProductUUID(id) {
		return api.ProductBillingDocument{}, ErrInvalidArgument
	}
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductBillingDocument{}, err
	}
	row, err := sqlc.New().ProductBillingDocument(ctx, s.pool, productBillingDocumentParams(scope, id))
	if err != nil {
		return api.ProductBillingDocument{}, mapErr(err)
	}
	return productBillingPGDocument(row)
}

func previewProductChangePG(ctx context.Context, db sqlc.DBTX, account, app string, target api.ProductCustomer) (api.ProductCustomerChangePreview, error) {
	head, err := productCatalogTx(ctx, db, account, app, "")
	if err != nil {
		return api.ProductCustomerChangePreview{}, err
	}
	catalog, err := productCatalogTx(ctx, db, account, app, target.Revision)
	if err != nil {
		return api.ProductCustomerChangePreview{}, err
	}
	var before *api.ProductCustomer
	var beforeCatalog *api.ProductCatalog
	current, err := productCustomerTx(ctx, db, account, app, target.TenantID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.ProductCustomerChangePreview{}, err
	}
	if err == nil {
		before = &current
		c, err := productCatalogTx(ctx, db, account, app, current.Revision)
		if err != nil {
			return api.ProductCustomerChangePreview{}, err
		}
		beforeCatalog = &c
	}
	return productChangePreview(account, app, before, beforeCatalog, target, catalog, head, time.Now())
}

func (s *PgStore) PreviewProductCustomerChange(ctx context.Context, account, app string, target api.ProductCustomer) (api.ProductCustomerChangePreview, error) {
	tx, err := s.productBillingTx(ctx, ProductBillingScope{AccountID: account, AppID: app, TenantID: target.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return api.ProductCustomerChangePreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := previewProductChangePG(ctx, tx, account, app, target)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func (s *PgStore) ApplyProductCustomerChange(ctx context.Context, account, app string, req api.ProductCustomerChangeRequest) (api.ProductCustomerChangeReceipt, error) {
	if err := validateProductChange(req); err != nil {
		return api.ProductCustomerChangeReceipt{}, err
	}
	tx, err := s.productBillingTx(ctx, ProductBillingScope{AccountID: account, AppID: app, TenantID: req.Customer.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return api.ProductCustomerChangeReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	p := sqlc.ProductOwnerGetChangeParams{ID: mustPgUUID(req.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(req.Customer.TenantID)}
	fingerprint := productChangeFingerprint(account, app, req)
	old, err := q.ProductOwnerGetChange(ctx, tx, p)
	if err == nil {
		if old.Fingerprint != fingerprint {
			return api.ProductCustomerChangeReceipt{}, ErrConflict
		}
		var receipt api.ProductCustomerChangeReceipt
		if err := json.Unmarshal(old.Receipt, &receipt); err != nil {
			return receipt, err
		}
		return receipt, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProductCustomerChangeReceipt{}, err
	}
	preview, err := previewProductChangePG(ctx, tx, account, app, req.Customer)
	if err != nil {
		return api.ProductCustomerChangeReceipt{}, err
	}
	if preview.Hash != req.ExpectedHash {
		return api.ProductCustomerChangeReceipt{}, ErrProductCustomerChangeStale
	}
	c := preview.After
	var org pgtype.UUID
	if c.AuthOrganizationID != "" {
		org = mustPgUUID(c.AuthOrganizationID)
	}
	if err := q.ProductControlSetCustomer(ctx, tx, sqlc.ProductControlSetCustomerParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Revision: c.Revision, Plan: c.Plan, Suspended: c.Suspended, AuthOrganizationID: org}); err != nil {
		return api.ProductCustomerChangeReceipt{}, mapErr(err)
	}
	receipt := api.ProductCustomerChangeReceipt{ID: req.ID, PreviewHash: preview.Hash, Before: preview.Before, After: preview.After, EffectiveAt: time.Now().UTC().Truncate(time.Microsecond)}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if err := q.ProductOwnerInsertChange(ctx, tx, sqlc.ProductOwnerInsertChangeParams{ID: p.ID, AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Fingerprint: fingerprint, Receipt: raw, CreatedAt: pgtype.Timestamptz{Time: receipt.EffectiveAt, Valid: true}}); err != nil {
		return api.ProductCustomerChangeReceipt{}, mapErr(err)
	}
	return receipt, tx.Commit(ctx)
}

func (s *PgStore) InspectProductBinding(ctx context.Context, account, app string, feature api.ProductFeature) (OperationDefinition, error) {
	a, err := operationUUID(account)
	if err != nil {
		return OperationDefinition{}, err
	}
	b, err := operationUUID(app)
	if err != nil {
		return OperationDefinition{}, err
	}
	if _, err := sqlc.New().ProductControlOwnedApp(ctx, s.pool, sqlc.ProductControlOwnedAppParams{AccountID: a, ID: b}); err != nil {
		return OperationDefinition{}, mapErr(err)
	}
	return productDefinitionTx(ctx, s.pool, account, app, feature)
}

func (s *PgStore) ListProductOwnerRuns(ctx context.Context, scope ProductBillingScope, raw string) (api.ProductOwnerRuns, error) {
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductOwnerRuns{}, err
	}
	c, err := decodeProductOwnerCursor(raw, productOwnerCursorScope("runs", scope.AccountID, scope.AppID, scope.TenantID, scope.Period), true)
	if err != nil {
		return api.ProductOwnerRuns{}, err
	}
	var after pgtype.UUID
	if c.ID != "" {
		after = mustPgUUID(c.ID)
	}
	rows, err := sqlc.New().ProductOwnerRuns(ctx, s.pool, sqlc.ProductOwnerRunsParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), Period: scope.Period, Now: pgtype.Timestamptz{Time: time.Now(), Valid: true}, AfterID: c.ID, AfterAt: pgtype.Timestamptz{Time: c.At, Valid: !c.At.IsZero()}, AfterUuid: after, PageLimit: api.ProductControlOwnerPageSize + 1})
	if err != nil {
		return api.ProductOwnerRuns{}, err
	}
	out := api.ProductOwnerRuns{Runs: []api.ProductOwnerRun{}}
	for _, r := range rows {
		evidence, err := productBillingPGEvidence(sqlc.ProductBillingReceiptRow{OperationID: r.OperationID, TenantID: r.TenantID, Revision: r.Revision, Feature: r.Feature, Plan: r.Plan, Currency: r.Currency, Period: r.Period, UnitPriceMillicents: r.UnitPriceMillicents, AdmittedAt: r.AdmittedAt, SucceededAt: r.SucceededAt, AdmissionEvidence: r.AdmissionEvidence, ResolvedState: r.ResolvedState, ResolvedAt: r.ResolvedAt, WaiverReason: r.WaiverReason, WaivedAt: r.WaivedAt})
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, api.ProductOwnerRun{Evidence: evidence, State: api.OperationState(r.State), CreditedMillicents: r.CreditedMillicents})
	}
	if len(out.Runs) > api.ProductControlOwnerPageSize {
		out.Runs = out.Runs[:api.ProductControlOwnerPageSize]
		last := out.Runs[len(out.Runs)-1].Evidence
		c.ID, c.At = last.OperationID, last.Receipt.AdmittedAt
		out.NextCursor = encodeProductOwnerCursor(c)
	}
	return out, nil
}

func (s *PgStore) ListProductOwnerAttention(ctx context.Context, account, app, raw string) (api.ProductOwnerAttentionPage, error) {
	if _, err := s.GetProductCatalog(ctx, account, app, ""); err != nil {
		return api.ProductOwnerAttentionPage{}, err
	}
	c, err := decodeProductOwnerCursor(raw, productOwnerCursorScope("attention", account, app, "", ""), false)
	if err != nil {
		return api.ProductOwnerAttentionPage{}, err
	}
	var after pgtype.UUID
	if c.ID != "" {
		after = mustPgUUID(c.ID)
	}
	rows, err := sqlc.New().ProductOwnerAttention(ctx, s.pool, sqlc.ProductOwnerAttentionParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), CurrentPeriod: productcontrol.Period(time.Now()), AfterPeriod: c.Period, AfterTenant: after, PageLimit: api.ProductControlOwnerPageSize + 1})
	if err != nil {
		return api.ProductOwnerAttentionPage{}, err
	}
	out := api.ProductOwnerAttentionPage{Items: []api.ProductOwnerAttention{}}
	for _, r := range rows {
		out.Items = append(out.Items, api.ProductOwnerAttention{TenantID: pgUUIDString(r.TenantID), Period: r.Period, Unresolved: r.Unresolved, NeedsReview: r.NeedsReview})
	}
	if len(out.Items) > api.ProductControlOwnerPageSize {
		out.Items = out.Items[:api.ProductControlOwnerPageSize]
		last := out.Items[len(out.Items)-1]
		c.ID, c.Period = last.TenantID, last.Period
		out.NextCursor = encodeProductOwnerCursor(c)
	}
	return out, nil
}
