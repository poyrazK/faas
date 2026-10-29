package mailoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Managed PostgreSQL compute cap warnings (ADR-20261025094915 decision 8).
// The cap sweep records a threshold once per subject and UTC month; this
// queues its email in the same transaction that links it to the event, so a
// threshold is mailed at most once.

const ManagedPostgresCapTemplate = "managed_postgres_cap"

// ManagedPostgresCapEvent is one recorded threshold, as apid passes it.
type ManagedPostgresCapEvent struct {
	SubjectID   string
	PeriodStart time.Time
	Threshold   string
	ReachedAt   time.Time
	Used        int64
	Allowance   int64
	Ceiling     int64
	Overage     bool
}

type managedPostgresCapPayload struct {
	Threshold   string    `json:"threshold"`
	PeriodStart time.Time `json:"period_start"`
	ReachedAt   time.Time `json:"reached_at"`
	Used        int64     `json:"used_unit_seconds"`
	Allowance   int64     `json:"allowance_unit_seconds"`
	Ceiling     int64     `json:"ceiling_unit_seconds"`
	Overage     bool      `json:"overage_enabled"`
}

func (s *Service) managedPostgresCapEnabled() bool {
	return s.Enabled() && s.managedPostgresCap != nil && s.managedPostgresCap.enabled != nil && s.managedPostgresCap.enabled()
}

// ConfigureManagedPostgresCap reads only its own gate when disabled. It shares
// apid's credential readers, never maild's payload keys.
func (s *Service) ConfigureManagedPostgresCap(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.ManagedPostgresCapOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("managed postgres cap outbox requires the configured producer outbox")
	}
	if err := s.queries.ManagedPostgresCapSchemaReady(ctx, s.pool); err != nil {
		return errors.New("managed postgres cap outbox schema unavailable")
	}
	ready, err := s.queries.ManagedPostgresCapReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("managed postgres cap outbox constraint unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("managed postgres cap outbox encryption identities unavailable")
	}
	keys, err := s.queries.ManagedPostgresCapKeyReferences(ctx, s.pool, q.ManagedPostgresCapKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("managed postgres cap outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("managed postgres cap outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.ManagedPostgresCapOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.managedPostgresCap = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithManagedPostgresCapOutbox(on), enabled: on}
	return nil
}

// NoticeManagedPostgresCap queues the email for one recorded threshold to the
// subject's account. It does nothing for a threshold with no email (the
// allowance without overage, which the suspension email covers), for one
// reached longer ago than the email's lifetime, for an account that is gone,
// inactive or being deleted, or for an event already mailed.
func (s *Service) NoticeManagedPostgresCap(ctx context.Context, accountID string, event ManagedPostgresCapEvent) error {
	if !s.managedPostgresCapEnabled() {
		return internalmail.ErrDisabled
	}
	payload := managedPostgresCapPayload{Threshold: event.Threshold, PeriodStart: event.PeriodStart.UTC(), ReachedAt: event.ReachedAt.UTC(),
		Used: event.Used, Allowance: event.Allowance, Ceiling: event.Ceiling, Overage: event.Overage}
	if _, _, ok := mail.ManagedPostgresCapBodyV1("", managedPostgresCapNotice(payload)); !ok {
		return nil
	}
	if !payload.ReachedAt.Add(api.InternalMailManagedPostgresCapTTL).After(time.Now()) {
		return nil
	}
	if !validID(accountID) || !validID(event.SubjectID) {
		return internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	account, err := s.queries.LockManagedPostgresCapAccount(ctx, tx, pgID(accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !quotaWarningAccountAvailable(account.Status) || account.DeletionRequestedAt.Valid {
		return nil
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	intent, err := s.saveManagedPostgresCap(ctx, tx, account, recovery.RecoveryID, payload)
	if err != nil {
		return err
	}
	linked, err := s.queries.LinkManagedPostgresCapEvent(ctx, tx, q.LinkManagedPostgresCapEventParams{IntentID: intent.ID, SubjectID: pgID(event.SubjectID),
		PeriodStart: pgtype.Timestamptz{Time: payload.PeriodStart, Valid: true}, Threshold: payload.Threshold})
	if err != nil || linked != 1 {
		return err // already mailed, or the event is gone: roll the intent back
	}
	if !s.managedPostgresCapEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) saveManagedPostgresCap(ctx context.Context, tx pgx.Tx, account q.LockManagedPostgresCapAccountRow, recovery pgtype.UUID, payload managedPostgresCapPayload) (q.PlatformMailProducerOutbox, error) {
	address, err := internalmail.NormalizeSuppressionAddress(account.Email)
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
	r := q.PlatformMailProducerOutbox{ID: pgID(uuid.NewString()), AccountID: account.ID, Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from,
		RecipientAddress: address, Template: ManagedPostgresCapTemplate, TemplateVersion: 1,
		ExpiresAt: pgtype.Timestamptz{Time: payload.ReachedAt.Add(api.InternalMailManagedPostgresCapTTL), Valid: true}, RecoveryID: recovery,
		SecurityAlertKeyID: s.managedPostgresCap.keyID}
	plain, err := json.Marshal(payload)
	if err != nil {
		return r, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.managedPostgresCap.writer.Recipient(), managedPostgresCapBinding(r), plain, api.InternalMailManagedPostgresCapMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return r, errVerificationPayloadUnavailable
	}
	return r, s.queries.CreateManagedPostgresCapIntent(ctx, tx, q.CreateManagedPostgresCapIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace,
		ProducerID: r.ProducerID, FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID,
		SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func managedPostgresCapBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-managed-postgres-cap:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func managedPostgresCapPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (managedPostgresCapPayload, error) {
	var payload managedPostgresCapPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != managedPostgresCapBinding(r) || len(plain) > api.InternalMailManagedPostgresCapMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.ReachedAt.IsZero() ||
		!payload.ReachedAt.Add(api.InternalMailManagedPostgresCapTTL).Equal(r.ExpiresAt.Time) {
		return managedPostgresCapPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) managedPostgresCapMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.managedPostgresCapEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := managedPostgresCapPayloadFor(s.managedPostgresCap, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body, ok := mail.ManagedPostgresCapBodyV1(r.RecipientAddress, managedPostgresCapNotice(payload))
	if !ok {
		return mail.Message{}, errVerificationPayloadUnavailable
	}
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "managed-postgres-cap:" + uuidString(r.ID.Bytes)}, nil
}

func managedPostgresCapNotice(p managedPostgresCapPayload) mail.ManagedPostgresCapNotice {
	return mail.ManagedPostgresCapNotice{Threshold: p.Threshold, UsedSeconds: p.Used, Allowance: p.Allowance, Ceiling: p.Ceiling, Overage: p.Overage, ReachedAt: p.ReachedAt}
}
