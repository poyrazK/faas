package neon

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ReadOnlyCredentialProber = (*Provider)(nil)

func (p *Provider) ProbeReadOnlyCredentials(ctx context.Context, id string) (evidence managedpostgres.ReadOnlyCredentialEvidence, err error) {
	key := "qualification-readonly-" + uuid.NewString()
	fixture := "gregale_ro_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	readerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-reader", IdempotencyKey: key + "-reader", Access: managedpostgres.CredentialReadOnly}
	replacementRequest := readerRequest
	replacementRequest.IdentityKey += "-replacement"
	replacementRequest.IdempotencyKey += "-replacement"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		owner, cleanupErr := p.qualificationOwnerConnection(cleanup, id)
		if cleanupErr == nil {
			_, cleanupErr = owner.Exec(cleanup, "DROP TABLE IF EXISTS public."+roleIdentifier(fixture)+", public."+roleIdentifier(fixture+"_future"))
			_ = owner.Close(cleanup)
		}
		for _, request := range []managedpostgres.CredentialRequest{readerRequest, replacementRequest, writerRequest} {
			if revokeErr := p.RevokeCredentials(cleanup, request); revokeErr != nil {
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
	if err := managedpostgres.PrepareReadOnlyCredentialProbe(ctx, migration, fixture); err != nil {
		return evidence, err
	}
	material, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil {
		return evidence, err
	}
	retried, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil || retried.ProviderIdentityID != material.ProviderIdentityID || retried.Username != material.Username || retried.Password != material.Password || retried.Database != material.Database {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.PasswordRecovered = true
	reader, err := connectProbe(ctx, material)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = reader.Close(ctx) }()
	if err := managedpostgres.VerifyReadOnlyCredentialProbe(ctx, reader, migration, fixture); err != nil {
		return evidence, err
	}
	evidence.Restricted = true
	replacement, err := p.IssueCredentials(ctx, replacementRequest)
	if err != nil || replacement.Username == material.Username {
		return evidence, managedpostgres.ErrUnavailable
	}
	newReader, err := connectProbe(ctx, replacement)
	if err != nil {
		return evidence, err
	}
	defer func() { _ = newReader.Close(ctx) }()
	if err := p.RevokeCredentials(ctx, readerRequest); err != nil {
		return evidence, err
	}
	if _, err := reader.Exec(ctx, "SELECT 1"); err == nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	if err := verifyRejectedCredentials(ctx, material, replacement); err != nil {
		return evidence, err
	}
	evidence.Revoked = true
	var count int
	if err := newReader.QueryRow(ctx, "SELECT count(*) FROM public."+roleIdentifier(fixture)).Scan(&count); err != nil || count != 1 {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.RotationPreservesData = true
	return evidence, nil
}
