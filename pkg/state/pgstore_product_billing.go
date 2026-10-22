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

func productBillingParams(scope ProductBillingScope) sqlc.ProductBillingStatementParams {
	return sqlc.ProductBillingStatementParams{AccountID: mustPgUUID(scope.AccountID), AppID: mustPgUUID(scope.AppID), TenantID: mustPgUUID(scope.TenantID), Period: scope.Period}
}

func productBillingDocumentParams(scope ProductBillingScope, id string) sqlc.ProductBillingDocumentParams {
	p := productBillingParams(scope)
	return sqlc.ProductBillingDocumentParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Period: p.Period, ID: mustPgUUID(id)}
}

func productBillingScopePG(ctx context.Context, db sqlc.DBTX, scope ProductBillingScope) error {
	if err := validateProductBillingScope(scope); err != nil {
		return err
	}
	p := productBillingParams(scope)
	exists, err := sqlc.New().ProductBillingScopeExists(ctx, db, sqlc.ProductBillingScopeExistsParams{AccountID: p.AccountID, ID: p.AppID, ID_2: p.TenantID})
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// Billing takes account, shared app, tenant, then commercial receipt locks.
// App/tenant locks precede receipts so insert foreign-key checks cannot invert
// the execution writers' code/receipt ordering.
// Execution writers take invocation/operation rows before the receipt, and never
// need this account lock. Billing must never acquire execution or code-pin rows.
func (s *PgStore) productBillingTx(ctx context.Context, scope ProductBillingScope) (pgx.Tx, error) {
	if err := validateProductBillingScope(scope); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = sqlc.New().LockCustomerOperationAccount(ctx, tx, mustPgUUID(scope.AccountID)); err == nil {
		err = productBillingScopePG(ctx, tx, scope)
	}
	if err == nil {
		_, err = sqlc.New().ProductControlOwnedApp(ctx, tx, sqlc.ProductControlOwnedAppParams{AccountID: mustPgUUID(scope.AccountID), ID: mustPgUUID(scope.AppID)})
	}
	if err == nil {
		_, err = sqlc.New().LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{AccountID: mustPgUUID(scope.AccountID), TenantID: mustPgUUID(scope.TenantID)})
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, mapErr(err)
	}
	return tx, nil
}

func productBillingPGDocument(row sqlc.ProductControlBillingDocument) (api.ProductBillingDocument, error) {
	doc := api.ProductBillingDocument{ID: pgUUIDString(row.ID), Kind: row.Kind, TenantID: pgUUIDString(row.TenantID), Period: row.Period, Currency: row.Currency, AmountMillicents: row.AmountMillicents, StatementID: pgUUIDString(row.StatementID), OperationID: pgUUIDString(row.OperationID), Reason: row.Reason, CreatedAt: row.CreatedAt.Time.UTC()}
	if row.Kind == "statement" {
		if err := json.Unmarshal(row.Snapshot, &doc.Snapshot); err != nil {
			return doc, err
		}
		if doc.Snapshot == nil {
			return doc, ErrConflict
		}
	}
	return doc, nil
}

func productBillingPGStatement(ctx context.Context, db sqlc.DBTX, scope ProductBillingScope) (api.ProductBillingDocument, error) {
	row, err := sqlc.New().ProductBillingStatement(ctx, db, productBillingParams(scope))
	if err != nil {
		return api.ProductBillingDocument{}, mapErr(err)
	}
	return productBillingPGDocument(row)
}

