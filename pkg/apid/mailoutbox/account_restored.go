package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
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

type accountRestoredPayload struct {
	RestoredAt time.Time `json:"restored_at"`
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the intent with the payment recovery.
func (s *Service) saveAccountRestored(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at time.Time, revision int64) error {
	if !s.accountRestoredEnabled() {
		return internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil {
		return internalmail.ErrInvalid
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return internalmail.ErrQuota
	}
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: id, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		Template: AccountRestoredTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: at.Add(api.InternalMailAccountRestoredTTL), Valid: true}, RecoveryID: recovery, AccountRestoredRevision: pgtype.Int8{Int64: revision, Valid: true}, SecurityAlertKeyID: s.accountRestored.keyID}
	plain, err := json.Marshal(accountRestoredPayload{RestoredAt: at.UTC()})
	if err != nil {
		return errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.accountRestored.writer.Recipient(), accountRestoredBinding(r), plain, api.InternalMailAccountRestoredMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	return s.queries.CreateAccountRestoredIntent(ctx, tx, q.CreateAccountRestoredIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID, AccountRestoredRevision: r.AccountRestoredRevision})
}

func accountRestoredBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		strconv.FormatInt(r.AccountRestoredRevision.Int64, 10), r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-account-restored:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func accountRestoredPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (accountRestoredPayload, error) {
	var payload accountRestoredPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != accountRestoredBinding(r) || len(plain) > api.InternalMailAccountRestoredMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.RestoredAt.IsZero() || !r.AccountRestoredRevision.Valid || r.AccountRestoredRevision.Int64 < 1 || !payload.RestoredAt.Add(api.InternalMailAccountRestoredTTL).Equal(r.ExpiresAt.Time) {
		return accountRestoredPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) accountRestoredMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.accountRestoredEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := accountRestoredPayloadFor(s.accountRestored, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.AccountRestoredBodyV1(r.RecipientAddress, payload.RestoredAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "account-restored:" + uuidString(r.ID.Bytes)}, nil
}
