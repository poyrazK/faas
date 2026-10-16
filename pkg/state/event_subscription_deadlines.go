package state

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionDeadlineStore interface {
	ListEventSubscriptionDeadlines(context.Context, string, string, string, api.EventSubscriptionDeadlineQuery) (api.EventSubscriptionDeadlinePage, error)
}

var _ EventSubscriptionDeadlineStore = (*PgStore)(nil)
var _ EventSubscriptionDeadlineStore = (*MemStore)(nil)

func (s *PgStore) ListEventSubscriptionDeadlines(ctx context.Context, account, app, sub string, query api.EventSubscriptionDeadlineQuery) (api.EventSubscriptionDeadlinePage, error) {
	var zero api.EventSubscriptionDeadlinePage
	if validSubscriptionControlIDs(account, app, sub) != nil || query.Validate() != nil {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionDeadlineReadTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionPendingSummaryBudget(ctx, tx, strconv.Itoa(api.EventSubscriptionDeadlineStatementMillis)); err != nil {
		return zero, pendingSummaryError(err)
	}
	if _, err = q.EventConsumerPressureTarget(ctx, tx, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return zero, pendingSummaryError(mapErr(err))
	}
	stamp, err := q.EventSubscriptionRateClock(ctx, tx)
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	now := timeFromPgtype(stamp).UTC()
	normalized, cursor, err := subscriptionDeadlineQuery(account, app, sub, query, now)
	if err != nil {
		return zero, err
	}
	rows, err := q.EventSubscriptionDeadlinePage(ctx, tx, sqlc.EventSubscriptionDeadlinePageParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: stamp, WindowAt: pgtypeFromTime(cursor.WindowAt), DeadlineFrom: nullableTimestamptzPtr(normalized.DeadlineFrom), DeadlineBefore: nullableTimestamptzPtr(normalized.DeadlineBefore), StatusFilter: normalized.Status, WithinMillis: normalized.WithinMillis, WaitingReason: normalized.WaitingReason, HasAfter: normalized.Cursor != "", AfterDeadlineAt: nullableTimestamptz(cursor.DeadlineAt), AfterAcceptedAt: nullableTimestamptz(cursor.AcceptedAt), AfterOutboxID: cursor.OutboxID, PageLimit: int32(normalized.Limit + 1)})
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	entries := make([]subscriptionDeadlineEntry, 0, len(rows))
	for _, row := range rows {
		if row.AcceptedAt.InfinityModifier != pgtype.Finite || row.DeadlineAt.InfinityModifier != pgtype.Finite || row.NextAttemptAt.InfinityModifier != pgtype.Finite || row.LeaseUntil.InfinityModifier != pgtype.Finite {
			return zero, errors.New("invalid retained deadline timestamps")
		}
		entry := subscriptionDeadlineEntry{outboxID: row.OutboxID, EventSubscriptionDeadlineRecipient: api.EventSubscriptionDeadlineRecipient{EventSource: row.Source, EventID: row.EventID, AcceptedAt: timeFromPgtype(row.AcceptedAt).UTC(), DeadlineAt: timeFromPgtype(row.DeadlineAt).UTC(), WaitingReason: row.WaitingReason, NextAttemptAt: timestamptzToTimePtr(row.NextAttemptAt), LeaseUntil: timestamptzToTimePtr(row.LeaseUntil)}}
		if !subscriptionDeadlineEntryValid(entry, now) {
			return zero, errors.New("invalid retained deadline metadata")
		}
		entries = append(entries, entry)
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, pendingSummaryError(err)
	}
	return subscriptionDeadlinePage(app, sub, now, normalized, cursor, entries), nil
}
func subscriptionDeadlineEntryValid(entry subscriptionDeadlineEntry, now time.Time) bool {
	if entry.outboxID < 1 || entry.EventSource == "" || entry.EventID == "" || !subscriptionDeadlineTimeValid(entry.AcceptedAt) || entry.AcceptedAt.After(now) || !subscriptionDeadlineTimeValid(entry.DeadlineAt) || !entry.DeadlineAt.After(entry.AcceptedAt) {
		return false
	}
	if _, ok := api.NewEventPendingWaitingReasonCounts()[entry.WaitingReason]; !ok {
		return false
	}
	for _, at := range []*time.Time{entry.NextAttemptAt, entry.LeaseUntil} {
		if at != nil && !subscriptionDeadlineTimeValid(at.UTC()) {
			return false
		}
	}
	return true
}
