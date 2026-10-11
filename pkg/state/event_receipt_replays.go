package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventReceiptReplayCursor struct {
	OutboxID     int64
	CreatedAt    time.Time
	InvocationID string
}

type EventReceiptReplayHistory struct {
	OutboxID                                                   int64
	EventSource, EventID, SubscriptionID, OriginalInvocationID string
	Replays                                                    []EventReceiptExecution
	NextCursor                                                 EventReceiptReplayCursor
}

type EventReceiptReplayStore interface {
	EventReceiptReplays(context.Context, string, string, string, string, EventReceiptReplayCursor, int) (EventReceiptReplayHistory, error)
}

func enrichPgEventReceiptReplays(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, receipt *EventReceipt, accountID string, ids []pgtype.UUID) error {
	rows, err := q.EventReceiptReplaySummaries(ctx, tx, sqlc.EventReceiptReplaySummariesParams{AccountID: mustPgUUID(accountID), InvocationIds: ids, AcceptedAt: pgtype.Timestamptz{Time: receipt.AcceptedAt, Valid: true}})
	if err != nil {
		return fmt.Errorf("read receipt replay summaries: %w", err)
	}
	byRoot := make(map[string]sqlc.EventReceiptReplaySummariesRow, len(rows))
	for _, row := range rows {
		byRoot[uuidString(row.RootID)] = row
	}
	for i := range receipt.Recipients {
		entry := &receipt.Recipients[i]
		root := PublishedEventInvocationID(receipt.invocationAccountID, receipt.EventSource, receipt.EventID, entry.SubscriptionID)
		row, ok := byRoot[root]
		if !ok || uuidString(row.AppID) != entry.AppID {
			continue
		}
		latest := receiptExecution(uuidString(row.ID), row.State, int(row.Attempts), row.ReplayGeneration, timeFromPgtype(row.DueAt), timeFromPgtype(row.CreatedAt), timestamptzToTimePtr(row.CompletedAt), row.LastError)
		latest.ReplayedFromInvocationID = uuidString(row.ReplayedFromInvocationID)
		entry.Recovery = &EventReceiptRecovery{RetainedReplayCount: row.RetainedReplayCount, LatestReplay: latest}
		entry.HandlerReplayMode = ""
		entry.HandlerReplayInvocationID = latest.InvocationID
		entry.HandlerReplayDeadLetterID = uuidString(row.DeadLetterID)
		if entry.TargetAvailable {
			entry.HandlerReplayMode = receiptHandlerReplay(row.State, row.WorkPolicyName, row.QueueBindingID.Valid, entry.AppSlug, timestamptzToTimePtr(row.WorkExpiresAt), timestamptzToTimePtr(row.StartDeadlineAt))
			if entry.HandlerReplayMode == "dead_letter_replay" && entry.HandlerReplayDeadLetterID == "" {
				entry.HandlerReplayMode = ""
			}
			if row.KeyedReplayCreated || entry.HandlerReplayMode == "handler_replay" && row.PlainReplayCreated {
				entry.HandlerReplayMode = ""
			}
		}
	}
	return nil
}

