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

// Managed PostgreSQL awake notices (ADR-20261125231222). The awake sweep
// records one notice per database and week when one cause kept a
// scale-to-zero database awake most of the time; this queues its email in the
// same transaction that marks the notice mailed, so a notice is mailed at most
// once.

const ManagedPostgresAwakeTemplate = "managed_postgres_awake"

// ManagedPostgresAwakeNotice is one recorded notice, as apid passes it.
type ManagedPostgresAwakeNotice struct {
	DatabaseID   string
	DatabaseName string
	WeekStart    time.Time
	// AwakeHoursPerDay is the week's average, rounded down.
	AwakeHoursPerDay int
	Starts           int
	Cause            string
	AppSlug          string
	Detail           string
	CauseStarts      int
	RecordedAt       time.Time
}

type managedPostgresAwakePayload struct {
	DatabaseName     string    `json:"database_name"`
	AwakeHoursPerDay int       `json:"awake_hours_per_day"`
	Starts           int       `json:"starts"`
	Cause            string    `json:"cause"`
	AppSlug          string    `json:"app_slug,omitempty"`
	Detail           string    `json:"detail,omitempty"`
	CauseStarts      int       `json:"cause_starts"`
	RecordedAt       time.Time `json:"recorded_at"`
}

// managedPostgresAwakeExpiry is when a notice's email stops being
// useful: two days after it was recorded.
func managedPostgresAwakeExpiry(p managedPostgresAwakePayload) time.Time {
	return p.RecordedAt.Add(api.InternalMailManagedPostgresAwakeTTL).UTC()
}

func (s *Service) managedPostgresAwakeEnabled() bool {
	return s.Enabled() && s.managedPostgresAwake != nil && s.managedPostgresAwake.enabled != nil && s.managedPostgresAwake.enabled()
}

// ConfigureManagedPostgresAwake reads only its own gate when disabled.
// It shares apid's credential readers, never maild's payload keys.
func (s *Service) ConfigureManagedPostgresAwake(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.ManagedPostgresAwakeOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("managed postgres awake outbox requires the configured producer outbox")
	}
	if err := s.queries.ManagedPostgresAwakeSchemaReady(ctx, s.pool); err != nil {
		return errors.New("managed postgres awake outbox schema unavailable")
	}
	ready, err := s.queries.ManagedPostgresAwakeReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("managed postgres awake outbox constraint unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("managed postgres awake outbox encryption identities unavailable")
	}
	keys, err := s.queries.ManagedPostgresAwakeKeyReferences(ctx, s.pool, q.ManagedPostgresAwakeKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("managed postgres awake outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("managed postgres awake outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.ManagedPostgresAwakeOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.managedPostgresAwake = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithManagedPostgresAwakeOutbox(on), enabled: on}
	return nil
}

