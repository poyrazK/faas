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

// Managed PostgreSQL plan excess notices (ADR-20261102125208). The cap
// sweep records a notice once per suspended database; this queues its email
// in the same transaction that links it to the notice, so a notice is mailed
// at most once.

const ManagedPostgresPlanExcessTemplate = "managed_postgres_plan_excess"

// ManagedPostgresPlanExcessNotice is one recorded notice, as apid passes it.
type ManagedPostgresPlanExcessNotice struct {
	DatabaseID   string
	DatabaseName string
	ServiceClass string
	Reason       string
	RecordedAt   time.Time
	// DeleteAt is when the database is deleted if it still does not fit
	// (ADR-20261110184700); zero while deletion is off.
	DeleteAt time.Time
}

type managedPostgresPlanExcessPayload struct {
	Reason       string    `json:"reason"`
	DatabaseName string    `json:"database_name"`
	ServiceClass string    `json:"service_class"`
	RecordedAt   time.Time `json:"recorded_at"`
	DeleteAt     time.Time `json:"delete_at,omitzero"`
}

// managedPostgresPlanExcessExpiry is when a notice's email stops being
// useful: a day after it was recorded.
func managedPostgresPlanExcessExpiry(p managedPostgresPlanExcessPayload) time.Time {
	return p.RecordedAt.Add(api.InternalMailManagedPostgresPlanExcessTTL).UTC()
}

func (s *Service) managedPostgresPlanExcessEnabled() bool {
	return s.Enabled() && s.managedPostgresPlanExcess != nil && s.managedPostgresPlanExcess.enabled != nil && s.managedPostgresPlanExcess.enabled()
}

// ConfigureManagedPostgresPlanExcess reads only its own gate when disabled.
// It shares apid's credential readers, never maild's payload keys.
func (s *Service) ConfigureManagedPostgresPlanExcess(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.ManagedPostgresPlanExcessOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("managed postgres plan excess outbox requires the configured producer outbox")
	}
	if err := s.queries.ManagedPostgresPlanExcessSchemaReady(ctx, s.pool); err != nil {
		return errors.New("managed postgres plan excess outbox schema unavailable")
	}
	ready, err := s.queries.ManagedPostgresPlanExcessReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("managed postgres plan excess outbox constraint unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("managed postgres plan excess outbox encryption identities unavailable")
	}
	keys, err := s.queries.ManagedPostgresPlanExcessKeyReferences(ctx, s.pool, q.ManagedPostgresPlanExcessKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("managed postgres plan excess outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("managed postgres plan excess outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.ManagedPostgresPlanExcessOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.managedPostgresPlanExcess = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithManagedPostgresPlanExcessOutbox(on), enabled: on}
	return nil
}

// NoticeManagedPostgresPlanExcess queues the email for one recorded notice to
// the account that owns the database. It does nothing for a notice past
// its email's lifetime, for an account that is gone, inactive or being
// deleted, or for a notice already mailed.
func (s *Service) NoticeManagedPostgresPlanExcess(ctx context.Context, accountID string, notice ManagedPostgresPlanExcessNotice) error {
	if !s.managedPostgresPlanExcessEnabled() {
		return internalmail.ErrDisabled
	}
	payload := managedPostgresPlanExcessPayload{Reason: notice.Reason, DatabaseName: notice.DatabaseName, ServiceClass: notice.ServiceClass,
		RecordedAt: notice.RecordedAt.UTC(), DeleteAt: notice.DeleteAt.UTC()}
	if _, _, ok := mail.ManagedPostgresPlanExcessBodyV1("", managedPostgresPlanExcessMailNotice(payload)); !ok || payload.RecordedAt.IsZero() {
		return internalmail.ErrInvalid
	}
	if !managedPostgresPlanExcessExpiry(payload).After(time.Now()) {
		return nil
	}
	if !validID(accountID) || !validID(notice.DatabaseID) {
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
	intent, err := s.saveManagedPostgresPlanExcess(ctx, tx, account, recovery.RecoveryID, payload)
	if err != nil {
		return err
	}
	// The notice keeps the deletion date this email names: the cap sweep
	// deletes the database only on a date its owner was mailed.
	var deleteAt pgtype.Timestamptz
	if !payload.DeleteAt.IsZero() {
		deleteAt = pgtype.Timestamptz{Time: payload.DeleteAt, Valid: true}
	}
	linked, err := s.queries.LinkManagedPostgresPlanExcessNotice(ctx, tx, q.LinkManagedPostgresPlanExcessNoticeParams{IntentID: intent.ID,
		DeleteAt: deleteAt, DatabaseID: pgID(notice.DatabaseID)})
	if err != nil || linked != 1 {
		return err // already mailed, or the notice is gone: roll the intent back
	}
	if !s.managedPostgresPlanExcessEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) saveManagedPostgresPlanExcess(ctx context.Context, tx pgx.Tx, account q.LockManagedPostgresCapAccountRow, recovery pgtype.UUID, payload managedPostgresPlanExcessPayload) (q.PlatformMailProducerOutbox, error) {
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
		RecipientAddress: address, Template: ManagedPostgresPlanExcessTemplate, TemplateVersion: 1,
		ExpiresAt: pgtype.Timestamptz{Time: managedPostgresPlanExcessExpiry(payload), Valid: true}, RecoveryID: recovery,
		SecurityAlertKeyID: s.managedPostgresPlanExcess.keyID}
	plain, err := json.Marshal(payload)
	if err != nil {
		return r, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.managedPostgresPlanExcess.writer.Recipient(), managedPostgresPlanExcessBinding(r), plain, api.InternalMailManagedPostgresPlanExcessMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return r, errVerificationPayloadUnavailable
	}
	return r, s.queries.CreateManagedPostgresPlanExcessIntent(ctx, tx, q.CreateManagedPostgresPlanExcessIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace,
		ProducerID: r.ProducerID, FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID,
		SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func managedPostgresPlanExcessBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-managed-postgres-plan-excess:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func managedPostgresPlanExcessPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (managedPostgresPlanExcessPayload, error) {
	var payload managedPostgresPlanExcessPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != managedPostgresPlanExcessBinding(r) || len(plain) > api.InternalMailManagedPostgresPlanExcessMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.RecordedAt.IsZero() ||
		!managedPostgresPlanExcessExpiry(payload).Equal(r.ExpiresAt.Time) {
		return managedPostgresPlanExcessPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) managedPostgresPlanExcessMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.managedPostgresPlanExcessEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := managedPostgresPlanExcessPayloadFor(s.managedPostgresPlanExcess, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body, ok := mail.ManagedPostgresPlanExcessBodyV1(r.RecipientAddress, managedPostgresPlanExcessMailNotice(payload))
	if !ok {
		return mail.Message{}, errVerificationPayloadUnavailable
	}
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "managed-postgres-plan-excess:" + uuidString(r.ID.Bytes)}, nil
}

func managedPostgresPlanExcessMailNotice(p managedPostgresPlanExcessPayload) mail.ManagedPostgresPlanExcessNotice {
	return mail.ManagedPostgresPlanExcessNotice{Reason: p.Reason, DatabaseName: p.DatabaseName, ServiceClass: p.ServiceClass, DeleteAt: p.DeleteAt}
}
