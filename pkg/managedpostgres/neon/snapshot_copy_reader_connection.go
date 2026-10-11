package neon

import (
	"context"
	"net"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
)

// Private, credential-bearing connection input. The durable reader owner must
// supply the exact endpoint ID and creation time. This helper makes no SQL
// connection and supplies no copied-data or writer-release authority.
func (p *Provider) snapshotCopyReaderConnectionConfig(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (*pgx.ConnConfig, error) {
	if p == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	if r.ExpectedEndpointID == "" || r.Validate() != nil {
		return nil, managedpostgres.ErrInvalid
	}
	before, err := p.snapshotCopyReaderConnectionPlacement(ctx, d, r)
	if err != nil {
		return nil, err
	}
	capture, err := parseResourceRef(r.Capture.ExpectedTargetResourceID)
	if err != nil {
		return nil, managedpostgres.ErrInvalid
	}
	var response connectionURIResponse
	if err := p.connectionURIForDatabaseEndpoint(ctx, capture.projectID, capture.branchID, r.ExpectedEndpointID,
		connectionfence.MaintenanceDatabase, maintenanceSourceRole, false, &response); err != nil {
		return nil, err
	}
	material, err := parseConnectionURI(response.URI)
	if err != nil || material.username != maintenanceSourceRole || material.database != connectionfence.MaintenanceDatabase ||
		material.host != before.Host || material.port != 5432 {
		return nil, managedpostgres.ErrConflict
	}
	after, err := p.snapshotCopyReaderConnectionPlacement(ctx, d, r)
	if err != nil {
		return nil, err
	}
	if after.ID != before.ID || after.Host != before.Host || !sameCopyReaderCreation(after.CreatedAt, before.CreatedAt) {
		return nil, managedpostgres.ErrConflict
	}
	// Provider URI startup options cannot control hostaddr, role, search_path,
	// replication mode, session authorization, or TLS fallback behavior.
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(material.host, "5432"), User: url.UserPassword(material.username, material.password),
		Path: "/" + connectionfence.MaintenanceDatabase, RawQuery: url.Values{"sslmode": {"verify-full"}}.Encode()}
	config, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, managedpostgres.ErrUnavailable
	}
	config.RuntimeParams = map[string]string{"application_name": "gregale-snapshot-copy-reader", "default_transaction_read_only": "on", "search_path": "pg_catalog"}
	return config, nil
}

func (p *Provider) snapshotCopyReaderConnectionPlacement(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (endpoint, error) {
	observed, err := p.FindSnapshotCopyReader(ctx, d, r)
	if err != nil {
		return endpoint{}, err
	}
	if !observed.Available {
		return endpoint{}, managedpostgres.ErrUnavailable
	}
	capture, err := parseResourceRef(r.Capture.ExpectedTargetResourceID)
	if err != nil {
		return endpoint{}, managedpostgres.ErrInvalid
	}
	actual, err := p.findSnapshotCopyReaderEndpoint(ctx, capture.projectID, r.ExpectedEndpointID, p.snapshotCopyReaderName(r.ResourceID))
	if err != nil {
		return endpoint{}, err
	}
	checked, err := p.snapshotCopyReaderObservation(d.Spec, r, capture, actual)
	if err != nil {
		return endpoint{}, err
	}
	if !checked.Available {
		return endpoint{}, managedpostgres.ErrUnavailable
	}
	return actual, nil
}
