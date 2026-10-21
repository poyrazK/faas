package state

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type productMemoryDocument struct {
	account, app string
	document     api.ProductBillingDocument
}

// SeedProductBillingDocumentForTest stores a finalized document, which a
// MemStore cannot otherwise create for a closed month.
func (m *MemStore) SeedProductBillingDocumentForTest(accountID, appID string, doc api.ProductBillingDocument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.productLocked().documents[doc.ID] = productMemoryDocument{account: accountID, app: appID, document: doc}
}

// SeedProductPlanHistoryForTest appends a backdated assignment to a
// customer's plan history (ADR-20261021154511), which a MemStore otherwise
// records at the current time.
func (m *MemStore) SeedProductPlanHistoryForTest(account, app, tenant, revision, plan string, first bool, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.productLocked()
	if d.planHistory == nil {
		d.planHistory = map[string][]productMemoryPlanEntry{}
	}
	key := app + "/" + tenant
	d.planHistory[key] = append(d.planHistory[key], productMemoryPlanEntry{account: account, revision: revision, plan: plan, first: first, at: at.UTC()})
	sort.SliceStable(d.planHistory[key], func(i, j int) bool { return d.planHistory[key][i].at.Before(d.planHistory[key][j].at) })
}

// SeedProductOperationForTest stores an operation's Product Control receipt
// and, when the operation is terminal, its outcome, which a MemStore cannot
// otherwise backdate into a closed month.
func (m *MemStore) SeedProductOperationForTest(op Operation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertProductOperationLocked(op)
	m.settleProductOperationLocked(op)
}

func (m *MemStore) productBillingScopeLocked(scope ProductBillingScope) (api.ProductCatalog, error) {
	if err := validateProductBillingScope(scope); err != nil {
		return api.ProductCatalog{}, err
	}
	app, ok := m.apps[scope.AppID]
	tenant, exists := m.platformTenants[scope.TenantID]
	if !ok || app.AccountID != scope.AccountID || app.Status == AppDeleted || !exists || tenant.AccountID != scope.AccountID {
		return api.ProductCatalog{}, ErrNotFound
	}
	d := m.productLocked()
	catalog, ok := d.revisions[scope.AppID+"/"+d.heads[scope.AppID]]
	if !ok {
		return catalog, ErrNotFound
	}
	return catalog, nil
}

func productMemoryReceiptInScope(row productMemoryReceipt, scope ProductBillingScope) bool {
	return row.account == scope.AccountID && row.app == scope.AppID && row.tenant == scope.TenantID && row.receipt.Period == scope.Period
}

func productMemoryDocumentInScope(row productMemoryDocument, scope ProductBillingScope) bool {
	return row.account == scope.AccountID && row.app == scope.AppID && row.document.TenantID == scope.TenantID && row.document.Period == scope.Period
}

func productMemoryEvidence(id string, row productMemoryReceipt) api.ProductReceiptEvidence {
	return api.ProductReceiptEvidence{OperationID: id, TenantID: row.tenant, Receipt: row.receipt, SucceededAt: row.succeededAt, ResolvedState: row.resolvedState, ResolvedAt: row.resolvedAt, WaivedAt: row.waivedAt, WaiverReason: row.waiverReason}
}

func (m *MemStore) productStatementLocked(scope ProductBillingScope) (api.ProductBillingDocument, bool) {
	for _, row := range m.productLocked().documents {
		if productMemoryDocumentInScope(row, scope) && row.document.Kind == "statement" {
			return cloneProductBilling(row.document), true
		}
	}
	return api.ProductBillingDocument{}, false
}

