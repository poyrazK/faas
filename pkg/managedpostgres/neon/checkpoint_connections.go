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
	if p == nil {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrUnavailable
	}
	if !managedpostgres.ValidCheckpointConnectionRecovery(maintenance, identity) {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrInvalid
	}
	if err := p.validateCheckpointSourceDefinition(definition, identity.SourceResourceID); err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, err
	}
	spec := definition.Spec
	config, err := p.maintenanceConnectionConfigForDatabase(ctx, connectionfence.Identity{OwnerToken: maintenance.OwnerToken,
		SourceResourceID: maintenance.SourceResourceID}, connectionfence.MaintenanceDatabase, spec.PostgresMajor)
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, err
	}
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrUnavailable
	}
	poolConfig.ConnConfig, poolConfig.MaxConns, poolConfig.MinConns = config.ConnConfig.Copy(), 1, 0
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, maintenanceConnectionError(ctx, err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, maintenanceConnectionError(ctx, err)
	}
	_, err = connectionfence.AuthenticateReadyMaintenance(ctx, conn.Conn(), connectionfence.BootstrapConfig{SourceDatabase: p.databaseName,
		SourceRole: maintenanceSourceRole, SourcePostgresMajor: spec.PostgresMajor}, connectionfence.Maintenance{
		Identity:  connectionfence.Identity{OwnerToken: maintenance.OwnerToken, SourceResourceID: maintenance.SourceResourceID},
		OwnerRole: "grg_ckpt_" + strings.ReplaceAll(maintenance.OwnerToken, "-", ""), OwnerOID: maintenance.OwnerOID,
		DatabaseOID: maintenance.DatabaseOID, State: "ready"})
	conn.Release()
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, err
	}
	controller, err := connectionfence.New(ctx, pool, connectionfence.Config{MaintenanceDatabase: connectionfence.MaintenanceDatabase,
		MaintenanceRole: maintenanceSourceRole, MaintenanceDatabaseOID: maintenance.DatabaseOID, MaintenanceOwnerOID: maintenance.OwnerOID})
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, err
	}
	remoteIdentity := connectionfence.Identity{OwnerToken: identity.OwnerToken, SourceResourceID: identity.SourceResourceID}
	var actual connectionfence.Observation
	if abandon {
		if err := controller.Install(ctx); err != nil {
			return managedpostgres.CheckpointConnectionTerminal{}, err
		}
		actual, err = controller.Abandon(ctx, remoteIdentity)
	} else {
		actual, err = controller.Observe(ctx, remoteIdentity)
	}
	if err != nil {
		return managedpostgres.CheckpointConnectionTerminal{}, err
	}
	return managedpostgres.CheckpointConnectionTerminal{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{
		OwnerToken: actual.OwnerToken, SourceResourceID: actual.SourceResourceID}, State: actual.State, ReleasedAt: actual.ReleasedAt}, nil
}
