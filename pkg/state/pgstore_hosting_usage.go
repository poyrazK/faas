package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func hostingUsageParams(account string) sqlc.HostingAccountUsageParams {
	id, _ := parsePgUUID(account)
	return sqlc.HostingAccountUsageParams{AccountID: id, Copies: api.HostingStorageCopies, ManifestReservation: api.HostingManifestMaxBytes}
}

func hostingUsageRow(account string, row sqlc.HostingAccountUsageRow) api.HostingUsage {
	return api.HostingUsage{AccountID: account, ReservedBytes: row.ReservedBytes, PayloadBytes: row.PayloadBytes, ReadyPayloadBytes: row.ReadyPayloadBytes,
		RetiringReservedBytes: row.RetiringReservedBytes, PendingUploads: row.PendingUploads, Releases: row.Releases,
		StorageLimitBytes: api.HostingAccountStorageMaxBytes, PendingUploadsLimit: api.HostingAccountPendingUploadsMax, ObservedAt: time.Now().UTC()}
}

func (s *PgStore) GetHostingUsage(ctx context.Context, account string) (api.HostingUsage, error) {
	if _, err := parsePgUUID(account); err != nil {
		return api.HostingUsage{}, ErrInvalidArgument
	}
	row, err := sqlc.New().HostingAccountUsage(ctx, s.pool, hostingUsageParams(account))
	return hostingUsageRow(account, row), err
}
