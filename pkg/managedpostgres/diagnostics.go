package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func (s *Service) Diagnose(ctx context.Context, accountID, id string) (api.ManagedPostgresDiagnostics, error) {
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresDiagnostics{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresDiagnostics{}, err
	}
	native, ok := backend.Provider.(interface {
		Diagnose(context.Context, string) (dbguest.Diagnostics, error)
	})
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresDiagnostics{}, ErrUnsupported
	}
	out := api.ManagedPostgresDiagnostics{DatabaseID: database.ID}
	if database.State != StateReady || database.ProviderResourceID == "" {
		out.State = dbguest.DiagnosticsUnavailable
		return out, nil
	}
	out.Diagnostics, err = native.Diagnose(ctx, database.ProviderResourceID)
	return out, err
}

func (s *Service) TableHealth(ctx context.Context, accountID, id string, options dbguest.TableHealthOptions) (api.ManagedPostgresTableHealth, error) {
	database, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresTableHealth{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresTableHealth{}, err
	}
	native, ok := backend.Provider.(interface {
		TableHealth(context.Context, string, dbguest.TableHealthOptions) (dbguest.TableHealth, error)
	})
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresTableHealth{}, ErrUnsupported
	}
	out := api.ManagedPostgresTableHealth{DatabaseID: database.ID}
	if database.State != StateReady || database.ProviderResourceID == "" {
		out.State = dbguest.DiagnosticsUnavailable
		return out, nil
	}
	out.TableHealth, err = native.TableHealth(ctx, database.ProviderResourceID, options)
	return out, err
}
