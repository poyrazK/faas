package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

var _ HostingAutomationStore = (*MemStore)(nil)

func (m *MemStore) CreateHostingAutomationCredential(_ context.Context, c api.HostingAutomationCredential) (api.HostingAutomationCredential, error) {
	if c.OIDCTrustID != "" || !validHostingCredential(c) {
		return c, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site := m.hostingSites[c.SiteID]
	if site.AccountID != c.AccountID {
		return c, ErrNotFound
	}
	if !site.DurableRouting || site.Status != "active" {
		return c, ErrConflict
	}
	count := 0
	for id, old := range m.hostingCredentials {
		if old.SiteID == site.ID && !old.ExpiresAt.After(time.Now()) {
			delete(m.hostingCredentials, id)
			continue
		}
		if old.SiteID == site.ID && old.OIDCTrustID == "" {
			count++
		}
		if old.ID == c.ID || string(old.Hash) == string(c.Hash) {
			return c, ErrConflict
		}
	}
	if count >= api.HostingAutomationCredentialsPerSite {
		return c, &HostingQuotaError{Limit: api.HostingAutomationCredentialsPerSite, Observed: api.HostingAutomationCredentialsPerSite + 1, Resource: "automation_credentials"}
	}
	if m.hostingCredentials == nil {
		m.hostingCredentials = make(map[string]api.HostingAutomationCredential)
	}
	m.hostingCredentials[c.ID] = cloneHostingCredential(c)
	return cloneHostingCredential(c), nil
}
func (m *MemStore) ListHostingAutomationCredentials(_ context.Context, account, site string) ([]api.HostingAutomationCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return nil, ErrNotFound
	}
	out := []api.HostingAutomationCredential{}
	for _, c := range m.hostingCredentials {
		if c.SiteID == site && c.OIDCTrustID == "" {
			c = cloneHostingCredential(c)
			c.Hash = nil
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *MemStore) RevokeHostingAutomationCredential(_ context.Context, account, site, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.hostingCredentials[id]
	if !ok || c.AccountID != account || c.SiteID != site || c.OIDCTrustID != "" {
		return ErrNotFound
	}
	delete(m.hostingCredentials, id)
	return nil
}
func (m *MemStore) AuthenticateHostingAutomationToken(_ context.Context, hash []byte) (Account, APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.hostingCredentials {
		if string(c.Hash) != string(hash) {
			continue
		}
		site := m.hostingSites[c.SiteID]
		account, ok := m.accounts[c.AccountID]
		if !ok || site.AccountID != c.AccountID || site.Status != "active" || !site.DurableRouting || !c.ExpiresAt.After(time.Now()) {
			return Account{}, APIKey{}, ErrNotFound
		}
		return account, hostingCredentialKey(c), nil
	}
	return Account{}, APIKey{}, ErrNotFound
}
func (m *MemStore) CreateHostingCIRun(_ context.Context, account, site, id string, req api.HostingCIRunRequest) (api.HostingCIRun, error) {
	if !hosting.ValidCIRunID(id) || !hosting.ValidCIRequest(req) {
		return api.HostingCIRun{}, ErrInvalidArgument
	}
	fingerprint, _ := hosting.CIFingerprint(req)
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := m.hostingSites[site]
	if owned.AccountID != account {
		return api.HostingCIRun{}, ErrNotFound
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingCIRun{}, ErrConflict
	}
	key := site + "/" + id
	if old, ok := m.hostingCIRuns[key]; ok {
		if old.Fingerprint != fingerprint {
			return old, ErrConflict
		}
		return old, nil
	}
	count := 0
	for _, run := range m.hostingCIRuns {
		if run.SiteID == site {
			count++
		}
	}
	if count >= api.HostingCIRunsPerSite {
		return api.HostingCIRun{}, &HostingQuotaError{Limit: api.HostingCIRunsPerSite, Observed: api.HostingCIRunsPerSite + 1, Resource: "ci_runs"}
	}
	if req.ReservePreview {
		if err := requireHostingPreviewCapacity(m.hostingPreviewCapacityLocked(site, "", time.Now().UTC())); err != nil {
			return api.HostingCIRun{}, err
		}
	}
	release, err := m.createHostingReleaseLocked(account, site, req.Manifest, req.Provenance)
	if err != nil {
		return api.HostingCIRun{}, err
	}
	run := newCIRun(id, site, release.ID, fingerprint, req.PreviewTTLSeconds)
	run.PreviewReserved = req.ReservePreview
	if m.hostingCIRuns == nil {
		m.hostingCIRuns = make(map[string]api.HostingCIRun)
	}
	m.hostingCIRuns[key] = run
	return run, nil
}
func (m *MemStore) GetHostingCIRun(_ context.Context, account, site, id string) (api.HostingCIRun, error) {
	if !hosting.ValidCIRunID(id) {
		return api.HostingCIRun{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return api.HostingCIRun{}, ErrNotFound
	}
	run, ok := m.hostingCIRuns[site+"/"+id]
	if !ok {
		return run, ErrNotFound
	}
	return run, nil
}
func (m *MemStore) CreateHostingCIPreview(_ context.Context, account, site, id string) (api.HostingPreview, error) {
	if !hosting.ValidCIRunID(id) {
		return api.HostingPreview{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := m.hostingSites[site]
	if owned.AccountID != account {
		return api.HostingPreview{}, ErrNotFound
	}
	key := site + "/" + id
	run, ok := m.hostingCIRuns[key]
	if !ok {
		return api.HostingPreview{}, ErrNotFound
	}
	if run.Canceled {
		return api.HostingPreview{}, ErrConflict
	}
	if run.PreviewCreated {
		if p, ok := m.hostingPreviews[run.PreviewID]; ok {
			return cloneHostingPreview(p), nil
		}
		return api.HostingPreview{}, ErrConflict
	}
	if !run.PreviewExpiresAt.After(time.Now()) {
		return api.HostingPreview{}, ErrConflict
	}
	p, err := m.createHostingPreviewWithReservationLocked(ciPreview(run, owned), run.ID)
	if err != nil {
		return p, err
	}
	run.PreviewCreated = true
	m.hostingCIRuns[key] = run
	return p, nil
}
