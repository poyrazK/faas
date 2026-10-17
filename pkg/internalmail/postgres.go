package internalmail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

type PGStore struct {
	pool       *pgxpool.Pool
	namespace  string
	queries    *q.Queries
	dailyLimit int
}

func (s *PGStore) Ready(ctx context.Context) error {
	_, err := s.queries.Quota(ctx, s.pool, s.namespace)
	if err == nil {
		err = s.queries.RetentionSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.OperationsSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.DomainLifecycleSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.DeliveryControlsSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.ProducersSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.SchedulingSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.PacingSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.RelayHealthSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.ReputationSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.EventFeedSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.ProducerSchedulerSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.ProducerLifecycleSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.EncryptionSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.SuppressionControlsSchema(ctx, s.pool)
	}
	if err == nil {
		err = s.queries.RecoverySchema(ctx, s.pool)
	}
	return err
}

func NewPGStore(pool *pgxpool.Pool, namespace string) (*PGStore, error) {
	return NewPGStoreWithDailyLimit(pool, namespace, api.InternalMailDailyRecipients)
}

func NewPGStoreWithDailyLimit(pool *pgxpool.Pool, namespace string, limit int) (*PGStore, error) {
	if pool == nil || !validNamespace(namespace) || limit < 1 || limit > api.InternalMailDailyRecipients {
		return nil, ErrInvalid
	}
	return &PGStore{pool: pool, namespace: namespace, queries: q.New(), dailyLimit: limit}, nil
}

