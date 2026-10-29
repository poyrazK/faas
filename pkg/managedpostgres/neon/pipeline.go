package neon

import (
	"context"
	"net"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.PipelineSQLProvider = (*Provider)(nil)

func (p *Provider) WithPipelineSQL(ctx context.Context, d managedpostgres.Database, run func(*pgx.Conn, string) error) error {
	// Only the exact ready, catalog-owned dataset can receive native SQL.
	before, err := p.Inspect(ctx, d.ProviderResourceID)
	if err != nil {
		return err
	}
	if before.Status != managedpostgres.ProviderStatusReady || before.DataResourceID != d.DataResourceID || before.Spec != d.Spec {
		return managedpostgres.ErrConflict
	}
	ref, err := parseResourceRef(d.DataResourceID)
	if err != nil || ref.branchID == "" {
		return managedpostgres.ErrConflict
	}
	material, err := p.ownerCredentials(ctx, ref.projectID, ref.branchID)
	if err != nil {
		return err
	}
	if len(material.Endpoints) != 1 || material.Username != ownerLogin {
		return managedpostgres.ErrConflict
	}
	ep := material.Endpoints[0]
	u := url.URL{Scheme: "postgres", User: url.UserPassword(material.Username, material.Password), Host: net.JoinHostPort(ep.Host, strconv.Itoa(int(ep.Port))), Path: "/" + material.Database, RawQuery: "sslmode=verify-full"}
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	cfg.RuntimeParams = map[string]string{"application_name": "gregale-pipeline-worker", "search_path": "pg_catalog", "statement_timeout": "15000", "lock_timeout": "2000", "TimeZone": "UTC"}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	role := p.credentialRole(managedpostgres.CredentialRequest{ProviderResourceID: d.ProviderResourceID, Access: managedpostgres.CredentialMigration})
	tx, err := conn.Begin(ctx)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1,0))`, role.schemaOwner); err != nil {
		return managedpostgres.ErrUnavailable
	}
	if err = initializeCredentialSchema(ctx, tx, role); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return managedpostgres.ErrUnavailable
	}
	current, err := p.Inspect(ctx, d.ProviderResourceID)
	if err != nil || current.DataResourceID != d.DataResourceID || current.Spec != d.Spec {
		return managedpostgres.ErrConflict
	}
	if err = run(conn, role.schemaOwner); err != nil {
		return err
	}
	after, err := p.Inspect(ctx, d.ProviderResourceID)
	if err != nil || after.DataResourceID != d.DataResourceID || after.Spec != d.Spec {
		return managedpostgres.ErrConflict
	}
	return nil
}
