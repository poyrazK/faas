package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ HostingPreviewStore = (*MemStore)(nil)

func (m *MemStore) CreateHostingPreview(_ context.Context, p api.HostingPreview) (api.HostingPreview, error) {
	if !validHostingPreview(p) {
		return api.HostingPreview{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createHostingPreviewLocked(p)
}
func (m *MemStore) createHostingPreviewLocked(p api.HostingPreview) (api.HostingPreview, error) {
	return m.createHostingPreviewWithReservationLocked(p, "")
}
func (m *MemStore) createHostingPreviewWithReservationLocked(p api.HostingPreview, excludeRun string) (api.HostingPreview, error) {
	s := m.hostingSites[p.SiteID]
	r := m.hostingReleases[p.ReleaseID]
	if s.AccountID != p.AccountID {
		return api.HostingPreview{}, ErrNotFound
	}
	if !s.DurableRouting || s.Status != "active" || s.OriginID != p.OriginID || r.SiteID != s.ID || r.State != "ready" || p.Hostname != "p-"+p.ID+strings.TrimPrefix(s.Hostname, s.ID) {
		return api.HostingPreview{}, ErrConflict
	}
	for _, v := range m.hostingPreviews {
		if v.ID == p.ID || v.Hostname == p.Hostname {
			return api.HostingPreview{}, ErrConflict
		}
	}
	if err := requireHostingPreviewCapacity(m.hostingPreviewCapacityLocked(s.ID, excludeRun, time.Now().UTC())); err != nil {
		return api.HostingPreview{}, err
	}
	p.Generation = 1
	p.Revoked = false
	p.PublishedGeneration = 0
	p.PublishedAt = nil
	p.LastErrorCode = ""
	p.NextAttemptAt = p.CreatedAt
	if m.hostingPreviews == nil {
		m.hostingPreviews = make(map[string]api.HostingPreview)
	}
	m.hostingPreviews[p.ID] = p
	return p, nil
}
func (m *MemStore) ListHostingPreviews(_ context.Context, account, site string) ([]api.HostingPreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return nil, ErrNotFound
	}
	out := []api.HostingPreview{}
	for _, p := range m.hostingPreviews {
		if p.SiteID == site {
			out = append(out, cloneHostingPreview(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *MemStore) RevokeHostingPreview(_ context.Context, account, site, id string, generation int64) (api.HostingPreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.hostingPreviews[id]
	if !ok || p.AccountID != account || p.SiteID != site || p.Generation != generation || p.Revoked {
		return api.HostingPreview{}, ErrConflict
	}
	p.Revoked = true
	p.Generation = 2
	p.NextAttemptAt = time.Now().UTC()
	p.LastErrorCode = ""
	m.hostingPreviews[id] = p
	return cloneHostingPreview(p), nil
}
func (m *MemStore) DueHostingPreviews(_ context.Context, now time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.HostingPreview{}
	for _, p := range m.hostingPreviews {
		if !p.NextAttemptAt.After(now) {
			rows = append(rows, p)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].NextAttemptAt.Equal(rows[j].NextAttemptAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].NextAttemptAt.Before(rows[j].NextAttemptAt)
	})
	out := []string{}
	for _, p := range rows {
		if len(out) == api.HostingPreviewPageSize {
			break
		}
		out = append(out, p.ID)
	}
	return out, nil
}
func (m *MemStore) PrepareHostingPreview(_ context.Context, id string, now time.Time) (api.HostingPreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.hostingPreviews[id]
	if !ok {
		return p, ErrNotFound
	}
	if !p.Revoked && (!p.ExpiresAt.After(now) || m.hostingSites[p.SiteID].Status != "active") {
		p.Revoked = true
		p.Generation = 2
		p.NextAttemptAt = now
		p.LastErrorCode = ""
		m.hostingPreviews[id] = p
	}
	return cloneHostingPreview(p), nil
}
func (m *MemStore) FinishHostingPreview(_ context.Context, p api.HostingPreview, now time.Time, code string) error {
	if now.IsZero() || !validHostingPreviewCode(code) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingPreviews[p.ID]
	if !ok || row.Generation != p.Generation {
		return ErrConflict
	}
	if code == "" {
		if row.PublishedGeneration < row.Generation {
			at := now
			row.PublishedAt = &at
		}
		row.PublishedGeneration = row.Generation
	}
	row.NextAttemptAt = hostingPreviewNext(row, now, code)
	row.LastErrorCode = code
	m.hostingPreviews[p.ID] = row
	return nil
}
func (m *MemStore) PurgeHostingPreview(_ context.Context, p api.HostingPreview, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.hostingPreviews[p.ID]
	if !ok || row.Generation != p.Generation || !row.Revoked || row.PublishedGeneration != row.Generation || row.PublishedAt == nil || now.Sub(*row.PublishedAt) < api.HostingPreviewRemovalGrace {
		return ErrConflict
	}
	delete(m.hostingPreviews, p.ID)
	return nil
}
