package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productMemoryChange struct {
	account, app string
	fingerprint  string
	receipt      api.ProductCustomerChangeReceipt
}

func (m *MemStore) GetProductOwnerDocument(_ context.Context, scope ProductBillingScope, id string) (api.ProductBillingDocument, error) {
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

func (m *MemStore) previewProductChangeLocked(account, app string, target api.ProductCustomer) (api.ProductCustomerChangePreview, error) {
	head, err := m.productBillingScopeLocked(ProductBillingScope{AccountID: account, AppID: app, TenantID: target.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return api.ProductCustomerChangePreview{}, err
	}
	d := m.productLocked()
	if target.Revision == "" {
		target.Revision = head.Revision
	}
	catalog, ok := d.revisions[app+"/"+target.Revision]
	if !ok {
		return api.ProductCustomerChangePreview{}, ErrNotFound
	}
	var before *api.ProductCustomer
	var beforeCatalog *api.ProductCatalog
	if current, ok := d.customers[app+"/"+target.TenantID]; ok {
		before = &current
		c := d.revisions[app+"/"+current.Revision]
		beforeCatalog = &c
	}
	return productChangePreview(account, app, before, beforeCatalog, target, catalog, head, time.Now())
}

func (m *MemStore) PreviewProductCustomerChange(_ context.Context, account, app string, target api.ProductCustomer) (api.ProductCustomerChangePreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.previewProductChangeLocked(account, app, target)
	return cloneProductBilling(p), err
}

func (m *MemStore) ApplyProductCustomerChange(_ context.Context, account, app string, req api.ProductCustomerChangeRequest) (api.ProductCustomerChangeReceipt, error) {
	if err := validateProductChange(req); err != nil {
		return api.ProductCustomerChangeReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check ownership before recovering a retained result, then recover before
	// re-reading mutable assignments. An older retry cannot reapply itself.
	if _, err := m.productBillingScopeLocked(ProductBillingScope{AccountID: account, AppID: app, TenantID: req.Customer.TenantID, Period: productcontrol.Period(time.Now())}); err != nil {
		return api.ProductCustomerChangeReceipt{}, err
	}
	d := m.productLocked()
	fingerprint := productChangeFingerprint(account, app, req)
	if old, ok := d.changes[req.ID]; ok {
		if old.fingerprint != fingerprint {
			return api.ProductCustomerChangeReceipt{}, ErrConflict
		}
		return cloneProductBilling(old.receipt), nil
	}
	p, err := m.previewProductChangeLocked(account, app, req.Customer)
	if err != nil {
		return api.ProductCustomerChangeReceipt{}, err
	}
	if p.Hash != req.ExpectedHash {
		return api.ProductCustomerChangeReceipt{}, ErrProductCustomerChangeStale
	}
	for key, assigned := range d.customers {
		if p.After.AuthOrganizationID != "" && key == app+"/"+assigned.TenantID && assigned.TenantID != p.After.TenantID && assigned.AuthOrganizationID == p.After.AuthOrganizationID {
			return api.ProductCustomerChangeReceipt{}, ErrConflict
		}
	}
	r := api.ProductCustomerChangeReceipt{ID: req.ID, PreviewHash: p.Hash, Before: p.Before, After: p.After, EffectiveAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.changes == nil {
		d.changes = map[string]productMemoryChange{}
	}
	d.customers[app+"/"+p.After.TenantID] = p.After
	d.changes[req.ID] = productMemoryChange{account: account, app: app, fingerprint: fingerprint, receipt: r}
	return cloneProductBilling(r), nil
}

func (m *MemStore) InspectProductBinding(_ context.Context, account, app string, feature api.ProductFeature) (OperationDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[app]; !ok || a.AccountID != account || a.Status == AppDeleted {
		return OperationDefinition{}, ErrNotFound
	}
	return m.productDefinitionLocked(account, app, feature)
}

func (m *MemStore) ListProductOwnerRuns(_ context.Context, scope ProductBillingScope, raw string) (api.ProductOwnerRuns, error) {
	c, err := decodeProductOwnerCursor(raw, productOwnerCursorScope("runs", scope.AccountID, scope.AppID, scope.TenantID, scope.Period), true)
	if err != nil {
		return api.ProductOwnerRuns{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productBillingScopeLocked(scope); err != nil {
		return api.ProductOwnerRuns{}, err
	}
	out := api.ProductOwnerRuns{Runs: []api.ProductOwnerRun{}}
	d := m.productLocked()
	for id, row := range d.receipts {
		if !productMemoryReceiptInScope(row, scope) || (raw != "" && (row.receipt.AdmittedAt.After(c.At) || row.receipt.AdmittedAt.Equal(c.At) && id >= c.ID)) {
			continue
		}
		run := api.ProductOwnerRun{Evidence: productMemoryEvidence(id, row)}
		if op, ok := m.operationMemoryLocked().operations[id]; ok && op.AccountID == scope.AccountID && op.AppID == scope.AppID && op.PlatformTenantID == scope.TenantID && operationRetained(op, time.Now()) {
			run.State = op.State
		}
		for _, doc := range d.documents {
			if productMemoryDocumentInScope(doc, scope) && doc.document.Kind == "credit_note" && doc.document.OperationID == id {
				run.CreditedMillicents -= doc.document.AmountMillicents
			}
		}
		out.Runs = append(out.Runs, run)
	}
	sort.Slice(out.Runs, func(i, j int) bool {
		a, b := out.Runs[i].Evidence, out.Runs[j].Evidence
		if a.Receipt.AdmittedAt.Equal(b.Receipt.AdmittedAt) {
			return a.OperationID > b.OperationID
		}
		return a.Receipt.AdmittedAt.After(b.Receipt.AdmittedAt)
	})
	if len(out.Runs) > api.ProductControlOwnerPageSize {
		out.Runs = out.Runs[:api.ProductControlOwnerPageSize]
		last := out.Runs[len(out.Runs)-1].Evidence
		c.ID, c.At = last.OperationID, last.Receipt.AdmittedAt
		out.NextCursor = encodeProductOwnerCursor(c)
	}
	return cloneProductBilling(out), nil
}

func (m *MemStore) ListProductOwnerAttention(_ context.Context, account, app, raw string) (api.ProductOwnerAttentionPage, error) {
	c, err := decodeProductOwnerCursor(raw, productOwnerCursorScope("attention", account, app, "", ""), false)
	if err != nil {
		return api.ProductOwnerAttentionPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[app]; !ok || a.AccountID != account || a.Status == AppDeleted {
		return api.ProductOwnerAttentionPage{}, ErrNotFound
	}
	groups := map[string]api.ProductOwnerAttention{}
	for id, row := range m.productLocked().receipts {
		if row.account != account || row.app != app {
			continue
		}
		scope := ProductBillingScope{AccountID: account, AppID: app, TenantID: row.tenant, Period: row.receipt.Period}
		if _, ok := m.productStatementLocked(scope); ok {
			continue
		}
		key := row.receipt.Period + "/" + row.tenant
		item := groups[key]
		item.TenantID, item.Period = row.tenant, row.receipt.Period
		if row.resolvedState == "" && row.waivedAt == nil {
			item.Unresolved++
			op, ok := m.operationMemoryLocked().operations[id]
			if !ok || !operationIsActive(op) {
				item.NeedsReview++
			}
		}
		groups[key] = item
	}
	out := api.ProductOwnerAttentionPage{Items: []api.ProductOwnerAttention{}}
	for _, item := range groups {
		if (item.Period < productcontrol.Period(time.Now()) || item.NeedsReview > 0) && (raw == "" || item.Period > c.Period || item.Period == c.Period && item.TenantID > c.ID) {
			out.Items = append(out.Items, item)
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Period == b.Period {
			return a.TenantID < b.TenantID
		}
		return a.Period < b.Period
	})
	if len(out.Items) > api.ProductControlOwnerPageSize {
		out.Items = out.Items[:api.ProductControlOwnerPageSize]
		last := out.Items[len(out.Items)-1]
		c.ID, c.Period = last.TenantID, last.Period
		out.NextCursor = encodeProductOwnerCursor(c)
	}
	return out, nil
}
