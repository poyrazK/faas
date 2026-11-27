package managedpostgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbstorage"
)

// StorageStatuses reads the scheduler's samples for customer databases
// already resolved in the caller's ownership scope. It never calls a VM.
func (s *Service) StorageStatuses(ctx context.Context, databases []Database) (map[string]*api.ManagedPostgresStorageStatus, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok {
		return nil, nil
	}
	statuses := make(map[string]*api.ManagedPostgresStorageStatus)
	byResource := make(map[string]*api.ManagedPostgresStorageStatus)
	var resources []pgtype.UUID
	for _, database := range databases {
		if database.State != StateReady && database.State != StateUpdating {
			continue
		}
		backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
		if err != nil || backend.Driver != "firecracker" {
			continue
		}
		id, err := postgresUUID(database.ProviderResourceID)
		if err != nil {
			continue
		}
		status := &api.ManagedPostgresStorageStatus{
			AllocatedBytes:  database.Spec.StorageLimitBytes,
			StandbyExpected: database.Spec.Availability == AvailabilityHighlyAvailable,
		}
		statuses[database.ID] = status
		byResource[postgresUUIDString(id)] = status
		resources = append(resources, id)
	}
	if len(resources) == 0 {
		return statuses, nil
	}
	samples, err := dbstorage.Read(ctx, store.pool, resources, s.now().UTC())
	if err != nil {
		return nil, err
	}
	for _, sample := range samples {
		status := byResource[sample.DatabaseID]
		volume := &api.ManagedPostgresVolumeStatus{
			TotalBytes: sample.TotalBytes, FreeBytes: sample.FreeBytes, Low: sample.Low, ReadOnly: sample.ReadOnly,
			SampledAt: sample.SampledAt.UTC(), Fresh: sample.Fresh,
		}
		switch sample.Role {
		case "primary":
			status.Primary = volume
		case "standby":
			status.Standby = volume
		}
	}
	alerts, err := dbstorage.ReadAlerts(ctx, store.pool, resources)
	if err != nil {
		return statuses, err
	}
	for _, alert := range alerts {
		status := byResource[alert.DatabaseID]
		status.Alerts = append(status.Alerts, api.ManagedPostgresStorageAlert{
			IncidentID: alert.IncidentID, State: alert.State, SampledAt: alert.SampledAt.UTC(),
			TotalBytes: alert.TotalBytes, FreeBytes: alert.FreeBytes, GrowthReason: alert.GrowthReason,
		})
	}
	return statuses, nil
}
