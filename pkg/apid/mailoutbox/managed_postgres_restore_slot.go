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

// Managed PostgreSQL restore slot notices (ADR-20261031123259). The slot
// sweep records a notice once per copy and kind; this queues its email in the
// same transaction that links it to the notice, so a notice is mailed at most
// once.

const ManagedPostgresRestoreSlotTemplate = "managed_postgres_restore_slot"

// ManagedPostgresRestoreSlotNotice is one recorded notice, as apid passes it.
type ManagedPostgresRestoreSlotNotice struct {
	DatabaseID      string
	DatabaseName    string
	Kind            string
	SlotUntil       time.Time
	RestorableUntil time.Time
	RecordedAt      time.Time
}

type managedPostgresRestoreSlotPayload struct {
	Kind            string    `json:"kind"`
	DatabaseName    string    `json:"database_name"`
	SlotUntil       time.Time `json:"slot_until"`
	RestorableUntil time.Time `json:"restorable_until"`
	RecordedAt      time.Time `json:"recorded_at"`
}

// managedPostgresRestoreSlotExpiry is when a notice's email stops being
// useful: a day after it was recorded, and an 'ending' notice no later than
// the slot's end.
func managedPostgresRestoreSlotExpiry(p managedPostgresRestoreSlotPayload) time.Time {
	expires := p.RecordedAt.Add(api.InternalMailManagedPostgresRestoreSlotTTL)
	if p.Kind == mail.ManagedPostgresRestoreSlotEnding && p.SlotUntil.Before(expires) {
		expires = p.SlotUntil
	}
	return expires.UTC()
}

func (s *Service) managedPostgresRestoreSlotEnabled() bool {
	return s.Enabled() && s.managedPostgresRestoreSlot != nil && s.managedPostgresRestoreSlot.enabled != nil && s.managedPostgresRestoreSlot.enabled()
}

// ConfigureManagedPostgresRestoreSlot reads only its own gate when disabled.
// It shares apid's credential readers, never maild's payload keys.
func (s *Service) ConfigureManagedPostgresRestoreSlot(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.ManagedPostgresRestoreSlotOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("managed postgres restore slot outbox requires the configured producer outbox")
	}
	if err := s.queries.ManagedPostgresRestoreSlotSchemaReady(ctx, s.pool); err != nil {
		return errors.New("managed postgres restore slot outbox schema unavailable")
	}
	ready, err := s.queries.ManagedPostgresRestoreSlotReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("managed postgres restore slot outbox constraint unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("managed postgres restore slot outbox encryption identities unavailable")
	}
	keys, err := s.queries.ManagedPostgresRestoreSlotKeyReferences(ctx, s.pool, q.ManagedPostgresRestoreSlotKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("managed postgres restore slot outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("managed postgres restore slot outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.ManagedPostgresRestoreSlotOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.managedPostgresRestoreSlot = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithManagedPostgresRestoreSlotOutbox(on), enabled: on}
	return nil
}

// NoticeManagedPostgresRestoreSlot queues the email for one recorded notice
// to the account that restored the copy. It does nothing for a notice past
// its email's lifetime, for an account that is gone, inactive or being
// deleted, or for a notice already mailed.
func (s *Service) NoticeManagedPostgresRestoreSlot(ctx context.Context, accountID string, notice ManagedPostgresRestoreSlotNotice) error {
	if !s.managedPostgresRestoreSlotEnabled() {
		return internalmail.ErrDisabled
	}
	payload := managedPostgresRestoreSlotPayload{Kind: notice.Kind, DatabaseName: notice.DatabaseName, SlotUntil: notice.SlotUntil.UTC(),
		RestorableUntil: notice.RestorableUntil.UTC(), RecordedAt: notice.RecordedAt.UTC()}
	if notice.RestorableUntil.IsZero() {
		payload.RestorableUntil = time.Time{}
	}
	if _, _, ok := mail.ManagedPostgresRestoreSlotBodyV1("", managedPostgresRestoreSlotMailNotice(payload)); !ok || payload.RecordedAt.IsZero() {
		return internalmail.ErrInvalid
	}
	if !managedPostgresRestoreSlotExpiry(payload).After(time.Now()) {
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
	intent, err := s.saveManagedPostgresRestoreSlot(ctx, tx, account, recovery.RecoveryID, payload)
	if err != nil {
		return err
	}
	linked, err := s.queries.LinkManagedPostgresRestoreSlotNotice(ctx, tx, q.LinkManagedPostgresRestoreSlotNoticeParams{IntentID: intent.ID,
		DatabaseID: pgID(notice.DatabaseID), Kind: payload.Kind})
	if err != nil || linked != 1 {
		return err // already mailed, or the notice is gone: roll the intent back
	}
	if !s.managedPostgresRestoreSlotEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) saveManagedPostgresRestoreSlot(ctx context.Context, tx pgx.Tx, account q.LockManagedPostgresCapAccountRow, recovery pgtype.UUID, payload managedPostgresRestoreSlotPayload) (q.PlatformMailProducerOutbox, error) {
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
		RecipientAddress: address, Template: ManagedPostgresRestoreSlotTemplate, TemplateVersion: 1,
		ExpiresAt: pgtype.Timestamptz{Time: managedPostgresRestoreSlotExpiry(payload), Valid: true}, RecoveryID: recovery,
		SecurityAlertKeyID: s.managedPostgresRestoreSlot.keyID}
	plain, err := json.Marshal(payload)
	if err != nil {
		return r, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.managedPostgresRestoreSlot.writer.Recipient(), managedPostgresRestoreSlotBinding(r), plain, api.InternalMailManagedPostgresRestoreSlotMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return r, errVerificationPayloadUnavailable
	}
	return r, s.queries.CreateManagedPostgresRestoreSlotIntent(ctx, tx, q.CreateManagedPostgresRestoreSlotIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace,
		ProducerID: r.ProducerID, FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID,
		SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func managedPostgresRestoreSlotBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-managed-postgres-restore-slot:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func managedPostgresRestoreSlotPayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (managedPostgresRestoreSlotPayload, error) {
	var payload managedPostgresRestoreSlotPayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != managedPostgresRestoreSlotBinding(r) || len(plain) > api.InternalMailManagedPostgresRestoreSlotMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.RecordedAt.IsZero() ||
		!managedPostgresRestoreSlotExpiry(payload).Equal(r.ExpiresAt.Time) {
		return managedPostgresRestoreSlotPayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) managedPostgresRestoreSlotMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.managedPostgresRestoreSlotEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := managedPostgresRestoreSlotPayloadFor(s.managedPostgresRestoreSlot, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body, ok := mail.ManagedPostgresRestoreSlotBodyV1(r.RecipientAddress, managedPostgresRestoreSlotMailNotice(payload))
	if !ok {
		return mail.Message{}, errVerificationPayloadUnavailable
	}
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "managed-postgres-restore-slot:" + uuidString(r.ID.Bytes)}, nil
}

func managedPostgresRestoreSlotMailNotice(p managedPostgresRestoreSlotPayload) mail.ManagedPostgresRestoreSlotNotice {
	return mail.ManagedPostgresRestoreSlotNotice{Kind: p.Kind, DatabaseName: p.DatabaseName, SlotUntil: p.SlotUntil, RestorableUntil: p.RestorableUntil}
}
