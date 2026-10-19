package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func (m *MemStore) CancelHostingCIRun(_ context.Context, account, site, id string) (api.HostingCIRun, error) {
	if !hosting.ValidCIRunID(id) {
		return api.HostingCIRun{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned, ok := m.hostingSites[site]
	if !ok || owned.AccountID != account {
		return api.HostingCIRun{}, ErrNotFound
	}
	key := site + "/" + id
	run, ok := m.hostingCIRuns[key]
	if !ok {
		return api.HostingCIRun{}, ErrNotFound
	}
	if run.Canceled {
		return run, nil
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingCIRun{}, ErrConflict
	}
	var release *api.HostingRelease
	if row, exists := m.hostingReleases[run.ReleaseID]; exists && row.SiteID == site {
		release = &row
	}
	previews := []api.HostingPreview{}
	for _, p := range m.hostingPreviews {
		if p.SiteID == site {
			previews = append(previews, p)
		}
	}
	if hostingCancellationProtected(owned, run, release, previews) {
		return api.HostingCIRun{}, ErrHostingCancellationProtected
	}
	now := time.Now().UTC()
	if p, exists := m.hostingPreviews[run.PreviewID]; exists && p.SiteID == site && !p.Revoked {
		p.Revoked = true
		p.Generation = 2
		p.NextAttemptAt = now
		p.LastErrorCode = ""
		m.hostingPreviews[p.ID] = p
	}
	if release != nil && (release.State == "pending" || release.State == "ready") {
		release.State = "retiring"
		release.RetiredAt = &now
		release.RetirementGeneration = m.hostingRouteChanged(site)
		m.hostingReleases[release.ID] = *release
	}
	run.Canceled = true
	m.hostingCIRuns[key] = run
	return run, nil
}
