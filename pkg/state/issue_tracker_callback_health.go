package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type IssueTrackerCallbackHealthStore interface {
	GetIssueTrackerCallbackHealth(context.Context, string, string, string, string) (issuetracker.CallbackHealth, error)
}

func (s *PgStore) GetIssueTrackerCallbackHealth(ctx context.Context, app, account, issue, link string) (issuetracker.CallbackHealth, error) {
	out := issuetracker.CallbackHealth{}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = autoSyncScope(ctx, q, tx, app, account, issue, link); err != nil {
		return out, err
	}
	row, err := q.IssueTrackerCallbackHealth(ctx, tx, sqlc.IssueTrackerCallbackHealthParams{LinkID: issueUUID(link), AppID: issueUUID(app), AccountID: issueUUID(account)})
	if err != nil {
		return out, err
	}
	out.RetainedDeliveries, out.PendingHints = row.RetainedDeliveries, row.PendingHints
	if row.LastReceivedAt.Valid {
		t := row.LastReceivedAt.Time
		out.LastReceivedAt = &t
	}
	if row.LastProcessedAt.Valid {
		t := row.LastProcessedAt.Time
		out.LastProcessedAt = &t
	}
	return out, tx.Commit(ctx)
}
