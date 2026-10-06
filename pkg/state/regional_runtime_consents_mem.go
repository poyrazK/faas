package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var _ RegionalRuntimeConsentStore = (*MemStore)(nil)

// Caller holds the same mutex as quota acceptance and revocation.
func (s *MemStore) currentRuntimeConsentApp(b runtimequota.Binding) bool {
	app, ok := s.apps[b.RegionalAppID]
	if !ok || app.ComputeRegion != b.Region || b.RegionalOrgID != "" || !s.currentRegionalQuotaApp(b) {
		return false
	}
	acct, ok := s.accounts[app.AccountID]
	return ok && acct.MayDeploy() && acct.EmailVerified()
}

func (s *MemStore) PutRegionalRuntimeConsent(_ context.Context, c runtimequota.Consent) (RegionalRuntimeConsent, error) {
	if c.Validate() != nil {
		return RegionalRuntimeConsent{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.currentRuntimeConsentApp(c.Binding) {
		return RegionalRuntimeConsent{}, ErrNotFound
	}
	if old, ok := s.regionalRuntimeConsents[c.ID]; ok {
		if old.Consent != c {
			return RegionalRuntimeConsent{}, ErrConflict
		}
		return cloneRuntimeConsent(old), nil // Never renew or un-revoke this UUID.
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := runtimeConsentExpiry(c, now); err != nil {
		return RegionalRuntimeConsent{}, err
	}
	count := int64(1)
	for _, old := range s.regionalRuntimeConsents {
		if old.Consent.Binding.RegionalAppID == c.Binding.RegionalAppID && old.RevokedAt == nil && time.UnixMicro(old.Consent.ExpiresAtMicros).After(now) {
			count++
		}
	}
	if count > api.RegionalRuntimeConsentsPerAppMax {
		return RegionalRuntimeConsent{}, &RegionalRuntimeConsentLimitError{Limit: api.RegionalRuntimeConsentsPerAppMax, Observed: count}
	}
	record := RegionalRuntimeConsent{Consent: c, CreatedAt: now}
	s.regionalRuntimeConsents[c.ID] = record
	return cloneRuntimeConsent(record), nil
}

func (s *MemStore) runtimeConsentLocked(owner, cell, app, id string) (RegionalRuntimeConsent, error) {
	c, ok := s.regionalRuntimeConsents[id]
	if !ok || c.Consent.Binding.RegionalAccountID != regionalQuotaCanonicalID(owner) || c.Consent.Binding.CellID != cell || c.Consent.Binding.RegionalAppID != app {
		return RegionalRuntimeConsent{}, ErrNotFound
	}
	return c, nil
}

func (s *MemStore) RegionalRuntimeConsent(_ context.Context, owner, cell, app, id string) (RegionalRuntimeConsent, error) {
	if !validRegionalReleaseIDs(cell, app, id) || regionalQuotaCanonicalID(owner) == "" {
		return RegionalRuntimeConsent{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.runtimeConsentLocked(owner, cell, app, id)
	return cloneRuntimeConsent(c), err
}

func (s *MemStore) RevokeRegionalRuntimeConsent(_ context.Context, owner, cell, app, id string) (RegionalRuntimeConsent, error) {
	if !validRegionalReleaseIDs(cell, app, id) || regionalQuotaCanonicalID(owner) == "" {
		return RegionalRuntimeConsent{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.runtimeConsentLocked(owner, cell, app, id)
	if err != nil {
		return RegionalRuntimeConsent{}, err
	}
	if c.RevokedAt == nil {
		now := time.Now().UTC().Truncate(time.Microsecond)
		c.RevokedAt = &now
		s.regionalRuntimeConsents[id] = c
	}
	if h, ok := s.regionalRuntimeQuotaHeads[app]; ok {
		h.AdmissionEnabled = false
		s.regionalRuntimeQuotaHeads[app] = h
	}
	return cloneRuntimeConsent(c), nil
}
