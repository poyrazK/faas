package mailoutbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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

const passwordResetPathV1 = "/auth/reset"

// QueuePasswordReset atomically saves a purpose-specific hash and encrypted
// mail intent, bound to the current address and password revision. Retrying
// this intent never mints another token or extends its original 15-minute TTL.
func (s *Service) QueuePasswordReset(ctx context.Context, accountID, expectedEmail string) error {
	if !s.passwordResetEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(accountID) {
		return internalmail.ErrInvalid
	}
	address, err := internalmail.NormalizeSuppressionAddress(expectedEmail)
	if err != nil {
		return internalmail.ErrInvalid
	}
	raw := make([]byte, api.PasswordResetTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return errors.New("password-reset token generation failed")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockPasswordResetAccount(ctx, tx, pgID(accountID))
	if err != nil {
		return reconciliationError(err)
	}
	if account.Status != "active" || !account.EmailVerifiedAt.Valid || !strings.EqualFold(account.Email, address) {
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
		Template: PasswordResetTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: issued.Time.Add(api.PasswordResetTTL), Valid: true}, RecoveryID: recovery.RecoveryID,
		VerificationTokenHash: api.HashToken(raw), VerificationKeyID: s.passwordReset.keyID, VerificationOrigin: s.passwordReset.origin}
	sealed, err := secretbox.SealBytes(s.passwordReset.writer.Recipient(), verificationBinding(r), raw, api.PasswordResetTokenBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	if err := s.queries.CreatePasswordResetToken(ctx, tx, q.CreatePasswordResetTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID, EmailAddress: address, PasswordRevision: account.PasswordResetRevision, ExpiresAt: r.ExpiresAt, Namespace: s.namespace, RecoveryID: r.RecoveryID}); err != nil {
		return err
	}
	if err := s.queries.CreatePasswordResetIntent(ctx, tx, q.CreatePasswordResetIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, VerificationTokenHash: r.VerificationTokenHash, SealedVerification: sealed, VerificationKeyID: r.VerificationKeyID, VerificationOrigin: r.VerificationOrigin}); err != nil {
		return err
	}
	if !s.passwordResetEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) passwordResetMessage(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox, revision int64) (mail.Message, error) {
	if !s.passwordResetEnabled() || s.passwordReset.origin != r.VerificationOrigin {
		return mail.Message{}, internalmail.ErrDisabled
	}
	token, err := s.queries.SharePasswordResetToken(ctx, tx, q.SharePasswordResetTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	if err != nil {
		return mail.Message{}, err
	}
	if token.ConsumedAt.Valid || !token.ExpiresAt.Time.After(time.Now()) || !token.ExpiresAt.Time.Equal(r.ExpiresAt.Time) || token.EmailAddress != r.RecipientAddress || token.PasswordRevision != revision || token.Namespace != r.Namespace || token.RecoveryID != r.RecoveryID {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	raw, err := credentialPayload(s.passwordReset, r)
	if err != nil {
		return mail.Message{}, err
	}
	defer clear(raw)
	link := r.VerificationOrigin + passwordResetPathV1 + "?token=" + base64.RawURLEncoding.EncodeToString(raw)
	subject, body := mail.PasswordResetBodyV1(link)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "password-reset:" + uuidString(r.ID.Bytes)}, nil
}
