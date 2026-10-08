package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type recoveryAuditKey struct{}
type recoveryAudit struct{ Kind, ID, Reason string }

// WithEventRecoveryActor supplies trusted server-resolved identity, never request-body identity.
func WithEventRecoveryActor(ctx context.Context, kind, id string) context.Context {
	return context.WithValue(ctx, recoveryAuditKey{}, recoveryAudit{Kind: kind, ID: id})
}
func WithEventRecoveryReason(ctx context.Context, reason string) (context.Context, error) {
	if err := (api.EventRecoveryControlRequest{Reason: reason}).Validate(); err != nil {
		return ctx, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	a, _ := ctx.Value(recoveryAuditKey{}).(recoveryAudit)
	a.Reason = reason
	return context.WithValue(ctx, recoveryAuditKey{}, a), nil
}
func recoveryAuditActor(ctx context.Context, action string) recoveryAudit {
	if action == "expired" {
		return recoveryAudit{Kind: "system", ID: "recovery_expiry"}
	}
	a, _ := ctx.Value(recoveryAuditKey{}).(recoveryAudit)
	if a.Kind == "" {
		a.Kind = "internal"
		a.ID = "store"
	}
	return a
}
func insertRecoveryHistory(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, id, action, previous string, rate int, now time.Time) error {
	a := recoveryAuditActor(ctx, action)
	return q.EventRecoveryHistoryInsert(ctx, tx, sqlc.EventRecoveryHistoryInsertParams{JobID: mustPgUUID(id), NowAt: pgtypeFromTime(now), Action: action, ActorKind: a.Kind, ActorID: a.ID, Reason: a.Reason, PreviousState: previous, PreviousRate: int32(rate)})
}
func insertRecoveryCancellation(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, id, reason string, now time.Time) error {
	action := "cancelled"
	if reason == "expired" {
		action = "expired"
	}
	a := recoveryAuditActor(ctx, action)
	return q.EventRecoveryHistoryCancel(ctx, tx, sqlc.EventRecoveryHistoryCancelParams{JobID: mustPgUUID(id), NowAt: pgtypeFromTime(now), Action: action, ActorKind: a.Kind, ActorID: a.ID, Reason: a.Reason})
}
func historyPage(job string, entries []api.EventRecoveryHistoryEntry, limit int) api.EventRecoveryHistory {
	out := api.EventRecoveryHistory{JobID: job, Entries: entries}
	if len(entries) > limit {
		out.Entries = entries[:limit]
		out.NextAfter = out.Entries[limit-1].ID
	}
	return out
}
func validateRecoveryHistory(account, job string, after int64, limit int) (int, error) {
	if err := eventRecoveryIDs(account, job); err != nil {
		return 0, err
	}
	if after < 0 || limit < 0 || limit > api.EventRecoveryHistoryPageMax {
		return 0, ErrEventRecoveryQuery
	}
	if limit == 0 {
		limit = api.EventRecoveryHistoryPageMax
	}
	return limit, nil
}
func (s *PgStore) ListEventRecoveryHistory(ctx context.Context, account, job string, after int64, limit int) (api.EventRecoveryHistory, error) {
	limit, err := validateRecoveryHistory(account, job, after, limit)
	if err != nil {
		return api.EventRecoveryHistory{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryHistory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = getEventRecoveryMetadata(ctx, q, tx, account, job); err != nil {
		return api.EventRecoveryHistory{}, err
	}
	rows, err := q.EventRecoveryHistoryList(ctx, tx, sqlc.EventRecoveryHistoryListParams{JobID: mustPgUUID(job), AccountID: mustPgUUID(account), AfterID: after, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.EventRecoveryHistory{}, err
	}
	entries := make([]api.EventRecoveryHistoryEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, api.EventRecoveryHistoryEntry{ID: r.ID, OccurredAt: timeFromPgtype(r.OccurredAt), Action: r.Action, ActorKind: r.ActorKind, ActorID: r.ActorID, Reason: r.Reason, PreviousState: r.PreviousState, State: r.State, PreviousRate: int(r.PreviousRate), Rate: int(r.Rate)})
	}
	return historyPage(canonicalMemUUID(job), entries, limit), tx.Commit(ctx)
}
func memRecoveryHistory(ctx context.Context, job *memEventRecoveryJob, action, previous string, rate int, now time.Time) {
	a := recoveryAuditActor(ctx, action)
	id := int64(len(job.History) + 1)
	job.History = append(job.History, api.EventRecoveryHistoryEntry{ID: id, OccurredAt: now, Action: action, ActorKind: a.Kind, ActorID: a.ID, Reason: a.Reason, PreviousState: previous, State: job.Job.State, PreviousRate: rate, Rate: job.Job.RatePerSecond})
}
func (m *MemStore) ListEventRecoveryHistory(ctx context.Context, account, id string, after int64, limit int) (api.EventRecoveryHistory, error) {
	limit, err := validateRecoveryHistory(account, id, after, limit)
	if err != nil {
		return api.EventRecoveryHistory{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return api.EventRecoveryHistory{}, err
	}
	entries := []api.EventRecoveryHistoryEntry{}
	for _, entry := range job.History {
		if entry.ID > after {
			entries = append(entries, entry)
			if len(entries) > limit {
				break
			}
		}
	}
	return historyPage(job.Job.ID, entries, limit), nil
}
