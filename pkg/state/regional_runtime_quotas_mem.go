package state

import (
	"context"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var _ RegionalRuntimeQuotaStore = (*MemStore)(nil)

func (s *MemStore) RegionalRuntimeQuota(_ context.Context, appID, allocationID string) (RegionalRuntimeQuotaReceipt, error) {
	if !validRegionalReleaseIDs(appID, allocationID, allocationID) {
		return RegionalRuntimeQuotaReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.regionalRuntimeQuotas[appID+"/"+allocationID]
	if !ok {
		return RegionalRuntimeQuotaReceipt{}, ErrNotFound
	}
	return cloneRegionalRuntimeQuota(r), nil
}

func (s *MemStore) RegionalRuntimeQuotaHead(_ context.Context, appID string) (RegionalRuntimeQuotaHead, error) {
	if !validRegionalReleaseIDs(appID, appID, appID) {
		return RegionalRuntimeQuotaHead{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.regionalRuntimeQuotaHeads[appID]
	if !ok {
		return RegionalRuntimeQuotaHead{}, ErrNotFound
	}
	return h, nil
}

func (s *MemStore) applyRegionalRuntimeQuota(_ context.Context, m regionalRuntimeQuotaMutation) (RegionalRuntimeQuotaReceipt, error) {
	if !m.valid() {
		return RegionalRuntimeQuotaReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := m.grant.Binding.RegionalAppID + "/" + m.grant.AllocationID
	for _, stored := range s.regionalRuntimeQuotas {
		if stored.Grant.Binding.RegionalAppID == m.grant.Binding.RegionalAppID && stored.Grant.Generation == m.grant.Generation && stored.Grant.AllocationID != m.grant.AllocationID {
			return RegionalRuntimeQuotaReceipt{}, ErrRegionalRuntimeQuotaOrder
		}
	}
	old, exists := s.regionalRuntimeQuotas[key]
	head, hasHead := s.regionalRuntimeQuotaHeads[m.grant.Binding.RegionalAppID]
	receipt, fresh, err := m.apply(old, exists, head, hasHead)
	if err != nil {
		return RegionalRuntimeQuotaReceipt{}, err
	}
	if fresh && m.retirement == nil && !s.currentRegionalQuotaApp(m.grant.Binding) {
		return RegionalRuntimeQuotaReceipt{}, ErrNotFound
	}
	s.regionalRuntimeQuotas[key] = cloneRegionalRuntimeQuota(receipt)
	if receipt.Retirement != nil {
		s.retireRegionalCapacityLocked(*receipt.Retirement)
	}
	if !hasHead || (fresh && m.grant.Generation > head.Grant.Generation) {
		s.regionalRuntimeQuotaHeads[m.grant.Binding.RegionalAppID] = RegionalRuntimeQuotaHead{Grant: m.grant}
	}
	return cloneRegionalRuntimeQuota(receipt), nil
}

func (s *MemStore) currentRegionalQuotaApp(b runtimequota.Binding) bool {
	app, ok := s.apps[b.RegionalAppID]
	if !ok || regionalQuotaCanonicalID(app.AccountID) != b.RegionalAccountID || regionalQuotaCanonicalID(app.OrgID) != b.RegionalOrgID || app.Slug != b.RegionalAppSlug || (app.Status != AppActive && app.Status != AppEvictedCold) {
		return false
	}
	if b.RegionalOrgID == "" {
		return true
	}
	org, ok := s.orgs[app.OrgID]
	return ok && org.Status == OrgStatusActive && !org.DeletedPending
}

func regionalQuotaCanonicalID(id string) string {
	u, err := uuid.Parse(id)
	if err != nil {
		return ""
	}
	return u.String()
}
