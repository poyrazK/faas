package neon

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// LogicalMigrationSource identifies the provider-owned primary endpoint
// used to bind a migration receipt to the app's managed source database.
func (p *Provider) LogicalMigrationSource(ctx context.Context, resourceID string) (managedpostgres.Endpoint, string, error) {
	ref, err := parseResourceRef(resourceID)
	if err != nil {
		return managedpostgres.Endpoint{}, "", err
	}
	if ref.branchID == "" {
		ref.branchID, err = p.defaultBranch(ctx, ref.projectID)
		if err != nil {
			return managedpostgres.Endpoint{}, "", err
		}
	}
	source, err := p.maintenanceEndpoint(ctx, ref)
	if err != nil {
		return managedpostgres.Endpoint{}, "", err
	}
	return managedpostgres.Endpoint{Role: managedpostgres.EndpointDirect, Host: source.endpoint.Host, Port: 5432}, p.databaseName, nil
}
