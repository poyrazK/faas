package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) RestoreDrill(ctx context.Context, accountID, id, key string, start bool) (api.ManagedPostgresRestoreDrill, error) {
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresRestoreDrill{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresRestoreDrill{}, err
	}
	native, ok := backend.Provider.(interface {
		RestoreDrill(context.Context, string, string, bool) (api.ManagedPostgresRestoreDrill, error)
	})
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresRestoreDrill{}, ErrUnsupported
	}
	if database.ProviderResourceID == "" || start && database.State != StateReady {
		return api.ManagedPostgresRestoreDrill{}, ErrConflict
	}
	out, err := native.RestoreDrill(ctx, database.ProviderResourceID, key, start)
	out.DatabaseID = database.ID
	return out, err
}
