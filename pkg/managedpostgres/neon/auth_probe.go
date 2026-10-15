package neon

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.AuthCredentialProber = (*Provider)(nil)

// Runs only on the explicitly disposable qualification database.
func (p *Provider) ProbeAuthCredentials(ctx context.Context, id string) (evidence managedpostgres.AuthCredentialEvidence, err error) {
	key := "qualification-auth-" + uuid.NewString()
	fixture := "gregale_auth_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	request := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key, IdempotencyKey: key, Access: managedpostgres.CredentialAuth}
	replacement := request
	replacement.IdentityKey += "-replacement"
	replacement.IdempotencyKey += "-replacement"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		owner, cleanupErr := p.qualificationOwnerConnection(cleanup, id)
		if cleanupErr == nil {
			_, cleanupErr = owner.Exec(cleanup, "DROP TABLE IF EXISTS auth."+roleIdentifier(fixture)+", public."+roleIdentifier(fixture))
			_ = owner.Close(cleanup)
		}
		for _, r := range []managedpostgres.CredentialRequest{request, replacement, writerRequest} {
			if revokeErr := p.RevokeCredentials(cleanup, r); revokeErr != nil {
				cleanupErr = revokeErr
			}
		}
		if cleanupErr != nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	writer, err := p.IssueCredentials(ctx, writerRequest)
	if err != nil {
		return evidence, err
	}
	migration, err := connectProbe(ctx, writer)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = migration.Close(ctx) }()
	material, err := p.IssueCredentials(ctx, request)
	if err != nil {
		return evidence, err
	}
	retried, err := p.IssueCredentials(ctx, request)
	if err != nil || retried.Username != material.Username || retried.Password != material.Password || retried.ProviderIdentityID != material.ProviderIdentityID {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.PasswordRecovered = true
	conn, err := connectProbe(ctx, material)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = conn.Close(ctx) }()
	role := p.credentialRole(request)
	if err := verifyAuthProbe(ctx, conn, migration, role.authOwner, role.schemaOwner, fixture); err != nil {
		return evidence, err
	}
	evidence.SchemaOwned, evidence.CustomerIsolated = true, true
	newMaterial, err := p.IssueCredentials(ctx, replacement)
	if err != nil || newMaterial.Username == material.Username {
		return evidence, managedpostgres.ErrUnavailable
	}
	newConn, err := connectProbe(ctx, newMaterial)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = newConn.Close(ctx) }()
	if err := p.RevokeCredentials(ctx, request); err != nil {
		return evidence, err
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err == nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	if err := verifyRejectedCredentials(ctx, material, newMaterial); err != nil {
		return evidence, err
	}
	evidence.Revoked = true
	var count int
	if err := newConn.QueryRow(ctx, "SELECT count(*) FROM auth."+roleIdentifier(fixture)).Scan(&count); err != nil || count != 1 {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.RotationPreservesData = true
	return evidence, nil
}

// verifyAuthProbe proves the auth login owns RLS-enabled auth tables, as
// GoTrue requires, and is denied every customer schema and database DDL.
func verifyAuthProbe(ctx context.Context, conn, migration *pgx.Conn, authOwner, schemaOwner, fixture string) error {
	if _, err := migration.Exec(ctx, "CREATE TABLE public."+roleIdentifier(fixture)+" (secret text)"); err != nil {
		return managedpostgres.ErrUnavailable
	}
	var current, searchPath string
	if err := conn.QueryRow(ctx, "SELECT current_user::text, current_setting('search_path')").Scan(&current, &searchPath); err != nil || current != authOwner || searchPath != "auth" {
		return managedpostgres.ErrUnavailable
	}
	table := "auth." + roleIdentifier(fixture)
	for _, query := range []string{
		"CREATE TABLE " + table + " (id integer PRIMARY KEY, body text NOT NULL)",
		"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
		"INSERT INTO " + table + " (id, body) VALUES (1, 'owned')",
		"UPDATE " + table + " SET body = 'updated' WHERE id = 1",
	} {
		if _, err := conn.Exec(ctx, query); err != nil {
			return managedpostgres.ErrUnavailable
		}
	}
	for _, query := range []string{
		"SELECT * FROM public." + roleIdentifier(fixture),
		"CREATE TABLE public." + roleIdentifier(fixture+"_forbidden") + " (id integer)",
		"CREATE SCHEMA " + roleIdentifier(fixture+"_forbidden"),
		"CREATE TEMP TABLE " + roleIdentifier(fixture+"_forbidden") + " (id integer)",
		"SET ROLE " + roleIdentifier(schemaOwner),
	} {
		if !sqlPermissionDenied(ctx, conn, query) {
			return managedpostgres.ErrUnavailable
		}
	}
	return nil
}
