package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

type extensionManager interface {
	Extensions(context.Context, string, string, int) (pgextension.Catalog, []pgextension.Installed, error)
}

func (s *Service) Extensions(ctx context.Context, accountID, databaseID, enable string, targetMajor int) (api.ManagedPostgresExtensions, error) {
	if enable != "" && !pgextension.Managed(enable) ||
		targetMajor != 0 && (enable != "" || targetMajor < dbguest.MinMajor || targetMajor > dbguest.MaxMajor) {
		return api.ManagedPostgresExtensions{}, ErrInvalid
	}
	database, err := customerDatabase(ctx, s.store, accountID, databaseID)
	if err != nil {
		return api.ManagedPostgresExtensions{}, err
	}
	if enable != "" && !database.Preview.IsZero() {
		return api.ManagedPostgresExtensions{}, ErrPreviewDatabase
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return api.ManagedPostgresExtensions{}, ErrConflict
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresExtensions{}, err
	}
	manager, ok := backend.Provider.(extensionManager)
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresExtensions{}, ErrUnsupported
	}
	if targetMajor != 0 {
		spec := database.Spec
		spec.PostgresMajor = targetMajor
		if targetMajor <= database.Spec.PostgresMajor {
			return api.ManagedPostgresExtensions{}, ErrInvalid
		}
		if err := backend.Capabilities.SupportsNewDatabase(spec); err != nil {
			return api.ManagedPostgresExtensions{}, err
		}
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	catalog, blockers, err := manager.Extensions(providerCtx, database.ProviderResourceID, enable, targetMajor)
	if err != nil {
		return api.ManagedPostgresExtensions{}, err
	}
	if !catalog.Valid() {
		return api.ManagedPostgresExtensions{}, ErrUnavailable
	}
	out := api.ManagedPostgresExtensions{
		Installed:              extensionVersions(catalog.Installed),
		Available:              make([]api.ManagedPostgresExtensionVersion, 0, len(catalog.Available)),
		IncompatibleExtensions: extensionVersions(blockers),
	}
	for _, a := range catalog.Available {
		out.Available = append(out.Available, api.ManagedPostgresExtensionVersion{Name: a.Name, Version: a.Version, Default: a.Default})
	}
	return out, nil
}

func extensionVersions(in []pgextension.Installed) []api.ManagedPostgresExtensionVersion {
	out := make([]api.ManagedPostgresExtensionVersion, 0, len(in))
	for _, i := range in {
		out = append(out, api.ManagedPostgresExtensionVersion{Name: i.Name, Version: i.Version})
	}
	return out
}
