package managedpostgres

import (
	"context"
	"time"
)

func billingProgressKey(provider, subjectID string, periodStart time.Time, mode string) string {
	return provider + "\x00" + subjectID + "\x00" + monthStart(periodStart).Format(time.RFC3339) + "\x00" + mode
}

// GetBillingProgress implements BillingProgressStore (ADR-20261031165104).
func (s *MemoryStore) GetBillingProgress(_ context.Context, provider, subjectID string, periodStart time.Time, mode string) (BillingProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.billingProgress[billingProgressKey(provider, subjectID, periodStart, mode)]
	if !ok {
		return BillingProgress{}, ErrNotFound
	}
	return cloneBillingProgress(p), nil
}

// SaveBillingProgress implements BillingProgressStore.
func (s *MemoryStore) SaveBillingProgress(_ context.Context, p BillingProgress, now time.Time) (BillingProgress, error) {
	p.PeriodStart = monthStart(p.PeriodStart)
	if !p.valid() {
		return BillingProgress{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := billingProgressKey(p.Provider, p.SubjectID, p.PeriodStart, p.Mode)
	if s.billingProgress[key].Version != p.Version {
		return BillingProgress{}, ErrConflict
	}
	if s.billingProgress == nil {
		s.billingProgress = map[string]BillingProgress{}
	}
	p.Version++
	p.UpdatedAt = now
	s.billingProgress[key] = cloneBillingProgress(p)
	return cloneBillingProgress(p), nil
}

func cloneBillingProgress(p BillingProgress) BillingProgress {
	if p.ClosedAt != nil {
		at := *p.ClosedAt
		p.ClosedAt = &at
	}
	return p
}
