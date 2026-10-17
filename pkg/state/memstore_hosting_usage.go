package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Caller holds m.mu, shared with all release admission and lifecycle mutations.
func (m *MemStore) hostingUsageLocked(account string) api.HostingUsage {
	usage := api.HostingUsage{AccountID: account, StorageLimitBytes: api.HostingAccountStorageMaxBytes, PendingUploadsLimit: api.HostingAccountPendingUploadsMax, ObservedAt: time.Now().UTC()}
	for _, release := range m.hostingReleases {
		if m.hostingSites[release.SiteID].AccountID != account || release.State == "deleted" {
			continue
		}
		usage.Releases++
		usage.PayloadBytes += release.ArtifactBytes
		reserved := hostingReservedBytes(release.ArtifactBytes)
		usage.ReservedBytes += reserved
		switch release.State {
		case "pending":
			usage.PendingUploads++
		case "ready":
			usage.ReadyPayloadBytes += release.ArtifactBytes
		case "retiring":
			usage.RetiringReservedBytes += reserved
		}
	}
	return usage
}

func (m *MemStore) GetHostingUsage(_ context.Context, account string) (api.HostingUsage, error) {
	if uuid.Validate(account) != nil {
		return api.HostingUsage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostingUsageLocked(account), nil
}
