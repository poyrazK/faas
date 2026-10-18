package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

var _ HostingRetentionStore = (*MemStore)(nil)

func (m *MemStore) hostingCleanupPlanLocked(account, site string, override *api.HostingRetentionRules, now time.Time) (api.HostingCleanupPlan, error) {
	owned, ok := m.hostingSites[site]
	if !ok || owned.AccountID != account {
		return api.HostingCleanupPlan{}, ErrNotFound
	}
	policy, ok := m.hostingRetentionPolicies[site]
	if !ok {
		policy = defaultHostingRetentionPolicy(site)
	}
	policy = cloneHostingRetentionPolicy(policy)
	rules := policy.HostingRetentionRules
	if override != nil {
		if !hosting.ValidRetentionRules(*override) {
			return api.HostingCleanupPlan{}, ErrInvalidArgument
		}
		rules = *override
	}
	releases := []api.HostingRelease{}
	for _, r := range m.hostingReleases {
		if r.SiteID == site && r.State != "deleted" {
			releases = append(releases, cloneHostingRelease(r))
		}
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].ID < releases[j].ID })
	previews := []api.HostingPreview{}
	for _, p := range m.hostingPreviews {
		if p.SiteID == site {
			previews = append(previews, p)
		}
	}
	expiry := map[string]time.Time{}
	for _, run := range m.hostingCIRuns {
		if run.SiteID == site && run.PreviewExpiresAt.After(expiry[run.ReleaseID]) {
			expiry[run.ReleaseID] = run.PreviewExpiresAt
		}
	}
	return hosting.CleanupPlan(owned, policy, rules, releases, previews, expiry, now), nil
}
func (m *MemStore) GetHostingCleanupPlan(_ context.Context, account, site string, rules *api.HostingRetentionRules, now time.Time) (api.HostingCleanupPlan, error) {
	if now.IsZero() {
		return api.HostingCleanupPlan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostingCleanupPlanLocked(account, site, rules, now)
}
func (m *MemStore) SetHostingRetentionPolicy(_ context.Context, account, site string, req api.SetHostingRetentionRequest) (api.HostingRetentionPolicy, error) {
	if !validHostingRetentionRequest(req) {
		return api.HostingRetentionPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned, ok := m.hostingSites[site]
	if !ok || owned.AccountID != account {
		return api.HostingRetentionPolicy{}, ErrNotFound
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingRetentionPolicy{}, ErrConflict
	}
	old := m.hostingRetentionPolicies[site]
	if old.Revision != req.ExpectedRevision {
		return api.HostingRetentionPolicy{}, ErrConflict
	}
	now := time.Now().UTC()
	p := api.HostingRetentionPolicy{SiteID: site, Revision: old.Revision + 1, Enabled: req.Enabled, HostingRetentionRules: req.HostingRetentionRules, UpdatedAt: &now}
	if m.hostingRetentionPolicies == nil {
		m.hostingRetentionPolicies = make(map[string]api.HostingRetentionPolicy)
	}
	m.hostingRetentionPolicies[site] = cloneHostingRetentionPolicy(p)
	return p, nil
}
func (m *MemStore) ApplyHostingRetention(_ context.Context, account, site, origin string, now time.Time) ([]string, error) {
	if now.IsZero() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned, ok := m.hostingSites[site]
	if !ok || owned.AccountID != account {
		return nil, ErrNotFound
	}
	if owned.OriginID != origin {
		return nil, ErrConflict
	}
	plan, err := m.hostingCleanupPlanLocked(account, site, nil, now)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	if !plan.Policy.Enabled {
		return ids, nil
	}
	for _, e := range plan.Entries {
		if !e.Eligible {
			continue
		}
		r := m.hostingReleases[e.ReleaseID]
		at := time.Now().UTC()
		r.State = "retiring"
		r.RetiredAt = &at
		r.RetirementGeneration = m.hostingRouteChanged(site)
		m.hostingReleases[r.ID] = r
		ids = append(ids, r.ID)
	}
	return ids, nil
}
func (m *MemStore) ListHostingRetentionSites(_ context.Context, after string, limit int) ([]api.HostingSite, error) {
	if limit <= 0 || limit > api.HostingRetentionSitesPerPass {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.HostingSite{}
	for id, p := range m.hostingRetentionPolicies {
		s := m.hostingSites[id]
		if p.Enabled && s.Status == "active" && s.DurableRouting && id > after {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *MemStore) TouchHostingRelease(_ context.Context, account, site, id string) (api.HostingRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	r, found := m.hostingReleases[id]
	if !ok || !found || s.AccountID != account || r.SiteID != site {
		return api.HostingRelease{}, ErrNotFound
	}
	if s.Status != "active" || r.State != "pending" && r.State != "ready" {
		return api.HostingRelease{}, ErrConflict
	}
	now := time.Now().UTC()
	if now.After(r.LastActivityAt) {
		r.LastActivityAt = now
	}
	m.hostingReleases[id] = r
	return cloneHostingRelease(r), nil
}
