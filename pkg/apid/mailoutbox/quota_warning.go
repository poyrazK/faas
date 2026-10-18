package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
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

type quotaWarningPayload struct {
	Day            time.Time `json:"day"`
	ObservedAt     time.Time `json:"observed_at"`
	Plan           api.Plan  `json:"plan"`
	UsedMBSeconds  int64     `json:"used_mb_seconds"`
	QuotaGB        int       `json:"quota_gb"`
	UnsubscribeURL string    `json:"unsubscribe_url,omitempty"`
}

func quotaWarningAccountAvailable(status string) bool {
	return status == "active" || status == "past_due" || status == "suspended"
}

func quotaWarningPaidPlan(plan api.Plan) bool {
	return plan == api.PlanHobby || plan == api.PlanPro || plan == api.PlanScale
}

func quotaWarningUnsubscribeURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if len(raw) > api.InternalMailQuotaWarningUnsubscribeMaxBytes || strings.ContainsAny(raw, "\r\n\x00<>") ||
		err != nil || u.User != nil || u.Fragment != "" || mail.ValidateUnsubscribeURL(raw) != nil {
		return "", errors.New("quota warning unsubscribe configuration unavailable")
	}
	return raw, nil
}

func (s *Service) saveQuotaWarning(ctx context.Context, tx pgx.Tx, id pgtype.UUID, email string, recovery pgtype.UUID, payload quotaWarningPayload, revision int64) (q.PlatformMailProducerOutbox, error) {
	if !s.quotaWarningEnabled() {
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
		Template: QuotaWarningTemplate, TemplateVersion: 1, ExpiresAt: pgtype.Timestamptz{Time: quotaWarningDeadline(payload.Day, payload.ObservedAt), Valid: true}, RecoveryID: recovery,
		QuotaWarningRevision: pgtype.Int8{Int64: revision, Valid: true}, QuotaWarningDay: pgtype.Timestamptz{Time: payload.Day, Valid: true}, QuotaWarningPlan: pgtype.Text{String: string(payload.Plan), Valid: true}, SecurityAlertKeyID: s.quotaWarning.keyID}
	plain, err := json.Marshal(payload)
	if err != nil {
		return r, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.quotaWarning.writer.Recipient(), quotaWarningBinding(r), plain, api.InternalMailQuotaWarningMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return r, errVerificationPayloadUnavailable
	}
	err = s.queries.CreateQuotaWarningIntent(ctx, tx, q.CreateQuotaWarningIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace, ProducerID: r.ProducerID,
		FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID, SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID,
		QuotaWarningRevision: r.QuotaWarningRevision, QuotaWarningDay: r.QuotaWarningDay, QuotaWarningPlan: r.QuotaWarningPlan})
	return r, err
}

func quotaWarningBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		strconv.FormatInt(r.QuotaWarningRevision.Int64, 10), r.QuotaWarningDay.Time.UTC().Format(time.RFC3339Nano), r.QuotaWarningPlan.String, r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-quota-warning:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func quotaWarningPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (quotaWarningPayload, error) {
	var payload quotaWarningPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != quotaWarningBinding(r) || len(plain) > api.InternalMailQuotaWarningMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(plain))
	d.DisallowUnknownFields()
	if d.Decode(&payload) != nil || d.Decode(new(any)) != io.EOF || !r.QuotaWarningDay.Valid || !payload.Day.Equal(r.QuotaWarningDay.Time) ||
		!r.QuotaWarningPlan.Valid || string(payload.Plan) != r.QuotaWarningPlan.String || !quotaWarningPaidPlan(payload.Plan) || !r.QuotaWarningRevision.Valid || r.QuotaWarningRevision.Int64 < 1 ||
		payload.ObservedAt.Before(payload.Day) || !payload.ObservedAt.Before(payload.Day.Add(24*time.Hour)) || payload.UsedMBSeconds < 0 || payload.QuotaGB < 1 ||
		api.InternalMailQuotaWarningUsageGBHours(payload.UsedMBSeconds) < float64(payload.QuotaGB) || !quotaWarningDeadline(payload.Day, payload.ObservedAt).Equal(r.ExpiresAt.Time) {
		return quotaWarningPayload{}, errVerificationPayloadUnavailable
	}
	if _, err := quotaWarningUnsubscribeURL(payload.UnsubscribeURL); err != nil {
		return quotaWarningPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) quotaWarningMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.quotaWarningEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := quotaWarningPayloadFor(s.quotaWarning, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, text, html := mail.QuotaWarningBodyV1(r.RecipientAddress, string(payload.Plan), payload.UsedMBSeconds, payload.QuotaGB, payload.Day)
	message := mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: text, HTMLBody: html, MessageID: "quota-warning:" + uuidString(r.ID.Bytes)}
	if payload.UnsubscribeURL != "" {
		message = mail.ApplyMarketingHeaders(message, payload.UnsubscribeURL)
	}
	return message, nil
}

func quotaWarningDeadline(day, observed time.Time) time.Time {
	deadline := observed.Add(api.InternalMailQuotaWarningTTL)
	if end := day.Add(24 * time.Hour); end.Before(deadline) {
		return end
	}
	return deadline
}
