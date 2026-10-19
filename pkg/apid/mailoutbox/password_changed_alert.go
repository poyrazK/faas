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

type passwordChangedAlertPayload struct {
	ChangedAt time.Time `json:"changed_at"`
	Method    string    `json:"method"`
}

func validPasswordChangedMethod(method string) bool {
	return method == "dashboard" || method == "password_reset"
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the alert with the password update and any token use.
func (s *Service) savePasswordChangedAlert(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at time.Time, method string) error {
	if !s.passwordChangedAlertEnabled() {
		return internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil || !validPasswordChangedMethod(method) {
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
		Template: PasswordChangedAlertTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: at.Add(api.InternalMailPasswordChangedAlertTTL), Valid: true}, RecoveryID: recovery, SecurityAlertKeyID: s.passwordChangedAlert.keyID}
	plain, err := json.Marshal(passwordChangedAlertPayload{ChangedAt: at.UTC(), Method: method})
	if err != nil {
		return errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.passwordChangedAlert.writer.Recipient(), passwordChangedAlertBinding(r), plain, api.InternalMailPasswordChangedAlertMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	return s.queries.CreatePasswordChangedAlertIntent(ctx, tx, q.CreatePasswordChangedAlertIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func passwordChangedAlertBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-password-changed:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func passwordChangedAlertPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (passwordChangedAlertPayload, error) {
	var payload passwordChangedAlertPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != passwordChangedAlertBinding(r) || len(plain) > api.InternalMailPasswordChangedAlertMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.ChangedAt.IsZero() || !validPasswordChangedMethod(payload.Method) || !payload.ChangedAt.Add(api.InternalMailPasswordChangedAlertTTL).Equal(r.ExpiresAt.Time) {
		return passwordChangedAlertPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) passwordChangedAlertMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.passwordChangedAlertEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := passwordChangedAlertPayloadFor(s.passwordChangedAlert, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.PasswordChangedBodyV1(r.RecipientAddress, payload.Method, payload.ChangedAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "password-changed:" + uuidString(r.ID.Bytes)}, nil
}
