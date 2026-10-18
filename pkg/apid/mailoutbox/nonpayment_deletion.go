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

type nonpaymentDeletionPayload struct {
	DeletionRequestedAt time.Time `json:"deletion_requested_at"`
	PastDueAt           time.Time `json:"past_due_at"`
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the intent with the non-payment deletion transition.
func (s *Service) saveNonpaymentDeletion(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, at, pastDueAt time.Time, revision int64) (q.PlatformMailProducerOutbox, error) {
	if !s.nonpaymentDeletionEnabled() {
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
		Template: NonpaymentDeletionTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: nonpaymentDeletionDeadline(at), Valid: true}, RecoveryID: recovery, NonpaymentDeletionRevision: pgtype.Int8{Int64: revision, Valid: true}, NonpaymentPastDueAt: pgtype.Timestamptz{Time: pastDueAt, Valid: true}, NonpaymentDeletionAt: pgtype.Timestamptz{Time: at, Valid: true}, SecurityAlertKeyID: s.nonpaymentDeletion.keyID}
	plain, err := json.Marshal(nonpaymentDeletionPayload{DeletionRequestedAt: at.UTC(), PastDueAt: pastDueAt.UTC()})
	if err != nil {
		return q.PlatformMailProducerOutbox{}, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.nonpaymentDeletion.writer.Recipient(), nonpaymentDeletionBinding(r), plain, api.InternalMailNonpaymentDeletionMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return q.PlatformMailProducerOutbox{}, errVerificationPayloadUnavailable
	}
	err = s.queries.CreateNonpaymentDeletionIntent(ctx, tx, q.CreateNonpaymentDeletionIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID, NonpaymentDeletionRevision: r.NonpaymentDeletionRevision, NonpaymentPastDueAt: r.NonpaymentPastDueAt, NonpaymentDeletionAt: r.NonpaymentDeletionAt})
	return r, err
}

func nonpaymentDeletionBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		strconv.FormatInt(r.NonpaymentDeletionRevision.Int64, 10), r.NonpaymentPastDueAt.Time.UTC().Format(time.RFC3339Nano), r.NonpaymentDeletionAt.Time.UTC().Format(time.RFC3339Nano), r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-nonpayment-deletion:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func nonpaymentDeletionPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (nonpaymentDeletionPayload, error) {
	var payload nonpaymentDeletionPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != nonpaymentDeletionBinding(r) || len(plain) > api.InternalMailNonpaymentDeletionMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.DeletionRequestedAt.IsZero() || !r.NonpaymentDeletionAt.Valid || !payload.DeletionRequestedAt.Equal(r.NonpaymentDeletionAt.Time) || !r.NonpaymentDeletionRevision.Valid || r.NonpaymentDeletionRevision.Int64 < 1 || !r.NonpaymentPastDueAt.Valid || !payload.PastDueAt.Equal(r.NonpaymentPastDueAt.Time) || !nonpaymentDeletionDeadline(payload.DeletionRequestedAt).Equal(r.ExpiresAt.Time) {
		return nonpaymentDeletionPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) nonpaymentDeletionMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.nonpaymentDeletionEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := nonpaymentDeletionPayloadFor(s.nonpaymentDeletion, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.NonpaymentDeletionBodyV1(r.RecipientAddress, payload.PastDueAt, payload.DeletionRequestedAt.Add(api.AccountDeletionGraceDays*24*time.Hour))
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "nonpayment-deletion:" + uuidString(r.ID.Bytes)}, nil
}

func nonpaymentDeletionDeadline(at time.Time) time.Time {
	deadline := at.Add(api.InternalMailNonpaymentDeletionTTL)
	if deletion := at.Add(api.AccountDeletionGraceDays * 24 * time.Hour); deletion.Before(deadline) {
		return deletion
	}
	return deadline
}