func (m *MemStore) receiptReplaysLocked(accountID, appID, root string, acceptedAt time.Time) []Invocation {
	rows := make([]Invocation, 0)
	for _, inv := range m.invocations {
		if sameMemUUID(inv.AccountID, accountID) && sameMemUUID(inv.AppID, appID) && sameMemUUID(inv.ReplayRootInvocationID, root) && inv.ReplayRootCreatedAt != nil && !inv.ReplayRootCreatedAt.Before(acceptedAt) {
			rows = append(rows, inv)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return canonicalMemUUID(rows[i].ID) > canonicalMemUUID(rows[j].ID)
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows
}

func receiptReplayExecution(inv Invocation) *EventReceiptExecution {
	execution := receiptExecution(inv.ID, string(inv.State), inv.Attempts, inv.ReplayGeneration, inv.DueAt, inv.CreatedAt, cloneEventReceiptTime(inv.CompletedAt), inv.LastError)
	execution.ReplayedFromInvocationID = inv.ReplayedFromInvocationID
	return execution
}

func (m *MemStore) enrichMemEventReceiptReplays(entry *EventReceiptRecipient, accountID, root string, owned bool, acceptedAt time.Time) {
	if !owned {
		return
	}
	rows := m.receiptReplaysLocked(accountID, entry.AppID, root, acceptedAt)
	if len(rows) == 0 {
		return
	}
	latest := rows[0]
	entry.Recovery = &EventReceiptRecovery{RetainedReplayCount: int64(len(rows)), LatestReplay: receiptReplayExecution(latest)}
	entry.HandlerReplayMode = ""
	entry.HandlerReplayInvocationID = latest.ID
	if entry.TargetAvailable {
		entry.HandlerReplayMode = receiptHandlerReplay(string(latest.State), latest.WorkPolicyName, latest.QueueBindingID != "", entry.AppSlug, latest.WorkExpiresAt, latest.StartDeadlineAt)
		if m.keyedReplayChildren[latest.ID] != "" || entry.HandlerReplayMode == "handler_replay" && m.plainReplayChildren[latest.ID].ChildID != "" {
			entry.HandlerReplayMode = ""
		}
	}
}

func validReplayCursor(cursor EventReceiptReplayCursor, outboxID int64) bool {
	if cursor.OutboxID == 0 {
		return cursor.CreatedAt.IsZero() && cursor.InvocationID == ""
	}
	_, err := uuid.Parse(cursor.InvocationID)
	return cursor.OutboxID == outboxID && !cursor.CreatedAt.IsZero() && err == nil
}

func (s *PgStore) EventReceiptReplays(ctx context.Context, accountID, source, eventID, subscriptionID string, cursor EventReceiptReplayCursor, limit int) (EventReceiptReplayHistory, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return EventReceiptReplayHistory{}, fmt.Errorf("begin receipt replay read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	meta, err := q.EventReceiptMetadata(ctx, tx, sqlc.EventReceiptMetadataParams{AccountID: mustPgUUID(accountID), EventSource: source, EventID: eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return EventReceiptReplayHistory{}, ErrNotFound
	}
	if err != nil {
		return EventReceiptReplayHistory{}, fmt.Errorf("read replay receipt: %w", err)
	}
	if !validReplayCursor(cursor, meta.ID) {
		return EventReceiptReplayHistory{}, ErrConflict
	}
	appID, err := q.EventReceiptReplayTarget(ctx, tx, sqlc.EventReceiptReplayTargetParams{OutboxID: meta.ID, AccountID: meta.AccountID, SubscriptionID: subscriptionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return EventReceiptReplayHistory{}, ErrNotFound
	}
	if err != nil {
		return EventReceiptReplayHistory{}, fmt.Errorf("read replay recipient: %w", err)
	}
	root := PublishedEventInvocationID(meta.InvocationAccountID, source, eventID, subscriptionID)
	params := sqlc.EventReceiptReplayHistoryParams{AccountID: meta.AccountID, AppID: appID, RootInvocationID: mustPgUUID(root), AcceptedAt: meta.CreatedAt, PageLimit: int32(receiptLimit(limit) + 1)}
	if cursor.OutboxID != 0 {
		params.AfterCreatedAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
		params.AfterID = mustPgUUID(cursor.InvocationID)
	}
	rows, err := q.EventReceiptReplayHistory(ctx, tx, params)
	if err != nil {
		return EventReceiptReplayHistory{}, fmt.Errorf("read replay history: %w", err)
	}
	history := EventReceiptReplayHistory{OutboxID: meta.ID, EventSource: source, EventID: eventID, SubscriptionID: subscriptionID, OriginalInvocationID: root, Replays: make([]EventReceiptExecution, 0, len(rows))}
	for _, row := range rows {
		execution := receiptExecution(uuidString(row.ID), row.State, int(row.Attempts), row.ReplayGeneration, timeFromPgtype(row.DueAt), timeFromPgtype(row.CreatedAt), timestamptzToTimePtr(row.CompletedAt), row.LastError)
		execution.ReplayedFromInvocationID = uuidString(row.ReplayedFromInvocationID)
		history.Replays = append(history.Replays, *execution)
	}
	pageReceiptReplayHistory(&history, receiptLimit(limit))
	if err := tx.Commit(ctx); err != nil {
		return EventReceiptReplayHistory{}, fmt.Errorf("commit replay history: %w", err)
	}
	return history, nil
}

func pageReceiptReplayHistory(history *EventReceiptReplayHistory, limit int) {
	if len(history.Replays) <= limit {
		return
	}
	last := history.Replays[limit-1]
	history.NextCursor = EventReceiptReplayCursor{OutboxID: history.OutboxID, CreatedAt: last.CreatedAt, InvocationID: last.InvocationID}
	history.Replays = history.Replays[:limit]
}

func (m *MemStore) EventReceiptReplays(ctx context.Context, accountID, source, eventID, subscriptionID string, cursor EventReceiptReplayCursor, limit int) (EventReceiptReplayHistory, error) {
	// Resolve envelope spelling and captured membership with the same receipt
	// reader, then lock again and reject replacement of the acceptance identity.
	receipt, err := m.EventReceipt(ctx, accountID, source, eventID, EventReceiptCursor{}, 1)
	if err != nil {
		return EventReceiptReplayHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.eventFanout[canonicalMemUUID(accountID)+"\x00"+source+"\x00"+eventID]
	if work == nil || work.ID != receipt.OutboxID {
		return EventReceiptReplayHistory{}, ErrNotFound
	}
	if !validReplayCursor(cursor, work.ID) {
		return EventReceiptReplayHistory{}, ErrConflict
	}
	appID := ""
	for _, recipient := range work.RecipientSnapshot {
		if recipient.ID == subscriptionID {
			appID = recipient.AppID
			break
		}
	}
	app, exists := m.apps[appID]
	if !exists {
		app, exists = m.apps[canonicalMemUUID(appID)]
	}
	if !exists || !sameMemUUID(app.AccountID, accountID) {
		return EventReceiptReplayHistory{}, ErrNotFound
	}
	root := PublishedEventInvocationID(receipt.invocationAccountID, source, eventID, subscriptionID)
	history := EventReceiptReplayHistory{OutboxID: work.ID, EventSource: source, EventID: eventID, SubscriptionID: subscriptionID, OriginalInvocationID: root, Replays: make([]EventReceiptExecution, 0)}
	for _, inv := range m.receiptReplaysLocked(accountID, appID, root, receipt.AcceptedAt) {
		if cursor.OutboxID != 0 && (inv.CreatedAt.After(cursor.CreatedAt) || (inv.CreatedAt.Equal(cursor.CreatedAt) && canonicalMemUUID(inv.ID) >= canonicalMemUUID(cursor.InvocationID))) {
			continue
		}
		history.Replays = append(history.Replays, *receiptReplayExecution(inv))
		if len(history.Replays) > receiptLimit(limit) {
			break
		}
	}
	pageReceiptReplayHistory(&history, receiptLimit(limit))
	return history, nil
}
