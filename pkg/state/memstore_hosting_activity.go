package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
)

var _ HostingActivityReader = (*MemStore)(nil)

func (m *MemStore) ListHostingActivityRecords(_ context.Context, account, site, before string) (HostingActivityRecords, error) {
	at, id, err := activity.ParseCursor(before)
	if err != nil {
		return HostingActivityRecords{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if owned, ok := m.hostingSites[site]; !ok || owned.AccountID != account {
		return HostingActivityRecords{}, ErrNotFound
	}
	rows := []HostingActivityRecord{}
	for _, run := range m.hostingCIRuns {
		if run.SiteID != site || before != "" && (run.CreatedAt.After(at) || run.CreatedAt.Equal(at) && run.ID >= id) {
			continue
		}
		run.Fingerprint = ""
		row := HostingActivityRecord{Run: run}
		if release, ok := m.hostingReleases[run.ReleaseID]; ok && release.SiteID == site {
			row.Release = &api.HostingRelease{ID: release.ID, SiteID: release.SiteID, State: release.State, Provenance: cloneHostingProvenance(release.Provenance)}
			if release.FirstPromotedAt != nil {
				at := *release.FirstPromotedAt
				row.Release.FirstPromotedAt = &at
			}
		}
		if preview, ok := m.hostingPreviews[run.PreviewID]; ok && preview.SiteID == site && preview.AccountID == account {
			p := cloneHostingPreview(preview)
			p.Hostname = ""
			row.Preview = &p
		}
		for _, p := range m.hostingPreviews {
			if p.SiteID == site && p.ReleaseID == run.ReleaseID && p.ID != run.PreviewID {
				row.OtherPreviews = true
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Run, rows[j].Run
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID > b.ID
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
	return hostingActivityPage(rows), nil
}