func reviewProductBillingPG(ctx context.Context, db sqlc.DBTX, scope ProductBillingScope) (api.ProductBillingReview, error) {
	if err := productBillingScopePG(ctx, db, scope); err != nil {
		return api.ProductBillingReview{}, err
	}
	doc, err := productBillingPGStatement(ctx, db, scope)
	if err == nil {
		review := *doc.Snapshot
		review.Finalized = &doc
		return review, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return api.ProductBillingReview{}, err
	}
	catalog, err := productCatalogTx(ctx, db, scope.AccountID, scope.AppID, "")
	if err != nil {
		return api.ProductBillingReview{}, err
	}
	p := productBillingParams(scope)
	rows, err := sqlc.New().ProductBillingLines(ctx, db, sqlc.ProductBillingLinesParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Period: p.Period, PageLimit: api.ProductControlBillingMaxLines + 1})
	if err != nil {
		return api.ProductBillingReview{}, err
	}
	lines := make([]api.ProductBillingLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, api.ProductBillingLine{Feature: r.Feature, Revision: r.Revision, Plan: r.Plan, UnitPriceMillicents: r.UnitPriceMillicents, Starts: r.Starts, Succeeded: r.Succeeded, Failed: r.Failed, Cancelled: r.Cancelled, Waived: r.Waived, Unresolved: r.Unresolved, AmountMillicents: r.AmountMillicents, ChangedAt: r.ChangedAt.Time.UTC()})
	}
	fee, err := productMonthFeePG(ctx, db, scope)
	if err != nil {
		return api.ProductBillingReview{}, err
	}
	return productBillingReview(scope, catalog.Spec.Currency, lines, fee, time.Now())
}

