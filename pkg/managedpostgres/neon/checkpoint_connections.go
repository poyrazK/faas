package neon

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

var _ managedpostgres.CheckpointConnectionRecoveryProvider = (*Provider)(nil)

func (p *Provider) AbandonCheckpointConnections(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, maintenance managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity) (managedpostgres.CheckpointConnectionTerminal, error) {
	return p.recoverCheckpointConnections(ctx, definition, maintenance, identity, true)
}

func (p *Provider) ObserveCheckpointConnections(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, maintenance managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity) (managedpostgres.CheckpointConnectionTerminal, error) {
	return p.recoverCheckpointConnections(ctx, definition, maintenance, identity, false)
}

func (p *Provider) recoverCheckpointConnections(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, maintenance managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity, abandon bool) (managedpostgres.CheckpointConnectionTerminal, error) {
	actual, err := p.withCheckpointConnectionController(ctx, definition, maintenance, identity, pgxpool.NewWithConfig,
		func(controller *connectionfence.Controller) (connectionfence.Observation, error) {
			remoteIdentity := connectionfence.Identity{OwnerToken: identity.OwnerToken, SourceResourceID: identity.SourceResourceID}
			if abandon {
				if err := controller.Install(ctx); err != nil {
					return connectionfence.Observation{}, err
				}
				return controller.Abandon(ctx, remoteIdentity)
			}
			return controller.Observe(ctx, remoteIdentity)
		})
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, err
	}
	return managedpostgres.CheckpointConnectionTerminal{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{
		OwnerToken: actual.OwnerToken, SourceResourceID: actual.SourceResourceID}, State: actual.State, ReleasedAt: actual.ReleasedAt}, nil
}

// The pool connector is private to qualification tests. Production always opens
// one direct verify-full connection to the authenticated private database. Both
// native maintenance ownership and independent placement are checked before
// dispatch and after observation; failed postchecks return no evidence.
func (p *Provider) withCheckpointConnectionController(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, maintenance managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity,
	connect func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error), run func(*connectionfence.Controller) (connectionfence.Observation, error)) (connectionfence.Observation, error) {
	if p == nil {
		return connectionfence.Observation{}, managedpostgres.ErrUnavailable
	}
	if connect == nil || run == nil || !managedpostgres.ValidCheckpointConnectionRecovery(maintenance, identity) {
		return connectionfence.Observation{}, managedpostgres.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return connectionfence.Observation{}, err
	}
	if err := p.validateCheckpointSourceDefinition(definition, identity.SourceResourceID); err != nil {
		return connectionfence.Observation{}, err
	}
	spec := definition.Spec
	config, err := p.maintenanceConnectionConfigForDatabase(ctx, connectionfence.Identity{OwnerToken: maintenance.OwnerToken,
		SourceResourceID: maintenance.SourceResourceID}, connectionfence.MaintenanceDatabase, spec.PostgresMajor)
	if err != nil {
		return connectionfence.Observation{}, err
	}
	placement := func() error {
		ref, err := parseResourceRef(identity.SourceResourceID)
		if err != nil {
			return managedpostgres.ErrInvalid
		}
		actual, err := p.maintenanceEndpoint(ctx, ref)
		if err != nil {
			return err
		}
		if actual.ID != config.placement.ID || actual.Host != config.placement.Host || actual.PostgresMajor != config.PostgresMajor {
			return managedpostgres.ErrConflict
		}
		return nil
	}
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		return connectionfence.Observation{}, managedpostgres.ErrUnavailable
	}
	poolConfig.ConnConfig, poolConfig.MaxConns, poolConfig.MinConns = config.Copy(), 1, 0
	pool, err := connect(ctx, poolConfig)
	if pool != nil {
		defer pool.Close()
	}
	if err != nil {
		return connectionfence.Observation{}, maintenanceConnectionError(ctx, err)
	}
	if pool == nil {
		return connectionfence.Observation{}, managedpostgres.ErrConflict
	}
	authenticate := func() error {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return maintenanceConnectionError(ctx, err)
		}
		defer conn.Release()
		_, err = connectionfence.AuthenticateReadyMaintenance(ctx, conn.Conn(), connectionfence.BootstrapConfig{SourceDatabase: p.databaseName,
			SourceRole: maintenanceSourceRole, SourcePostgresMajor: spec.PostgresMajor}, connectionfence.Maintenance{
			Identity:  connectionfence.Identity{OwnerToken: maintenance.OwnerToken, SourceResourceID: maintenance.SourceResourceID},
			OwnerRole: "grg_ckpt_" + strings.ReplaceAll(maintenance.OwnerToken, "-", ""), OwnerOID: maintenance.OwnerOID,
			DatabaseOID: maintenance.DatabaseOID, State: "ready"})
		return err
	}
	if err := authenticate(); err != nil {
		return connectionfence.Observation{}, err
	}
	if err := placement(); err != nil {
		return connectionfence.Observation{}, err
	}
	controller, err := connectionfence.New(ctx, pool, connectionfence.Config{MaintenanceDatabase: connectionfence.MaintenanceDatabase,
		MaintenanceRole: maintenanceSourceRole, MaintenanceDatabaseOID: maintenance.DatabaseOID, MaintenanceOwnerOID: maintenance.OwnerOID})
	if err != nil {
		return connectionfence.Observation{}, err
	}
	actual, err := run(controller)
	if err != nil {
		return connectionfence.Observation{}, err
	}
	if err := authenticate(); err != nil {
		return connectionfence.Observation{}, err
	}
	if err := placement(); err != nil {
		return connectionfence.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return connectionfence.Observation{}, err
	}
	return actual, nil
}