func (m *MemStore) reviewProductBillingLocked(scope ProductBillingScope) (api.ProductBillingReview, error) {
	catalog, err := m.productBillingScopeLocked(scope)
	if err != nil {
		return api.ProductBillingReview{}, err
	}
	if doc, ok := m.productStatementLocked(scope); ok {
		review := *doc.Snapshot
		review.Finalized = &doc
		return review, nil
	}
	lines := map[string]api.ProductBillingLine{}
	for _, row := range m.productLocked().receipts {
		if !productMemoryReceiptInScope(row, scope) {
			continue
		}
		r := row.receipt
		key := r.Feature + "/" + r.Revision + "/" + r.Plan + "/" + strconv.FormatInt(r.UnitPriceMillicents, 10)
		line := lines[key]
		line.Feature, line.Revision, line.Plan, line.UnitPriceMillicents = r.Feature, r.Revision, r.Plan, r.UnitPriceMillicents
		line.Starts++
		switch {
		case row.waivedAt != nil:
			line.Waived++
		case row.resolvedState == api.OperationSucceeded:
			line.Succeeded++
			line.AmountMillicents += r.UnitPriceMillicents
		case row.resolvedState == api.OperationFailed:
			line.Failed++
		case row.resolvedState == api.OperationCancelled:
			line.Cancelled++
		default:
			line.Unresolved++
		}
		for _, at := range []*time.Time{&r.AdmittedAt, row.resolvedAt, row.waivedAt} {
			if at != nil && at.After(line.ChangedAt) {
				line.ChangedAt = at.UTC()
			}
		}
		lines[key] = line
	}
	out := make([]api.ProductBillingLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, line)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Feature != b.Feature {
			return a.Feature < b.Feature
		}
		if a.Revision != b.Revision {
			return a.Revision < b.Revision
		}
		if a.Plan != b.Plan {
			return a.Plan < b.Plan
		}
		return a.UnitPriceMillicents < b.UnitPriceMillicents
	})
	fee := productMonthFee(m.productMonthPlansLocked(scope), scope.Period, func(revision string) (api.ProductCatalog, bool) {
		c, ok := m.productLocked().revisions[scope.AppID+"/"+revision]
		return c, ok
	})
	return productBillingReview(scope, catalog.Spec.Currency, out, fee, time.Now())
}

func (m *MemStore) ReviewProductBilling(_ context.Context, scope ProductBillingScope) (api.ProductBillingReview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reviewProductBillingLocked(scope)
}

