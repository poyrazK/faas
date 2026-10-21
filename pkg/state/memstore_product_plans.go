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
	head, err := m.productBillingScopeLocked(ProductBillingScope{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Period: productcontrol.Period(time.Now())})
	if err != nil {
		return v, err
	}
	d := m.productLocked()
	c, ok := d.customers[s.AppID+"/"+s.TenantID]
	if !ok {
		return v, ErrNotFound
	}
	if err := productPlanAuthorized(s, head, c, m.platformTenants[s.TenantID]); err != nil {
		return v, err
	}
	v = productPlanView{head: head, current: d.revisions[s.AppID+"/"+c.Revision], customer: c, starts: map[string]int64{}, period: productcontrol.Period(time.Now())}
	features := map[string]bool{}
	for _, name := range productPlanFeatures(v) {
		features[name] = true
	}
	for _, r := range d.receipts {
		if r.account == s.AccountID && r.app == s.AppID && r.tenant == s.TenantID && r.receipt.Period == v.period && features[r.receipt.Feature] {
			v.starts[r.receipt.Feature]++
		}
	}
	return v, nil
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
	p, err := productPlanPreview(s, v, c.Request)
	if err != nil {
		return api.ProductPlanChangeReceipt{}, err
	}
	if p.Hash != c.ExpectedHash {
		return api.ProductPlanChangeReceipt{}, ErrProductPlanChangeStale
	}
	r := productPlanReceipt(p, s.Role)
	if d.planChanges == nil {
		d.planChanges = map[string]productMemoryPlanChange{}
	}
	m.assignProductCustomerLocked(s.AccountID, s.AppID, p.AfterCustomer)
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
