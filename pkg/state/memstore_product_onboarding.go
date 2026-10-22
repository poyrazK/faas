package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productMemoryOnboarding struct {
	account, app, fingerprint string
	receipt                   api.ProductOnboardingReceipt
	access                    *time.Time
}

func (m *MemStore) onboardingOwnerLocked(account, app string) (Account, error) {
	a, ok := m.accounts[account]
	b, exists := m.apps[app]
	if !ok || !exists || b.AccountID != account || b.Status == AppDeleted {
		return Account{}, ErrNotFound
	}
	if !a.Active() {
		return Account{}, ErrProductAccess
	}
	return a, nil
}

func (m *MemStore) onboardingAuthAppLocked(account string, req api.ProductOnboardingRequest) (App, error) {
	for _, a := range m.apps {
		if a.AccountID == account && a.Slug == req.AuthApp && a.Status != AppDeleted && sameProductID(a.ID, req.AuthAppID) {
			return a, nil
		}
	}
	return App{}, ErrProductOnboardingStale
}

func (m *MemStore) previewProductOnboardingLocked(account, app string, req api.ProductOnboardingRequest) (api.ProductOnboardingPreview, error) {
	a, err := m.onboardingOwnerLocked(account, app)
	if err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	if a.Plan.ConsumerKeysPerAccount() == 0 {
		return api.ProductOnboardingPreview{}, ErrProductAccess
	}
	if _, err := m.onboardingAuthAppLocked(account, req); err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	d := m.productLocked()
	head, exists := d.revisions[app+"/"+d.heads[app]]
	if !exists {
		return api.ProductOnboardingPreview{}, ErrNotFound
	}
	revision := req.Revision
	if revision == "" {
		revision = head.Revision
	}
	catalog, exists := d.revisions[app+"/"+revision]
	if !exists {
		return api.ProductOnboardingPreview{}, ErrNotFound
	}
	var tenant *PlatformTenant
	count := 0
	for _, t := range m.platformTenants {
		if t.AccountID != account {
			continue
		}
		count++
		if (req.TenantID != "" && t.ID == req.TenantID) || (req.TenantID == "" && t.ExternalRef == req.ExternalRef) {
			v := t
			tenant = &v
		}
	}
	if req.TenantID != "" && tenant == nil {
		return api.ProductOnboardingPreview{}, ErrNotFound
	}
	var before *api.ProductCustomer
	for key, c := range d.customers {
		if key != app+"/"+c.TenantID {
			continue
		}
		if tenant != nil && c.TenantID == tenant.ID {
			v := c
			before = &v
		} else if c.AuthOrganizationID == req.OrganizationID {
			return api.ProductOnboardingPreview{}, ErrConflict
		}
	}
	if req.Revision == "" && before != nil {
		var ok bool
		catalog, ok = d.revisions[app+"/"+before.Revision]
		if !ok {
			return api.ProductOnboardingPreview{}, ErrNotFound
		}
	}
	return productOnboardingPreview(account, app, req, head, catalog, tenant, before, count, a.Plan.ConsumerKeysPerAccount(), time.Now())
}

