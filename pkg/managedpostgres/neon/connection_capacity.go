package neon

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

var _ managedpostgres.ConnectionCapacityProvider = (*Provider)(nil)

func (p *Provider) ObserveConnectionCapacity(ctx context.Context, database managedpostgres.Database) (connectioncapacity.Observation, error) {
	return p.observeConnectionCapacity(ctx, database, pgx.ConnectConfig)
}

// Reuse the maintenance credential/endpoint validator, but never bootstrap a
// maintenance database or install objects. An idle or moving compute is unknown;
// do not intentionally wake it just to collect capacity.
func (p *Provider) observeConnectionCapacity(ctx context.Context, database managedpostgres.Database,
	connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) (connectioncapacity.Observation, error) {
	definition := managedpostgres.RestoreSourceDefinition{Spec: database.Spec, ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID}
	if err := p.validateCheckpointSourceDefinition(definition, database.DataResourceID); err != nil {
		return connectioncapacity.Observation{}, err
	}
	identity := connectionfence.Identity{OwnerToken: uuid.NewString(), SourceResourceID: database.DataResourceID}
	config, err := p.maintenanceConnectionConfigForDatabase(ctx, identity, p.databaseName, database.Spec.PostgresMajor)
	if err != nil {
		return connectioncapacity.Observation{}, err
	}
	if computeState(config.placement.CurrentState) != managedpostgres.ComputeStateActive || config.placement.PendingState != "" {
		return connectioncapacity.Observation{}, managedpostgres.ErrUnavailable
	}
	config.RuntimeParams = map[string]string{"application_name": "gregale-capacity-observer"}
	conn, err := connect(ctx, config.ConnConfig)
	if err != nil {
		return connectioncapacity.Observation{}, maintenanceConnectionError(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	observed, err := connectioncapacity.Read(ctx, conn, p.databaseName, maintenanceSourceRole, database.Spec.PostgresMajor)
	if err != nil {
		return connectioncapacity.Observation{}, managedpostgres.ErrUnavailable
	}
	source, _ := parseResourceRef(database.DataResourceID)
	after, err := p.maintenanceEndpoint(ctx, source)
	if err != nil {
		return connectioncapacity.Observation{}, err
	}
	if after.ID != config.placement.ID || after.Host != config.placement.Host || after.PostgresMajor != config.PostgresMajor ||
		computeState(after.CurrentState) != managedpostgres.ComputeStateActive || after.PendingState != "" {
		return connectioncapacity.Observation{}, managedpostgres.ErrConflict
	}
	return observed, nil
}
