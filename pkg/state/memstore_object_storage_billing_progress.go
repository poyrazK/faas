package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func objectStorageBillingProgressKey(provider, accountID string, periodStart time.Time, mode string) string {
	return provider + "\x00" + accountID + "\x00" + ObjectStoragePeriod(periodStart).Format(time.RFC3339) + "\x00" + mode
}

func (m *MemStore) GetObjectStorageBillingProgress(ctx context.Context, provider, accountID string, periodStart time.Time, mode string) (ObjectStorageBillingProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ObjectStorageBillingProgress{}, err
	}
	p, ok := m.objectStorageBillingProgress[objectStorageBillingProgressKey(provider, accountID, periodStart, mode)]
	if !ok {
		return ObjectStorageBillingProgress{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) SaveObjectStorageBillingProgress(ctx context.Context, p ObjectStorageBillingProgress) (ObjectStorageBillingProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ObjectStorageBillingProgress{}, err
	}
	p, err := validateObjectStorageBillingProgress(p)
	if err != nil {
		return ObjectStorageBillingProgress{}, err
	}
	key := objectStorageBillingProgressKey(p.Provider, p.AccountID, p.PeriodStart, p.Mode)
	stored, exists := m.objectStorageBillingProgress[key]
	// Delivered millicents were billed; a write may never lower them.
	if stored.Version != p.Version || exists != (p.Version != 0) || p.DeliveredMillicents < stored.DeliveredMillicents {
		return ObjectStorageBillingProgress{}, ErrConflict
	}
	if m.objectStorageBillingProgress == nil {
		m.objectStorageBillingProgress = map[string]ObjectStorageBillingProgress{}
	}
	p.CreditedMillicents = stored.CreditedMillicents
	p.Version++
	p.UpdatedAt = m.clock().UTC()
	m.objectStorageBillingProgress[key] = p
	return p, nil
}

func (m *MemStore) LiveObjectStorageMillicents(ctx context.Context, accountID string, periodStart time.Time) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return 0, 0, err
	}
	periodStart = ObjectStoragePeriod(periodStart)
	var delivered, pending int64
	for _, p := range m.objectStorageBillingProgress {
		if p.AccountID == accountID && p.Mode == ObjectStorageDeliveryLive && p.PeriodStart.Equal(periodStart) {
			delivered = boundedObjectAdd(delivered, p.DeliveredMillicents)
			pending = boundedObjectAdd(pending, p.PendingMillicents)
		}
	}
	return delivered, pending, nil
}

// creditObjectStorageAdoptionLocked moves what the organization's members
// were charged for the current month's storage onto the organization's rows
// as credit when it adopts their buckets: the month's storage moves to the
// organization, which owes only the rest (ADR-20261027093220). A member's
// charge can include buckets that stay its own, so adoption can under-bill
// the month but never bills it twice.
func (m *MemStore) creditObjectStorageAdoptionLocked(org Org) {
	members := map[string]bool{}
	for _, b := range m.objectBuckets {
		if b.OrgID == org.ID || m.apps[b.AppID].OrgID == org.ID {
			members[b.AccountID] = true
		}
	}
	period := ObjectStoragePeriod(m.clock())
	credits := map[string]ObjectStorageBillingProgress{}
	for _, p := range m.objectStorageBillingProgress {
		if !members[p.AccountID] || p.AccountID == org.ID || !p.PeriodStart.Equal(period) {
			continue
		}
		key := objectStorageBillingProgressKey(p.Provider, org.ID, period, p.Mode)
		credit := credits[key]
		credit.Provider, credit.AccountID, credit.PeriodStart, credit.Mode = p.Provider, org.ID, period, p.Mode
		credit.CreditedMillicents = boundedObjectAdd(credit.CreditedMillicents, boundedObjectAdd(p.DeliveredMillicents, p.PendingMillicents))
		credits[key] = credit
	}
	for key, credit := range credits {
		row, ok := m.objectStorageBillingProgress[key]
		if !ok {
			row = credit
			row.CreditedMillicents = 0
		}
		row.CreditedMillicents = min(boundedObjectAdd(row.CreditedMillicents, credit.CreditedMillicents), api.MaxObjectStoragePolicyValue)
		row.Version++
		row.UpdatedAt = m.clock().UTC()
		m.objectStorageBillingProgress[key] = row
	}
}
