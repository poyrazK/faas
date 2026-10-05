package neon

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

var _ managedpostgres.CheckpointMaintenanceProvider = (*Provider)(nil)

func (p *Provider) ReconcileCheckpointMaintenance(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, request managedpostgres.CheckpointMaintenanceRequest) (managedpostgres.CheckpointMaintenance, error) {
	if p == nil {
		return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrUnavailable
	}
	if request.Validate() != nil {
		return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrInvalid
	}
	if err := p.validateCheckpointSourceDefinition(definition, request.SourceResourceID); err != nil {
		return managedpostgres.CheckpointMaintenance{}, err
	}
	spec := definition.Spec
	identity := connectionfence.Identity{OwnerToken: request.OwnerToken, SourceResourceID: request.SourceResourceID}
	databaseName := p.databaseName
	if request.Phase == "ready" {
		databaseName = connectionfence.MaintenanceDatabase
	}
	config, err := p.maintenanceConnectionConfigForDatabase(ctx, identity, databaseName, spec.PostgresMajor)
	if err != nil {
		return managedpostgres.CheckpointMaintenance{}, err
	}
	conn, err := pgx.ConnectConfig(ctx, config.ConnConfig)
	if err != nil {
		return managedpostgres.CheckpointMaintenance{}, maintenanceConnectionError(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	receipt := connectionfence.Maintenance{Identity: identity, OwnerRole: "grg_ckpt_" + strings.ReplaceAll(request.OwnerToken, "-", ""),
		OwnerOID: request.OwnerOID, DatabaseOID: request.DatabaseOID, State: "reserved"}
	bootstrapConfig := connectionfence.BootstrapConfig{SourceDatabase: p.databaseName, SourceRole: maintenanceSourceRole, SourcePostgresMajor: spec.PostgresMajor}
	var actual connectionfence.Maintenance
	if request.Phase == "ready" {
		receipt.State = "ready"
		actual, err = connectionfence.AuthenticateReadyMaintenance(ctx, conn, bootstrapConfig, receipt)
	} else {
		bootstrap, bootstrapErr := connectionfence.NewBootstrap(conn, bootstrapConfig)
		if bootstrapErr != nil {
			return managedpostgres.CheckpointMaintenance{}, bootstrapErr
		}
		switch request.Phase {
		case "role":
			actual, err = bootstrap.Reserve(ctx, identity)
		case "database":
			actual, err = bootstrap.Create(ctx, receipt)
		case "activation":
			actual, err = bootstrap.Activate(ctx, receipt)
		}
	}
	if err != nil {
		return managedpostgres.CheckpointMaintenance{}, err
	}
	return managedpostgres.CheckpointMaintenance{OwnerToken: actual.OwnerToken, SourceResourceID: actual.SourceResourceID,
		State: actual.State, OwnerOID: actual.OwnerOID, DatabaseOID: actual.DatabaseOID}, nil
}

func (p *Provider) validateCheckpointSourceDefinition(definition managedpostgres.RestoreSourceDefinition, sourceID string) error {
	if p == nil {
		return managedpostgres.ErrUnavailable
	}
	lifecycle, lifecycleErr := parseResourceRef(definition.ProviderResourceID)
	dataset, datasetErr := parseResourceRef(sourceID)
	if lifecycleErr != nil || datasetErr != nil || dataset.branchID == "" || definition.DataResourceID != sourceID || definition.Spec.Region != p.logicalRegion {
		return managedpostgres.ErrInvalid
	}
	if lifecycle.projectID != dataset.projectID || lifecycle.branchID != "" && lifecycle.branchID != dataset.branchID {
		return managedpostgres.ErrConflict
	}
	if err := p.Capabilities().Supports(definition.Spec); err != nil {
		return err
	}
	if definition.Spec.PostgresMajor < 16 {
		return managedpostgres.ErrUnsupported
	}
	return nil
}
