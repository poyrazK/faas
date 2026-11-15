package secretpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg/sqlc"
)

func (s *Store) PostgresCutoverCredential(ctx context.Context, account, cutover, credential, lease string) (secretd.PostgresCredential, error) {
	a, e1 := pgUUID(account)
	c, e2 := pgUUID(cutover)
	m, e3 := pgUUID(credential)
	if e1 != nil || e2 != nil || e3 != nil || lease == "" {
		return secretd.PostgresCredential{}, secretd.ErrUnknownPostgresCredential
	}
	r, err := s.q.PostgresCutoverVerification(ctx, s.db, sqlc.PostgresCutoverVerificationParams{AccountID: a, CutoverID: c, CredentialID: m, LeaseToken: lease})
	if errors.Is(err, pgx.ErrNoRows) {
		return secretd.PostgresCredential{}, secretd.ErrUnknownPostgresCredential
	}
	if err != nil {
		return secretd.PostgresCredential{}, err
	}
	return secretd.PostgresCredential{AccountID: account, AppID: uuid.UUID(r.AppID.Bytes).String(), Key: r.EnvironmentKey, Access: r.Access, Major: int(r.PostgresMajor),
		ProviderIdentity: r.ProviderIdentityID.String, Ref: r.CredentialRef.String, Ciphertext: r.Ciphertext, Kid: r.Kid.String, ValueHash: r.ValueHash.String}, nil
}

func (s *Store) RecordPostgresVerification(ctx context.Context, account, app, cutover, credential string) error {
	a, e1 := pgUUID(account)
	p, e2 := pgUUID(app)
	c, e3 := pgUUID(cutover)
	m, e4 := pgUUID(credential)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return secretd.ErrUnknownPostgresCredential
	}
	return s.q.RecordPostgresVerification(ctx, s.db, sqlc.RecordPostgresVerificationParams{AccountID: a, AppID: p, CutoverID: c, CredentialID: m})
}
