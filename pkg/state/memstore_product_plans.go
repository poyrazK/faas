package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productMemoryPlanChange struct {
	account, app, tenant, fingerprint string
	receipt                           api.ProductPlanChangeReceipt
}

func (m *MemStore) productPlanViewLocked(s ProductPlanScope) (productPlanView, error) {
	var v productPlanView
	if err := validateProductPlanScope(s); err != nil {
		return v, err
	}
	if _, err := m.onboardingOwnerLocked(s.AccountID, s.AppID); err != nil {
		return v, err
	}
	if _, err := m.onboardingAuthAppLocked(s.AccountID, api.ProductOnboardingRequest{AuthApp: s.AuthApp, AuthAppID: s.AuthAppID}); err != nil {
		return v, ErrProductAccess
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	head, err := m.productBillingScopeLocked(ProductBillingScope{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Period: productcontrol.Period(now)})
	if err != nil {
		return v, err
	}
	m.applyDueProductPlanLocked(s.AccountID, s.AppID, s.TenantID, now)
	d := m.productLocked()
	c, ok := d.customers[s.AppID+"/"+s.TenantID]
	if !ok {
		return v, ErrNotFound
	}
	if err := productPlanAuthorized(s, head, c, m.platformTenants[s.TenantID]); err != nil {
		return v, err
	}
	v = productPlanView{head: head, current: d.revisions[s.AppID+"/"+c.Revision], customer: c, period: productcontrol.Period(now), now: now}
	v.starts = m.productPlanStartsLocked(ProductBillingScope{s.AccountID, s.AppID, s.TenantID, v.period}, c)
	return v, nil
}

func (m *MemStore) productPlanStartsLocked(scope ProductBillingScope, customer api.ProductCustomer) map[string]productPlanStartCounts {
	starts := map[string]productPlanStartCounts{}
	from, until, hasTrial := productTrialInPeriod(customer, scope.Period)
	for _, r := range m.productLocked().receipts {
		if r.account != scope.AccountID || r.app != scope.AppID || r.tenant != scope.TenantID || r.receipt.Period != scope.Period {
			continue
		}
		count := starts[r.receipt.Feature]
		count.total++
		if hasTrial && !r.receipt.AdmittedAt.Before(from) && r.receipt.AdmittedAt.Before(until) {
			count.trial++
		}
		starts[r.receipt.Feature] = count
	}
	return starts
}

func (m *MemStore) productPlanHistoryLocked(scope ProductBillingScope) productPlanHistory {
	return productPlanHistory{held: m.productMonthPlansLocked(scope), catalog: func(revision string) (api.ProductCatalog, bool) {
		catalog, ok := m.productLocked().revisions[scope.AppID+"/"+revision]
		return catalog, ok
	}}
}

func (m *MemStore) GetProductPlanOptions(_ context.Context, s ProductPlanScope) (api.ProductPlanOptions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.productPlanViewLocked(s)
	if err != nil {
		return api.ProductPlanOptions{}, err
	}
	return productPlanOptions(v), nil
}

func (m *MemStore) PreviewProductPlanChange(_ context.Context, s ProductPlanScope, selection api.ProductPlanSelection) (api.ProductPlanChangePreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.productPlanViewLocked(s)
	if err != nil {
		return api.ProductPlanChangePreview{}, err
	}
	v.history = m.productPlanHistoryLocked(ProductBillingScope{s.AccountID, s.AppID, s.TenantID, v.period})
	return productPlanPreview(s, v, selection)
}

func (m *MemStore) ApplyProductPlanChange(_ context.Context, s ProductPlanScope, c api.ProductPlanChangeConfirmation) (api.ProductPlanChangeReceipt, error) {
	if err := validateProductPlanConfirmation(c); err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.productPlanViewLocked(s)
	if err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	d := m.productLocked()
	fingerprint := productPlanFingerprint(s, c)
	if old, ok := d.planChanges[c.Request.ID]; ok {
		if old.fingerprint != fingerprint {
			return api.ProductPlanChangeReceipt{}, ErrConflict
		}
		return cloneProductBilling(old.receipt), nil
	}
	v.history = m.productPlanHistoryLocked(ProductBillingScope{s.AccountID, s.AppID, s.TenantID, v.period})
	p, err := productPlanPreview(s, v, c.Request)
	if err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	if p.Hash != c.ExpectedHash {
		return api.ProductPlanChangeReceipt{}, ErrProductPlanChangeStale
	}
	r := productPlanReceipt(p, s.Role, v.now)
	if d.planChanges == nil {
		d.planChanges = map[string]productMemoryPlanChange{}
	}
	m.assignProductCustomerAtLocked(s.AccountID, s.AppID, p.AfterCustomer, v.now)
	d.planChanges[r.ID] = productMemoryPlanChange{account: s.AccountID, app: s.AppID, tenant: s.TenantID, fingerprint: fingerprint, receipt: r}
	return cloneProductBilling(r), nil
}

func (m *MemStore) GetProductPlanChange(_ context.Context, s ProductPlanScope, id string) (api.ProductPlanChangeReceipt, error) {
	if !validProductUUID(id) {
		return api.ProductPlanChangeReceipt{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productPlanViewLocked(s); err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	r, ok := m.productLocked().planChanges[id]
	if !ok || r.account != s.AccountID || r.app != s.AppID || r.tenant != s.TenantID {
		return api.ProductPlanChangeReceipt{}, ErrNotFound
	}
	return cloneProductBilling(r.receipt), nil
}

func (m *MemStore) ListProductPlanChanges(_ context.Context, s ProductPlanScope, raw string) (api.ProductPlanChanges, error) {
	c, err := productPlanCursor(s, raw)
	if err != nil {
		return api.ProductPlanChanges{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.productPlanViewLocked(s); err != nil {
		return api.ProductPlanChanges{}, err
	}
	out := api.ProductPlanChanges{Changes: []api.ProductPlanChangeReceipt{}}
	for _, r := range m.productLocked().planChanges {
		if r.account != s.AccountID || r.app != s.AppID || r.tenant != s.TenantID || raw != "" && (r.receipt.EffectiveAt.After(c.At) || r.receipt.EffectiveAt.Equal(c.At) && r.receipt.ID >= c.ID) {
			continue
		}
		out.Changes = append(out.Changes, r.receipt)
	}
	sort.Slice(out.Changes, func(i, j int) bool {
		a, b := out.Changes[i], out.Changes[j]
		if a.EffectiveAt.Equal(b.EffectiveAt) {
			return a.ID > b.ID
		}
		return a.EffectiveAt.After(b.EffectiveAt)
	})
	if len(out.Changes) > api.ProductControlOwnerPageSize {
		out.Changes = out.Changes[:api.ProductControlOwnerPageSize]
		last := out.Changes[len(out.Changes)-1]
		c.ID, c.At = last.ID, last.EffectiveAt
		out.NextCursor = encodeProductOwnerCursor(c)
	}
	return cloneProductBilling(out), nil
}