func (m *MemStore) PreviewProductOnboarding(_ context.Context, account, app string, req api.ProductOnboardingRequest) (api.ProductOnboardingPreview, error) {
	if err := validateProductOnboarding(req); err != nil {
		return api.ProductOnboardingPreview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.previewProductOnboardingLocked(account, app, req)
	return cloneProductBilling(p), err
}

func (m *MemStore) ApplyProductOnboarding(_ context.Context, account, app string, c api.ProductOnboardingConfirmation) (api.ProductOnboardingReceipt, error) {
	if err := validateProductOnboardingConfirmation(c); err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.onboardingOwnerLocked(account, app); err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	d := m.productLocked()
	fingerprint := productOnboardingFingerprint(account, app, c)
	if old, ok := d.onboardings[c.Request.ID]; ok {
		if old.account != account || old.app != app || old.fingerprint != fingerprint {
			return api.ProductOnboardingReceipt{}, ErrConflict
		}
		return cloneProductBilling(old.receipt), nil
	}
	p, err := m.previewProductOnboardingLocked(account, app, c.Request)
	if err != nil {
		return api.ProductOnboardingReceipt{}, err
	}
	if p.Hash != c.ExpectedHash {
		return api.ProductOnboardingReceipt{}, ErrProductOnboardingStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenant := PlatformTenant{ID: p.Tenant.ID, AccountID: account, ExternalRef: p.Tenant.ExternalRef, Name: p.Tenant.Name, Status: PlatformTenantActive, CreatedAt: now, UpdatedAt: now}
	if p.CreatesTenant {
		tenant.ID = uuid.NewString()
		m.platformTenants[tenant.ID] = tenant
	} else {
		tenant = m.platformTenants[p.Tenant.ID]
	}
	receipt := newProductOnboardingReceipt(p, tenant, now)
	if d.onboardings == nil {
		d.onboardings = map[string]productMemoryOnboarding{}
	}
	m.assignProductCustomerLocked(account, app, receipt.Customer)
	d.onboardings[receipt.ID] = productMemoryOnboarding{account: account, app: app, fingerprint: fingerprint, receipt: receipt}
	return cloneProductBilling(receipt), nil
}

func (m *MemStore) productOnboardingStatusLocked(row productMemoryOnboarding) (api.ProductOnboardingStatus, error) {
	if _, err := m.onboardingOwnerLocked(row.account, row.app); err != nil {
		return api.ProductOnboardingStatus{}, err
	}
	d := m.productLocked()
	tenant, ok := m.platformTenants[row.receipt.Tenant.ID]
	if !ok || tenant.AccountID != row.account {
		return api.ProductOnboardingStatus{}, ErrNotFound
	}
	var current *api.ProductCustomer
	if c, ok := d.customers[row.app+"/"+tenant.ID]; ok {
		current = &c
	}
	head := d.revisions[row.app+"/"+d.heads[row.app]]
	authApp, _ := m.onboardingAuthAppLocked(row.account, row.receipt.Request)
	var first *api.ProductOnboardingSuccess
	for id, r := range d.receipts {
		if r.account != row.account || r.app != row.app || r.tenant != tenant.ID || r.receipt.AdmittedAt.Before(row.receipt.CreatedAt) || r.resolvedState != api.OperationSucceeded || r.resolvedAt == nil {
			continue
		}
		if first == nil || r.resolvedAt.Before(first.VerifiedAt) || (r.resolvedAt.Equal(first.VerifiedAt) && id < first.OperationID) {
			first = &api.ProductOnboardingSuccess{OperationID: id, Feature: r.receipt.Feature, VerifiedAt: *r.resolvedAt}
		}
	}
	return cloneProductBilling(productOnboardingStatus(row.receipt, current, tenant, head, authApp, row.access, first)), nil
}

func (m *MemStore) GetProductOnboarding(_ context.Context, account, app, id string) (api.ProductOnboardingStatus, error) {
	if !validProductUUID(id) {
		return api.ProductOnboardingStatus{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.productLocked().onboardings[id]
	if !ok || row.account != account || row.app != app {
		return api.ProductOnboardingStatus{}, ErrNotFound
	}
	return m.productOnboardingStatusLocked(row)
}

func (m *MemStore) ProductOnboardingByTenant(_ context.Context, account, app, tenant string) (api.ProductOnboardingStatus, error) {
	if !validProductUUID(tenant) {
		return api.ProductOnboardingStatus{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *productMemoryOnboarding
	for _, r := range m.productLocked().onboardings {
		if r.account != account || r.app != app || r.receipt.Tenant.ID != tenant {
			continue
		}
		if latest == nil || r.receipt.CreatedAt.After(latest.receipt.CreatedAt) || (r.receipt.CreatedAt.Equal(latest.receipt.CreatedAt) && r.receipt.ID > latest.receipt.ID) {
			v := r
			latest = &v
		}
	}
	if latest == nil {
		return api.ProductOnboardingStatus{}, ErrNotFound
	}
	return m.productOnboardingStatusLocked(*latest)
}

func (m *MemStore) ObserveProductCustomerAccess(_ context.Context, account, app, tenant, authApp, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.onboardingOwnerLocked(account, app); err != nil {
		return err
	}
	d := m.productLocked()
	c, ok := d.customers[app+"/"+tenant]
	t := m.platformTenants[tenant]
	head := d.revisions[app+"/"+d.heads[app]]
	if !ok || c.Suspended || c.AuthOrganizationID != org || head.Spec.AuthApp != authApp || t.AccountID != account || t.Status != PlatformTenantActive {
		return ErrProductAccess
	}
	for id, row := range d.onboardings {
		if row.account != account || row.app != app || row.receipt.Tenant.ID != tenant || row.receipt.Request.AuthApp != authApp || row.receipt.Request.OrganizationID != org || row.access != nil {
			continue
		}
		if _, err := m.onboardingAuthAppLocked(account, row.receipt.Request); err != nil {
			continue
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		row.access = &now
		d.onboardings[id] = row
	}
	return nil
}
