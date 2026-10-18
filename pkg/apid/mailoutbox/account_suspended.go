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

type accountSuspendedPayload struct {
	SuspendedAt time.Time `json:"suspended_at"`
	PastDueAt   time.Time `json:"past_due_at"`
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the intent with the overdue-account suspension.
func (s *Service) saveAccountSuspended(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at, pastDueAt time.Time, revision int64) (q.PlatformMailProducerOutbox, error) {
	if !s.accountSuspendedEnabled() {
		return q.PlatformMailProducerOutbox{}, internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil {
		return q.PlatformMailProducerOutbox{}, internalmail.ErrInvalid
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return q.PlatformMailProducerOutbox{}, internalmail.ErrQuota
	}
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: id, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		Template: AccountSuspendedTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: suspensionDeadline(at, pastDueAt), Valid: true}, RecoveryID: recovery, AccountSuspendedRevision: pgtype.Int8{Int64: revision, Valid: true}, SuspensionPastDueAt: pgtype.Timestamptz{Time: pastDueAt, Valid: true}, SecurityAlertKeyID: s.accountSuspended.keyID}
	plain, err := json.Marshal(accountSuspendedPayload{SuspendedAt: at.UTC(), PastDueAt: pastDueAt.UTC()})
	if err != nil {
		return q.PlatformMailProducerOutbox{}, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.accountSuspended.writer.Recipient(), accountSuspendedBinding(r), plain, api.InternalMailAccountSuspendedMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return q.PlatformMailProducerOutbox{}, errVerificationPayloadUnavailable
	}
	err = s.queries.CreateAccountSuspendedIntent(ctx, tx, q.CreateAccountSuspendedIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID, AccountSuspendedRevision: r.AccountSuspendedRevision, SuspensionPastDueAt: r.SuspensionPastDueAt})
	return r, err
}

func accountSuspendedBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		strconv.FormatInt(r.AccountSuspendedRevision.Int64, 10), r.SuspensionPastDueAt.Time.UTC().Format(time.RFC3339Nano), r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-account-suspended:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func accountSuspendedPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (accountSuspendedPayload, error) {
	var payload accountSuspendedPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != accountSuspendedBinding(r) || len(plain) > api.InternalMailAccountSuspendedMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.SuspendedAt.IsZero() || !r.AccountSuspendedRevision.Valid || r.AccountSuspendedRevision.Int64 < 1 || !r.SuspensionPastDueAt.Valid || !payload.PastDueAt.Equal(r.SuspensionPastDueAt.Time) || !suspensionDeadline(payload.SuspendedAt, payload.PastDueAt).Equal(r.ExpiresAt.Time) {
		return accountSuspendedPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) accountSuspendedMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.accountSuspendedEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := accountSuspendedPayloadFor(s.accountSuspended, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.AccountSuspendedBodyV1(r.RecipientAddress, payload.SuspendedAt, payload.PastDueAt.Add(api.InternalMailDunningDeleteAfter))
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "account-suspended:" + uuidString(r.ID.Bytes)}, nil
}

func suspensionDeadline(at, pastDueAt time.Time) time.Time {
	deadline := at.Add(api.InternalMailAccountSuspendedTTL)
	if deletion := pastDueAt.Add(api.InternalMailDunningDeleteAfter); deletion.Before(deadline) {
		return deletion
	}
	return deadline
}
