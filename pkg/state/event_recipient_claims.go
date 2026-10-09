package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublishedEventRecipientWork is one routing lease. Attempts counts claims in
// this replay generation; TotalAttempts preserves the lifetime attempt count.
// Invocation identity remains independent of both the lease and generation.
type PublishedEventRecipientWork struct {
	AcceptedAt                  time.Time
	PreviousProgress            PublishedEventRecipientProgress
	OutboxID                    int64
	Recipient                   PublishedEventRecipient
	BackfillJobID               string
	Payload                     []byte
	State                       string
	ClaimToken                  string
	Generation                  int64
	Attempts                    int
	CapacityDeferrals           int
	GenerationCapacityDeferrals int
	TotalAttempts               int
	AvailableAt                 time.Time
	LeaseUntil                  time.Time
}

type PublishedEventRecipientWorkStore interface {
	InitializePublishedEventRecipients(context.Context, *PublishedEventWork, time.Time) error
	ClaimDuePublishedEventRecipient(context.Context, time.Time, ...bool) (*PublishedEventRecipientWork, error)
	FinishPublishedEventRecipient(context.Context, *PublishedEventRecipientWork, PublishedEventRecipientProgress, time.Time) error
}

func (s *PgStore) InitializePublishedEventRecipients(ctx context.Context, work *PublishedEventWork, now time.Time) error {
	if work == nil || !work.SnapshotCaptured {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	n, err := q.EventRecipientInitializeReceipt(ctx, tx, sqlc.EventRecipientInitializeReceiptParams{
		ID: work.ID, ClaimToken: mustPgUUID(work.ClaimToken),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	for _, recipient := range work.RecipientSnapshot {
		encoded, err := json.Marshal(recipient)
		if err != nil {
			return err
		}
		progress := work.RecipientProgress[recipient.ID]
		if progress.State == "" {
			progress.State = PublishedEventRecipientPending
		}
		if err := q.EventRecipientInsert(ctx, tx, sqlc.EventRecipientInsertParams{
			OutboxID: work.ID, SubscriptionID: recipient.ID, AppID: mustPgUUID(recipient.AppID),
			Recipient: encoded, State: progress.State, TotalAttempts: int32(progress.Attempts), CapacityDeferrals: int32(progress.CapacityDeferrals), AvailableAt: pgtypeFromTime(eventAdoptionAvailableAt(progress, now)),
		}); err != nil {
			return fmt.Errorf("initialize event recipient %s: %w", recipient.ID, err)
		}
	}
	if err := q.EventRecipientSettleReceipt(ctx, tx, work.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// includeWorkflows defaults to true. Schedulers pass false while the workflow
// runtime is disabled so waiting workflows do not spend routing attempts.
func (s *PgStore) ClaimDuePublishedEventRecipient(ctx context.Context, now time.Time, includeWorkflows ...bool) (*PublishedEventRecipientWork, error) {
	row, err := sqlc.New().EventRecipientClaim(ctx, s.pool, sqlc.EventRecipientClaimParams{
		NowAt: pgtypeFromTime(now), IncludeWorkflows: len(includeWorkflows) == 0 || includeWorkflows[0],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("claim event recipient: %w", err)
	}
	work := &PublishedEventRecipientWork{
		AcceptedAt: timeFromPgtype(row.AcceptedAt), OutboxID: row.OutboxID, Payload: row.Payload, State: "processing",
		ClaimToken: uuidFromPgtype(row.ClaimToken).String(), Generation: row.Generation,
		Attempts: int(row.Attempts), TotalAttempts: int(row.TotalAttempts),
		CapacityDeferrals: int(row.CapacityDeferrals), GenerationCapacityDeferrals: int(row.GenerationCapacityDeferrals),
		AvailableAt: timeFromPgtype(row.AvailableAt), LeaseUntil: timeFromPgtype(row.LeaseUntil),
	}
	if len(row.PreviousProgress) > 0 {
		if err := json.Unmarshal(row.PreviousProgress, &work.PreviousProgress); err != nil {
			return nil, err
		}
	}
	if row.BackfillJobID.Valid {
		work.BackfillJobID = uuidFromPgtype(row.BackfillJobID).String()
	}
	if err := json.Unmarshal(row.Recipient, &work.Recipient); err != nil {
		return nil, fmt.Errorf("decode claimed event recipient: %w", err)
	}
	return work, nil
}

func (s *PgStore) FinishPublishedEventRecipient(ctx context.Context, work *PublishedEventRecipientWork, progress PublishedEventRecipientProgress, next time.Time) error {
	controlDeferred := progress.State == PublishedEventRecipientPending && progress.DeliveryControlReason != "" || progress.State == PublishedEventRecipientFiltered && progress.FilterReason == "schema_version_mismatch" || progress.FailureCode == EventFanoutFailureCodeDeliveryExpired
	expected := 0
	if work != nil {
		expected = work.TotalAttempts
		if controlDeferred {
			expected--
		}
	}
	if work == nil || progress.Attempts != expected {
		return ErrConflict
	}
	if err := validatePublishedEventRecipientProgress(progress); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	if work.BackfillJobID != "" {
		if _, err := q.EventReplayBackfillLockJob(ctx, tx, mustPgUUID(work.BackfillJobID)); err != nil {
			return err
		}
	}
	// Every completion/replay locks the parent first, then its recipient. The
	// claim itself holds only a short recipient lock, never the routing work.
	if _, err := q.EventRecipientLockReceipt(ctx, tx, work.OutboxID); err != nil {
		return err
	}
	n, err := q.EventRecipientFinish(ctx, tx, sqlc.EventRecipientFinishParams{
		OutboxID: work.OutboxID, SubscriptionID: work.Recipient.ID,
		ClaimToken: mustPgUUID(work.ClaimToken), Generation: work.Generation,
		State: progress.State, AvailableAt: pgtypeFromTime(next), ControlDeferred: controlDeferred,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	action := EventFanoutAttemptActionAttempt
	if work.BackfillJobID != "" {
		action = EventFanoutAttemptActionBackfill
		updated, err := q.EventReplayBackfillFinishItem(ctx, tx, sqlc.EventReplayBackfillFinishItemParams{
			JobID: mustPgUUID(work.BackfillJobID), OutboxID: work.OutboxID, State: progress.State,
			Attempts: int32(progress.Attempts), FailureCode: progress.FailureCode, LastError: progress.LastError, Retryable: progress.Retryable,
		})
		if err != nil {
			return err
		}
		if updated == 0 {
			return ErrConflict
		}
		if err := q.EventReplayBackfillFinalize(ctx, tx, mustPgUUID(work.BackfillJobID)); err != nil {
			return err
		}
	}
	if err := recordEventRecipientOutcome(ctx, q, tx, work.OutboxID, work.Recipient.AppID, work.Recipient.ID, action, progress); err != nil {
		return err
	}
	if err := q.EventRecipientSettleReceipt(ctx, tx, work.OutboxID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func recordEventRecipientOutcome(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, outboxID int64, appID, subscriptionID, action string, progress PublishedEventRecipientProgress) error {
	encoded, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	if err := q.EventRecipientUpdateProgress(ctx, tx, sqlc.EventRecipientUpdateProgressParams{
		ID: outboxID, SubscriptionID: subscriptionID, Progress: encoded,
	}); err != nil {
		return err
	}
	return appendEventRecipientHistory(ctx, q, tx, outboxID, appID, subscriptionID, action, progress)
}

func appendEventRecipientHistory(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, outboxID int64, appID, subscriptionID, action string, progress PublishedEventRecipientProgress) error {
	return recordBoundedEventHistory(ctx, q, tx, outboxID, appID, subscriptionID, action, progress)

}

func cloneEventRecipientWork(work *PublishedEventRecipientWork) *PublishedEventRecipientWork {
	copy := *work
	copy.Payload = bytes.Clone(work.Payload)
	// The captured work policy and object notification contain nested pointers.
	encoded, _ := json.Marshal(work.Recipient)
	copy.Recipient = PublishedEventRecipient{}
	_ = json.Unmarshal(encoded, &copy.Recipient)
	return &copy
}

func (m *MemStore) InitializePublishedEventRecipients(_ context.Context, claimed *PublishedEventWork, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if claimed == nil || !claimed.SnapshotCaptured {
		return ErrConflict
	}
	for _, work := range m.eventFanout {
		if work.ID != claimed.ID {
			continue
		}
		if work.ClaimToken == "" || work.ClaimToken != claimed.ClaimToken || !work.LeaseUntil.After(now) || work.RecipientClaims {
			return ErrConflict
		}
		work.routingRecipients = make(map[string]*PublishedEventRecipientWork, len(work.RecipientSnapshot))
		if work.RecipientProgress == nil {
			work.RecipientProgress = make(map[string]PublishedEventRecipientProgress)
		}
		for _, recipient := range work.RecipientSnapshot {
			progress := work.RecipientProgress[recipient.ID]
			if progress.State == "" {
				progress.State = PublishedEventRecipientPending
			}
			work.routingRecipients[recipient.ID] = &PublishedEventRecipientWork{
				AcceptedAt: work.CreatedAt, OutboxID: work.ID, Recipient: recipient, Payload: work.Payload, State: progress.State,
				Generation: 1, TotalAttempts: progress.Attempts, CapacityDeferrals: progress.CapacityDeferrals, AvailableAt: eventAdoptionAvailableAt(progress, now),
			}
		}
		work.RecipientClaims = true
		work.ClaimToken = ""
		work.LeaseUntil = time.Time{}
		settleEventRecipientsLocked(work, now)
		return nil
	}
	return ErrNotFound
}

func (m *MemStore) ClaimDuePublishedEventRecipient(_ context.Context, now time.Time, includeWorkflows ...bool) (*PublishedEventRecipientWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var chosen *PublishedEventRecipientWork
	for _, receipt := range m.eventFanout {
		for _, work := range receipt.routingRecipients {
			if len(includeWorkflows) != 0 && !includeWorkflows[0] && len(work.Recipient.Workflow) != 0 {
				continue
			}
			if (work.State != PublishedEventRecipientPending || work.AvailableAt.After(now) && !EventSchemaVersionMismatch(work.Recipient, receipt.Payload) && !EventDeliveryExpired(work.Recipient, receipt.CreatedAt, receipt.RecipientProgress[work.Recipient.ID], now)) &&
				(work.State != "processing" || work.LeaseUntil.After(now)) {
				continue
			}
			if !EventSchemaVersionMismatch(work.Recipient, receipt.Payload) && !EventDeliveryExpired(work.Recipient, receipt.CreatedAt, receipt.RecipientProgress[work.Recipient.ID], now) && m.eventSubscriptionWaitingReasonLocked(eventRoutingAccount(receipt), work.Recipient.AppID, work.Recipient.ID, now) != "" {
				continue
			}
			recipientIndex := -1
			for index, recipient := range receipt.RecipientSnapshot {
				if recipient.ID == work.Recipient.ID {
					recipientIndex = index
					break
				}
			}
			if !EventSchemaVersionMismatch(work.Recipient, receipt.Payload) && !EventDeliveryExpired(work.Recipient, receipt.CreatedAt, receipt.RecipientProgress[work.Recipient.ID], now) && m.orderedEventRecipientBlockedLocked(receipt, recipientIndex, true) {
				continue
			}
			account := canonicalMemUUID(work.Recipient.AccountID)
			currentAccount, currentConsumer := m.eventFairTimeLocked(account, ""), m.eventFairTimeLocked(account, work.Recipient.ID)
			chosenAccount, chosenConsumer := time.Time{}, time.Time{}
			if chosen != nil {
				a := canonicalMemUUID(chosen.Recipient.AccountID)
				chosenAccount, chosenConsumer = m.eventFairTimeLocked(a, ""), m.eventFairTimeLocked(a, chosen.Recipient.ID)
			}
			if chosen == nil || currentAccount.Before(chosenAccount) || (currentAccount.Equal(chosenAccount) && (currentConsumer.Before(chosenConsumer) || (currentConsumer.Equal(chosenConsumer) && (work.AvailableAt.Before(chosen.AvailableAt) ||
				(work.AvailableAt.Equal(chosen.AvailableAt) && (work.OutboxID < chosen.OutboxID ||
					(work.OutboxID == chosen.OutboxID && work.Recipient.ID < chosen.Recipient.ID))))))) {
				chosen = work
			}
		}
	}
	if chosen == nil {
		return nil, ErrNotFound
	}
	m.recordEventFairClaimLocked(canonicalMemUUID(chosen.Recipient.AccountID), chosen.Recipient.ID)
	chosen.State = "processing"
	chosen.ClaimToken = uuid.NewString()
	chosen.LeaseUntil = now.Add(PublishedEventLease)
	chosen.Attempts++
	chosen.TotalAttempts++
	chosen.PreviousProgress = m.routingReceiptLocked(chosen.OutboxID).RecipientProgress[chosen.Recipient.ID]
	return cloneEventRecipientWork(chosen), nil
}

func (m *MemStore) FinishPublishedEventRecipient(_ context.Context, claimed *PublishedEventRecipientWork, progress PublishedEventRecipientProgress, next time.Time) error {
	controlDeferred := progress.State == PublishedEventRecipientPending && progress.DeliveryControlReason != "" || progress.State == PublishedEventRecipientFiltered && progress.FilterReason == "schema_version_mismatch" || progress.FailureCode == EventFanoutFailureCodeDeliveryExpired
	expected := 0
	if claimed != nil {
		expected = claimed.TotalAttempts
		if controlDeferred {
			expected--
		}
	}
	if claimed == nil || progress.Attempts != expected {
		return ErrConflict
	}
	if err := validatePublishedEventRecipientProgress(progress); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, receipt := range m.eventFanout {
		if receipt.ID != claimed.OutboxID {
			continue
		}
		work := receipt.routingRecipients[claimed.Recipient.ID]
		if work == nil || work.State != "processing" || work.ClaimToken != claimed.ClaimToken ||
			work.Generation != claimed.Generation || !work.LeaseUntil.After(progress.UpdatedAt) {
			return ErrConflict
		}
		if controlDeferred {
			work.Attempts--
			work.TotalAttempts--
		}
		work.State = progress.State
		work.AvailableAt = next
		work.ClaimToken = ""
		work.LeaseUntil = time.Time{}
		receipt.RecipientProgress[claimed.Recipient.ID] = progress
		m.appendEventFanoutAttemptLocked(receipt, claimed.Recipient.ID, EventFanoutAttemptActionAttempt, progress)
		settleEventRecipientsLocked(receipt, progress.UpdatedAt)
		return nil
	}
	return ErrNotFound
}

func settleEventRecipientsLocked(receipt *PublishedEventWork, now time.Time) {
	receipt.Delivered = true
	for _, work := range receipt.routingRecipients {
		if work.State == PublishedEventRecipientPending || work.State == "processing" {
			receipt.Delivered = false
			break
		}
	}
	if receipt.Delivered {
		receipt.DeliveredAt = now
	} else {
		receipt.DeliveredAt = time.Time{}
	}
}

func resetEventRecipientForReplay(receipt *PublishedEventWork, subscriptionID string, now time.Time) {
	work := receipt.routingRecipients[subscriptionID]
	work.State = PublishedEventRecipientPending
	work.Generation++
	work.Attempts = 0
	work.GenerationCapacityDeferrals = 0
	work.AvailableAt = now
	work.ClaimToken = ""
	work.LeaseUntil = time.Time{}
}

func (s *PgStore) replayClaimedEventRecipient(ctx context.Context, accountID, appID, source, eventID, subscriptionID string, override ...bool) (bool, error) {
	jobID, err := sqlc.New().EventReplayBackfillRecipientJob(ctx, s.pool, sqlc.EventReplayBackfillRecipientJobParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EventSource: source, EventID: eventID, SubscriptionID: subscriptionID,
	})
	if err == nil {
		_, err = s.retryFailedEventReplayBackfill(ctx, accountID, uuidString(jobID), 1, backfillRetryTarget{AppID: appID, Source: source, EventID: eventID, SubscriptionID: subscriptionID, AllowExpired: len(override) > 0 && override[0]})
		return true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return true, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	row, err := q.EventRecipientReplayReceipt(ctx, tx, sqlc.EventRecipientReplayReceiptParams{
		AccountID: mustPgUUID(accountID), AppID: appID, EventSource: source,
		EventID: eventID, SubscriptionID: subscriptionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return true, ErrNotFound
	}
	if err != nil {
		return true, err
	}
	if !row.RecipientClaims {
		return false, nil
	}
	if err := replayEventRecipientTx(ctx, q, tx, row.ID, appID, subscriptionID, row.Progress, true, time.Now().UTC(), override...); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func replayEventRecipientTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, outboxID int64, appID, subscriptionID string, encoded []byte, recipientClaims bool, now time.Time, override ...bool) error {
	var prior PublishedEventRecipientProgress
	if err := json.Unmarshal(encoded, &prior); err != nil {
		return ErrNotFound
	}
	if prior.State != PublishedEventRecipientFailed {
		return ErrNotFound
	}
	allowExpired := len(override) > 0 && override[0]
	target, err := q.EventAgeReplayTarget(ctx, tx, sqlc.EventAgeReplayTargetParams{OutboxID: outboxID, SubscriptionID: subscriptionID})
	if err != nil {
		return err
	}
	var recipient PublishedEventRecipient
	if err := json.Unmarshal(target.Recipient, &recipient); err != nil {
		return err
	}
	if !allowExpired && EventDeliveryExpired(recipient, timeFromPgtype(target.CreatedAt), PublishedEventRecipientProgress{}, now) {
		return ErrEventDeliveryExpired
	}
	var n int64
	if recipientClaims {
		n, err = q.EventRecipientReplay(ctx, tx, sqlc.EventRecipientReplayParams{
			OutboxID: outboxID, SubscriptionID: subscriptionID, NowAt: pgtypeFromTime(now), AllowExpired: allowExpired,
		})
	} else {
		n, err = q.EventRecipientReplayLegacy(ctx, tx, sqlc.EventRecipientReplayLegacyParams{
			ID: outboxID, SubscriptionID: subscriptionID, NowAt: pgtypeFromTime(now),
		})
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	history := prior
	history.DeliveryAgeOverride = allowExpired
	if allowExpired {
		history.LastError = "operator explicitly overrode delivery age limit"
	}
	history.State = PublishedEventRecipientPending
	history.UpdatedAt = now
	if err := appendEventRecipientHistory(ctx, q, tx, outboxID, appID, subscriptionID, EventFanoutAttemptActionReplay, history); err != nil {
		return err
	}
	progress, err := json.Marshal(PublishedEventRecipientProgress{
		DeliveryAgeOverride: allowExpired, State: PublishedEventRecipientPending, Attempts: prior.Attempts, CapacityDeferrals: prior.CapacityDeferrals, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if err := q.EventRecipientUpdateProgress(ctx, tx, sqlc.EventRecipientUpdateProgressParams{
		ID: outboxID, SubscriptionID: subscriptionID, Progress: progress,
	}); err != nil {
		return err
	}
	if recipientClaims {
		return q.EventRecipientSettleReceipt(ctx, tx, outboxID)
	}
	return nil
}

// ReplayRetryablePublishedEventRecipientsForApp atomically requeues the oldest
// bounded failures across legacy and recipient-owned receipts. Only legacy
// receipts wait for a whole-event routing claim to settle.
func (s *PgStore) ReplayRetryablePublishedEventRecipientsForApp(ctx context.Context, accountID, appID, source, eventID string, limit int) (EventFanoutReplayBatch, error) {
	limit = normalizeEventFanoutReplayLimit(limit)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EventFanoutReplayBatch{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	rows, err := q.EventRecipientReplayCandidates(ctx, tx, sqlc.EventRecipientReplayCandidatesParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EventSource: source,
		EventID: eventID, PageLimit: int32(limit),
	})
	if err != nil {
		return EventFanoutReplayBatch{}, err
	}
	for _, row := range rows {
		if err := replayEventRecipientTx(ctx, q, tx, row.ID, appID, row.SubscriptionID, row.Progress, row.RecipientClaims, time.Now().UTC()); err != nil {
			return EventFanoutReplayBatch{}, err
		}
	}
	result := EventFanoutReplayBatch{Replayed: len(rows)}
	result.HasMore, err = q.EventRecipientReplayHasMore(ctx, tx, sqlc.EventRecipientReplayHasMoreParams{
		AccountID: mustPgUUID(accountID), AppID: appID, EventSource: source, EventID: eventID,
	})
	if err != nil {
		return EventFanoutReplayBatch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EventFanoutReplayBatch{}, err
	}
	return result, nil
}
