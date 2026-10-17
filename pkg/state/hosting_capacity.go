package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// All preview writers honor persisted reservations, independently of admission flags.
type HostingPreviewCapacityReader interface {
	GetHostingPreviewCapacity(context.Context, string, string) (api.HostingPreviewCapacity, error)
}

func hostingPreviewCapacity(site string, previews, reservations int64, now time.Time) api.HostingPreviewCapacity {
	available := max(int64(0), int64(api.HostingPreviewsPerSite)-previews-reservations)
	return api.HostingPreviewCapacity{SiteID: site, Limit: api.HostingPreviewsPerSite, Previews: previews, Reservations: reservations, Available: available, ObservedAt: now}
}
func requireHostingPreviewCapacity(c api.HostingPreviewCapacity) error {
	if c.Available == 0 {
		return &HostingQuotaError{Limit: api.HostingPreviewsPerSite, Observed: c.Previews + c.Reservations + 1, Resource: "previews"}
	}
	return nil
}
func (m *MemStore) hostingPreviewCapacityLocked(site, exclude string, now time.Time) api.HostingPreviewCapacity {
	var previews, reservations int64
	for _, p := range m.hostingPreviews {
		if p.SiteID == site {
			previews++
		}
	}
	for _, r := range m.hostingCIRuns {
		release := m.hostingReleases[r.ReleaseID]
		if r.SiteID == site && r.ID != exclude && r.PreviewReserved && !r.Canceled && !r.PreviewCreated && r.PreviewExpiresAt.After(now) && release.SiteID == site && (release.State == "pending" || release.State == "ready") {
			reservations++
		}
	}
	return hostingPreviewCapacity(site, previews, reservations, now)
}
func (m *MemStore) GetHostingPreviewCapacity(_ context.Context, account, site string) (api.HostingPreviewCapacity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hostingSites[site].AccountID != account {
		return api.HostingPreviewCapacity{}, ErrNotFound
	}
	return m.hostingPreviewCapacityLocked(site, "", time.Now().UTC()), nil
}
func hostingPreviewCapacityQuery(ctx context.Context, db sqlc.DBTX, account, site, exclude string, now time.Time) (api.HostingPreviewCapacity, error) {
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	hasExcludedRun := exclude != ""
	if exclude == "" {
		exclude = uuid.Nil.String()
	}
	id, _ := parsePgUUID(exclude)
	row, err := sqlc.New().HostingPreviewCapacity(ctx, db, sqlc.HostingPreviewCapacityParams{AccountID: a, SiteID: si, ExcludeRunID: id, HasExcludedRun: hasExcludedRun, ObservedAt: hostingTimestamp(now)})
	if err != nil {
		return api.HostingPreviewCapacity{}, hostingStoreError(err)
	}
	return hostingPreviewCapacity(uuidString(row.ID), row.Previews, row.Reservations, now), nil
}
func (s *PgStore) GetHostingPreviewCapacity(ctx context.Context, account, site string) (api.HostingPreviewCapacity, error) {
	return hostingPreviewCapacityQuery(ctx, s.pool, account, site, "", time.Now().UTC())
}
