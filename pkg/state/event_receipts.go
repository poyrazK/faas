package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const EventReceiptMaxRecipients = api.EventReceiptPageMax

type EventReceiptCursor struct{ OutboxID, Position int64 }

// EventReceiptStore reads one account/source/ID identity. Cursor positions
// refer to immutable acceptance order, never mutable delivery timestamps.
type EventReceiptStore interface {
	EventReceipt(context.Context, string, string, string, EventReceiptCursor, int) (EventReceipt, error)
}

type EventReceipt struct {
	invocationAccountID                            string
	OutboxID                                       int64
	EventID, EventSource, EventType, SchemaVersion string
	AcceptedAt                                     time.Time
	RoutingSettledAt, RetainUntil                  *time.Time
	SnapshotCaptured, RecipientClaims              bool
	RecipientCount                                 int
	RoutingSummary                                 map[string]int
	Recipients                                     []EventReceiptRecipient
	NextPosition                                   int64
}

type EventReceiptRouting struct {
	State              string     `json:"state"`
	Attempts           int        `json:"attempts"`
	Generation         *int64     `json:"generation,omitempty"`
	GenerationAttempts *int       `json:"generation_attempts,omitempty"`
	NextAttemptAt      *time.Time `json:"next_attempt_at,omitempty"`
	LeaseUntil         *time.Time `json:"lease_until,omitempty"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	FailureCode        string     `json:"failure_code,omitempty"`
	Retryable          bool       `json:"retryable"`
	ReplayCount        int64      `json:"replay_count"`
	LastReplayedAt     *time.Time `json:"last_replayed_at,omitempty"`
}

type EventReceiptExecution struct {
	InvocationID             string     `json:"invocation_id"`
	State                    string     `json:"state"`
	Attempts                 int        `json:"attempts"`
	ReplayGeneration         int64      `json:"replay_generation"`
	NextAttemptAt            *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
	LastError                string     `json:"last_error,omitempty"`
	ReplayedFromInvocationID string     `json:"replayed_from_invocation_id,omitempty"`
}

type EventReceiptRecovery struct {
	RetainedReplayCount int64
	LatestReplay        *EventReceiptExecution
}

type EventReceiptCancellation struct {
	ReceiptID      string    `json:"receipt_id"`
	CancelledCount int64     `json:"cancelled_count"`
	CreatedAt      time.Time `json:"created_at"`
}

type EventReceiptRecipient struct {
	Position                       int64
	SubscriptionID, AppID, AppSlug string
	Routing                        EventReceiptRouting
	Execution                      *EventReceiptExecution
	Recovery                       *EventReceiptRecovery
	Cancellation                   *EventReceiptCancellation
	ExecutionUnavailable           string
	TargetAvailable                bool
	RoutingReplayEligible          bool
	HandlerReplayMode              string
	HandlerReplayInvocationID      string
}

func receiptLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return min(limit, EventReceiptMaxRecipients)
}

func receiptRetention(receipt *EventReceipt) {
	if receipt.RoutingSettledAt != nil {
		until := receipt.RoutingSettledAt.Add(PublishedEventIdentityRetention)
		receipt.RetainUntil = &until
	}
}

func (s *PgStore) EventReceipt(ctx context.Context, accountID, source, eventID string, cursor EventReceiptCursor, limit int) (EventReceipt, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return EventReceipt{}, fmt.Errorf("begin event receipt read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	meta, err := q.EventReceiptMetadata(ctx, tx, sqlc.EventReceiptMetadataParams{AccountID: mustPgUUID(accountID), EventSource: source, EventID: eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return EventReceipt{}, ErrNotFound
	}
	if err != nil {
		return EventReceipt{}, fmt.Errorf("read event receipt: %w", err)
	}
	if cursor.Position < 0 || (cursor.OutboxID != 0 && cursor.OutboxID != meta.ID) {
		return EventReceipt{}, ErrConflict
	}
	accountID = uuidFromPgtype(meta.AccountID).String()
	receipt := EventReceipt{invocationAccountID: meta.InvocationAccountID, OutboxID: meta.ID, EventID: meta.EventID, EventSource: meta.Source, EventType: meta.EventType,
		SchemaVersion: meta.SchemaVersion, AcceptedAt: timeFromPgtype(meta.CreatedAt), RoutingSettledAt: timestamptzToTimePtr(meta.DeliveredAt),
		SnapshotCaptured: meta.SnapshotCaptured, RecipientClaims: meta.RecipientClaims, RecipientCount: int(meta.RecipientCount),
		Recipients: make([]EventReceiptRecipient, 0)}
	if err := json.Unmarshal(meta.RoutingSummary, &receipt.RoutingSummary); err != nil {
		return EventReceipt{}, fmt.Errorf("decode receipt summary: %w", err)
	}
	receiptRetention(&receipt)
	limit = receiptLimit(limit)
	rows, err := q.EventReceiptRecipients(ctx, tx, sqlc.EventReceiptRecipientsParams{OutboxID: meta.ID, AccountID: meta.AccountID, AfterPosition: cursor.Position, PageLimit: int32(limit + 1)})
	if err != nil {
		return EventReceipt{}, fmt.Errorf("read receipt recipients: %w", err)
	}
	if len(rows) > limit {
		receipt.NextPosition = rows[limit-1].SPosition
		rows = rows[:limit]
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		var recipient PublishedEventRecipient
		var progress PublishedEventRecipientProgress
		if err := json.Unmarshal(row.SRecipient, &recipient); err != nil {
			return EventReceipt{}, fmt.Errorf("decode receipt recipient: %w", err)
		}
		if err := json.Unmarshal(row.Progress, &progress); err != nil {
			return EventReceipt{}, fmt.Errorf("decode receipt progress: %w", err)
		}
		entry := EventReceiptRecipient{Position: row.SPosition, SubscriptionID: recipient.ID, AppID: recipient.AppID, AppSlug: row.AppSlug, TargetAvailable: row.TargetAvailable,
			Routing:               receiptRouting(progress, row.RoutingState, int(row.RoutingAttempts)),
			RoutingReplayEligible: row.TargetAvailable && row.RoutingState == PublishedEventRecipientFailed && (meta.RecipientClaims || meta.State == "delivered")}
		entry.Routing.NextAttemptAt, entry.Routing.LeaseUntil = timestamptzToTimePtr(row.NextAttemptAt), timestamptzToTimePtr(row.LeaseUntil)
		entry.Routing.ReplayCount, entry.Routing.LastReplayedAt = row.ReplayCount, timestamptzToTimePtr(row.LastReplayedAt)
		if meta.RecipientClaims {
			cycle := int(row.GenerationAttempts)
			entry.Routing.Generation, entry.Routing.GenerationAttempts = &row.Generation, &cycle
		}
		receipt.Recipients = append(receipt.Recipients, entry)
		ids = append(ids, mustPgUUID(PublishedEventInvocationID(meta.InvocationAccountID, source, eventID, recipient.ID)))
	}
	if err := enrichPgEventReceipt(ctx, q, tx, &receipt, accountID, ids); err != nil {
		return EventReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EventReceipt{}, fmt.Errorf("commit event receipt read: %w", err)
	}
	return receipt, nil
}

func receiptRouting(progress PublishedEventRecipientProgress, status string, attempts int) EventReceiptRouting {
	routing := EventReceiptRouting{State: status, Attempts: attempts, LastError: progress.LastError, FailureCode: progress.FailureCode, Retryable: progress.Retryable}
	if !progress.UpdatedAt.IsZero() {
		at := progress.UpdatedAt
		routing.UpdatedAt = &at
	}
	return routing
}

func enrichPgEventReceipt(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, receipt *EventReceipt, accountID string, ids []pgtype.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	acceptedAt := pgtype.Timestamptz{Time: receipt.AcceptedAt, Valid: true}
	invocations, err := q.EventReceiptInvocations(ctx, tx, sqlc.EventReceiptInvocationsParams{AccountID: mustPgUUID(accountID), InvocationIds: ids, AcceptedAt: acceptedAt})
	if err != nil {
		return fmt.Errorf("read receipt executions: %w", err)
	}
	byID := make(map[string]sqlc.EventReceiptInvocationsRow, len(invocations))
	for _, invocation := range invocations {
		byID[uuidFromPgtype(invocation.ID).String()] = invocation
	}
	cancellations, err := q.EventReceiptCancellations(ctx, tx, sqlc.EventReceiptCancellationsParams{AccountID: mustPgUUID(accountID), InvocationIds: ids, AcceptedAt: acceptedAt})
	if err != nil {
		return fmt.Errorf("read receipt cancellations: %w", err)
	}
	cancelByID := make(map[string]sqlc.EventReceiptCancellationsRow, len(cancellations))
	for _, cancellation := range cancellations {
		cancelByID[uuidFromPgtype(cancellation.ID).String()] = cancellation
	}
	for i := range receipt.Recipients {
		entry := &receipt.Recipients[i]
		id := PublishedEventInvocationID(receipt.invocationAccountID, receipt.EventSource, receipt.EventID, entry.SubscriptionID)
		if invocation, ok := byID[id]; ok && uuidFromPgtype(invocation.AppID).String() == entry.AppID {
			entry.Execution = receiptExecution(id, invocation.State, int(invocation.Attempts), invocation.ReplayGeneration, timeFromPgtype(invocation.DueAt), timeFromPgtype(invocation.CreatedAt), timestamptzToTimePtr(invocation.CompletedAt), invocation.LastError)
			if entry.TargetAvailable {
				entry.HandlerReplayMode = receiptHandlerReplay(invocation.State, invocation.WorkPolicyName, invocation.QueueBindingID.Valid, entry.AppSlug)
			}
		} else if cancellation, ok := cancelByID[id]; ok && uuidFromPgtype(cancellation.AppID).String() == entry.AppID {
			entry.Cancellation = &EventReceiptCancellation{ReceiptID: id, CancelledCount: cancellation.CancelledCount, CreatedAt: timeFromPgtype(cancellation.CreatedAt)}
			entry.ExecutionUnavailable = "cancel_pending"
		} else {
			receiptMissingExecution(entry)
		}
	}
	return enrichPgEventReceiptReplays(ctx, q, tx, receipt, accountID, ids)
}

func receiptExecution(id, status string, attempts int, generation int64, dueAt, createdAt time.Time, completedAt *time.Time, lastError string) *EventReceiptExecution {
	execution := &EventReceiptExecution{InvocationID: id, State: status, Attempts: attempts, ReplayGeneration: generation, CreatedAt: createdAt, CompletedAt: completedAt, LastError: lastError}
	if status == string(InvocationPending) {
		execution.NextAttemptAt = &dueAt
	}
	return execution
}

func receiptHandlerReplay(status, policy string, bound bool, slug string) string {
	if slug == "" {
		return ""
	}
	if status == string(InvocationDeadLetter) {
		return "dead_letter_replay"
	}
	if status == string(InvocationFailed) && policy == "" && !bound {
		return "handler_replay"
	}
	return ""
}

func receiptMissingExecution(entry *EventReceiptRecipient) {
	entry.ExecutionUnavailable = "not_enqueued"
	if entry.Routing.State == PublishedEventRecipientEnqueued {
		entry.ExecutionUnavailable = "record_unavailable"
	}
}

func (m *MemStore) EventReceipt(_ context.Context, accountID, source, eventID string, cursor EventReceiptCursor, limit int) (EventReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.eventFanout[canonicalMemUUID(accountID)+"\x00"+source+"\x00"+eventID]
	if work == nil {
		return EventReceipt{}, ErrNotFound
	}
	if cursor.Position < 0 || (cursor.OutboxID != 0 && cursor.OutboxID != work.ID) {
		return EventReceipt{}, ErrConflict
	}
	var envelope publishedEventIdentity
	if err := json.Unmarshal(work.Payload, &envelope); err != nil {
		return EventReceipt{}, err
	}
	var identity struct {
		AccountID       string `json:"accountid"`
		LegacyAccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(work.Payload, &identity); err != nil {
		return EventReceipt{}, fmt.Errorf("decode receipt invocation identity: %w", err)
	}
	invocationAccountID := identity.AccountID
	if invocationAccountID == "" {
		invocationAccountID = identity.LegacyAccountID
	}
	if invocationAccountID == "" {
		invocationAccountID = canonicalMemUUID(accountID)
	}
	receipt := EventReceipt{invocationAccountID: invocationAccountID, OutboxID: work.ID, EventID: eventID, EventSource: source, EventType: envelope.Type, SchemaVersion: envelope.SchemaVersion,
		AcceptedAt: work.CreatedAt, SnapshotCaptured: work.SnapshotCaptured, RecipientClaims: work.RecipientClaims,
		RecipientCount: len(work.RecipientSnapshot), RoutingSummary: map[string]int{}, Recipients: make([]EventReceiptRecipient, 0)}
	if work.Delivered {
		at := work.DeliveredAt
		receipt.RoutingSettledAt = &at
	}
	receiptRetention(&receipt)
	limit = receiptLimit(limit)
	for i, recipient := range work.RecipientSnapshot {
		progress := work.RecipientProgress[recipient.ID]
		status, attempts := progress.State, progress.Attempts
		if status == "" {
			status = PublishedEventRecipientPending
		}
		own := work.routingRecipients[recipient.ID]
		if own != nil {
			status, attempts = own.State, own.TotalAttempts
		}
		receipt.RoutingSummary[status]++
		position := int64(i + 1)
		if position <= cursor.Position {
			continue
		}
		if len(receipt.Recipients) == limit {
			receipt.NextPosition = receipt.Recipients[len(receipt.Recipients)-1].Position
			continue
		}
		entry := EventReceiptRecipient{Position: position, SubscriptionID: recipient.ID, AppID: recipient.AppID, Routing: receiptRouting(progress, status, attempts)}
		app, exists := m.apps[recipient.AppID]
		if !exists {
			app, exists = m.apps[canonicalMemUUID(recipient.AppID)]
		}
		owned := exists && sameMemUUID(app.AccountID, accountID)
		if owned {
			entry.AppSlug = app.Slug
		}
		entry.TargetAvailable = owned && app.Status != AppDeleted
		entry.RoutingReplayEligible = owned && app.Status != AppDeleted && status == PublishedEventRecipientFailed && (work.RecipientClaims || work.Delivered)
		if own != nil {
			generation, cycle := own.Generation, own.Attempts
			entry.Routing.Generation, entry.Routing.GenerationAttempts = &generation, &cycle
			if own.State == PublishedEventRecipientPending {
				at := own.AvailableAt
				entry.Routing.NextAttemptAt = &at
			}
			if !own.LeaseUntil.IsZero() {
				at := own.LeaseUntil
				entry.Routing.LeaseUntil = &at
			}
		} else if status == PublishedEventRecipientPending && !work.Delivered && work.ClaimToken == "" {
			at := work.AvailableAt
			entry.Routing.NextAttemptAt = &at
		}
		for _, attempt := range m.eventFanoutAttempts {
			if attempt.OutboxID == work.ID && attempt.SubscriptionID == recipient.ID && attempt.Action == EventFanoutAttemptActionReplay {
				entry.Routing.ReplayCount++
				at := attempt.OccurredAt
				if entry.Routing.LastReplayedAt == nil || at.After(*entry.Routing.LastReplayedAt) {
					entry.Routing.LastReplayedAt = &at
				}
			}
		}
		id := PublishedEventInvocationID(invocationAccountID, source, eventID, recipient.ID)
		if invocation, ok := m.invocations[id]; ok && owned && sameMemUUID(invocation.AccountID, accountID) && sameMemUUID(invocation.AppID, recipient.AppID) && !invocation.CreatedAt.Before(work.CreatedAt) {
			entry.Execution = receiptExecution(id, string(invocation.State), invocation.Attempts, invocation.ReplayGeneration, invocation.DueAt, invocation.CreatedAt, cloneEventReceiptTime(invocation.CompletedAt), invocation.LastError)
			if entry.TargetAvailable {
				entry.HandlerReplayMode = receiptHandlerReplay(string(invocation.State), invocation.WorkPolicyName, invocation.QueueBindingID != "", entry.AppSlug)
			}
		} else if cancellation, ok := m.workCancellations[id]; ok && owned && sameMemUUID(cancellation.AppID, recipient.AppID) && !cancellation.CreatedAt.Before(work.CreatedAt) {
			entry.Cancellation = &EventReceiptCancellation{ReceiptID: id, CancelledCount: cancellation.CancelledCount, CreatedAt: cancellation.CreatedAt}
			entry.ExecutionUnavailable = "cancel_pending"
		} else {
			receiptMissingExecution(&entry)
		}
		m.enrichMemEventReceiptReplays(&entry, accountID, id, owned, receipt.AcceptedAt)
		receipt.Recipients = append(receipt.Recipients, entry)
	}
	return receipt, nil
}

func cloneEventReceiptTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// EventReceiptAcceptanceStore returns the first durable acceptance timestamp,
// including on identical publish retries.
type EventReceiptAcceptanceStore interface {
	EventReceiptAcceptedAt(context.Context, string, string, string) (time.Time, error)
}

func (s *PgStore) EventReceiptAcceptedAt(ctx context.Context, accountID, source, eventID string) (time.Time, error) {
	at, err := sqlc.New().EventReceiptAcceptedAt(ctx, s.pool, sqlc.EventReceiptAcceptedAtParams{AccountID: mustPgUUID(accountID), EventSource: source, EventID: eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read event acceptance: %w", err)
	}
	return timeFromPgtype(at), nil
}

func (m *MemStore) EventReceiptAcceptedAt(_ context.Context, accountID, source, eventID string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.eventFanout[canonicalMemUUID(accountID)+"\x00"+source+"\x00"+eventID]
	if work == nil {
		return time.Time{}, ErrNotFound
	}
	return work.CreatedAt, nil
}
