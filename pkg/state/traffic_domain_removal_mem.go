// adr: 570
package state

import (
	"context"
	"time"
)

func (m *MemStore) checkMemTrafficDomainBindingLocked(ctx context.Context, domain string, proposed CustomDomain) error {
	account := m.apps[m.domains[domain].AppID].AccountID
	if app, found := m.apps[proposed.AppID]; found {
		account = app.AccountID
	}
	return m.checkMemTrafficTenantBindingLocked(ctx, account, []string{domain}, memTrafficPolicyChange{Domains: map[string]CustomDomain{domain: proposed}})
}

func (m *MemStore) deleteTrafficCustomDomainLocked(ctx context.Context, domain, expectedApp string, activity *OrgActivity) (int64, error) {
	claim, found := m.domains[domain]
	if !found || expectedApp != "" && claim.AppID != expectedApp {
		return 0, ErrNotFound
	}
	if err := m.checkMemTrafficDomainBindingLocked(ctx, domain, CustomDomain{}); err != nil {
		return 0, err
	}
	if err := m.deleteCustomDomainLocked(domain); err != nil {
		return 0, err
	}
	if activity == nil {
		return 0, nil
	}
	return m.enqueueOrgActivityOutboxLocked(*activity), nil
}

func (m *MemStore) DeleteCustomDomainForApp(ctx context.Context, domain, app string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.deleteTrafficCustomDomainLocked(ctx, domain, app, nil)
	return err
}

func (m *MemStore) DeleteCustomDomainForAppWithActivity(ctx context.Context, domain, app string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteTrafficCustomDomainLocked(ctx, domain, app, &entry)
}
