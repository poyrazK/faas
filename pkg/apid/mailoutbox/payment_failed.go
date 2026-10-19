package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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

type paymentFailedPayload struct {
	PastDueAt time.Time `json:"past_due_at"`
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the intent with the first overdue transition.
func (s *Service) savePaymentFailed(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at time.Time) error {
	if !s.paymentFailedEnabled() {
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
		Template: PaymentFailedTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: at.Add(api.InternalMailPaymentFailedTTL), Valid: true}, RecoveryID: recovery, PaymentPastDueAt: pgtype.Timestamptz{Time: at, Valid: true}, SecurityAlertKeyID: s.paymentFailed.keyID}
	plain, err := json.Marshal(paymentFailedPayload{PastDueAt: at.UTC()})
	if err != nil {
		return errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.paymentFailed.writer.Recipient(), paymentFailedBinding(r), plain, api.InternalMailPaymentFailedMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	return s.queries.CreatePaymentFailedIntent(ctx, tx, q.CreatePaymentFailedIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID, PaymentPastDueAt: r.PaymentPastDueAt})
}

func paymentFailedBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.PaymentPastDueAt.Time.UTC().Format(time.RFC3339Nano), r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-payment-failed:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func paymentFailedPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (paymentFailedPayload, error) {
	var payload paymentFailedPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != paymentFailedBinding(r) || len(plain) > api.InternalMailPaymentFailedMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.PastDueAt.IsZero() || !payload.PastDueAt.Equal(r.PaymentPastDueAt.Time) || !payload.PastDueAt.Add(api.InternalMailPaymentFailedTTL).Equal(r.ExpiresAt.Time) {
		return paymentFailedPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) paymentFailedMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.paymentFailedEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := paymentFailedPayloadFor(s.paymentFailed, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.PaymentFailedBodyV1(r.RecipientAddress, payload.PastDueAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "payment-failed:" + uuidString(r.ID.Bytes)}, nil
}