func (m *MemStore) ListProductBillingReceipts(_ context.Context, scope ProductBillingScope, cursor string) (api.ProductBillingReceipts, error) {
	if err := validateProductBillingCursor(cursor); err != nil {
		return api.ProductBillingReceipts{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductBillingReceipts{}, err
	}
	out := api.ProductBillingReceipts{Receipts: []api.ProductReceiptEvidence{}}
	for id, row := range m.productLocked().receipts {
		if id > cursor && productMemoryReceiptInScope(row, scope) {
			out.Receipts = append(out.Receipts, productMemoryEvidence(id, row))
		}
	}
	sort.Slice(out.Receipts, func(i, j int) bool { return out.Receipts[i].OperationID < out.Receipts[j].OperationID })
	if len(out.Receipts) > api.ProductControlBillingPageSize {
		out.Receipts = out.Receipts[:api.ProductControlBillingPageSize]
		out.NextCursor = out.Receipts[len(out.Receipts)-1].OperationID
	}
	return cloneProductBilling(out), nil
}

func (m *MemStore) ListProductBillingDocuments(_ context.Context, scope ProductBillingScope, cursor string) (api.ProductBillingDocuments, error) {
	if err := validateProductBillingCursor(cursor); err != nil {
		return api.ProductBillingDocuments{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductBillingDocuments{}, err
	}
	out := api.ProductBillingDocuments{Documents: []api.ProductBillingDocument{}}
	for id, row := range m.productLocked().documents {
		if id > cursor && productMemoryDocumentInScope(row, scope) {
			out.Documents = append(out.Documents, row.document)
		}
	}
	sort.Slice(out.Documents, func(i, j int) bool { return out.Documents[i].ID < out.Documents[j].ID })
	if len(out.Documents) > api.ProductControlBillingPageSize {
		out.Documents = out.Documents[:api.ProductControlBillingPageSize]
		out.NextCursor = out.Documents[len(out.Documents)-1].ID
	}
	return cloneProductBilling(out), nil
}

func (m *MemStore) GetProductBillingDocument(_ context.Context, scope ProductBillingScope, id string) (api.ProductBillingDocument, error) {
	if !validProductUUID(id) {
		return api.ProductBillingDocument{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductBillingDocument{}, err
	}
	row, ok := m.productLocked().documents[id]
	if !ok || !productMemoryDocumentInScope(row, scope) {
		return api.ProductBillingDocument{}, ErrNotFound
	}
	return cloneProductBilling(row.document), nil
}

func (m *MemStore) FinalizeProductBilling(_ context.Context, scope ProductBillingScope, expected string) (api.ProductBillingDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	review, err := m.reviewProductBillingLocked(scope)
	if err != nil {
		return api.ProductBillingDocument{}, err
	}
	if err := validateProductFinalization(review, expected); err != nil {
		return api.ProductBillingDocument{}, err
	}
	if review.Finalized != nil {
		return *review.Finalized, nil
	}
	doc := productStatement(review, time.Now().UTC())
	m.productLocked().documents[doc.ID] = productMemoryDocument{scope.AccountID, scope.AppID, cloneProductBilling(doc)}
	return doc, nil
}

func (m *MemStore) WaiveProductReceipt(_ context.Context, scope ProductBillingScope, id, reason string) (api.ProductReceiptEvidence, error) {
	if !validProductUUID(id) || !validProductBillingText(reason, api.ProductControlBillingReasonMaxBytes) {
		return api.ProductReceiptEvidence{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductReceiptEvidence{}, err
	}
	d := m.productLocked()
	row, ok := d.receipts[id]
	if !ok || !productMemoryReceiptInScope(row, scope) {
		return api.ProductReceiptEvidence{}, ErrNotFound
	}
	if row.waivedAt != nil {
		if row.waiverReason != reason {
			return api.ProductReceiptEvidence{}, ErrConflict
		}
		return cloneProductBilling(productMemoryEvidence(id, row)), nil
	}
	if _, ok := m.productStatementLocked(scope); ok || row.resolvedState != "" {
		return api.ProductReceiptEvidence{}, ErrConflict
	}
	now := time.Now().UTC()
	row.waivedAt, row.waiverReason = &now, reason
	d.receipts[id] = row
	return cloneProductBilling(productMemoryEvidence(id, row)), nil
}

func (m *MemStore) CreditProductReceipt(_ context.Context, scope ProductBillingScope, req api.ProductCreditRequest) (api.ProductBillingDocument, error) {
	if err := validateProductCredit(req); err != nil || req.TenantID != scope.TenantID {
		return api.ProductBillingDocument{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductBillingDocument{}, err
	}
	d := m.productLocked()
	if row, ok := d.documents[req.ID]; ok {
		if !productMemoryDocumentInScope(row, scope) {
			return api.ProductBillingDocument{}, ErrConflict
		}
		if !productCreditMatches(row.document, scope, req) {
			return api.ProductBillingDocument{}, ErrConflict
		}
		return cloneProductBilling(row.document), nil
	}
	r, ok := d.receipts[req.OperationID]
	if !ok || !productMemoryReceiptInScope(r, scope) {
		return api.ProductBillingDocument{}, ErrNotFound
	}
	statement, ok := m.productStatementLocked(scope)
	if !ok || r.resolvedState != api.OperationSucceeded || r.waivedAt != nil {
		return api.ProductBillingDocument{}, ErrConflict
	}
	var credited int64
	for _, row := range d.documents {
		if row.document.Kind == "credit_note" && row.document.OperationID == req.OperationID {
			credited -= row.document.AmountMillicents
		}
	}
	if req.AmountMillicents > r.receipt.UnitPriceMillicents-credited {
		return api.ProductBillingDocument{}, ErrConflict
	}
	doc := newProductCredit(statement, req, time.Now().UTC())
	d.documents[doc.ID] = productMemoryDocument{scope.AccountID, scope.AppID, doc}
	return doc, nil
}

func (m *MemStore) productHandoffLocked(scope ProductBillingScope, id string) (api.ProductInvoiceHandoff, error) {
	if !validProductUUID(id) {
		return api.ProductInvoiceHandoff{}, ErrInvalidArgument
	}
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductInvoiceHandoff{}, err
	}
	d := m.productLocked()
	row, ok := d.documents[id]
	if !ok || !productMemoryDocumentInScope(row, scope) {
		return api.ProductInvoiceHandoff{}, ErrNotFound
	}
	handoff, ok := d.handoffs[id]
	if !ok {
		return handoff, ErrNotFound
	}
	return handoff, nil
}

func (m *MemStore) GetProductBillingHandoff(_ context.Context, scope ProductBillingScope, id string) (api.ProductInvoiceHandoff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.productHandoffLocked(scope, id)
}

func (m *MemStore) HandoffProductBilling(_ context.Context, scope ProductBillingScope, id, external string) (api.ProductInvoiceHandoff, error) {
	if !validProductUUID(id) || !validProductBillingText(external, api.ProductControlInvoiceReferenceMaxBytes) {
		return api.ProductInvoiceHandoff{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductInvoiceHandoff{}, err
	}
	d := m.productLocked()
	row, ok := d.documents[id]
	if !ok || !productMemoryDocumentInScope(row, scope) {
		return api.ProductInvoiceHandoff{}, ErrNotFound
	}
	if existing, ok := d.handoffs[id]; ok {
		if existing.ExternalInvoiceID != external {
			return existing, ErrConflict
		}
		return existing, nil
	}
	for docID, handoff := range d.handoffs {
		if d.documents[docID].account == scope.AccountID && handoff.ExternalInvoiceID == external {
			return api.ProductInvoiceHandoff{}, ErrConflict
		}
	}
	handoff := api.ProductInvoiceHandoff{DocumentID: id, ExternalInvoiceID: external, Currency: row.document.Currency, AmountMillicents: row.document.AmountMillicents, CreatedAt: time.Now().UTC()}
	d.handoffs[id] = handoff
	return handoff, nil
}
