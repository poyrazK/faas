package neon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
	"golang.org/x/sync/errgroup"
)

const maintenanceSourceRole = "gregale_owner"

type maintenancePlacement struct {
	endpoint
	PostgresMajor int
}

type maintenanceConnection struct {
	*pgx.ConnConfig
	PostgresMajor int
}

// These private worker seams do not advertise complete checkpoint support.
// The caller must first reserve the exact dataset and private maintenance
// owner durably under its live clone lease. Public clone admission stays closed.
func (p *Provider) ReserveMaintenance(ctx context.Context, identity connectionfence.Identity) (connectionfence.Maintenance, error) {
	return p.runMaintenance(ctx, identity, func(b *connectionfence.Bootstrap) (connectionfence.Maintenance, error) {
		return b.Reserve(ctx, identity)
	})
}

func (p *Provider) CreateMaintenance(ctx context.Context, receipt connectionfence.Maintenance) (connectionfence.Maintenance, error) {
	return p.runMaintenance(ctx, receipt.Identity, func(b *connectionfence.Bootstrap) (connectionfence.Maintenance, error) {
		return b.Create(ctx, receipt)
	})
}

func (p *Provider) ObserveMaintenance(ctx context.Context, receipt connectionfence.Maintenance) (connectionfence.Maintenance, error) {
	return p.runMaintenance(ctx, receipt.Identity, func(b *connectionfence.Bootstrap) (connectionfence.Maintenance, error) {
		return b.Observe(ctx, receipt)
	})
}

func (p *Provider) ActivateMaintenance(ctx context.Context, receipt connectionfence.Maintenance) (connectionfence.Maintenance, error) {
	return p.runMaintenance(ctx, receipt.Identity, func(b *connectionfence.Bootstrap) (connectionfence.Maintenance, error) {
		return b.Activate(ctx, receipt)
	})
}

func (p *Provider) RetireReservedMaintenance(ctx context.Context, receipt connectionfence.Maintenance) (connectionfence.Maintenance, error) {
	return p.runMaintenance(ctx, receipt.Identity, func(b *connectionfence.Bootstrap) (connectionfence.Maintenance, error) {
		return b.RetireReserved(ctx, receipt)
	})
}

func (p *Provider) runMaintenance(ctx context.Context, identity connectionfence.Identity,
	run func(*connectionfence.Bootstrap) (connectionfence.Maintenance, error)) (connectionfence.Maintenance, error) {
	config, err := p.maintenanceConnectionConfig(ctx, identity)
	if err != nil {
		return connectionfence.Maintenance{}, err
	}
	conn, err := pgx.ConnectConfig(ctx, config.ConnConfig)
	if err != nil {
		return connectionfence.Maintenance{}, maintenanceConnectionError(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	b, err := connectionfence.NewBootstrap(conn, connectionfence.BootstrapConfig{SourceDatabase: p.databaseName,
		SourceRole: maintenanceSourceRole, SourcePostgresMajor: config.PostgresMajor})
	if err != nil {
		return connectionfence.Maintenance{}, err
	}
	return run(b)
}

// Fetch credentials only for the exact frozen branch, and verify the returned
// direct host independently against its endpoint. Default-branch aliases,
// pooled sessions and provider-supplied URI session options are not used.
func (p *Provider) maintenanceConnectionConfig(ctx context.Context, identity connectionfence.Identity) (*maintenanceConnection, error) {
	if p == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	token, tokenErr := uuid.Parse(identity.OwnerToken)
	source, err := parseResourceRef(identity.SourceResourceID)
	if tokenErr != nil || token == uuid.Nil || token.String() != identity.OwnerToken || err != nil || source.branchID == "" {
		return nil, managedpostgres.ErrInvalid
	}
	before, err := p.maintenanceEndpoint(ctx, source)
	if err != nil {
		return nil, err
	}
	var response connectionURIResponse
	if err := p.connectionURI(ctx, source.projectID, source.branchID, maintenanceSourceRole, false, &response); err != nil {
		return nil, err
	}
	material, err := parseConnectionURI(response.URI)
	if err != nil || material.username != maintenanceSourceRole || material.database != p.databaseName ||
		material.host != before.Host || material.port != 5432 {
		return nil, managedpostgres.ErrConflict
	}
	after, err := p.maintenanceEndpoint(ctx, source)
	if err != nil {
		return nil, err
	}
	if after.ID != before.ID || after.Host != before.Host || after.PostgresMajor != before.PostgresMajor {
		return nil, managedpostgres.ErrConflict
	}
	// Rebuild a minimal URI: API material never controls role, hostaddr,
	// replication mode, session authorization, or startup SQL options.
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(material.host, "5432"),
		User: url.UserPassword(material.username, material.password), Path: "/" + material.database,
		RawQuery: url.Values{"sslmode": {"verify-full"}}.Encode()}
	config, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, managedpostgres.ErrUnavailable
	}
	config.RuntimeParams = map[string]string{"application_name": "gregale-maintenance-bootstrap"}
	return &maintenanceConnection{ConnConfig: config, PostgresMajor: before.PostgresMajor}, nil
}

func (p *Provider) maintenanceEndpoint(ctx context.Context, source resourceRef) (maintenancePlacement, error) {
	path := "/projects/" + url.PathEscape(source.projectID)
	var projectResult projectResponse
	var branchesResult branchesResponse
	var endpointsResult endpointsResponse
	var operationsResult operationsResponse
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, path, nil, nil, &projectResult, http.StatusOK)
	})
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, path+"/branches", nil, nil, &branchesResult, http.StatusOK)
	})
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, path+"/endpoints", nil, nil, &endpointsResult, http.StatusOK)
	})
	group.Go(func() error {
		return p.doJSON(groupContext, http.MethodGet, path+"/operations", url.Values{"limit": {"1000"}}, nil, &operationsResult, http.StatusOK)
	})
	if err := group.Wait(); err != nil {
		return maintenancePlacement{}, err
	}
	if projectResult.Project.ID != source.projectID || projectResult.Project.OrganizationID != p.organizationID ||
		projectResult.Project.RegionID != p.regionID || operationsResult.Pagination.Cursor != "" {
		return maintenancePlacement{}, managedpostgres.ErrConflict
	}
	if projectResult.Project.PostgresMajor < 16 || !slices.Contains(p.Capabilities().PostgresMajors, projectResult.Project.PostgresMajor) {
		return maintenancePlacement{}, managedpostgres.ErrUnsupported
	}
	selected, primary, ready := selectBranch(branchesResult.Branches, endpointsResult.Endpoints, source.branchID)
	if selected.ID != source.branchID || selected.ProjectID != source.projectID || primary.ProjectID != source.projectID ||
		primary.RegionID != p.regionID || !validProviderID.MatchString(primary.ID) || primary.Host == "" || primary.Disabled == nil || *primary.Disabled ||
		!ready || operationStatus(operationsResult.Operations, ready) != managedpostgres.ProviderStatusReady {
		return maintenancePlacement{}, managedpostgres.ErrConflict
	}
	return maintenancePlacement{endpoint: primary, PostgresMajor: projectResult.Project.PostgresMajor}, nil
}

func maintenanceConnectionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return managedpostgres.ErrUnavailable
}