func (s *PGStore) CreateDomain(ctx context.Context, d Domain) (Domain, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Domain{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.shareEncryption(ctx, tx); err != nil {
		return Domain{}, err
	}
	if err := s.checkEncryptionWrite(ctx, tx, d.SealedKey, nil, true); err != nil {
		return Domain{}, err
	}
	r, err := s.queries.CreateDomain(ctx, tx, q.CreateDomainParams{ID: pgID(d.ID), Namespace: s.namespace, Name: d.Name, Selector: d.Selector, VerificationToken: d.VerificationToken, PublicKey: d.PublicKey, SealedKey: d.SealedKey})
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		return s.DomainByName(ctx, d.Name)
	}
	if err != nil {
		return Domain{}, storeError(err)
	}
	if err := s.queries.EnsureLegacyDKIMKey(ctx, tx, q.EnsureLegacyDKIMKeyParams{Namespace: s.namespace, ID: r.ID}); err != nil {
		return Domain{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Domain{}, err
	}
	return domainRow(r), nil
}

func (s *PGStore) Domain(ctx context.Context, id string) (Domain, error) {
	r, err := s.queries.Domain(ctx, s.pool, q.DomainParams{Namespace: s.namespace, ID: pgID(id)})
	return domainRow(r), storeError(err)
}

func (s *PGStore) DomainByName(ctx context.Context, name string) (Domain, error) {
	r, err := s.queries.DomainByName(ctx, s.pool, q.DomainByNameParams{Namespace: s.namespace, Name: name})
	return domainRow(r), storeError(err)
}

func (s *PGStore) SetDomainStatus(ctx context.Context, id, status string) error {
	n, err := s.queries.SetDomainStatus(ctx, s.pool, q.SetDomainStatusParams{Namespace: s.namespace, ID: pgID(id), Status: status})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) Enqueue(ctx context.Context, m Message, recipients []Recipient) (Message, bool, error) {
	return s.enqueueAuthorized(ctx, m, recipients, domainAuthorization{})
}

func (s *PGStore) enqueueAuthorized(ctx context.Context, m Message, recipients []Recipient, a domainAuthorization) (Message, bool, error) {
	if m.ProducerID != "" || m.SenderAddress != "" || !m.IsZero() {
		return Message{}, false, ErrInvalid
	}
	return s.enqueueMessage(ctx, m, recipients, a, nil, nil)
}

func (s *PGStore) enqueueMessage(ctx context.Context, m Message, recipients []Recipient, a domainAuthorization, producer *Producer, enabled func() bool) (Message, bool, error) {
	if !producerOutboxAdmissionEnabled(ctx) {
		return Message{}, false, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.shareRecovery(ctx, tx, ""); err != nil {
		return Message{}, false, err
	}
	if expected, ok := ctx.Value(producerOutboxGenerationKey{}).(string); ok {
		if err := s.checkOutboxGeneration(ctx, tx, expected); err != nil {
			return Message{}, false, err
		}
	}
	if err := s.shareEncryption(ctx, tx); err != nil {
		return Message{}, false, err
	}
	if err := s.shareAdmissionSuppressions(ctx, tx, recipients); err != nil {
		return Message{}, false, err
	}
	if err := s.shareProducerAccess(ctx, tx, m.ProducerID); err != nil {
		return Message{}, false, err
	}
	if err := s.queries.LockNamespace(ctx, tx, s.namespace); err != nil {
		return Message{}, false, err
	}
	existing, err := s.queries.MessageByKey(ctx, tx, q.MessageByKeyParams{Namespace: s.namespace, ProducerID: m.ProducerID, IdempotencyKey: m.IdempotencyKey})
	if err == nil {
		if !producerOutboxAdmissionEnabled(ctx) {
			return Message{}, false, ErrDisabled
		}
		if enabled != nil && !enabled() {
			return Message{}, false, ErrDisabled
		}
		if err := s.recheckProducerCredential(ctx, tx, m.ProducerID); err != nil {
			return Message{}, false, err
		}
		return messageRow(existing), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Message{}, false, err
	}
	if err := s.checkEncryptionWrite(ctx, tx, m.SealedPayload, m.PayloadFingerprint, true); err != nil {
		return Message{}, false, err
	}
	if !m.IsZero() {
		if err := s.checkTimingAdmission(ctx, tx, m.MessageTiming); err != nil {
			return Message{}, false, err
		}
	}
	if err := s.checkAdmission(ctx, tx, m, len(recipients), a); err != nil {
		return Message{}, false, err
	}
	if producer != nil {
		if err := s.checkProducerAdmission(ctx, tx, *producer, len(recipients)); err != nil {
			return Message{}, false, err
		}
	}
	r, err := s.queries.CreateMessage(ctx, tx, q.CreateMessageParams{ID: pgID(m.ID), Namespace: s.namespace, DomainID: pgID(m.DomainID), IdempotencyKey: m.IdempotencyKey, SealedPayload: m.SealedPayload, PayloadFingerprint: m.PayloadFingerprint, ProducerID: m.ProducerID, SenderAddress: m.SenderAddress, SendAt: sqlTime(m.SendAt), ExpiresAt: sqlTime(m.ExpiresAt)})
	if err != nil {
		return Message{}, false, storeError(err)
	}
	for _, recipient := range recipients {
		if err := s.queries.CreateRecipient(ctx, tx, q.CreateRecipientParams{ID: pgID(recipient.ID), Namespace: s.namespace, MessageID: pgID(m.ID), Address: recipient.Address, NextAttemptAt: sqlTime(m.SendAt)}); err != nil {
			return Message{}, false, err
		}
		if err := s.event(ctx, tx, uuid.NewString(), recipient.ID, "queued", ""); err != nil {
			return Message{}, false, err
		}
	}
	if enabled != nil && !enabled() {
		return Message{}, false, ErrDisabled
	}
	if !producerOutboxAdmissionEnabled(ctx) {
		return Message{}, false, ErrDisabled
	}
	if err := s.recheckProducerCredential(ctx, tx, m.ProducerID); err != nil {
		return Message{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, false, err
	}
	return messageRow(r), true, nil
}

func (s *PGStore) checkAdmission(ctx context.Context, tx pgx.Tx, m Message, count int, a domainAuthorization) error {
	d, err := s.queries.LockMailDomain(ctx, tx, q.LockMailDomainParams{Namespace: s.namespace, ID: pgID(m.DomainID)})
	if err != nil {
		return storeError(err)
	}
	if d.Status != "verified" || !authorizedDomain(domainRow(d), a, time.Now()) {
		return ErrUnverified
	}
	usage, err := s.queries.Quota(ctx, tx, s.namespace)
	if err != nil {
		return err
	}
	if usage.Daily+int64(count) > int64(s.dailyLimit) || usage.Pending+int64(count) > api.InternalMailMaxPending {
		return ErrQuota
	}
	return nil
}

func (s *PGStore) Message(ctx context.Context, id string) (MessageResponse, error) {
	m, err := s.queries.Message(ctx, s.pool, q.MessageParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return MessageResponse{}, storeError(err)
	}
	rs, err := s.queries.Recipients(ctx, s.pool, q.RecipientsParams{Namespace: s.namespace, MessageID: pgID(id)})
	if err != nil {
		return MessageResponse{}, err
	}
	result := MessageResponse{Message: messageRow(m), Recipients: make([]Recipient, 0, len(rs))}
	for _, r := range rs {
		result.Recipients = append(result.Recipients, recipientRow(r))
	}
	return result, nil
}

func (s *PGStore) Events(ctx context.Context, id string) ([]Event, error) {
	if _, err := s.queries.Message(ctx, s.pool, q.MessageParams{Namespace: s.namespace, ID: pgID(id)}); err != nil {
		return nil, storeError(err)
	}
	rs, err := s.queries.Events(ctx, s.pool, q.EventsParams{Namespace: s.namespace, MessageID: pgID(id), Limit: api.InternalMailMaxEvents})
	if err != nil {
		return nil, err
	}
	result := make([]Event, 0, len(rs))
	for _, r := range rs {
		result = append(result, Event{ID: r.ID, RecipientID: idString(r.RecipientID), Kind: r.Kind, Code: r.Code, CreatedAt: r.CreatedAt.Time})
	}
	return result, nil
}

func (s *PGStore) Claim(ctx context.Context) (Recipient, error) {
	return s.claimAuthorized(ctx, domainAuthorization{})
}

func (s *PGStore) claimAuthorized(ctx context.Context, a domainAuthorization) (Recipient, error) {
	return s.claimWithProducers(ctx, a, nil)
}

func (s *PGStore) claimWithProducers(ctx context.Context, a domainAuthorization, allowed []string) (Recipient, error) {
	return s.claimTimed(ctx, a, allowed, false)
}

func (s *PGStore) claimTimed(ctx context.Context, a domainAuthorization, allowed []string, scheduling bool) (Recipient, error) {
	return s.claimPaced(ctx, a, allowed, scheduling, PacingPolicy{})
}

func (s *PGStore) claimPaced(ctx context.Context, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy) (Recipient, error) {
	return s.claimWithRelay(ctx, a, allowed, scheduling, pacing, false)
}

func (s *PGStore) claimWithRelay(ctx context.Context, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy, relay bool) (Recipient, error) {
	return s.claimProtected(ctx, a, allowed, scheduling, pacing, relay, false)
}

func (s *PGStore) claimProtected(ctx context.Context, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy, relay, reputation bool) (Recipient, error) {
	return s.claimScheduled(ctx, a, allowed, scheduling, pacing, relay, reputation, DispatchPolicy{})
}

func (s *PGStore) claimScheduled(ctx context.Context, a domainAuthorization, allowed []string, scheduling bool, pacing PacingPolicy, relay, reputation bool, dispatch DispatchPolicy) (Recipient, error) {
	if !pacing.valid() || !dispatch.valid() {
		return Recipient{}, ErrInvalid
	}
	if reputation {
		if err := s.refreshReputation(ctx); err != nil {
			return Recipient{}, err
		}
	}
	if relay {
		if _, err := s.refreshRelay(ctx); err != nil {
			return Recipient{}, err
		}
	}
	if err := s.queries.RecoverSubmissions(ctx, s.pool, s.namespace); err != nil {
		return Recipient{}, err
	}
	if err := s.expireRecipients(ctx, q.ExpireRecipientsParams{Namespace: s.namespace, MaxAttempts: api.InternalMailMaxAttempts, MaxAgeSeconds: int32(api.InternalMailMaxQueueAge / time.Second)}); err != nil {
		return Recipient{}, err
	}
	params := q.ClaimParams{Namespace: s.namespace, LeaseID: pgID(uuid.NewString()), LeaseSeconds: int32(api.InternalMailLeaseDuration / time.Second), MaxAttempts: api.InternalMailMaxAttempts, RequireFresh: a.RequireFresh, SendingIp: a.SendingIP, MaxCheckAgeSeconds: int32(api.InternalMailDomainCheckMaxAge / time.Second), AllowedProducerSenders: allowed, SchedulingEnabled: scheduling, MaxAgeSeconds: int32(api.InternalMailMaxQueueAge / time.Second), PacingEnabled: pacing.Interval > 0, ReputationEnabled: reputation, RelayEnabled: relay, RelayMaxAgeSeconds: int32(api.InternalMailRelayMaxSampleAge / time.Second), RelayClockSkewSeconds: int32(api.InternalMailRelayClockSkew / time.Second), RelayHighWater: api.InternalMailRelayQueueHighWater, RelayMaxQueueAgeSeconds: int32(api.InternalMailRelayQueueMaxAge / time.Second), ProducerSchedulingEnabled: dispatch.Enabled, DispatchProducerIds: dispatch.IDs, DispatchLimits: dispatch.Limits, DispatchWindowSeconds: int32(api.InternalMailProducerDispatchWindow / time.Second)}
	if !dispatch.Enabled {
		r, err := s.queries.Claim(ctx, s.pool, params)
		return recipientRow(r), storeError(err)
	}
	return s.claimFair(ctx, params)
}

func (s *PGStore) BeginSubmission(ctx context.Context, r Recipient) error {
	return s.beginSignedSubmission(ctx, r, "", domainAuthorization{})
}

func (s *PGStore) Settle(ctx context.Context, r Recipient, status, code string, next time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var n int64
	if status == "queued" && code == pacingWaitCode {
		n, err = s.queries.SettlePacingWait(ctx, tx, q.SettlePacingWaitParams{Namespace: s.namespace, ID: pgID(r.ID), LeaseID: pgID(r.LeaseID)})
	} else {
		n, err = s.queries.Settle(ctx, tx, q.SettleParams{Namespace: s.namespace, ID: pgID(r.ID), LeaseID: pgID(r.LeaseID), Status: status, Code: code, NextAttemptAt: pgtype.Timestamptz{Time: next, Valid: true}})
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	if status != "queued" {
		if err := s.event(ctx, tx, uuid.NewString(), r.ID, status, code); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PGStore) Suppressed(ctx context.Context, address string) (bool, error) {
	return s.queries.Suppressed(ctx, s.pool, q.SuppressedParams{Namespace: s.namespace, Column2: address})
}

func (s *PGStore) Report(ctx context.Context, report Report) error {
	return s.report(ctx, report, nil)
}

func (s *PGStore) reportFeedback(ctx context.Context, report Report, enabled func() bool) error {
	if enabled == nil || !enabled() {
		return ErrDisabled
	}
	return s.report(ctx, report, enabled)
}

func (s *PGStore) report(ctx context.Context, report Report, protection func() bool) error {
	if err := validateReport(report); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	adverse := report.Kind == "complained" || hardBounce(report)
	if adverse {
		address, err := s.queries.RecipientSuppressionAddress(ctx, tx, q.RecipientSuppressionAddressParams{Namespace: s.namespace, ID: pgID(report.RecipientID)})
		if err != nil {
			return storeError(err)
		}
		if err := s.lockSuppressionAddress(ctx, tx, address); err != nil {
			return err
		}
	}
	// Reporting remains useful while delivery is paused, including after a
	// crash between the relay's DATA response and local settlement. Only an
	// expired submitting lease may be recovered; active attempts still fence
	// an early notification and make Postfix retry it later.
	if protection != nil {
		if err := s.lockReputation(ctx, tx); err != nil {
			return err
		}
	}
	if err := s.queries.RecoverReportedSubmission(ctx, tx, q.RecoverReportedSubmissionParams{Namespace: s.namespace, ID: pgID(report.RecipientID)}); err != nil {
		return err
	}
	r, err := s.queries.LockRecipient(ctx, tx, q.LockRecipientParams{Namespace: s.namespace, ID: pgID(report.RecipientID)})
	if err != nil {
		return storeError(err)
	}
	if report.RecipientAddress != "" && report.RecipientAddress != r.Address {
		return ErrInvalid
	}
	if report.MessageDomain != "" {
		domain, err := s.queries.ReportMessageDomain(ctx, tx, q.ReportMessageDomainParams{Namespace: s.namespace, ID: r.MessageID})
		if err != nil {
			return storeError(err)
		}
		if domain != report.MessageDomain {
			return ErrInvalid
		}
	}
	e, err := s.queries.Event(ctx, tx, q.EventParams{Namespace: s.namespace, ID: report.EventID})
	if err == nil {
		if idString(e.RecipientID) == report.RecipientID && e.Kind == report.Kind && e.Code == report.Code {
			return nil
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !validReportTransition(r.Status, report.Kind) {
		return ErrConflict
	}
	if err := s.event(ctx, tx, report.EventID, report.RecipientID, report.Kind, report.Code); err != nil {
		return storeError(err)
	}
	if reportChangesState(r.Status, report.Kind) {
		status := report.Kind
		if status == "retry" {
			status = "queued"
		}
		if err := s.queries.ReportStatus(ctx, tx, q.ReportStatusParams{Namespace: s.namespace, ID: r.ID, Status: status, Code: report.Code}); err != nil {
			return err
		}
	}
	if adverse {
		if err := s.recordSuppressionReport(ctx, tx, strings.ToLower(r.Address), report); err != nil {
			return err
		}
	}
	if protection != nil {
		if reputationAdverse(report) {
			if err := s.recordReputation(ctx, tx, report); err != nil {
				return err
			}
		}
		if !protection() {
			return ErrDisabled
		}
	}
	return tx.Commit(ctx)
}

func hardBounce(report Report) bool {
	// Queue expiry after a temporary destination failure ends this delivery,
	// but does not prove the mailbox is permanently undeliverable.
	return report.Kind == "bounced" && !strings.HasPrefix(report.Code, "dsn_4.")
}

func validReportTransition(status, kind string) bool {
	switch kind {
	case "accepted":
		return status == "submitted" || status == "uncertain" || status == "accepted" || status == "bounced" || status == "complained"
	case "bounced":
		return status == "submitted" || status == "uncertain" || status == "accepted" || status == "bounced" || status == "complained"
	case "complained":
		return status == "submitted" || status == "uncertain" || status == "accepted" || status == "bounced" || status == "complained"
	case "deferred":
		return status == "submitted" || status == "uncertain" || status == "accepted" || status == "bounced" || status == "complained"
	case "retry":
		return status == "uncertain"
	default:
		return false
	}
}

// Postfix can deliver different queued DSNs out of order. Record delayed
// observations without regressing terminal state or creating another send.
func reportChangesState(status, kind string) bool {
	if kind == "deferred" || kind == "accepted" && (status == "bounced" || status == "complained") {
		return false
	}
	return kind != "bounced" || status != "complained"
}

func (s *PGStore) event(ctx context.Context, db q.DBTX, eventID, recipientID, kind, code string) error {
	return s.queries.InsertEvent(ctx, db, q.InsertEventParams{ID: eventID, Namespace: s.namespace, RecipientID: pgID(recipientID), Kind: kind, Code: code})
}

func pgID(id string) pgtype.UUID {
	u, err := uuid.Parse(id)
	return pgtype.UUID{Bytes: [16]byte(u), Valid: err == nil}
}
func idString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func domainRow(r q.InternalMailDomain) Domain {
	d := domainPublicRow(q.OperatorDomainRow{ID: r.ID, Name: r.Name, Status: r.Status, Selector: r.Selector, VerificationToken: r.VerificationToken, PublicKey: r.PublicKey, CreatedAt: r.CreatedAt, DkimKeyID: r.DkimKeyID, AuthorizationState: r.AuthorizationState, LastCheckedAt: r.LastCheckedAt, LastVerifiedAt: r.LastVerifiedAt, VerifiedSendingIp: r.VerifiedSendingIp, CheckResult: r.CheckResult, FailureCount: r.FailureCount, FirstFailureAt: r.FirstFailureAt, DnsRevision: r.DnsRevision})
	d.SealedKey = r.SealedKey
	return d
}
func messageRow(r q.InternalMailMessage) Message {
	m := Message{ID: idString(r.ID), DomainID: idString(r.DomainID), ProducerID: r.ProducerID, SenderAddress: r.SenderAddress, IdempotencyKey: r.IdempotencyKey, SealedPayload: r.SealedPayload, PayloadFingerprint: r.PayloadFingerprint, CreatedAt: r.CreatedAt.Time, MessageTiming: MessageTiming{SendAt: timePointer(r.SendAt), ExpiresAt: timePointer(r.ExpiresAt)}}
	if r.PayloadPrunedAt.Valid {
		at := r.PayloadPrunedAt.Time
		m.PayloadPrunedAt = &at
	}
	return m
}
func recipientRow(r q.InternalMailRecipient) Recipient {
	return Recipient{ID: idString(r.ID), MessageID: idString(r.MessageID), Address: r.Address, Status: r.Status, Attempts: r.Attempts, LeaseID: idString(r.LeaseID), Code: r.Code, UpdatedAt: r.UpdatedAt.Time, SigningSelector: r.SigningSelector}
}
func storeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("internal mail store: %w", err)
}
