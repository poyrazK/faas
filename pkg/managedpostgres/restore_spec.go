package managedpostgres

import (
	"context"
	"time"
)

// RestoreSpecResolver resolves a historical major before reserving a target.
// It must only observe the named dataset and must never create a resource.
// Every other field stays at the source definition's captured specification.
type RestoreSpecResolver interface {
	ResolveRestoreSpec(context.Context, RestoreSourceDefinition, time.Time) (Spec, error)
}

func (s *Service) resolveRestoreSpec(ctx context.Context, source Database, point time.Time, frozen bool) (Spec, error) {
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return Spec{}, err
	}
	resolver, ok := backend.Provider.(RestoreSpecResolver)
	if !ok {
		return source.Spec, nil
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	spec, err := resolver.ResolveRestoreSpec(providerCtx, RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID,
		BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: databaseDataResource(source)}, point)
	if err != nil {
		return Spec{}, err
	}
	rest := spec
	rest.PostgresMajor = source.Spec.PostgresMajor
	if spec.Validate() != nil || rest != source.Spec || spec.PostgresMajor > source.Spec.PostgresMajor || frozen && spec != source.Spec {
		return Spec{}, ErrConflict
	}
	return spec, nil
}
