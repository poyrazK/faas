package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InvocationClaim identifies one lease, including across in-place replay.
type InvocationClaim struct {
	Attempt          int
	ReplayGeneration int64
}

type InvocationClaimCompletionStore interface {
	CompleteInvocationClaim(context.Context, string, InvocationClaim, json.RawMessage) error
}

func (s *PgStore) CompleteInvocationClaim(ctx context.Context, id string, claim InvocationClaim, result json.RawMessage) error {
	return s.completeInvocation(ctx, id, claim.Attempt, &claim, result)
}

func (m *MemStore) CompleteInvocationClaim(_ context.Context, id string, claim InvocationClaim, result json.RawMessage) error {
	return m.completeInvocation(id, 0, &claim, result)
}

func lockInvocationAttemptClaim(ctx context.Context, tx pgx.Tx, id string, claim InvocationClaim) error {
	if claim.Attempt <= 0 || claim.Attempt > math.MaxInt32 || claim.ReplayGeneration < 0 {
		return ErrNotFound
	}
	_, err := sqlc.New().LockInvocationAttemptClaim(ctx, tx, sqlc.LockInvocationAttemptClaimParams{
		ID: mustPgUUID(id), Attempt: int32(claim.Attempt), ReplayGeneration: claim.ReplayGeneration,
	})
	if err != nil {
		return fmt.Errorf("state: lock invocation attempt claim: %w", mapErr(err))
	}
	return nil
}

func validInvocationAttemptClaim(inv Invocation, claim InvocationClaim) bool {
	return claim.Attempt > 0 && inv.State == InvocationDispatching && inv.Attempts == claim.Attempt &&
		inv.ReplayGeneration == claim.ReplayGeneration && inv.LeaseExpiresAt != nil && inv.LeaseExpiresAt.After(time.Now())
}

// An attempt records dispatch ownership, not proof that application side
// effects ran. Final outcomes are immutable; expired leases are unknown.
type InvocationAttempt struct {
	ID               int64      `json:"id"`
	InvocationID     string     `json:"invocation_id"`
	ReplayGeneration int64      `json:"replay_generation"`
	Attempt          int        `json:"attempt"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	Outcome          string     `json:"outcome"`
	ErrorDetail      string     `json:"error_detail,omitempty"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	RetainUntil      time.Time  `json:"retain_until"`
}

type retainedInvocationAttempt struct {
	InvocationAttempt
	accountID, appID, rootID string
	rootCreatedAt            time.Time
}

type EventReceiptAttemptCursor struct{ OutboxID, AfterID int64 }
type EventReceiptAttemptHistory struct {
	OutboxID                                                   int64
	EventSource, EventID, SubscriptionID, OriginalInvocationID string
	Attempts                                                   []InvocationAttempt
	NextCursor                                                 EventReceiptAttemptCursor
}

type EventReceiptAttemptStore interface {
	EventReceiptAttempts(context.Context, string, string, string, string, EventReceiptAttemptCursor, int) (EventReceiptAttemptHistory, error)
}
type InvocationAttemptRetentionStore interface {
	PruneInvocationAttemptHistory(context.Context, time.Time, int) (int, error)
}

func validAttemptCursor(cursor EventReceiptAttemptCursor, outboxID int64) bool {
	return cursor.OutboxID == 0 && cursor.AfterID == 0 || cursor.OutboxID == outboxID && cursor.AfterID > 0
}

