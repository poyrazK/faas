package mailoutbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

var errVerificationTokenUnavailable = errors.New("verification token unavailable")
var errVerificationPayloadUnavailable = errors.New("verification payload unavailable")

const verificationPathV1 = "/v1/auth/verify-email"

// QueueVerification commits the address-bound token hash and encrypted intent
// together. A new user request intentionally mints a new link; worker retries
// use only that saved token, template, canonical origin and absolute expiry.
func (s *Service) QueueVerification(ctx context.Context, accountID, expectedEmail string) error {
	if !s.verificationEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(accountID) {
		return internalmail.ErrInvalid
	}
	address, err := internalmail.NormalizeSuppressionAddress(expectedEmail)
	if err != nil {
		return internalmail.ErrInvalid
	}
	raw := make([]byte, api.EmailVerificationTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return errors.New("verification token generation failed")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockVerificationAccount(ctx, tx, pgID(accountID))
	if err != nil {
		return reconciliationError(err)
	}
	if account.Status != "active" || !strings.EqualFold(account.Email, address) {
		return ErrAccountUnavailable
	}
	if account.EmailVerifiedAt.Valid {
		return nil
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return internalmail.ErrQuota
	}
	issued, err := s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return err
	}
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: account.ID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		Template: VerificationTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: issued.Time.Add(api.EmailVerificationTTL), Valid: true}, RecoveryID: recovery.RecoveryID,
		VerificationTokenHash: api.HashToken(raw), VerificationKeyID: s.verification.keyID, VerificationOrigin: s.verification.origin}
	sealed, err := secretbox.SealBytes(s.verification.writer.Recipient(), verificationBinding(r), raw, api.EmailVerificationTokenBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	if err := s.queries.CreateVerificationToken(ctx, tx, q.CreateVerificationTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID, ExpiresAt: r.ExpiresAt, EmailAddress: address}); err != nil {
		return err
	}
	if err := s.queries.CreateVerificationIntent(ctx, tx, q.CreateVerificationIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, VerificationTokenHash: r.VerificationTokenHash, SealedVerification: sealed, VerificationKeyID: r.VerificationKeyID, VerificationOrigin: r.VerificationOrigin}); err != nil {
		return err
	}
	if !s.verificationEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

// The authenticated seal includes every immutable renderer/routing input. A
// database copy to another intent/account or a changed deadline fails closed.
func verificationBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.VerificationKeyID, r.VerificationOrigin, hex.EncodeToString(r.VerificationTokenHash)}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-verification:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func (s *Service) verificationMessage(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.verificationEnabled() || s.verification.origin != r.VerificationOrigin {
		return mail.Message{}, internalmail.ErrDisabled
	}
	token, err := s.queries.ShareVerificationToken(ctx, tx, q.ShareVerificationTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	if err != nil {
		return mail.Message{}, err
	}
	if token.ConsumedAt.Valid || !token.ExpiresAt.Time.After(time.Now()) || !token.ExpiresAt.Time.Equal(r.ExpiresAt.Time) || token.EmailAddress != r.RecipientAddress {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	raw, err := credentialPayload(s.verification, r)
	if err != nil {
		return mail.Message{}, err
	}
	defer clear(raw)
	link := r.VerificationOrigin + verificationPathV1 + "?token=" + base64.RawURLEncoding.EncodeToString(raw)
	subject, body := mail.EmailVerificationBodyV1(r.RecipientAddress, link, r.ExpiresAt.Time)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "verify-email:" + uuidString(r.ID.Bytes)}, nil
}

func credentialPayload(config *credentialConfig, r q.PlatformMailProducerOutbox) ([]byte, error) {
	reader := config.readers[r.VerificationKeyID]
	if reader == nil || len(r.SealedVerification) > api.InternalMailVerificationCipherMaxBytes {
		return nil, errVerificationPayloadUnavailable
	}
	binding, raw, err := secretbox.OpenBytes(reader, r.SealedVerification)
	if err != nil || binding != verificationBinding(r) || len(raw) != api.EmailVerificationTokenBytes || subtle.ConstantTimeCompare(api.HashToken(raw), r.VerificationTokenHash) != 1 {
		clear(raw)
		return nil, errVerificationPayloadUnavailable
	}
	return raw, nil
}