// NoticeManagedPostgresAwake queues the email for one recorded notice to
// the account that owns the database. It does nothing for a notice past
// its email's lifetime, for an account that is gone, inactive or being
// deleted, or for a notice already mailed.
func (s *Service) NoticeManagedPostgresAwake(ctx context.Context, accountID string, notice ManagedPostgresAwakeNotice) error {
	if !s.managedPostgresAwakeEnabled() {
		return internalmail.ErrDisabled
	}
	payload := managedPostgresAwakePayload{DatabaseName: notice.DatabaseName, AwakeHoursPerDay: notice.AwakeHoursPerDay, Starts: notice.Starts,
		Cause: notice.Cause, AppSlug: notice.AppSlug, Detail: notice.Detail, CauseStarts: notice.CauseStarts, RecordedAt: notice.RecordedAt.UTC()}
	if _, _, ok := mail.ManagedPostgresAwakeBodyV1("", managedPostgresAwakeMailNotice(payload)); !ok || payload.RecordedAt.IsZero() {
		return internalmail.ErrInvalid
	}
	if !managedPostgresAwakeExpiry(payload).After(time.Now()) {
		return nil
	}
	if !validID(accountID) || !validID(notice.DatabaseID) || notice.WeekStart.IsZero() {
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
	_, err = s.saveManagedPostgresAwake(ctx, tx, account, recovery.RecoveryID, payload)
	if err != nil {
		return err
	}
	marked, err := s.queries.MarkManagedPostgresAwakeNoticeMailed(ctx, tx, q.MarkManagedPostgresAwakeNoticeMailedParams{
		DatabaseID: pgID(notice.DatabaseID), WeekStart: pgtype.Date{Time: notice.WeekStart.UTC(), Valid: true}})
	if err != nil || marked != 1 {
		return err // already mailed, or the notice is gone: roll the intent back
	}
	if !s.managedPostgresAwakeEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) saveManagedPostgresAwake(ctx context.Context, tx pgx.Tx, account q.LockManagedPostgresCapAccountRow, recovery pgtype.UUID, payload managedPostgresAwakePayload) (q.PlatformMailProducerOutbox, error) {
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
		RecipientAddress: address, Template: ManagedPostgresAwakeTemplate, TemplateVersion: 1,
		ExpiresAt: pgtype.Timestamptz{Time: managedPostgresAwakeExpiry(payload), Valid: true}, RecoveryID: recovery,
		SecurityAlertKeyID: s.managedPostgresAwake.keyID}
	plain, err := json.Marshal(payload)
	if err != nil {
		return r, errVerificationPayloadUnavailable
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytes(s.managedPostgresAwake.writer.Recipient(), managedPostgresAwakeBinding(r), plain, api.InternalMailManagedPostgresAwakeMaxBytes)
	if err != nil || len(sealed) > api.InternalMailVerificationCipherMaxBytes {
		return r, errVerificationPayloadUnavailable
	}
	return r, s.queries.CreateManagedPostgresAwakeIntent(ctx, tx, q.CreateManagedPostgresAwakeIntentParams{ID: r.ID, AccountID: r.AccountID, Namespace: r.Namespace,
		ProducerID: r.ProducerID, FromAddress: r.FromAddress, RecipientAddress: address, ExpiresAt: r.ExpiresAt, RecoveryID: r.RecoveryID,
		SealedSecurityAlert: sealed, SecurityAlertKeyID: r.SecurityAlertKeyID})
}

func managedPostgresAwakeBinding(r q.PlatformMailProducerOutbox) string {
	metadata := strings.Join([]string{uuidString(r.ID.Bytes), uuidString(r.AccountID.Bytes), r.Namespace, r.ProducerID, r.FromAddress, r.RecipientAddress,
		r.Template, "1", r.ExpiresAt.Time.UTC().Format(time.RFC3339Nano), uuidString(r.RecoveryID.Bytes), r.SecurityAlertKeyID}, "\x00")
	digest := sha256.Sum256([]byte(metadata))
	return "producer-managed-postgres-awake:v1:" + r.Namespace + ":" + uuidString(r.ID.Bytes) + ":" + hex.EncodeToString(digest[:])
}

func managedPostgresAwakePayloadFor(config *credentialConfig, r q.PlatformMailProducerOutbox) (managedPostgresAwakePayload, error) {
	var payload managedPostgresAwakePayload
	reader := config.readers[r.SecurityAlertKeyID]
	if reader == nil || len(r.SealedSecurityAlert) > api.InternalMailVerificationCipherMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	binding, plain, err := secretbox.OpenBytes(reader, r.SealedSecurityAlert)
	defer clear(plain)
	if err != nil || binding != managedPostgresAwakeBinding(r) || len(plain) > api.InternalMailManagedPostgresAwakeMaxBytes {
		return payload, errVerificationPayloadUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.RecordedAt.IsZero() ||
		!managedPostgresAwakeExpiry(payload).Equal(r.ExpiresAt.Time) {
		return managedPostgresAwakePayload{}, errVerificationPayloadUnavailable
	}
	return payload, nil
}

func (s *Service) managedPostgresAwakeMessage(r q.PlatformMailProducerOutbox) (mail.Message, error) {
	if !s.managedPostgresAwakeEnabled() {
		return mail.Message{}, internalmail.ErrDisabled
	}
	payload, err := managedPostgresAwakePayloadFor(s.managedPostgresAwake, r)
	if err != nil {
		return mail.Message{}, err
	}
	subject, body, ok := mail.ManagedPostgresAwakeBodyV1(r.RecipientAddress, managedPostgresAwakeMailNotice(payload))
	if !ok {
		return mail.Message{}, errVerificationPayloadUnavailable
	}
	return mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "managed-postgres-awake:" + uuidString(r.ID.Bytes)}, nil
}

func managedPostgresAwakeMailNotice(p managedPostgresAwakePayload) mail.ManagedPostgresAwakeNotice {
	return mail.ManagedPostgresAwakeNotice{DatabaseName: p.DatabaseName, AwakeHoursPerDay: p.AwakeHoursPerDay, Starts: p.Starts,
		Cause: p.Cause, AppSlug: p.AppSlug, Detail: p.Detail, CauseStarts: p.CauseStarts}
}
