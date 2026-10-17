package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

var _ HostingOIDCStore = (*MemStore)(nil)

func (m *MemStore) CreateHostingOIDCTrust(_ context.Context, trust api.HostingOIDCTrust) (api.HostingOIDCTrust, error) {
	if !hosting.ValidHostingOIDCTrust(trust.HostingOIDCTrustRules) || uuid.Validate(trust.ID) != nil || trust.CreatedAt.IsZero() {
		return trust, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site := m.hostingSites[trust.SiteID]
	if site.AccountID != trust.AccountID || site.ID == "" {
		return trust, ErrNotFound
	}
	if site.Status != "active" || !site.DurableRouting {
		return trust, ErrConflict
	}
	count := 0
	for _, old := range m.hostingOIDCTrusts {
		if old.ID == trust.ID {
			return trust, ErrConflict
		}
		if old.SiteID == trust.SiteID {
			count++
		}
	}
	if count >= api.HostingOIDCTrustsPerSite {
		return trust, &HostingQuotaError{Resource: "oidc_trusts", Limit: api.HostingOIDCTrustsPerSite, Observed: int64(count) + 1}
	}
	if m.hostingOIDCTrusts == nil {
		m.hostingOIDCTrusts = make(map[string]api.HostingOIDCTrust)
	}
	m.hostingOIDCTrusts[trust.ID] = trust
	return trust, nil
}

func (m *MemStore) ListHostingOIDCTrusts(_ context.Context, account, site string) ([]api.HostingOIDCTrust, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return nil, ErrNotFound
	}
	out := []api.HostingOIDCTrust{}
	for _, trust := range m.hostingOIDCTrusts {
		if trust.SiteID == site {
			out = append(out, trust)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) GetHostingOIDCTrust(_ context.Context, site, id string) (api.HostingOIDCTrust, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	trust, ok := m.hostingOIDCTrusts[id]
	if !ok || trust.SiteID != site {
		return trust, ErrNotFound
	}
	return trust, nil
}

func (m *MemStore) RevokeHostingOIDCTrust(_ context.Context, account, site, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	trust, ok := m.hostingOIDCTrusts[id]
	if !ok || trust.AccountID != account || trust.SiteID != site {
		return ErrNotFound
	}
	delete(m.hostingOIDCTrusts, id)
	for id, c := range m.hostingCredentials {
		if c.OIDCTrustID == trust.ID {
			delete(m.hostingCredentials, id)
		}
	}
	return nil
}

func (m *MemStore) CreateHostingOIDCCredential(_ context.Context, expected api.HostingOIDCTrust, c api.HostingAutomationCredential) (api.HostingAutomationCredential, error) {
	if !validHostingCredential(c) || c.OIDCTrustID != expected.ID || c.AccountID != expected.AccountID || c.SiteID != expected.SiteID || len(c.Operations) != 2 {
		return c, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	trust, ok := m.hostingOIDCTrusts[expected.ID]
	if !ok || trust != expected {
		return c, ErrNotFound
	}
	site := m.hostingSites[c.SiteID]
	if site.AccountID != c.AccountID || !site.DurableRouting || site.Status != "active" || site.Suspended || !c.ExpiresAt.After(time.Now()) {
		return c, ErrConflict
	}
	count := 0
	for id, old := range m.hostingCredentials {
		if old.SiteID == c.SiteID && !old.ExpiresAt.After(time.Now()) {
			delete(m.hostingCredentials, id)
			continue
		}
		if old.ID == c.ID || string(old.Hash) == string(c.Hash) {
			return c, ErrConflict
		}
		if old.SiteID == c.SiteID && old.OIDCTrustID != "" {
			count++
		}
	}
	if count >= api.HostingOIDCSessionsPerSite {
		return c, &HostingQuotaError{Resource: "oidc_sessions", Limit: api.HostingOIDCSessionsPerSite, Observed: int64(count) + 1}
	}
	if m.hostingCredentials == nil {
		m.hostingCredentials = make(map[string]api.HostingAutomationCredential)
	}
	m.hostingCredentials[c.ID] = cloneHostingCredential(c)
	return cloneHostingCredential(c), nil
}
