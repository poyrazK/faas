package neon

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	inventorysql "github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
)

var _ managedpostgres.SnapshotCopyTargetSQLInspectionProvider = (*Provider)(nil)

func (p *Provider) InspectSnapshotCopyTargetSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
	return p.inspectSnapshotCopyTargetSQL(ctx, d, r, pgx.ConnectConfig)
}

// Discovery performs no bootstrap DDL. SQL and exact independent placement are
// authenticated again before any observation may leave the owned connection.
func (p *Provider) inspectSnapshotCopyTargetSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) (managedpostgres.SnapshotCopyTargetSQLObservation, error) {
	var zero managedpostgres.SnapshotCopyTargetSQLObservation
	if connect == nil || r.Validate() != nil || r.ExpectedProviderResourceID == "" || r.Capture.Snapshot.SourceResourceID != d.DataResourceID || r.ExpectedProviderResourceID == d.ProviderResourceID {
		return zero, managedpostgres.ErrInvalid
	}
	before, host, err := p.snapshotCopyTargetBootstrapPlacement(ctx, d, r)
	if err != nil {
		return zero, err
	}
	ref, err := parseResourceRef(before.DataResourceID)
	if err != nil {
		return zero, managedpostgres.ErrConflict
	}
	config, err := p.snapshotCopyTargetConnectionConfig(ctx, ref.projectID, ref.branchID, before.EndpointID, host)
	if err != nil {
		return zero, err
	}
	placement := func() error {
		actual, actualHost, err := p.snapshotCopyTargetBootstrapPlacement(ctx, d, r)
		if err != nil {
			return err
		}
		if actualHost != host || actual.ProviderResourceID != before.ProviderResourceID || actual.DataResourceID != before.DataResourceID || actual.EndpointID != before.EndpointID ||
			!actual.ProviderCreatedAt.Equal(before.ProviderCreatedAt) || !actual.EndpointCreatedAt.Equal(before.EndpointCreatedAt) {
			return managedpostgres.ErrConflict
		}
		return nil
	}
	if err := placement(); err != nil {
		return zero, err
	}
	expected := config.Copy()
	conn, err := connect(ctx, config)
	if conn != nil {
		defer closeSnapshotCopySQLConnection(ctx, conn)
	}
	if err != nil {
		return zero, maintenanceConnectionError(ctx, err)
	}
	identity, err := authenticateSnapshotCopyTargetBootstrapSQL(ctx, conn, expected, d.Spec.PostgresMajor)
	if err != nil {
		return zero, err
	}
	if err := placement(); err != nil {
		return zero, err
	}
	after, err := authenticateSnapshotCopyTargetBootstrapSQL(ctx, conn, expected, d.Spec.PostgresMajor)
	if err != nil || after != identity {
		if err == nil {
			err = managedpostgres.ErrConflict
		}
		return zero, err
	}
	before.Identity = identity
	if err := before.Validate(d, r); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return before, nil
}

func (p *Provider) snapshotCopyTargetBootstrapPlacement(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetSQLObservation, string, error) {
	var zero managedpostgres.SnapshotCopyTargetSQLObservation
	topology, err := p.snapshotCopyTargetTopology(ctx, d, r, false)
	if err != nil {
		return zero, "", err
	}
	if !topology.observation.Prepared {
		return zero, "", managedpostgres.ErrUnavailable
	}
	x := topology.endpoint
	at, err := time.Parse(time.RFC3339Nano, x.CreatedAt)
	if x.Type != "read_write" || x.BranchID != topology.branch.ID || !validCopyReaderEndpointID(x.ID) || err != nil || at.Before(topology.observation.CreatedAt) || at.After(time.Now()) || at.Nanosecond()%1000 != 0 ||
		x.Host == "" || !strings.HasPrefix(x.Host, x.ID+".") || x.Disabled == nil || *x.Disabled || x.PasswordlessAccess == nil || *x.PasswordlessAccess {
		return zero, "", managedpostgres.ErrConflict
	}
	if x.PendingState != "" {
		return zero, "", managedpostgres.ErrUnavailable
	}
	return managedpostgres.SnapshotCopyTargetSQLObservation{ProviderResourceID: topology.observation.ProviderResourceID, ProviderCreatedAt: topology.observation.CreatedAt,
		DataResourceID: topology.observation.ProviderResourceID + "/" + topology.branch.ID, EndpointID: x.ID, EndpointCreatedAt: at.UTC()}, x.Host, nil
}

func authenticateSnapshotCopyTargetBootstrapSQL(ctx context.Context, conn *pgx.Conn, expected *pgx.ConnConfig, major int) (managedpostgres.SnapshotCopyTargetSQLIdentity, error) {
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
		return managedpostgres.SnapshotCopyTargetSQLIdentity{}, managedpostgres.ErrConflict
	}
	a, err := inventorysql.New().CopyClusterIdentity(ctx, conn)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetSQLIdentity{}, maintenanceConnectionError(ctx, err)
	}
	return snapshotCopyTargetIdentity(a, expected, major)
}
