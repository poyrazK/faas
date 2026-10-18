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

type subscriptionEndedPayload struct {
	FromPlan api.Plan  `json:"from_plan"`
	EndedAt  time.Time `json:"ended_at"`
}

// The owner locks the account and recovery fence before saving the event.
// Its caller commits the intent with the subscription termination.
func (s *Service) saveSubscriptionEnded(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, fromPlan api.Plan, at time.Time, revision int64) (pgtype.UUID, error) {
	if !s.subscriptionEndedEnabled() {
		return pgtype.UUID{}, internalmail.ErrDisabled
	}
	address, err := internalmail.NormalizeSuppressionAddress(email)
	if err != nil {
		return pgtype.UUID{}, internalmail.ErrInvalid
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return pgtype.UUID{}, internalmail.ErrQuota
	}
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: id, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, RecipientAddress: address,
		Template: SubscriptionEndedTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: at.Add(api.InternalMailSubscriptionEndedTTL), Valid: true}, RecoveryID: recovery, SubscriptionEndedRevision: pgtype.Int8{Int64: revision, Valid: true}, SecurityAlertKeyID: s.subscriptionEnded.keyID}
	plain, err := json.Marshal(subscriptionEndedPayload{FromPlan: fromPlan, EndedAt: at.UTC()})
	if err != nil {
		return pgtype.UUID{}, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.subscriptionEnded.writer.Recipient(), subscriptionEndedBinding(r), plain, api.InternalMailSubscriptionEndedMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return pgtype.UUID{}, errVerificationPayloadUnavailable
	}
	err = s.queries.CreateSubscriptionEndedIntent(ctx, tx, q.CreateSubscriptionEndedIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID, SubscriptionEndedRevision: r.SubscriptionEndedRevision})
	return r.ID, err
}

func subscriptionEndedBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		strconv.FormatInt(r.SubscriptionEndedRevision.Int64, 10), r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-subscription-ended:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func subscriptionEndedPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (subscriptionEndedPayload, error) {
	var payload subscriptionEndedPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != subscriptionEndedBinding(r) || len(plain) > api.InternalMailSubscriptionEndedMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.EndedAt.IsZero() || !payload.FromPlan.Valid() || payload.FromPlan == api.PlanFree || !r.SubscriptionEndedRevision.Valid || r.SubscriptionEndedRevision.Int64 < 1 || !payload.EndedAt.Add(api.InternalMailSubscriptionEndedTTL).Equal(r.ExpiresAt.Time) {
		return subscriptionEndedPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) subscriptionEndedMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.subscriptionEndedEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := subscriptionEndedPayloadFor(s.subscriptionEnded, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.SubscriptionEndedBodyV1(r.RecipientAddress, string(payload.FromPlan), payload.EndedAt)
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "subscription-ended:" + uuidString(r.ID.Bytes)}, nil
}
