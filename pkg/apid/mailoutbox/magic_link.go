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

const magicLinkPathV1 = "/auth/verify"

// QueueMagicLink atomically saves a purpose-specific hash and encrypted
// mail intent, bound to address, verification state, password revision and
// recovery generation. Retrying never extends its original 15-minute TTL.
func (s *Service) QueueMagicLink(ctx context.Context, accountID, expectedEmail string) error {
	if !s.magicLinkEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(accountID) {
		return internalmail.ErrInvalid
	}
	address, err := internalmail.NormalizeSuppressionAddress(expectedEmail)
	if err != nil {
		return internalmail.ErrInvalid
	}
	raw := make([]byte, api.MagicLinkTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		return errors.New("magic-link token generation failed")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockMagicLinkAccount(ctx, tx, pgID(accountID))
	if err != nil {
		return reconciliationError(err)
	}
	if !magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !strings.EqualFold(account.Email, address) {
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
		Template: MagicLinkTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: issued.Time.Add(api.MagicLinkTTL), Valid: true}, RecoveryID: recovery.RecoveryID,
		VerificationTokenHash: api.HashToken(raw), VerificationKeyID: s.magicLink.keyID, VerificationOrigin: s.magicLink.origin}
	sealed, err := secretbox.SealBytes(s.magicLink.writer.Recipient(), verificationBinding(r), raw, api.MagicLinkTokenBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	if err := s.queries.CreateMagicLinkToken(ctx, tx, q.CreateMagicLinkTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID, EmailAddress: address, PasswordRevision: account.PasswordResetRevision, EmailVerifiedAt: account.EmailVerifiedAt, ExpiresAt: r.ExpiresAt, Namespace: s.namespace, RecoveryID: r.RecoveryID}); err != nil {
		return err
	}
	if err := s.queries.CreateMagicLinkIntent(ctx, tx, q.CreateMagicLinkIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, VerificationTokenHash: r.VerificationTokenHash, SealedVerification: sealed, VerificationKeyID: r.VerificationKeyID, VerificationOrigin: r.VerificationOrigin}); err != nil {
		return err
	}
	if !s.magicLinkEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) magicLinkMessage(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox, account q.ShareAccountRow) (mail.Message, error) {
	if !s.magicLinkEnabled() || s.magicLink.origin != r.VerificationOrigin {
		return mail.Message{}, internalmail.ErrDisabled
	}
	token, err := s.queries.ShareMagicLinkToken(ctx, tx, q.ShareMagicLinkTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID})
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
	if !magicLinkAvailable(account.Email, account.Status, account.EmailVerifiedAt, account.AbuseHoldAt, account.PasswordResetRevision, token, now.Time) || !token.ExpiresAt.Time.Equal(r.ExpiresAt.Time) || token.EmailAddress != r.RecipientAddress || token.Namespace != r.Namespace || token.RecoveryID != r.RecoveryID {
		return mail.Message{}, errVerificationTokenUnavailable
	}
	raw, err := credentialPayload(s.magicLink, r)
	if err != nil {
		return mail.Message{}, err
	}
	defer clear(raw)
	link := r.VerificationOrigin + magicLinkPathV1 + "?token=" + base64.RawURLEncoding.EncodeToString(raw)
	subject, body := mail.MagicLinkBodyV1(link)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "magic-link:" + uuidString(r.ID.Bytes)}, nil
}
