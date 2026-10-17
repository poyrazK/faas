package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
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
	site.Suspended = false
	m.hostingSites[site.ID] = site
	m.hostingRouteChanged(site.ID)
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

func (m *MemStore) CreateHostingRelease(ctx context.Context, account, site string, manifest api.HostingManifest) (api.HostingRelease, error) {
	return m.CreateHostingReleaseWithProvenance(ctx, account, site, manifest, nil)
}
func (m *MemStore) CreateHostingReleaseWithProvenance(_ context.Context, account, site string, manifest api.HostingManifest, provenance *api.HostingReleaseProvenance) (api.HostingRelease, error) {
	if hosting.ValidateProvenance(provenance) != nil {
		return api.HostingRelease{}, ErrInvalidArgument
	}
	if validateHostingManifest(manifest) != nil {
		return api.HostingRelease{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createHostingReleaseLocked(account, site, manifest, provenance)
}
func (m *MemStore) createHostingReleaseLocked(account, site string, manifest api.HostingManifest, provenance *api.HostingReleaseProvenance) (api.HostingRelease, error) {
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
	payload := hostingPayloadBytes(manifest)
	if err := checkHostingAdmission(m.hostingUsageLocked(account), payload); err != nil {
		return api.HostingRelease{}, err
	}
	if m.hostingReleases == nil {
		m.hostingReleases = make(map[string]api.HostingRelease)
	}
	r := api.HostingRelease{Provenance: cloneHostingProvenance(provenance), ArtifactBytes: payload, ID: uuid.NewString(), SiteID: site, Manifest: manifest, State: "pending", CreatedAt: time.Now().UTC()}
	r.LastActivityAt = r.CreatedAt
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
	if r.State != "ready" {
		m.hostingRouteChanged(site)
	}
	r.State = "ready"
	m.hostingReleases[id] = r
	return cloneHostingRelease(r), nil
}

func (m *MemStore) PromoteHostingRelease(_ context.Context, account, site, id string, expected int64) (api.HostingSite, error) {
	return m.promoteHostingRelease(account, site, id, expected, nil)
}
func (m *MemStore) PromoteHostingReleaseRecorded(_ context.Context, account, site, id string, expected int64, actor api.HostingPromotionActor) (api.HostingSite, error) {
	if !validHostingActor(actor) {
		return api.HostingSite{}, ErrInvalidArgument
	}
	return m.promoteHostingRelease(account, site, id, expected, &actor)
}
func (m *MemStore) promoteHostingRelease(account, site, id string, expected int64, actor *api.HostingPromotionActor) (api.HostingSite, error) {
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
	if r.FirstPromotedAt == nil {
		at := time.Now().UTC()
		r.FirstPromotedAt = &at
	}
	m.hostingReleases[id] = r
	if actor != nil {
		event := api.HostingPromotionEvent{SiteID: site, Revision: s.Revision + 1, PreviousRevision: s.Revision, ReleaseID: id, PreviousReleaseID: s.ActiveReleaseID, Actor: *actor, AcceptedAt: time.Now().UTC(), Provenance: cloneHostingProvenance(r.Provenance)}
		if m.hostingHistory == nil {
			m.hostingHistory = make(map[string][]api.HostingPromotionEvent)
		}
		events := append([]api.HostingPromotionEvent{event}, m.hostingHistory[site]...)
		if len(events) > api.HostingHistoryPerSite {
			events = events[:api.HostingHistoryPerSite]
		}
		m.hostingHistory[site] = events
	}
	s.Revision++
	s.ActiveReleaseID = id
	m.hostingSites[site] = s
	m.hostingRouteChanged(site)
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
	for _, p := range m.hostingPreviews {
		if p.ReleaseID == id {
			return api.HostingRelease{}, ErrConflict
		}
	}
	if s.ActiveReleaseID == id || r.State != "pending" && r.State != "ready" {
		return api.HostingRelease{}, ErrConflict
	}
	now := time.Now().UTC()
	r.State, r.RetiredAt = "retiring", &now
	r.RetirementGeneration = m.hostingRouteChanged(site)
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
	if s.DurableRouting && (r.RetirementPublishedAt == nil || time.Since(*r.RetirementPublishedAt) < api.HostingRetirementGrace) {
		return ErrConflict
	}
	for _, p := range m.hostingPreviews {
		if p.ReleaseID == id {
			return ErrConflict
		}
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
	generation := m.hostingRouteChanged(site)
	now := time.Now().UTC()
	for id, r := range m.hostingReleases {
		if r.SiteID == site && (r.State == "pending" || r.State == "ready") {
			r.State, r.RetiredAt = "retiring", &now
			r.RetirementGeneration = generation
			m.hostingReleases[id] = r
		}
	}
	for id, p := range m.hostingPreviews {
		if p.SiteID == site && !p.Revoked {
			p.Revoked = true
			p.Generation = 2
			p.NextAttemptAt = now
			p.LastErrorCode = ""
			m.hostingPreviews[id] = p
		}
	}
	for id, row := range m.hostingDomains {
		if row.Domain.SiteID == site && row.Domain.State != "deleting" {
			row.Domain.State = "deleting"
			row.Domain.Revision++
			row.Domain.Generation++
			row.Domain.NextAttemptAt = now
			row.Domain.LastErrorCode = ""
			m.hostingDomains[id] = row
		}
	}
	return s, nil
}

func (m *MemStore) PurgeHostingSite(_ context.Context, account, site string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.hostingSites[site]
	for _, p := range m.hostingPreviews {
		if p.SiteID == site {
			return ErrConflict
		}
	}
	for _, domain := range m.hostingDomains {
		if domain.Domain.SiteID == site {
			return ErrConflict
		}
	}
	if s.AccountID != account || s.Status != "deleting" {
		return ErrConflict
	}
	publication := m.hostingPublications[site]
	if s.DurableRouting && (publication.PublishedGeneration != publication.Generation || publication.PublishedAt.IsZero() || time.Since(publication.PublishedAt) < api.HostingRetirementGrace) {
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
	for id, trust := range m.hostingOIDCTrusts {
		if trust.SiteID == site {
			delete(m.hostingOIDCTrusts, id)
		}
	}
	for id, c := range m.hostingCredentials {
		if c.SiteID == site {
			delete(m.hostingCredentials, id)
		}
	}
	for key, run := range m.hostingCIRuns {
		if run.SiteID == site {
			delete(m.hostingCIRuns, key)
		}
	}
	delete(m.hostingRetentionPolicies, site)
	delete(m.hostingHistory, site)
	delete(m.hostingSites, site)
	delete(m.hostingPublications, site)
	return nil
}

// Caller holds m.mu. Even failed delivery leaves the latest generation dirty.
func (m *MemStore) hostingRouteChanged(site string) int64 {
	if !m.hostingSites[site].DurableRouting {
		return 0
	}
	if m.hostingPublications == nil {
		m.hostingPublications = make(map[string]hostingPublication)
	}
	p := m.hostingPublications[site]
	p.Generation++
	p.NextAttemptAt = time.Now().UTC()
	if p.PendingSince.IsZero() {
		p.PendingSince = p.NextAttemptAt
	}
	p.LastAttemptAt, p.RetryCount, p.LastErrorCode = time.Time{}, 0, ""
	m.hostingPublications[site] = p
	return p.Generation
}

func (m *MemStore) SetHostingSiteSuspended(_ context.Context, account, site string, expected int64, suspended bool) (api.HostingSite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	if !ok || s.AccountID != account {
		return api.HostingSite{}, ErrNotFound
	}
	if !s.DurableRouting || s.Status != "active" || s.Revision != expected || expected < 1 {
		return api.HostingSite{}, ErrConflict
	}
	s.Suspended = suspended
	s.Revision++
	m.hostingSites[site] = s
	m.hostingRouteChanged(site)
	return s, nil
}

func (m *MemStore) ListDueHostingRoutePublications(_ context.Context, now time.Time, limit int) ([]string, error) {
	if limit < 1 || limit > api.HostingRoutePageSize {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []string{}
	for id, p := range m.hostingPublications {
		if !p.NextAttemptAt.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.hostingPublications[ids[i]], m.hostingPublications[ids[j]]
		if a.NextAttemptAt.Equal(b.NextAttemptAt) {
			return ids[i] < ids[j]
		}
		return a.NextAttemptAt.Before(b.NextAttemptAt)
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (m *MemStore) HostingRouteSnapshot(_ context.Context, site string) (HostingRouteSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.hostingSites[site]
	p, exists := m.hostingPublications[site]
	if !ok || !exists || !s.DurableRouting {
		return HostingRouteSnapshot{}, ErrNotFound
	}
	out := hostingPublicationSnapshot(s, p)
	for _, r := range m.hostingReleases {
		if r.SiteID == site && r.State != "deleted" {
			out.Releases = append(out.Releases, cloneHostingRelease(api.HostingRelease{ID: r.ID, SiteID: r.SiteID, State: r.State, FirstPromotedAt: r.FirstPromotedAt}))
		}
	}
	return out, nil
}

func (m *MemStore) AcknowledgeHostingRoute(_ context.Context, site string, generation int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.hostingPublications[site]
	if !ok {
		return ErrNotFound
	}
	if generation < 1 || generation > p.Generation || at.IsZero() {
		return ErrConflict
	}
	if generation > p.PublishedGeneration {
		p.PublishedGeneration, p.PublishedAt = generation, at.UTC()
	}
	if generation == p.Generation {
		p.NextAttemptAt = at.Add(api.HostingRouteRepairInterval)
		p.PendingSince, p.LastAttemptAt, p.RetryCount, p.LastErrorCode = time.Time{}, at.UTC(), 0, ""
	}
	m.hostingPublications[site] = p
	for id, r := range m.hostingReleases {
		if r.SiteID == site && r.State == "retiring" && r.RetirementGeneration > 0 && r.RetirementGeneration <= generation && r.RetirementPublishedAt == nil {
			t := at.UTC()
			r.RetirementPublishedAt = &t
			m.hostingReleases[id] = r
		}
	}
	return nil
}

func (m *MemStore) RetryHostingRoute(_ context.Context, site string, generation int64, at time.Time, code string) error {
	if generation < 1 || at.IsZero() || !validHostingRouteFailure(code) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.hostingPublications[site]
	if !ok {
		return ErrNotFound
	}
	if generation == p.Generation {
		p.NextAttemptAt = at.Add(api.HostingRouteRetryInterval)
		p.LastAttemptAt, p.LastErrorCode = at.UTC(), code
		p.RetryCount++
		m.hostingPublications[site] = p
	}
	return nil
}

func (m *MemStore) HostingRouteBacklog(_ context.Context) (HostingRouteBacklog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out HostingRouteBacklog
	for _, p := range m.hostingPublications {
		if p.Generation <= p.PublishedGeneration {
			continue
		}
		out.Pending++
		if out.OldestPendingAt.IsZero() || p.PendingSince.Before(out.OldestPendingAt) {
			out.OldestPendingAt = p.PendingSince
		}
	}
	return out, nil
}
