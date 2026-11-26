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

func previewProductChangePG(ctx context.Context, db sqlc.DBTX, account, app string, target api.ProductCustomer, scheduled bool) (api.ProductCustomerChangePreview, time.Time, error) {
	head, err := productCatalogTx(ctx, db, account, app, "")
	if err != nil {
		return api.ProductCustomerChangePreview{}, time.Time{}, err
	}
	catalog, err := productCatalogTx(ctx, db, account, app, target.Revision)
	if err != nil {
		return api.ProductCustomerChangePreview{}, time.Time{}, err
	}
	var before *api.ProductCustomer
	var beforeCatalog *api.ProductCatalog
	current, err := productCustomerForChangePG(ctx, db, account, app, target.TenantID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.ProductCustomerChangePreview{}, time.Time{}, err
	}
	if err == nil {
		before = &current
		c, err := productCatalogTx(ctx, db, account, app, current.Revision)
		if err != nil {
			return api.ProductCustomerChangePreview{}, time.Time{}, err
		}
		beforeCatalog = &c
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	p, err := productChangePreview(before, beforeCatalog, target, catalog, head, now)
	if err != nil {
		return p, now, err
	}
	if scheduled {
		if err := scheduleProductPreview(&p); err != nil {
			return p, now, err
		}
	}
	scope := ProductBillingScope{AccountID: account, AppID: app, TenantID: target.TenantID, Period: p.Period}
	history, err := productPlanHistoryPG(ctx, db, scope)
	if err != nil {
		return p, now, err
	}
	var oldPlan api.ProductPlan
	if before != nil {
		oldPlan = beforeCatalog.Spec.Plans[before.Plan]
	}
	starts, err := productPlanStartsPG(ctx, db, scope, current, productPlanFeatureNames(oldPlan, catalog.Spec.Plans[target.Plan]))
	if err != nil {
		return p, now, err
	}
	return priceProductChange(p, scope, history, starts, now), now, nil
}

func (s *PgStore) PreviewProductCustomerChange(ctx context.Context, account, app string, target api.ProductCustomer) (api.ProductCustomerChangePreview, error) {
	return s.previewProductCustomerChange(ctx, account, app, target, false)
}

func (s *PgStore) PreviewScheduledProductCustomerChange(ctx context.Context, account, app string, target api.ProductCustomer) (api.ProductCustomerChangePreview, error) {
	return s.previewProductCustomerChange(ctx, account, app, target, true)
}

func (s *PgStore) previewProductCustomerChange(ctx context.Context, account, app string, target api.ProductCustomer, scheduled bool) (api.ProductCustomerChangePreview, error) {
	tx, err := s.productBillingTx(ctx, ProductBillingScope{AccountID: account, AppID: app, TenantID: target.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return api.ProductCustomerChangePreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, _, err := previewProductChangePG(ctx, tx, account, app, target, scheduled)
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
	preview, now, err := previewProductChangePG(ctx, tx, account, app, req.Customer, req.NextMonth)
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
	receipt := api.ProductCustomerChangeReceipt{ID: req.ID, PreviewHash: preview.Hash, Before: preview.Before, After: preview.After, EffectiveAt: now}
	insert := sqlc.ProductOwnerInsertChangeParams{ID: p.ID, AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Fingerprint: fingerprint, CreatedAt: pgTime(now)}
	if preview.StartsNextMonthAt != nil {
		receipt.EffectiveAt = *preview.StartsNextMonthAt
		insert.ScheduledFor = pgTime(receipt.EffectiveAt)
		insert.ScheduleStatus = pgtype.Text{String: productPlanPending, Valid: true}
	} else if err := q.ProductControlSetCustomer(ctx, tx, sqlc.ProductControlSetCustomerParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Revision: c.Revision, Plan: c.Plan, Suspended: c.Suspended, AuthOrganizationID: org, EffectiveAt: pgTime(now)}); err != nil {
		return api.ProductCustomerChangeReceipt{}, mapErr(err)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	insert.Receipt = raw
	if err := q.ProductOwnerInsertChange(ctx, tx, insert); err != nil {
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
	return s.listProductOwnerRuns(ctx, scope, raw, false)
}

func (s *PgStore) ListProductOwnerReviewRuns(ctx context.Context, scope ProductBillingScope, raw string) (api.ProductOwnerRuns, error) {
	return s.listProductOwnerRuns(ctx, scope, raw, true)
}

func (s *PgStore) listProductOwnerRuns(ctx context.Context, scope ProductBillingScope, raw string, reviewOnly bool) (api.ProductOwnerRuns, error) {
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductOwnerRuns{}, err
	}
	c, err := productOwnerRunCursor(scope, raw, reviewOnly)
	if err != nil {
		return api.ProductOwnerRuns{}, err
	}
	var after pgtype.UUID
	if c.ID != "" {
		after = mustPgUUID(c.ID)
	}
	now := time.Now()
	rows, err := sqlc.New().ProductOwnerRuns(ctx, s.pool, sqlc.ProductOwnerRunsParams{
		AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID),
		Period: scope.Period, Now: pgTime(now), ReviewOnly: reviewOnly, CurrentPeriod: productcontrol.Period(now),
		AfterID: c.ID, AfterAt: pgtype.Timestamptz{Time: c.At, Valid: !c.At.IsZero()}, AfterUuid: after, PageLimit: api.ProductControlOwnerPageSize + 1,
	})
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

func (s *PgStore) ListProductOwnerBillingQueue(ctx context.Context, account, app, period, raw string) (ProductOwnerBillingQueue, error) {
	cursor, err := productOwnerBillingQueueCursor(account, app, period, raw, time.Now())
	if err != nil {
		return ProductOwnerBillingQueue{}, err
	}
	if _, err := s.GetProductCatalog(ctx, account, app, ""); err != nil {
		return ProductOwnerBillingQueue{}, err
	}
	var after pgtype.UUID
	if cursor.ID != "" {
		after = mustPgUUID(cursor.ID)
	}
	start, end := productMonthBounds(period)
	rows, err := sqlc.New().ProductOwnerBillingQueueCandidates(ctx, s.pool, sqlc.ProductOwnerBillingQueueCandidatesParams{
		AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Period: period, AfterTenant: after,
		MonthStart: pgTime(start), MonthEnd: pgTime(end), PageLimit: productOwnerBillingQueuePageSize + 1,
	})
	if err != nil {
		return ProductOwnerBillingQueue{}, err
	}
	candidates := make([]productOwnerBillingCandidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, productOwnerBillingCandidate{tenantID: pgUUIDString(row.TenantID), name: row.Name})
	}
	return productOwnerBillingQueue(account, app, cursor, candidates, func(scope ProductBillingScope) (api.ProductBillingReview, error) {
		return s.ReviewProductBilling(ctx, scope)
	})
}