// productMonthFeePG prices the plans a customer held in the month
// (ADR-20261021154511).
func productMonthFeePG(ctx context.Context, db sqlc.DBTX, scope ProductBillingScope) (*api.ProductBillingFee, error) {
	start, end := productMonthBounds(scope.Period)
	p := productBillingParams(scope)
	rows, err := sqlc.New().ProductControlMonthPlans(ctx, db, sqlc.ProductControlMonthPlansParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID,
		MonthStart: pgtype.Timestamptz{Time: start, Valid: true}, MonthEnd: pgtype.Timestamptz{Time: end, Valid: true}, PageLimit: api.ProductControlPlanHistoryMonthMaxPlans + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.ProductControlPlanHistoryMonthMaxPlans {
		return nil, NewOperationLimitError("product_plan_history", api.ProductControlPlanHistoryMonthMaxPlans, int64(len(rows)))
	}
	held := make([]productcontrol.PlanHeld, 0, len(rows))
	catalogs := map[string]api.ProductCatalog{}
	for _, r := range rows {
		h := productcontrol.PlanHeld{Revision: r.Revision, Plan: r.Plan, Since: r.Since.Time.UTC(), First: r.FirstAssignment}
		if r.HeldUntil.Valid {
			h.Until = r.HeldUntil.Time.UTC()
		}
		held = append(held, h)
		if _, ok := catalogs[r.Revision]; ok {
			continue
		}
		catalog, err := productCatalogTx(ctx, db, scope.AccountID, scope.AppID, r.Revision)
		if err != nil {
			return nil, err
		}
		catalogs[r.Revision] = catalog
	}
	// The trial's minutes owe no fee (ADR-20261021195901).
	var trial *productcontrol.Trial
	if customer, err := productCustomerTx(ctx, db, scope.AccountID, scope.AppID, scope.TenantID); err == nil {
		trial = productTrial(customer)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return productMonthFee(held, scope.Period, trial, func(revision string) (api.ProductCatalog, bool) {
		c, ok := catalogs[revision]
		return c, ok
	}), nil
}

func (s *PgStore) ReviewProductBilling(ctx context.Context, scope ProductBillingScope) (api.ProductBillingReview, error) {
	if err := validateProductBillingScope(scope); err != nil {
		return api.ProductBillingReview{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ProductBillingReview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := reviewProductBillingPG(ctx, tx, scope)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func productBillingPGEvidence(row sqlc.ProductBillingReceiptRow) (api.ProductReceiptEvidence, error) {
	out := api.ProductReceiptEvidence{OperationID: pgUUIDString(row.OperationID), TenantID: pgUUIDString(row.TenantID), Receipt: api.ProductOperationReceipt{Revision: row.Revision, Feature: row.Feature, Plan: row.Plan, Currency: row.Currency, Period: row.Period, UnitPriceMillicents: row.UnitPriceMillicents, AdmittedAt: row.AdmittedAt.Time.UTC()}, ResolvedState: api.OperationState(row.ResolvedState), WaiverReason: row.WaiverReason.String}
	if len(row.AdmissionEvidence) > 0 {
		if err := json.Unmarshal(row.AdmissionEvidence, &out.Receipt.Admission); err != nil {
			return out, err
		}
	}
	if row.SucceededAt.Valid {
		at := row.SucceededAt.Time.UTC()
		out.SucceededAt = &at
	}
	if row.ResolvedAt.Valid {
		at := row.ResolvedAt.Time.UTC()
		out.ResolvedAt = &at
	}
	if row.WaivedAt.Valid {
		at := row.WaivedAt.Time.UTC()
		out.WaivedAt = &at
	}
	return out, nil
}

func productBillingPGReceipt(ctx context.Context, db sqlc.DBTX, scope ProductBillingScope, id string, lock bool) (api.ProductReceiptEvidence, error) {
	p := productBillingParams(scope)
	args := sqlc.ProductBillingReceiptParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Period: p.Period, OperationID: mustPgUUID(id)}
	if lock {
		if err := sqlc.New().ProductBillingLockReceipt(ctx, db, sqlc.ProductBillingLockReceiptParams(args)); err != nil {
			return api.ProductReceiptEvidence{}, err
		}
	}
	row, err := sqlc.New().ProductBillingReceipt(ctx, db, args)
	if err != nil {
		return api.ProductReceiptEvidence{}, mapErr(err)
	}
	return productBillingPGEvidence(row)
}

func (s *PgStore) ListProductBillingReceipts(ctx context.Context, scope ProductBillingScope, cursor string) (api.ProductBillingReceipts, error) {
	if err := validateProductBillingCursor(cursor); err != nil {
		return api.ProductBillingReceipts{}, err
	}
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductBillingReceipts{}, err
	}
	p := productBillingParams(scope)
	rows, err := sqlc.New().ProductBillingReceipts(ctx, s.pool, sqlc.ProductBillingReceiptsParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Period: p.Period, Cursor: cursor, PageLimit: api.ProductControlBillingPageSize + 1})
	if err != nil {
		return api.ProductBillingReceipts{}, err
	}
	out := api.ProductBillingReceipts{Receipts: []api.ProductReceiptEvidence{}}
	for _, row := range rows {
		evidence, err := productBillingPGEvidence(sqlc.ProductBillingReceiptRow(row))
		if err != nil {
			return out, err
		}
		out.Receipts = append(out.Receipts, evidence)
	}
	if len(out.Receipts) > api.ProductControlBillingPageSize {
		out.Receipts = out.Receipts[:api.ProductControlBillingPageSize]
		out.NextCursor = out.Receipts[len(out.Receipts)-1].OperationID
	}
	return out, nil
}

func (s *PgStore) ListProductBillingDocuments(ctx context.Context, scope ProductBillingScope, cursor string) (api.ProductBillingDocuments, error) {
	if err := validateProductBillingCursor(cursor); err != nil {
		return api.ProductBillingDocuments{}, err
	}
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductBillingDocuments{}, err
	}
	p := productBillingParams(scope)
	rows, err := sqlc.New().ProductBillingDocuments(ctx, s.pool, sqlc.ProductBillingDocumentsParams{AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Period: p.Period, Cursor: cursor, PageLimit: api.ProductControlBillingPageSize + 1})
	if err != nil {
		return api.ProductBillingDocuments{}, err
	}
	out := api.ProductBillingDocuments{Documents: []api.ProductBillingDocument{}}
	for _, row := range rows {
		doc, err := productBillingPGDocument(row)
		if err != nil {
			return out, err
		}
		out.Documents = append(out.Documents, doc)
	}
	if len(out.Documents) > api.ProductControlBillingPageSize {
		out.Documents = out.Documents[:api.ProductControlBillingPageSize]
		out.NextCursor = out.Documents[len(out.Documents)-1].ID
	}
	return out, nil
}

func (s *PgStore) GetProductBillingDocument(ctx context.Context, scope ProductBillingScope, id string) (api.ProductBillingDocument, error) {
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

func productBillingPGInsert(ctx context.Context, tx pgx.Tx, scope ProductBillingScope, doc api.ProductBillingDocument) error {
	snapshot := []byte(`{}`)
	if doc.Snapshot != nil {
		var err error
		snapshot, err = json.Marshal(doc.Snapshot)
		if err != nil {
			return err
		}
	}
	p := productBillingParams(scope)
	return mapErr(sqlc.New().ProductBillingInsertDocument(ctx, tx, sqlc.ProductBillingInsertDocumentParams{ID: mustPgUUID(doc.ID), AccountID: p.AccountID, AppID: p.AppID, TenantID: p.TenantID, Period: p.Period, Kind: doc.Kind, Currency: doc.Currency, AmountMillicents: doc.AmountMillicents, StatementID: mustPgUUID(doc.StatementID), OperationID: mustPgUUID(doc.OperationID), Reason: doc.Reason, Snapshot: snapshot, CreatedAt: pgtype.Timestamptz{Time: doc.CreatedAt, Valid: true}}))
}

func (s *PgStore) FinalizeProductBilling(ctx context.Context, scope ProductBillingScope, expected string) (api.ProductBillingDocument, error) {
	tx, err := s.productBillingTx(ctx, scope)
	if err != nil {
		return api.ProductBillingDocument{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p := productBillingParams(scope)
	if err := sqlc.New().ProductBillingLockReceipts(ctx, tx, sqlc.ProductBillingLockReceiptsParams(p)); err != nil {
		return api.ProductBillingDocument{}, err
	}
	review, err := reviewProductBillingPG(ctx, tx, scope)
	if err != nil {
		return api.ProductBillingDocument{}, err
	}
	if err := validateProductFinalization(review, expected); err != nil {
		return api.ProductBillingDocument{}, err
	}
	if review.Finalized != nil {
		return *review.Finalized, tx.Commit(ctx)
	}
	doc := productStatement(review, time.Now().UTC())
	if err := productBillingPGInsert(ctx, tx, scope, doc); err != nil {
		return doc, err
	}
	return doc, tx.Commit(ctx)
}

func (s *PgStore) WaiveProductReceipt(ctx context.Context, scope ProductBillingScope, id, reason string) (api.ProductReceiptEvidence, error) {
	if !validProductUUID(id) || !validProductBillingText(reason, api.ProductControlBillingReasonMaxBytes) {
		return api.ProductReceiptEvidence{}, ErrInvalidArgument
	}
	tx, err := s.productBillingTx(ctx, scope)
	if err != nil {
		return api.ProductReceiptEvidence{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := productBillingPGReceipt(ctx, tx, scope, id, true)
	if err != nil {
		return r, err
	}
	if r.WaivedAt != nil {
		if r.WaiverReason != reason {
			return r, ErrConflict
		}
		return r, tx.Commit(ctx)
	}
	if r.ResolvedState != "" {
		return r, ErrConflict
	}
	if _, err := productBillingPGStatement(ctx, tx, scope); err == nil {
		return r, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return r, err
	}
	if err := sqlc.New().ProductBillingWaive(ctx, tx, sqlc.ProductBillingWaiveParams{OperationID: mustPgUUID(id), Reason: reason}); err != nil {
		return r, mapErr(err)
	}
	r, err = productBillingPGReceipt(ctx, tx, scope, id, false)
	if err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}

func (s *PgStore) CreditProductReceipt(ctx context.Context, scope ProductBillingScope, req api.ProductCreditRequest) (api.ProductBillingDocument, error) {
	if err := validateProductCredit(req); err != nil || req.TenantID != scope.TenantID {
		return api.ProductBillingDocument{}, ErrInvalidArgument
	}
	tx, err := s.productBillingTx(ctx, scope)
	if err != nil {
		return api.ProductBillingDocument{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().ProductBillingDocument(ctx, tx, productBillingDocumentParams(scope, req.ID))
	if err == nil {
		doc, err := productBillingPGDocument(row)
		if err != nil {
			return doc, err
		}
		if !productCreditMatches(doc, scope, req) {
			return doc, ErrConflict
		}
		return doc, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProductBillingDocument{}, err
	}
	r, err := productBillingPGReceipt(ctx, tx, scope, req.OperationID, true)
	if err != nil {
		return api.ProductBillingDocument{}, err
	}
	statement, err := productBillingPGStatement(ctx, tx, scope)
	if errors.Is(err, ErrNotFound) {
		return statement, ErrConflict
	}
	if err != nil {
		return statement, err
	}
	if r.ResolvedState != api.OperationSucceeded || r.WaivedAt != nil {
		return api.ProductBillingDocument{}, ErrConflict
	}
	credited, err := sqlc.New().ProductBillingCredited(ctx, tx, mustPgUUID(req.OperationID))
	if err != nil {
		return api.ProductBillingDocument{}, err
	}
	if req.AmountMillicents > r.Receipt.UnitPriceMillicents-credited {
		return api.ProductBillingDocument{}, ErrConflict
	}
	doc := newProductCredit(statement, req, time.Now().UTC())
	if err := productBillingPGInsert(ctx, tx, scope, doc); err != nil {
		return doc, err
	}
	return doc, tx.Commit(ctx)
}

func productBillingPGHandoff(ctx context.Context, db sqlc.DBTX, scope ProductBillingScope, id string) (api.ProductInvoiceHandoff, error) {
	r, err := sqlc.New().ProductBillingGetHandoff(ctx, db, sqlc.ProductBillingGetHandoffParams(productBillingDocumentParams(scope, id)))
	if err != nil {
		return api.ProductInvoiceHandoff{}, mapErr(err)
	}
	return api.ProductInvoiceHandoff{DocumentID: pgUUIDString(r.DocumentID), ExternalInvoiceID: r.ExternalInvoiceID, Currency: r.Currency, AmountMillicents: r.AmountMillicents, CreatedAt: r.CreatedAt.Time.UTC()}, nil
}

func (s *PgStore) GetProductBillingHandoff(ctx context.Context, scope ProductBillingScope, id string) (api.ProductInvoiceHandoff, error) {
	if !validProductUUID(id) {
		return api.ProductInvoiceHandoff{}, ErrInvalidArgument
	}
	if err := productBillingScopePG(ctx, s.pool, scope); err != nil {
		return api.ProductInvoiceHandoff{}, err
	}
	return productBillingPGHandoff(ctx, s.pool, scope, id)
}

func (s *PgStore) HandoffProductBilling(ctx context.Context, scope ProductBillingScope, id, external string) (api.ProductInvoiceHandoff, error) {
	if !validProductUUID(id) || !validProductBillingText(external, api.ProductControlInvoiceReferenceMaxBytes) {
		return api.ProductInvoiceHandoff{}, ErrInvalidArgument
	}
	tx, err := s.productBillingTx(ctx, scope)
	if err != nil {
		return api.ProductInvoiceHandoff{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if existing, err := productBillingPGHandoff(ctx, tx, scope, id); err == nil {
		if existing.ExternalInvoiceID != external {
			return existing, ErrConflict
		}
		return existing, tx.Commit(ctx)
	} else if !errors.Is(err, ErrNotFound) {
		return existing, err
	}
	if _, err := sqlc.New().ProductBillingDocument(ctx, tx, productBillingDocumentParams(scope, id)); err != nil {
		return api.ProductInvoiceHandoff{}, mapErr(err)
	}
	if err := sqlc.New().ProductBillingInsertHandoff(ctx, tx, sqlc.ProductBillingInsertHandoffParams{DocumentID: mustPgUUID(id), AccountID: mustPgUUID(scope.AccountID), ExternalInvoiceID: external}); err != nil {
		return api.ProductInvoiceHandoff{}, mapErr(err)
	}
	out, err := productBillingPGHandoff(ctx, tx, scope, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
