package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
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
	setup, err := q.IssueTrackerCallbackSetup(ctx, tx, sqlc.IssueTrackerCallbackSetupParams{AppID: issueUUID(app), AccountID: issueUUID(account), LinkID: issueUUID(link)})
	if err != nil {
		return out, mapErr(err)
	}
	out.Provider, out.Configured = setup.Provider, setup.Configured
	out.InvalidSignature, out.WrongScope, out.StaleDelivery, out.InvalidPayload, out.DeliveryConflict = setup.InvalidSignature, setup.WrongScope, setup.StaleDelivery, setup.InvalidPayload, setup.DeliveryConflict
	if setup.LastVerifiedAt.Valid {
		t := setup.LastVerifiedAt.Time
		out.LastVerifiedAt = &t
	}
	if setup.LastRejectedAt.Valid {
		t := setup.LastRejectedAt.Time
		out.LastRejectedAt = &t
	}
	if out.Provider == "github" {
		out.LastVerifiedAt = out.LastReceivedAt
	}
	out.Setup = out.SetupStatus(time.Now())
	return out, tx.Commit(ctx)
}

type IssueTrackerCallbackDiagnosticStore interface {
	RecordIssueTrackerCallbackDiagnostic(context.Context, string, string, int64, string) error
}

func (s *PgStore) RecordIssueTrackerCallbackDiagnostic(ctx context.Context, app, account string, revision int64, reason string) error {
	switch reason {
	case "verified", "invalid_signature", "wrong_scope", "stale_delivery", "invalid_payload", "delivery_conflict":
	default:
		return ErrInvalidArgument
	}
	return sqlc.New().IssueTrackerCallbackDiagnosticRecord(ctx, s.pool, sqlc.IssueTrackerCallbackDiagnosticRecordParams{AppID: issueUUID(app), AccountID: issueUUID(account), ConnectionRevision: revision, Reason: reason})
}
