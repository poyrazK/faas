// adr:20261019072523 — control-plane credential receipts for enforced candidates.
package apidserver

import (
	"context"
	"errors"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// verifyCandidatePostgresCredentials records a receipt for every unverified
// managed PostgreSQL delivery of the graph's enforced candidates. Candidates
// cannot reach their database, so apid checks the exact sealed credential.
// A failed check leaves activation blocked by the missing receipt.
func (b *environmentGitOpsBackend) verifyCandidatePostgresCredentials(ctx context.Context, graphID string) error {
	store, ok := b.intent.(state.EnvironmentQualificationPostgresStore)
	if !ok || graphID == "" {
		return nil
	}
	checks, err := store.PendingEnvironmentQualificationPostgresChecks(ctx, graphID)
	if err != nil {
		return err
	}
	verify := b.server.candidatePostgresVerifier
	if verify == nil {
		verify = verifyCandidatePostgresCredential
	}
	for _, check := range checks {
		if err := verify(ctx, check); err != nil {
			// Verifier errors are sentinel values; they never carry connection strings.
			slog.WarnContext(ctx, "candidate PostgreSQL credential was not verified",
				"request_id", check.RequestID, "binding_id", check.BindingID, "error", err)
			continue
		}
		if err := store.RecordEnvironmentQualificationPostgresReceipt(ctx, check); err != nil && !errors.Is(err, state.ErrBindingPromotionChanged) {
			return err
		}
	}
	return nil
}

// verifyCandidatePostgresCredential decrypts the exact sealed delivery with
// the host identities and runs the read-only ACL probe for its access.
func verifyCandidatePostgresCredential(ctx context.Context, c state.EnvironmentQualificationPostgresCheck) error {
	sink := &appSecretCredentialSink{hmacKey: managedPostgresHMACKey, identities: managedPostgresCredentialIdentities, probe: managedpostgres.VerifyCredentialSQL}
	return verifyCandidatePostgresCredentialWith(ctx, sink, c)
}

func verifyCandidatePostgresCredentialWith(ctx context.Context, sink *appSecretCredentialSink, c state.EnvironmentQualificationPostgresCheck) error {
	return sink.VerifyCredential(ctx, candidatePostgresBinding(c),
		managedpostgres.SealedCredential{Ref: c.CredentialRef, Ciphertext: c.Ciphertext, Kid: c.Kid, ValueHash: c.ValueHash},
		managedpostgres.Database{ID: c.DatabaseID, AccountID: c.AccountID, Spec: managedpostgres.Spec{PostgresMajor: c.PostgresMajor}})
}

func candidatePostgresBinding(c state.EnvironmentQualificationPostgresCheck) managedpostgres.Binding {
	return managedpostgres.Binding{ID: c.BindingID, AccountID: c.AccountID, DatabaseID: c.DatabaseID, AppID: c.AppID, Scope: c.Scope,
		EnvironmentKey: c.EnvironmentKey, Access: managedpostgres.CredentialAccess(c.Access), CredentialGeneration: c.CredentialGeneration}
}
