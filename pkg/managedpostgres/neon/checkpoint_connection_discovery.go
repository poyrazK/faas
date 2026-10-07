package neon

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

var _ managedpostgres.CheckpointConnectionDiscoveryProvider = (*Provider)(nil)

func (p *Provider) DiscoverCheckpointConnections(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity) (managedpostgres.CheckpointConnectionRequest, error) {
	return p.discoverCheckpointConnections(ctx, d, m, identity, pgxpool.NewWithConfig)
}

func (p *Provider) discoverCheckpointConnections(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity,
	connect func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error)) (managedpostgres.CheckpointConnectionRequest, error) {
	var selected connectionfence.Request
	_, err := p.withCheckpointConnectionController(ctx, d, m, identity, connect, func(c *connectionfence.Controller) (connectionfence.Observation, error) {
		var err error
		selected, err = c.DiscoverDatabaseSelection(ctx, connectionfence.Identity{OwnerToken: identity.OwnerToken, SourceResourceID: identity.SourceResourceID})
		return connectionfence.Observation{}, err
	})
	if err != nil {
		return managedpostgres.CheckpointConnectionRequest{}, err
	}
	return managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: identity, DatabaseNames: selected.DatabaseNames}, nil
}
