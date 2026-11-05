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
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// An agent run's summary (ADR-20261027090010 §8) travels sealed: only the agent's
// name, the run ID and a bounded, redacted excerpt of the run's chosen
// result.json string. The outputs themselves stay behind the authenticated
// agent API.
type agentRunDeliveredPayload struct {
	Agent      string    `json:"agent"`
	RunID      string    `json:"run_id"`
	Summary    string    `json:"summary"`
	Truncated  bool      `json:"truncated,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

// agentRunSummary redacts the delivery body and cuts it to the outbox's
// bound at a character boundary.
func agentRunSummary(body string) (string, bool) {
	summary := mail.AgentRunSummaryText(body)
	if len(summary) <= api.InternalMailAgentRunSummaryMaxBytes {
		return summary, false
	}
	cut := api.InternalMailAgentRunSummaryMaxBytes
	for cut > 0 && !utf8.RuneStart(summary[cut]) {
		cut--
	}
	return strings.TrimSpace(summary[:cut]), true
}

func (s *Service) saveAgentRunDelivered(ctx context.Context, tx pgx.Tx, address string, recovery pgtype.UUID, d q.ShareAgentRunDeliveryRow) (pgtype.UUID, error) {
	if !s.agentRunDeliveredEnabled() {
		return pgtype.UUID{}, internalmail.ErrDisabled
	}
	pending, err := s.queries.Pending(ctx, tx, s.namespace)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if pending >= api.InternalMailProducerOutboxMaxPending {
		return pgtype.UUID{}, internalmail.ErrQuota
	}
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: d.AccountID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from,
		RecipientAddress: address, Template: AgentRunDeliveredTemplate, TemplateVersion: 1,
		ExpiresAt: pgtype.Timestamptz{Time: d.CreatedAt.Time.Add(api.InternalMailAgentRunDeliveredTTL), Valid: true}, RecoveryID: recovery,
		SecurityAlertKeyID: s.agentRunDelivered.keyID, AgentDeliveryID: d.ID}
	summary, cut := agentRunSummary(d.Body)
	payload := agentRunDeliveredPayload{Agent: d.AgentName, RunID: uuidString(d.RunID.Bytes), Summary: summary, Truncated: cut || d.BodyTruncated,
		FinishedAt: d.CreatedAt.Time.UTC()}
	plain, err := json.Marshal(payload)
	if err != nil || len(plain) > api.InternalMailAgentRunDeliveredMaxBytes {
		return pgtype.UUID{}, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.agentRunDelivered.writer.Recipient(), agentRunDeliveredBinding(r), plain, api.InternalMailAgentRunDeliveredMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return pgtype.UUID{}, errVerificationPayloadUnavailable
	}
	err = s.queries.CreateAgentRunDeliveredIntent(ctx, tx, q.CreateAgentRunDeliveredIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace,
		ProducerID: r.ProducerID, FromAddress: r.FromAddress, RecipientAddress: r.RecipientAddress, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID,
		SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID, AgentDeliveryID: r.AgentDeliveryID})
	return r.ID, err
}

func agentRunDeliveredBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		uuidString(r.AgentDeliveryID.Bytes), r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes),
		r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-agent-run-delivered:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func agentRunDeliveredPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (agentRunDeliveredPayload, error) {
	var payload agentRunDeliveredPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes || !r.AgentDeliveryID.Valid {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != agentRunDeliveredBinding(r) || len(plain) > api.InternalMailAgentRunDeliveredMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.FinishedAt.IsZero() ||
		!payload.FinishedAt.Add(api.InternalMailAgentRunDeliveredTTL).Equal(r.ExpiresAt.Time) || !validID(payload.RunID) ||
		payload.Agent == "" || len(payload.Agent) > api.InternalMailAgentRunNameMaxBytes || len(payload.Summary) > api.InternalMailAgentRunSummaryMaxBytes {
		return agentRunDeliveredPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) agentRunDeliveredMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.agentRunDeliveredEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := agentRunDeliveredPayloadFor(s.agentRunDelivered, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body := mail.AgentRunDeliveredBodyV1(mail.AgentRunDelivered{Agent: payload.Agent, RunID: payload.RunID, Summary: payload.Summary,
		Truncated: payload.Truncated, FinishedAt: payload.FinishedAt})
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "agent-run-delivered:" + uuidString(r.ID.Bytes)}, nil
}

// agentRunDeliveredAccountAvailable is whether an account still receives its
// agents' summaries.
func agentRunDeliveredAccountAvailable(status string) bool {
	return status == "active" || status == "past_due"
}
