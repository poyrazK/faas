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

type mfaEnabledAlertPayload struct {
	EnabledAt time.Time `json:"enabled_at"`
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the alert with enrollment confirmation.
func (s *Service) saveMFAEnabledAlert(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at time.Time) error {
	if !s.mfaEnabledAlertEnabled() {
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
		Template: MFAEnabledAlertTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: at.Add(api.InternalMailMFAEnabledAlertTTL), Valid: true}, RecoveryID: recovery, SecurityAlertKeyID: s.mfaEnabledAlert.keyID}
	plain, err := json.Marshal(mfaEnabledAlertPayload{EnabledAt: at.UTC()})
	if err != nil {
		return errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.mfaEnabledAlert.writer.Recipient(), mfaEnabledAlertBinding(r), plain, api.InternalMailMFAEnabledAlertMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	return s.queries.CreateMFAEnabledAlertIntent(ctx, tx, q.CreateMFAEnabledAlertIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func mfaEnabledAlertBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-mfa-enabled:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func mfaEnabledAlertPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (mfaEnabledAlertPayload, error) {
	var payload mfaEnabledAlertPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != mfaEnabledAlertBinding(r) || len(plain) > api.InternalMailMFAEnabledAlertMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.EnabledAt.IsZero() || !payload.EnabledAt.Add(api.InternalMailMFAEnabledAlertTTL).Equal(r.ExpiresAt.Time) {
		return mfaEnabledAlertPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) mfaEnabledAlertMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.mfaEnabledAlertEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := mfaEnabledAlertPayloadFor(s.mfaEnabledAlert, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.MFAEnabledBodyV1(r.RecipientAddress, payload.EnabledAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "mfa-enabled:" + uuidString(r.ID.Bytes)}, nil
}
