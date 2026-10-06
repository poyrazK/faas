package neon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.CredentialPrivilegeProber = (*Provider)(nil)
var _ managedpostgres.RestoreCredentialIsolationProber = (*Provider)(nil)

// This is only called on explicitly disposable operator qualification resources.
func (p *Provider) ProbeCredentialPrivileges(ctx context.Context, id string, runtime managedpostgres.CredentialMaterial) (evidence managedpostgres.CredentialPrivilegeEvidence, err error) {
	identity := "qualification-privileges-" + uuid.NewString()
	request := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: identity, IdempotencyKey: identity, Access: managedpostgres.CredentialMigration}
	role := p.credentialRole(request)
	fixture := "gregale_privileges_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// Issuance may create a role before password recovery fails. Always retire it.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		owner, cleanupErr := p.qualificationOwnerConnection(cleanupCtx, id)
		if cleanupErr == nil {
			_, cleanupErr = owner.Exec(cleanupCtx, "DROP TABLE IF EXISTS public."+roleIdentifier(fixture))
			_ = owner.Close(cleanupCtx)
		}
		revokeErr := p.RevokeCredentials(cleanupCtx, request)
		if cleanupErr != nil || revokeErr != nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	migration, err := p.IssueCredentials(ctx, request)
	if err != nil {
		return evidence, err
	}
	retried, err := p.IssueCredentials(ctx, request)
	if err != nil || retried.Username != migration.Username || retried.Password != migration.Password || retried.Database != migration.Database {
		return evidence, managedpostgres.ErrUnavailable
	}
	runtimeConn, err := connectProbe(ctx, runtime)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = runtimeConn.Close(ctx) }()
	migrationConn, err := connectProbe(ctx, migration)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = migrationConn.Close(ctx) }()
	if err := verifyCredentialPrivileges(ctx, runtimeConn, migrationConn, role, fixture); err != nil {
		return evidence, err
	}
	evidence.RuntimeRestricted = true
	evidence.MigrationSeparated = true
	if err := p.RevokeCredentials(ctx, request); err != nil {
		return evidence, err
	}
	var count int
	if err := runtimeConn.QueryRow(ctx, "SELECT count(*) FROM public."+roleIdentifier(fixture)).Scan(&count); err != nil || count != 1 {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.RotationPreservesData = true
	return evidence, nil
}

func connectProbe(ctx context.Context, material managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
	dsn, err := probeDSN(material)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.Connect(ctx, dsn)
	return conn, credentialSQLError(err)
}

func (p *Provider) qualificationOwnerConnection(ctx context.Context, id string) (*pgx.Conn, error) {
	ref, err := parseResourceRef(id)
	if err != nil {
		return nil, err
	}
	branch := ref.branchID
	if branch == "" {
		branch, err = p.defaultBranch(ctx, ref.projectID)
	}
	if err != nil {
		return nil, err
	}
	material, err := p.ownerCredentials(ctx, ref.projectID, branch)
	if err != nil {
		return nil, err
	}
	conn, err := connectProbe(ctx, material)
	if err != nil {
		return nil, err
	}
	role := p.credentialRole(managedpostgres.CredentialRequest{ProviderResourceID: id})
	if _, err := conn.Exec(ctx, "SET ROLE "+roleIdentifier(role.schemaOwner)); err != nil {
		_ = conn.Close(ctx)
		return nil, managedpostgres.ErrUnavailable
	}
	return conn, nil
}

func verifyCredentialPrivileges(ctx context.Context, runtime, migration *pgx.Conn, role credentialRole, fixture string) error {
	var owner, login string
	if err := migration.QueryRow(ctx, `SELECT current_user, session_user`).Scan(&owner, &login); err != nil || owner != role.schemaOwner || login != role.name {
		return managedpostgres.ErrUnavailable
	}
	var unsafe bool
	if err := runtime.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR rolinherit
 OR EXISTS (SELECT 1 FROM pg_auth_members WHERE member=r.oid) FROM pg_roles r WHERE rolname=current_user`).Scan(&unsafe); err != nil || unsafe {
		return managedpostgres.ErrUnavailable
	}
	table := "public." + roleIdentifier(fixture)
	queries := []string{
		"CREATE TABLE " + table + " (id serial PRIMARY KEY, tenant integer NOT NULL, value text)",
		"INSERT INTO " + table + " (tenant,value) VALUES (1,'visible'),(2,'hidden')",
		"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY tenant_one ON " + table + " USING (tenant=1) WITH CHECK (tenant=1)",
	}
	for _, query := range queries {
		if _, err := migration.Exec(ctx, query); err != nil {
			return managedpostgres.ErrUnavailable
		}
	}
	var count int
	if err := runtime.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
		return managedpostgres.ErrUnavailable
	}
	for _, query := range []string{"INSERT INTO " + table + " (tenant,value) VALUES (1,'write')", "UPDATE " + table + " SET value='changed' WHERE value='write'", "DELETE FROM " + table + " WHERE value='changed'"} {
		if _, err := runtime.Exec(ctx, query); err != nil {
			return managedpostgres.ErrUnavailable
		}
	}
	for _, query := range []string{
		"CREATE TABLE public." + roleIdentifier(fixture+"_forbidden") + " (id integer)",
		"CREATE TEMP TABLE " + roleIdentifier(fixture+"_forbidden") + " (id integer)",
		"TRUNCATE " + table, "ALTER TABLE " + table + " ADD COLUMN forbidden integer",
		"CREATE ROLE " + roleIdentifier(fixture+"_forbidden"), "SET ROLE " + roleIdentifier(role.schemaOwner),
		"INSERT INTO " + table + " (tenant,value) VALUES (2,'forbidden')",
	} {
		if !sqlPermissionDenied(ctx, runtime, query) {
			return managedpostgres.ErrUnavailable
		}
	}
	if _, err := runtime.Exec(ctx, `SET row_security=off`); err != nil {
		return managedpostgres.ErrUnavailable
	}
	denied := sqlPermissionDenied(ctx, runtime, "SELECT * FROM "+table)
	if _, err := runtime.Exec(ctx, `SET row_security=on`); err != nil || !denied {
		return managedpostgres.ErrUnavailable
	}
	if _, err := migration.Exec(ctx, `SET ROLE NONE`); err != nil {
		return managedpostgres.ErrUnavailable
	}
	if !sqlPermissionDenied(ctx, migration, "CREATE TABLE public."+roleIdentifier(fixture+"_login_owned")+" (id integer)") {
		return managedpostgres.ErrUnavailable
	}
	if _, err := migration.Exec(ctx, "SET ROLE "+roleIdentifier(role.schemaOwner)); err != nil {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func sqlPermissionDenied(ctx context.Context, conn *pgx.Conn, query string) bool {
	_, err := conn.Exec(ctx, query)
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func (*Provider) VerifyRestoreCredentialIsolation(ctx context.Context, source, target managedpostgres.CredentialMaterial) error {
	return verifyRejectedCredentials(ctx, source, target)
}

// Require an authentication denial; network failures cannot prove revocation.
func verifyRejectedCredentials(ctx context.Context, source, target managedpostgres.CredentialMaterial) error {
	if source.Username == target.Username {
		return managedpostgres.ErrUnavailable
	}
	inherited := target
	inherited.Username = source.Username
	inherited.Password = source.Password
	dsn, err := probeDSN(inherited)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if conn != nil {
		_ = conn.Close(ctx)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "28000" || pgErr.Code == "28P01") {
		return nil
	}
	return managedpostgres.ErrUnavailable
}
