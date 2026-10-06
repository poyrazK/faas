package neon

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

var _ managedpostgres.CheckpointConnectionClosureProvider = (*Provider)(nil)

func (p *Provider) CloseCheckpointConnections(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, r managedpostgres.CheckpointConnectionRequest) (managedpostgres.CheckpointConnectionClosure, error) {
	return p.checkpointConnectionClosure(ctx, d, m, r, true, pgxpool.NewWithConfig)
}

func (p *Provider) ObserveCheckpointConnectionClosure(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, r managedpostgres.CheckpointConnectionRequest) (managedpostgres.CheckpointConnectionClosure, error) {
	return p.checkpointConnectionClosure(ctx, d, m, r, false, pgxpool.NewWithConfig)
}

func (p *Provider) checkpointConnectionClosure(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, r managedpostgres.CheckpointConnectionRequest, closeAdmission bool,
	connect func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error)) (managedpostgres.CheckpointConnectionClosure, error) {
	if r.Validate() != nil || slices.Contains(r.DatabaseNames, connectionfence.MaintenanceDatabase) {
		return managedpostgres.CheckpointConnectionClosure{}, managedpostgres.ErrInvalid
	}
	r.DatabaseNames = slices.Clone(r.DatabaseNames)
	actual, err := p.withCheckpointConnectionController(ctx, d, m, r.CheckpointConnectionIdentity, connect,
		func(controller *connectionfence.Controller) (connectionfence.Observation, error) {
			identity := connectionfence.Identity{OwnerToken: r.OwnerToken, SourceResourceID: r.SourceResourceID}
			if closeAdmission {
				if err := controller.Install(ctx); err != nil {
					return connectionfence.Observation{}, err
				}
				return controller.Close(ctx, connectionfence.Request{Identity: identity, DatabaseNames: r.DatabaseNames})
			}
			return controller.Observe(ctx, identity)
		})
	if err != nil {
		return managedpostgres.CheckpointConnectionClosure{}, err
	}
	out := managedpostgres.CheckpointConnectionClosure{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{
		OwnerToken: actual.OwnerToken, SourceResourceID: actual.SourceResourceID}, State: actual.State, ClosedAt: actual.ClosedAt,
		Drained: actual.Drained, UnselectedDatabases: actual.UnselectedDatabases}
	for _, db := range actual.Databases {
		out.Databases = append(out.Databases, managedpostgres.CheckpointConnectionDatabase{OID: db.OID, OwnerOID: db.OwnerOID, Name: db.Name,
			OriginalAllowConnections: db.OriginalAllowConnections, Sessions: db.Sessions, PreparedTransactions: db.PreparedTransactions})
	}
	if err := out.Validate(r); err != nil {
		return managedpostgres.CheckpointConnectionClosure{}, err
	}
	if err := ctx.Err(); err != nil {
		return managedpostgres.CheckpointConnectionClosure{}, err
	}
	return out, nil
}
