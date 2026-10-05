package managedpostgres

import "context"

func discoverResource(ctx context.Context, provider Provider, database Database) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	discoverer, ok := provider.(ResourceDiscoverer)
	if !ok {
		return "", ErrUnsupported
	}
	identity, err := discoverer.Discover(ctx, ResourceDiscoveryRequest{
		ResourceID: database.ID, RestoreSourceResourceID: database.RestoreSourceResourceID,
	})
	if err != nil {
		return "", normalizeProviderError(err)
	}
	if identity == "" {
		return "", ErrUnavailable
	}
	return identity, nil
}
