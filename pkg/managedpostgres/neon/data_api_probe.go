package neon

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.DataAPICredentialProber = (*Provider)(nil)

// Runs only on the explicitly disposable qualification database.
func (p *Provider) ProbeDataAPICredentials(ctx context.Context, id string) (evidence managedpostgres.DataAPICredentialEvidence, err error) {
	key := "qualification-data-api-" + uuid.NewString()
	fixture := "gregale_api_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	request := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key, IdempotencyKey: key, Access: managedpostgres.CredentialDataAPI}
	replacement := request
	replacement.IdentityKey += "-replacement"
	replacement.IdempotencyKey += "-replacement"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		owner, cleanupErr := p.qualificationOwnerConnection(cleanup, id)
		if cleanupErr == nil {
			_, cleanupErr = owner.Exec(cleanup, "DROP TABLE IF EXISTS api."+roleIdentifier(fixture)+", public."+roleIdentifier(fixture))
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
	if err := managedpostgres.VerifyDataAPICredentialProbe(ctx, conn, migration, p.credentialRole(writerRequest).schemaOwner, fixture); err != nil {
		return evidence, err
	}
	evidence.SchemaIsolated, evidence.RLSEnforced = true, true
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
	if _, err := newConn.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"alice"}',false)`); err != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	var count int
	if err := newConn.QueryRow(ctx, "SELECT count(*) FROM api."+roleIdentifier(fixture)).Scan(&count); err != nil || count != 1 {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.RotationPreservesData = true
	return evidence, nil
}
