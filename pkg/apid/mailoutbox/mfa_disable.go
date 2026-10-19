package mailoutbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const mfaDisablePathV1 = "/auth/reset/mfa"

// QueueMFADisable saves one purpose-specific credential and its encrypted mail
// intent together. A new user request supersedes older requests; worker retries
// preserve the original token, event time, cooldown and absolute deadlines.
func (s *Service) QueueMFADisable(ctx context.Context, accountID, expectedEmail string) error {
	if !s.mfaDisableEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(accountID) {
		return internalmail.ErrInvalid
	}
	address, err := internalmail.NormalizeSuppressionAddress(expectedEmail)
	if err != nil {
		return internalmail.ErrInvalid
	}
	raw := make([]byte, api.MFADisableTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return errors.New("MFA disable token generation failed")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockMFADisableAccount(ctx, tx, pgID(accountID))
	if err != nil {
		return reconciliationError(err)
	}
	if !magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !account.MfaEnrolledAt.Valid || !strings.EqualFold(account.Email, address) {
		return ErrAccountUnavailable
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
		Template: MFADisableTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: issued.Time.Add(api.InternalMailMFADisableTTL), Valid: true}, RecoveryID: recovery.RecoveryID,
		VerificationTokenHash: api.HashToken(raw), VerificationKeyID: s.mfaDisable.keyID, VerificationOrigin: s.mfaDisable.origin}
	sealed, err := secretbox.SealBytes(s.mfaDisable.writer.Recipient(), verificationBinding(r), raw, api.MFADisableTokenBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	if err := s.queries.InvalidateMFADisableTokens(ctx, tx, account.ID); err != nil {
		return err
	}
	if err := s.queries.InvalidateLegacyMFADisableRequests(ctx, tx, account.ID); err != nil {
		return err
	}
	ready := issued.Time.Add(api.MFADisableEmailCooldown)
	if err := s.queries.CreateMFADisableToken(ctx, tx, q.CreateMFADisableTokenParams{TokenHash: r.VerificationTokenHash, AccountID: account.ID, EmailAddress: address,
		PasswordRevision: account.PasswordResetRevision, MfaRevision: account.MfaRevision, MfaEnrolledAt: account.MfaEnrolledAt,
		Namespace: s.namespace, RecoveryID: r.RecoveryID, RequestedAt: issued, ReadyAt: pgtype.Timestamptz{Time: ready, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: ready.Add(api.MFADisableEmailConfirmWindow), Valid: true}}); err != nil {
		return err
	}
	if err := s.queries.CreateMFADisableIntent(ctx, tx, q.CreateMFADisableIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: r.RecipientAddress, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID,
		VerificationTokenHash: r.VerificationTokenHash, SealedVerification: sealed, VerificationKeyID: r.VerificationKeyID, VerificationOrigin: r.VerificationOrigin}); err != nil {
		return err
	}
	if !s.mfaDisableEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) mfaDisableMessage(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox, account q.ShareAccountRow) (mail.Message, error) {
	if !s.mfaDisableEnabled() || s.mfaDisable.origin != r.VerificationOrigin {
		return mail.Message{}, internalmail.ErrDisabled
	}
	token, err := s.queries.ShareMFADisableToken(ctx, tx, q.ShareMFADisableTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	if err != nil {
		return mail.Message{}, err
	}
	now, err := s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return mail.Message{}, err
	}
	if !mfaDisableAvailable(account.Email, account.Status, account.AbuseHoldAt, account.PasswordResetRevision, account.MfaRevision, account.MfaEnrolledAt, token, now.Time) ||
		!token.RequestedAt.Time.Add(api.InternalMailMFADisableTTL).Equal(r.ExpiresAt.Time) || token.EmailAddress != r.RecipientAddress || token.Namespace != r.Namespace || token.RecoveryID != r.RecoveryID {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	raw, err := credentialPayload(s.mfaDisable, r)
	if err != nil {
		return mail.Message{}, err
	}
	defer clear(raw)
	link := r.VerificationOrigin + mfaDisablePathV1 + "?token=" + base64.RawURLEncoding.EncodeToString(raw)
	subject, body := mail.MFADisableEmailRequestedBodyV1(r.RecipientAddress, link, token.RequestedAt.Time)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "mfa-disable:" + uuidString(r.ID.Bytes)}, nil
}
