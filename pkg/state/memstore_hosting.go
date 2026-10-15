package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ HostingStore = (*MemStore)(nil)

func (m *MemStore) CreateHostingSite(_ context.Context, site api.HostingSite) (api.HostingSite, error) {
	if !validHostingSite(site) {
		return api.HostingSite{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := false
	for _, environment := range m.projectEnvironments {
		if environment.AccountID == site.AccountID && environment.ProjectID == site.ProjectID && environment.Slug == site.Environment {
			owned = true
			break
		}
	}
	if !owned {
		return api.HostingSite{}, ErrNotFound
	}
	count := 0
	for _, old := range m.hostingSites {
		if old.Hostname == site.Hostname || old.AccountID == site.AccountID && old.ProjectID == site.ProjectID && old.Environment == site.Environment && old.Name == site.Name {
			return api.HostingSite{}, ErrConflict
		}
		if old.AccountID == site.AccountID {
			count++
		}
	}
	if count >= api.HostingSitesPerAccount {
		return api.HostingSite{}, hostingQuota(api.HostingSitesPerAccount)
	}
	if m.hostingSites == nil {
		m.hostingSites = make(map[string]api.HostingSite)
	}
	site.Revision, site.CreatedAt, site.ActiveReleaseID = 1, time.Now().UTC(), ""
	site.Status = "active"
	m.hostingSites[site.ID] = site
	return site, nil
}

func (m *MemStore) GetHostingSite(_ context.Context, account, id string) (api.HostingSite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[id]
	if !ok || s.AccountID != account {
		return api.HostingSite{}, ErrNotFound
	}
	return s, nil
}

func (m *MemStore) ListHostingSites(_ context.Context, account string) ([]api.HostingSite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.HostingSite{}
	for _, site := range m.hostingSites {
		if site.AccountID == account {
			rows = append(rows, site)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (m *MemStore) ListHostingRoutes(_ context.Context, after string, limit int) ([]api.HostingSite, error) {
	if limit < 1 || limit > api.HostingRoutePageSize {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.HostingSite{}
	for _, site := range m.hostingSites {
		if site.ID > after {
			rows = append(rows, site)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemStore) CreateHostingRelease(_ context.Context, account, site string, manifest api.HostingManifest) (api.HostingRelease, error) {
	if validateHostingManifest(manifest) != nil {
		return api.HostingRelease{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	if !ok || s.AccountID != account {
		return api.HostingRelease{}, ErrNotFound
	}
	if s.Status != "active" {
		return api.HostingRelease{}, ErrConflict
	}
	count := 0
	for _, old := range m.hostingReleases {
		if old.SiteID == site && old.State != "deleted" {
			count++
		}
	}
	if count >= api.HostingReleasesPerSite {
		return api.HostingRelease{}, hostingQuota(api.HostingReleasesPerSite)
	}
	if m.hostingReleases == nil {
		m.hostingReleases = make(map[string]api.HostingRelease)
	}
	r := api.HostingRelease{ID: uuid.NewString(), SiteID: site, Manifest: manifest, State: "pending", CreatedAt: time.Now().UTC()}
	m.hostingReleases[r.ID] = cloneHostingRelease(r)
	return cloneHostingRelease(r), nil
}

func (m *MemStore) GetHostingRelease(_ context.Context, account, site, id string) (api.HostingRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hostingReleases[id]
	s, owns := m.hostingSites[site]
	if !ok || !owns || s.AccountID != account || r.SiteID != site {
		return api.HostingRelease{}, ErrNotFound
	}
	return cloneHostingRelease(r), nil
}

func (m *MemStore) ListHostingReleases(_ context.Context, account, site string) ([]api.HostingRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	if !ok || s.AccountID != account {
		return nil, ErrNotFound
	}
	rows := []api.HostingRelease{}
	for _, r := range m.hostingReleases {
		if r.SiteID == site && r.State != "deleted" {
			rows = append(rows, cloneHostingRelease(r))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (m *MemStore) ReadyHostingRelease(_ context.Context, account, site, id string) (api.HostingRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.hostingReleases[id]
	s := m.hostingSites[site]
	if !ok || s.AccountID != account || r.SiteID != site {
		return api.HostingRelease{}, ErrNotFound
	}
	if s.Status != "active" || r.State != "pending" && r.State != "ready" {
		return api.HostingRelease{}, ErrConflict
	}
	r.State = "ready"
	m.hostingReleases[id] = r
	return cloneHostingRelease(r), nil
}

func (m *MemStore) PromoteHostingRelease(_ context.Context, account, site, id string, expected int64) (api.HostingSite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	if !ok || s.AccountID != account {
		return api.HostingSite{}, ErrNotFound
	}
	r := m.hostingReleases[id]
	if s.Status != "active" || expected < 1 || s.Revision != expected || r.SiteID != site || r.State != "ready" {
		return api.HostingSite{}, ErrConflict
	}
	s.Revision++
	s.ActiveReleaseID = id
	m.hostingSites[site] = s
	return s, nil
}

func (m *MemStore) RetireHostingRelease(_ context.Context, account, site, id string) (api.HostingRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	r, found := m.hostingReleases[id]
	if !ok || !found || s.AccountID != account || r.SiteID != site {
		return api.HostingRelease{}, ErrNotFound
	}
	if s.ActiveReleaseID == id || r.State != "pending" && r.State != "ready" {
		return api.HostingRelease{}, ErrConflict
	}
	now := time.Now().UTC()
	r.State, r.RetiredAt = "retiring", &now
	m.hostingReleases[id] = r
	return cloneHostingRelease(r), nil
}

func (m *MemStore) DeleteHostingReleaseRecord(_ context.Context, account, site, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.hostingSites[site]
	r := m.hostingReleases[id]
	if s.AccountID != account || r.SiteID != site || r.State != "retiring" {
		return ErrConflict
	}
	delete(m.hostingReleases, id)
	return nil
}

func (m *MemStore) DeleteHostingSite(_ context.Context, account, site string) (api.HostingSite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	if !ok || s.AccountID != account {
		return api.HostingSite{}, ErrNotFound
	}
	if s.Status == "deleting" {
		return s, nil
	}
	s.Status, s.ActiveReleaseID = "deleting", ""
	s.Revision++
	m.hostingSites[site] = s
	now := time.Now().UTC()
	for id, r := range m.hostingReleases {
		if r.SiteID == site && (r.State == "pending" || r.State == "ready") {
			r.State, r.RetiredAt = "retiring", &now
			m.hostingReleases[id] = r
		}
	}
	return s, nil
}

func (m *MemStore) PurgeHostingSite(_ context.Context, account, site string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.hostingSites[site]
	if s.AccountID != account || s.Status != "deleting" {
		return ErrConflict
	}
	for _, r := range m.hostingReleases {
		if r.SiteID == site && r.State != "deleted" {
			return ErrConflict
		}
	}
	for id, r := range m.hostingReleases {
		if r.SiteID == site {
			delete(m.hostingReleases, id)
		}
	}
	delete(m.hostingSites, site)
	return nil
}