func (s *PgStore) EventReceiptAttempts(ctx context.Context, accountID, source, eventID, subscriptionID string, cursor EventReceiptAttemptCursor, limit int) (EventReceiptAttemptHistory, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return EventReceiptAttemptHistory{}, fmt.Errorf("begin attempt history: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	meta, err := q.EventReceiptMetadata(ctx, tx, sqlc.EventReceiptMetadataParams{AccountID: mustPgUUID(accountID), EventSource: source, EventID: eventID})
	if err != nil {
		return EventReceiptAttemptHistory{}, mapErr(err)
	}
	if !validAttemptCursor(cursor, meta.ID) {
		return EventReceiptAttemptHistory{}, ErrConflict
	}
	appID, err := q.EventReceiptReplayTarget(ctx, tx, sqlc.EventReceiptReplayTargetParams{OutboxID: meta.ID, AccountID: meta.AccountID, SubscriptionID: subscriptionID})
	if err != nil {
		return EventReceiptAttemptHistory{}, mapErr(err)
	}
	root := PublishedEventInvocationID(meta.InvocationAccountID, source, eventID, subscriptionID)
	rows, err := q.EventReceiptAttemptHistory(ctx, tx, sqlc.EventReceiptAttemptHistoryParams{
		AccountID: meta.AccountID, AppID: appID, RootInvocationID: mustPgUUID(root), AcceptedAt: meta.CreatedAt,
		AfterID: cursor.AfterID, PageLimit: int32(receiptLimit(limit) + 1),
	})
	if err != nil {
		return EventReceiptAttemptHistory{}, fmt.Errorf("read attempt history: %w", err)
	}
	history := EventReceiptAttemptHistory{OutboxID: meta.ID, EventSource: source, EventID: eventID, SubscriptionID: subscriptionID, OriginalInvocationID: root, Attempts: make([]InvocationAttempt, 0, len(rows))}
	for _, row := range rows {
		history.Attempts = append(history.Attempts, InvocationAttempt{ID: row.ID, InvocationID: uuidString(row.InvocationID),
			ReplayGeneration: row.ReplayGeneration, Attempt: int(row.Attempt), StartedAt: timeFromPgtype(row.StartedAt),
			FinishedAt: timestamptzToTimePtr(row.FinishedAt), Outcome: row.Outcome, ErrorDetail: row.ErrorDetail,
			NextAttemptAt: timestamptzToTimePtr(row.NextAttemptAt), RetainUntil: timeFromPgtype(row.RetainUntil)})
	}
	pageReceiptAttempts(&history, receiptLimit(limit))
	if err := tx.Commit(ctx); err != nil {
		return EventReceiptAttemptHistory{}, fmt.Errorf("commit attempt history: %w", err)
	}
	return history, nil
}

func pageReceiptAttempts(history *EventReceiptAttemptHistory, limit int) {
	if len(history.Attempts) > limit {
		history.NextCursor = EventReceiptAttemptCursor{OutboxID: history.OutboxID, AfterID: history.Attempts[limit-1].ID}
		history.Attempts = history.Attempts[:limit]
	}
}

func (m *MemStore) EventReceiptAttempts(ctx context.Context, accountID, source, eventID, subscriptionID string, cursor EventReceiptAttemptCursor, limit int) (EventReceiptAttemptHistory, error) {
	receipt, err := m.EventReceipt(ctx, accountID, source, eventID, EventReceiptCursor{}, 1)
	if err != nil {
		return EventReceiptAttemptHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.eventFanout[canonicalMemUUID(accountID)+"\x00"+source+"\x00"+eventID]
	if work == nil || work.ID != receipt.OutboxID {
		return EventReceiptAttemptHistory{}, ErrNotFound
	}
	if !validAttemptCursor(cursor, work.ID) {
		return EventReceiptAttemptHistory{}, ErrConflict
	}
	appID := ""
	for _, recipient := range work.RecipientSnapshot {
		if recipient.ID == subscriptionID {
			appID = recipient.AppID
			break
		}
	}
	app, ok := m.eventSubscriptionAppLocked(appID)
	if !ok || !sameMemUUID(app.AccountID, accountID) {
		return EventReceiptAttemptHistory{}, ErrNotFound
	}
	root := PublishedEventInvocationID(receipt.invocationAccountID, source, eventID, subscriptionID)
	history := EventReceiptAttemptHistory{OutboxID: work.ID, EventSource: source, EventID: eventID, SubscriptionID: subscriptionID, OriginalInvocationID: root, Attempts: make([]InvocationAttempt, 0)}
	for _, row := range m.invocationAttemptHistory {
		if sameMemUUID(row.accountID, accountID) && sameMemUUID(row.appID, appID) && sameMemUUID(row.rootID, root) && !row.rootCreatedAt.Before(receipt.AcceptedAt) && (cursor.AfterID == 0 || row.ID < cursor.AfterID) {
			attempt := row.InvocationAttempt
			attempt.FinishedAt, attempt.NextAttemptAt = cloneEventReceiptTime(attempt.FinishedAt), cloneEventReceiptTime(attempt.NextAttemptAt)
			history.Attempts = append(history.Attempts, attempt)
		}
	}
	sort.Slice(history.Attempts, func(i, j int) bool { return history.Attempts[i].ID > history.Attempts[j].ID })
	pageReceiptAttempts(&history, receiptLimit(limit))
	return history, nil
}

// Centralize memory writes to mirror the PostgreSQL transition trigger.
func (m *MemStore) setInvocationLocked(id string, inv Invocation) {
	old := m.invocations[id]
	m.invocations[id] = cloneInvocationWorkEnvelope(inv)
	if inv.Source != InvocationAsyncInvoke && inv.Source != InvocationReplay {
		return
	}
	now := time.Now().UTC()
	changed := old.State != inv.State || old.Attempts != inv.Attempts || old.ReplayGeneration != inv.ReplayGeneration
	if !changed {
		return
	}
	if old.State == InvocationDispatching {
		for key, row := range m.invocationAttemptHistory {
			if row.InvocationID != id || row.ReplayGeneration != old.ReplayGeneration || row.Attempt != old.Attempts || row.Outcome != "running" {
				continue
			}
			row.Outcome = invocationAttemptOutcome(old, inv)
			finished := now
			if finished.Before(row.StartedAt) {
				finished = row.StartedAt
			}
			row.FinishedAt = &finished
			detail := []rune(inv.LastError)
			row.ErrorDetail = string(detail[:min(len(detail), 1024)])
			if inv.State == InvocationPending {
				row.NextAttemptAt = cloneEventReceiptTime(&inv.DueAt)
			}
			row.RetainUntil = now.Add(30 * 24 * time.Hour)
			if inv.ResultRetentionUntil != nil && inv.ResultRetentionUntil.Before(row.RetainUntil) {
				row.RetainUntil = *inv.ResultRetentionUntil
			}
			m.invocationAttemptHistory[key] = row
		}
	}
	if inv.State == InvocationDispatching && inv.Attempts > 0 {
		if m.invocationAttemptHistory == nil {
			m.invocationAttemptHistory = make(map[int64]retainedInvocationAttempt)
		}
		m.nextInvocationAttemptID++
		root, at := inv.ReplayRootInvocationID, inv.ReplayRootCreatedAt
		if root == "" || at == nil {
			root, at = inv.ID, &inv.CreatedAt
		}
		m.invocationAttemptHistory[m.nextInvocationAttemptID] = retainedInvocationAttempt{
			InvocationAttempt: InvocationAttempt{ID: m.nextInvocationAttemptID, InvocationID: inv.ID, ReplayGeneration: inv.ReplayGeneration,
				Attempt: inv.Attempts, StartedAt: now, Outcome: "running", RetainUntil: now.Add(30 * 24 * time.Hour)},
			accountID: inv.AccountID, appID: inv.AppID, rootID: root, rootCreatedAt: *at,
		}
	}
}

func invocationAttemptOutcome(old, inv Invocation) string {
	switch {
	case old.ReplayGeneration != inv.ReplayGeneration:
		return "unknown"
	case inv.State == InvocationCompleted:
		return "succeeded"
	case inv.Outcome != nil && *inv.Outcome == OutcomeUncertain:
		return "unknown"
	case inv.State == InvocationPending && inv.LastError == "dispatch lease expired; requeued":
		return "unknown"
	case inv.State == InvocationPending:
		return "retry"
	case inv.State == InvocationFailed:
		return "failed"
	case inv.State == InvocationDeadLetter:
		return "dead_letter"
	case inv.State == InvocationCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func (m *MemStore) deleteInvocationAttemptsLocked(id string) {
	for key, row := range m.invocationAttemptHistory {
		if row.InvocationID == id {
			delete(m.invocationAttemptHistory, key)
		}
	}
}

func (s *PgStore) PruneInvocationAttemptHistory(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	n, err := sqlc.New().PruneInvocationAttemptHistory(ctx, s.pool, sqlc.PruneInvocationAttemptHistoryParams{NowAt: pgtype.Timestamptz{Time: now, Valid: true}, BatchLimit: int32(limit)})
	if err != nil {
		return 0, fmt.Errorf("state: prune invocation attempt history: %w", err)
	}
	return int(n), nil
}

func (m *MemStore) PruneInvocationAttemptHistory(_ context.Context, now time.Time, limit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	ids := make([]int64, 0)
	for id, row := range m.invocationAttemptHistory {
		if row.Outcome != "running" && !row.RetainUntil.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.invocationAttemptHistory[ids[i]], m.invocationAttemptHistory[ids[j]]
		if a.RetainUntil.Equal(b.RetainUntil) {
			return ids[i] < ids[j]
		}
		return a.RetainUntil.Before(b.RetainUntil)
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		delete(m.invocationAttemptHistory, id)
	}
	return len(ids), nil
}
