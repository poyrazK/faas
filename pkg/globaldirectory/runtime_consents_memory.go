package globaldirectory

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ RuntimeConsentStore = (*MemoryStore)(nil)

// Caller holds the directory mutex; the account adapter supplies live state.
func (s *MemoryStore) checkRuntimeConsent(ctx context.Context, r RuntimeConsentRecord) error {
	o, ok := s.owners[r.OwnerID]
	if !ok {
		return ErrNotFound
	}
	if o.Shared() {
		return ErrOrgTargetsUnavailable
	}
	if err := s.quotaAuthority(ctx, r.OwnerID, o.AccountID, ""); err != nil {
		return err
	}
	a, err := s.app(r.OwnerID, r.Slug)
	if err != nil || a.ID != r.Consent.GlobalAppID || runtimeConsentOwner(a.OwnerID) != r.Consent.OwnerID || s.runtimeQuotaIssuer != r.Consent.IssuerID {
		return ErrRuntimeConsentUnavailable
	}
	t, ok := s.delegationTarget(a.ID, r.Consent.Binding.Region)
	if !ok || !t.Resolvable() || runtimeConsentBinding(t) != r.Consent.Binding {
		return ErrStaleCell
	}
	if !r.setupAllowed(time.Now()) {
		return ErrRuntimeConsentUnavailable
	}
	return nil
}

func (s *MemoryStore) PrepareRuntimeConsent(ctx context.Context, owner, slug string, c runtimequota.Consent) (RuntimeConsentRecord, error) {
	if c.Validate() != nil || !ValidSlug(slug) || runtimeConsentOwner(owner) != c.OwnerID {
		return RuntimeConsentRecord{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := RuntimeConsentRecord{Consent: c, OwnerID: owner, Slug: slug, CreatedAt: now, Status: RuntimeConsentPending}
	if old, ok := s.runtimeConsents[c.ID]; ok {
		if old.Consent != c || old.OwnerID != owner || old.Slug != slug {
			return RuntimeConsentRecord{}, ErrConflict
		}
		r = old
	}
	if err := s.checkRuntimeConsent(ctx, r); err != nil {
		return RuntimeConsentRecord{}, err
	}
	if old, ok := s.runtimeConsents[c.ID]; ok {
		return copyRuntimeConsent(old), nil
	}
	if time.UnixMicro(c.ExpiresAtMicros).After(now.Add(api.RegionalRuntimeConsentMaxLifetime)) {
		return RuntimeConsentRecord{}, ErrConflict
	}
	count := int64(1)
	for _, old := range s.runtimeConsents {
		if old.Consent.GlobalAppID == c.GlobalAppID && old.Status != RuntimeConsentRevoked && time.UnixMicro(old.Consent.ExpiresAtMicros).After(now) {
			count++
		}
	}
	if count > api.GlobalRuntimeConsentsPerAppMax {
		return RuntimeConsentRecord{}, &RuntimeConsentLimitError{Limit: api.GlobalRuntimeConsentsPerAppMax, Observed: count}
	}
	s.runtimeConsents[c.ID] = r
	return copyRuntimeConsent(r), nil
}

func (s *MemoryStore) runtimeConsent(owner, slug, id string) (RuntimeConsentRecord, error) {
	r, ok := s.runtimeConsents[id]
	if !ok || runtimeConsentOwner(owner) == "" || runtimeConsentOwner(owner) != r.Consent.OwnerID || r.Slug != slug {
		return RuntimeConsentRecord{}, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) RuntimeConsent(_ context.Context, owner, slug, id string) (RuntimeConsentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.runtimeConsent(owner, slug, id)
	return copyRuntimeConsent(r), err
}

func (s *MemoryStore) CheckRuntimeConsent(ctx context.Context, owner, slug, id string) (RuntimeConsentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.runtimeConsent(owner, slug, id)
	if err != nil {
		return r, err
	}
	if r.Status != RuntimeConsentActive {
		return r, ErrRuntimeConsentUnavailable
	}
	return copyRuntimeConsent(r), s.checkRuntimeConsent(ctx, r)
}

func (s *MemoryStore) ObserveRuntimeConsent(ctx context.Context, owner, slug, id string, meta state.RegionalRuntimeConsent) (RuntimeConsentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.runtimeConsent(owner, slug, id)
	if err != nil {
		return r, err
	}
	r, err = r.observe(meta)
	if err != nil {
		return r, err
	}
	if meta.RevokedAt == nil {
		if err := s.checkRuntimeConsent(ctx, r); err != nil {
			return r, err
		}
	}
	s.runtimeConsents[id] = copyRuntimeConsent(r)
	return copyRuntimeConsent(r), nil
}

func (s *MemoryStore) BeginRevokeRuntimeConsent(_ context.Context, owner, slug, id string) (RuntimeConsentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.runtimeConsent(owner, slug, id)
	if err != nil {
		return r, err
	}
	if r.Status != RuntimeConsentRevoked {
		r.Status = RuntimeConsentRevoking
		s.runtimeConsents[id] = r
	}
	return copyRuntimeConsent(r), nil
}
