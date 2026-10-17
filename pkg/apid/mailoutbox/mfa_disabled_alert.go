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

type mfaDisabledAlertPayload struct {
	DisabledAt time.Time `json:"disabled_at"`
	Method     string    `json:"method"`
}

func validMFADisabledMethod(method string) bool {
	return method == "password" || method == "recovery_code" || method == "email"
}

// The owner has locked the account and checked the recovery fence. This helper
// only saves intent; its caller commits it with factor removal and any token use.
func (s *Service) saveMFADisabledAlert(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at time.Time, method string) error {
	if !s.mfaDisabledAlertEnabled() {
		return internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil || !validMFADisabledMethod(method) {
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
		Template: MFADisabledAlertTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: at.Add(api.InternalMailMFADisabledAlertTTL), Valid: true}, RecoveryID: recovery, SecurityAlertKeyID: s.mfaDisabledAlert.keyID}
	plain, err := json.Marshal(mfaDisabledAlertPayload{DisabledAt: at.UTC(), Method: method})
	if err != nil {
		return errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.mfaDisabledAlert.writer.Recipient(), mfaDisabledAlertBinding(r), plain, api.InternalMailMFADisabledAlertMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return errVerificationPayloadUnavailable
	}
	return s.queries.CreateMFADisabledAlertIntent(ctx, tx, q.CreateMFADisabledAlertIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func mfaDisabledAlertBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-mfa-disabled:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func mfaDisabledAlertPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (mfaDisabledAlertPayload, error) {
	var payload mfaDisabledAlertPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != mfaDisabledAlertBinding(r) || len(plain) > api.InternalMailMFADisabledAlertMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.DisabledAt.IsZero() || !validMFADisabledMethod(payload.Method) || !payload.DisabledAt.Add(api.InternalMailMFADisabledAlertTTL).Equal(r.ExpiresAt.Time) {
		return mfaDisabledAlertPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) mfaDisabledAlertMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.mfaDisabledAlertEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := mfaDisabledAlertPayloadFor(s.mfaDisabledAlert, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.MFADisabledBodyV1(r.RecipientAddress, payload.Method, payload.DisabledAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "mfa-disabled:" + uuidString(r.ID.Bytes)}, nil
}
