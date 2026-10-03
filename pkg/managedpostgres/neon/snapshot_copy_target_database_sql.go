package neon

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	inventorysql "github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
)

var _ managedpostgres.SnapshotCopyTargetDatabaseSQLProvider = (*Provider)(nil)

func (p *Provider) WithSnapshotCopyTargetDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetDatabaseSQLRequest, run managedpostgres.SnapshotCopyTargetSQLRun) error {
	return p.withSnapshotCopyTargetDatabaseSQL(ctx, d, r, run, pgx.ConnectConfig)
}

// The test-only connector qualifies actual SQL against a local private socket.
// Production establishes the exact independent endpoint with verify-full TLS.
func (p *Provider) withSnapshotCopyTargetDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetDatabaseSQLRequest, run managedpostgres.SnapshotCopyTargetSQLRun, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) error {
	if run == nil || connect == nil {
		return managedpostgres.ErrInvalid
	}
	if err := r.Validate(d); err != nil {
		return err
	}
	if p == nil {
		return managedpostgres.ErrUnavailable
	}
	// The independently created project has this platform-owned bootstrap role.
	// Other role identities require separately qualified globals provisioning.
	if r.Target.RoleName != maintenanceSourceRole {
		return managedpostgres.ErrUnsupported
	}
	before, err := p.snapshotCopyTargetSQLPlacement(ctx, d, r)
	if err != nil {
		return err
	}
	ref, err := parseResourceRef(r.Target.DataResourceID)
	if err != nil {
		return managedpostgres.ErrInvalid
	}
	var response connectionURIResponse
	// Obtain credentials using the fixed provisioned bootstrap database. A
	// selected SQL name never controls a provider path, URI option or endpoint.
	if err := p.connectionURIForDatabaseEndpoint(ctx, ref.projectID, ref.branchID, r.Target.EndpointID, p.databaseName, maintenanceSourceRole, false, &response); err != nil {
		return err
	}
	material, err := parseConnectionURI(response.URI)
	if err != nil || material.username != maintenanceSourceRole || material.database != p.databaseName || material.host != before.Host || material.port != 5432 {
		return managedpostgres.ErrConflict
	}
	confirmed, err := p.snapshotCopyTargetSQLPlacement(ctx, d, r)
	if err != nil {
		return err
	}
	if confirmed.Host != before.Host {
		return managedpostgres.ErrConflict
	}
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(material.host, "5432"), User: url.UserPassword(material.username, material.password),
		Path: "/" + p.databaseName, RawQuery: url.Values{"sslmode": {"verify-full"}}.Encode()}
	config, err := pgx.ParseConfig(u.String())
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	config.Database = r.Target.DatabaseName
	config.RuntimeParams = map[string]string{"application_name": "gregale-snapshot-copy-target", "default_transaction_read_only": "off", "search_path": "pg_catalog"}
	expected := config.Copy()
	conn, err := connect(ctx, config)
	if conn != nil {
		defer closeSnapshotCopySQLConnection(ctx, conn)
	}
	if err != nil {
		return maintenanceConnectionError(ctx, err)
	}
	identity, err := authenticateSnapshotCopyTargetSQL(ctx, conn, r)
	if err != nil {
		return err
	}
	connected, err := p.snapshotCopyTargetSQLPlacement(ctx, d, r)
	if err != nil {
		return err
	}
	if connected.Host != expected.Host {
		return managedpostgres.ErrConflict
	}
	if err := run(ctx, conn, identity); err != nil {
		return err
	}
	after, err := p.snapshotCopyTargetSQLPlacement(ctx, d, r)
	if err != nil {
		return err
	}
	if after.Host != expected.Host {
		return managedpostgres.ErrConflict
	}
	observed, err := authenticateSnapshotCopyTargetSQL(ctx, conn, r)
	if err != nil {
		return err
	}
	if observed != identity {
		return managedpostgres.ErrConflict
	}
	return ctx.Err()
}

func (p *Provider) snapshotCopyTargetSQLPlacement(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetDatabaseSQLRequest) (endpoint, error) {
	topology, err := p.snapshotCopyTargetTopology(ctx, d, r.Preparation, false)
	if err != nil {
		return endpoint{}, err
	}
	if !topology.observation.Prepared {
		return endpoint{}, managedpostgres.ErrUnavailable
	}
	ref, err := parseResourceRef(r.Target.DataResourceID)
	x, target := topology.endpoint, r.Target
	at, timeErr := time.Parse(time.RFC3339Nano, x.CreatedAt)
	if err != nil || ref.projectID != target.ProviderResourceID || ref.branchID != topology.branch.ID || x.ID != target.EndpointID ||
		x.Type != "read_write" || x.BranchID != ref.branchID || !validCopyReaderEndpointID(x.ID) || timeErr != nil || !at.Equal(target.EndpointCreatedAt) ||
		x.Host == "" || !strings.HasPrefix(x.Host, x.ID+".") || x.Disabled == nil || *x.Disabled || x.PasswordlessAccess == nil || *x.PasswordlessAccess {
		return endpoint{}, managedpostgres.ErrConflict
	}
	if x.PendingState != "" {
		return endpoint{}, managedpostgres.ErrUnavailable
	}
	return x, nil
}

func authenticateSnapshotCopyTargetSQL(ctx context.Context, conn *pgx.Conn, r managedpostgres.SnapshotCopyTargetDatabaseSQLRequest) (managedpostgres.SnapshotCopyTargetSQLIdentity, error) {
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
		return managedpostgres.SnapshotCopyTargetSQLIdentity{}, managedpostgres.ErrConflict
	}
	a, err := inventorysql.New().CopyClusterIdentity(ctx, conn)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetSQLIdentity{}, maintenanceConnectionError(ctx, err)
	}
	t := r.Target
	if int(a.ServerVersion/10000) != t.Scope.PostgresMajor || a.DatabaseName != t.DatabaseName || !a.DatabaseOid.Valid || a.DatabaseOid.Uint32 != t.DatabaseOID ||
		!a.RoleOid.Valid || a.RoleOid.Uint32 != t.RoleOID || a.RoleName != t.RoleName || a.SessionRole != t.RoleName || conn.Config().User != t.RoleName || a.ReadOnly {
		return managedpostgres.SnapshotCopyTargetSQLIdentity{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetSQLIdentity{PostgresMajor: t.Scope.PostgresMajor, DatabaseName: a.DatabaseName, RoleName: a.RoleName,
		DatabaseOID: a.DatabaseOid.Uint32, RoleOID: a.RoleOid.Uint32}, nil
}
