package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func (s *Service) Autovacuum(ctx context.Context, accountID, id string, profile dbguest.AutovacuumProfile) (api.ManagedPostgresAutovacuum, error) {
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresAutovacuum{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresAutovacuum{}, err
	}
	native, ok := backend.Provider.(interface {
		Autovacuum(context.Context, string, dbguest.AutovacuumProfile) (dbguest.AutovacuumStatus, error)
	})
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresAutovacuum{}, ErrUnsupported
	}
	out := api.ManagedPostgresAutovacuum{DatabaseID: database.ID}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return out, ErrConflict
	}
	out.AutovacuumStatus, err = native.Autovacuum(ctx, database.ProviderResourceID, profile)
	return out, err
}
