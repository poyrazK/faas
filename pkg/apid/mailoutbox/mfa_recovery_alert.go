package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type mfaRecoveryAlertPayload struct {
	UsedAt    time.Time `json:"used_at"`
	Remaining int       `json:"remaining"`
}

// ConsumeMFARecoveryAlert commits the security event and encrypted notification
// together. A refused last code, replay, failed save or late disabled gate never
// consumes a code. It never opens a mail socket inside this owner transaction.
func (s *Service) ConsumeMFARecoveryAlert(ctx context.Context, accountID, expectedEmail string, presented []byte) (matched, lastCode bool, remaining int, err error) {
	if !s.mfaRecoveryAlertEnabled() {
		return false, false, 0, internalmail.ErrDisabled
	}
	if !validID(accountID) || len(presented) != authcode.HMACSize {
		return false, false, 0, internalmail.ErrInvalid
	}
	address, err := internalmail.NormalizeSuppressionAddress(expectedEmail)
	if err != nil {
		return false, false, 0, internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, 0, err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return false, false, 0, err
	}
	account, err := s.queries.LockMFARecoveryAlertAccount(ctx, tx, pgID(accountID))
	if err != nil {
		return false, false, 0, reconciliationError(err)
	}
	hashes := account.MfaRecoveryCodesHash
	defer func() {
		for _, hash := range hashes {
			clear(hash)
		}
	}()
	if !magicLinkAccountAvailable(account.Status, account.AbuseHoldAt) || !account.MfaEnrolledAt.Valid || !strings.EqualFold(account.Email, address) {
		return false, false, 0, ErrAccountUnavailable
	}
	index, matches := -1, 0
	for i, hash := range hashes {
		if len(hash) != authcode.HMACSize {
			return false, false, 0, internalmail.ErrInvalid
		}
		if subtle.ConstantTimeCompare(hash, presented) == 1 {
			index = i
			matches++
		}
	}
	if matches > 1 {
		return false, false, 0, internalmail.ErrInvalid
	}
	if index < 0 {
		return false, false, 0, nil
	}
	if len(hashes) == 1 {
		return true, true, 1, nil
	}
	if len(hashes) > authcode.RecoveryCodeCount {
		return false, false, 0, internalmail.ErrInvalid
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return false, false, 0, err
	}
	if recovery.Held {
		return false, false, 0, internalmail.ErrRecoveryHeld
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return false, false, 0, err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return false, false, 0, internalmail.ErrQuota
	}
	issued, err := s.queries.VerificationClock(ctx, tx)
	if err != nil {
		return false, false, 0, err
	}
	next := make([][]byte, 0, len(hashes)-1)
	next = append(next, hashes[:index]...)
	next = append(next, hashes[index+1:]...)
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: account.ID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		Template: MFARecoveryAlertTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: issued.Time.Add(api.InternalMailMFARecoveryAlertTTL), Valid: true}, RecoveryID: recovery.RecoveryID, SecurityAlertKeyID: s.mfaRecoveryAlert.keyID}
	plain, err := json.Marshal(mfaRecoveryAlertPayload{UsedAt: issued.Time.UTC(), Remaining: len(next)})
	if err != nil {
		return false, false, 0, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.mfaRecoveryAlert.writer.Recipient(), mfaRecoveryAlertBinding(r), plain, api.InternalMailMFARecoveryAlertMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return false, false, 0, errVerificationPayloadUnavailable
	}
	if err := s.queries.ConsumeMFARecoveryAlertCode(ctx, tx, q.ConsumeMFARecoveryAlertCodeParams{ID: account.ID, RecoveryHashes: next}); err != nil {
		return false, false, 0, err
	}
	if err := s.queries.CreateMFARecoveryAlertIntent(ctx, tx, q.CreateMFARecoveryAlertIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID, FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID}); err != nil {
		return false, false, 0, err
	}
	if !s.mfaRecoveryAlertEnabled() {
		return false, false, 0, internalmail.ErrDisabled
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, 0, err
	}
	return true, false, len(next), nil
}

func mfaRecoveryAlertBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-mfa-recovery:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func mfaRecoveryAlertPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (mfaRecoveryAlertPayload, error) {
	var payload mfaRecoveryAlertPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != mfaRecoveryAlertBinding(r) || len(plain) > api.InternalMailMFARecoveryAlertMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.UsedAt.IsZero() || payload.Remaining < 1 || payload.Remaining >= authcode.RecoveryCodeCount || !payload.UsedAt.Add(api.InternalMailMFARecoveryAlertTTL).Equal(r.ExpiresAt.Time) {
		return mfaRecoveryAlertPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) mfaRecoveryAlertMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.mfaRecoveryAlertEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := mfaRecoveryAlertPayloadFor(s.mfaRecoveryAlert, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.RecoveryCodeBurnedBodyV1(r.RecipientAddress, payload.Remaining, payload.UsedAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "mfa-recovery-alert:" + uuidString(r.ID.Bytes)}, nil
}
