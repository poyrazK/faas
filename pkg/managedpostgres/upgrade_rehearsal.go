package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) UpgradeRehearsal(ctx context.Context, accountID, id, key string, major int, start bool) (api.ManagedPostgresUpgradeRehearsal, error) {
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresUpgradeRehearsal{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresUpgradeRehearsal{}, err
	}
	native, ok := backend.Provider.(interface {
		UpgradeRehearsal(context.Context, string, string, int, bool) (api.ManagedPostgresUpgradeRehearsal, error)
	})
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresUpgradeRehearsal{}, ErrUnsupported
	}
	resource := databaseDataResource(database)
	if resource == "" || start && database.State != StateReady {
		return api.ManagedPostgresUpgradeRehearsal{}, ErrConflict
	}
	out, err := native.UpgradeRehearsal(ctx, resource, key, major, start)
	out.DatabaseID = database.ID
	return out, err
}
