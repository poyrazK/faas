package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Caller holds the app/policy locks; receipt and bounded pruning commit with the action.
func recordIssueTrackerSyncHistory(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, r IssueTrackerAutoSyncRecord, event, claim string) error {
	if err := q.IssueTrackerSyncHistoryAdd(ctx, tx, sqlc.IssueTrackerSyncHistoryAddParams{LinkID: issueUUID(r.Policy.LinkID), AppID: issueUUID(r.AppID), AccountID: issueUUID(r.AccountID), SourceIssueID: issueUUID(r.Policy.SourceIssueID), PolicyRevision: r.Policy.Revision, ClaimID: issueUUID(claim), EventType: event, Direction: r.Policy.Direction, Outcome: r.Policy.Outcome, Reason: r.Policy.Reason, SourceState: r.Policy.SourceState, ObservedState: r.Policy.ObservedState, PendingState: r.Policy.PendingState}); err != nil {
		return err
	}
	if err := q.IssueTrackerSyncHistoryPrune(ctx, tx, sqlc.IssueTrackerSyncHistoryPruneParams{LinkID: issueUUID(r.Policy.LinkID), RetainedCount: api.IssueTrackerSyncHistoryMax}); err != nil {
		return err
	}
	if event == "checked" {
		return pgTrackerSyncNotifications(ctx, q, tx, r)
	}
	return nil
}

func (s *PgStore) GetIssueTrackerSyncHealth(ctx context.Context, app, account, issue, link string) (IssueTrackerSyncHealth, error) {
	return s.getIssueTrackerSyncHealth(ctx, app, account, account, issue, link)
}

func (s *PgStore) getIssueTrackerSyncHealth(ctx context.Context, app, account, actor, issue, link string) (IssueTrackerSyncHealth, error) {
	out := IssueTrackerSyncHealth{History: []IssueTrackerSyncHistory{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()

	allowed, e := q.IssueTrackerNotificationCanRead(ctx, tx, sqlc.IssueTrackerNotificationCanReadParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(account), ActorAccountID: issueUUID(actor), LinkID: issueUUID(link)})
	if e != nil {
		return out, e
	}
	if !allowed {
		return out, ErrNotFound
	}
	if _, err = autoSyncScope(ctx, q, tx, app, account, issue, link); err != nil {
		return out, err
	}
	row, err := q.IssueTrackerAutoSyncGet(ctx, tx, issueUUID(link))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if row.LastSuccessfulAt.Valid && issueID(row.AccountID) == account {
		v := row.LastSuccessfulAt.Time
		out.LastSuccessfulAt = &v
	}
	rows, err := q.IssueTrackerSyncHistoryList(ctx, tx, sqlc.IssueTrackerSyncHistoryListParams{LinkID: issueUUID(link), AccountID: issueUUID(account), Limit: api.IssueTrackerSyncHistoryMax})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.History = append(out.History, IssueTrackerSyncHistory{ID: issueID(r.ID), EventType: r.EventType, Direction: r.Direction, Outcome: r.Outcome, Reason: r.Reason, SourceState: r.SourceState, ObservedState: r.ObservedState, PendingState: r.PendingState, PolicyRevision: r.PolicyRevision, CreatedAt: r.CreatedAt.Time})
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) RecordIssueTrackerSyncInspection(ctx context.Context, in IssueTrackerSyncInspection) error {
	r := in.Record
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(r.AppID))
	if err != nil {
		return mapErr(err)
	}
	if issueID(owner.AccountID) != r.AccountID {
		return ErrNotFound
	}
	if _, err = autoSyncScope(ctx, q, tx, r.AppID, r.AccountID, r.Policy.SourceIssueID, r.Policy.LinkID); err != nil {
		return err
	}
	row, err := q.IssueTrackerInboundPolicyLock(ctx, tx, issueUUID(r.Policy.LinkID))
	if err != nil {
		return mapErr(err)
	}
	guard, err := q.IssueTrackerInboundGuardLock(ctx, tx, issueUUID(r.AppID))
	if err != nil {
		return mapErr(err)
	}
	if in.GuardRevision <= 0 || guard != in.GuardRevision || row.Revision != r.Policy.Revision || issueID(row.AccountID) != r.AccountID || issueID(row.AppID) != r.AppID || issueID(row.SourceIssueID) != r.Policy.SourceIssueID {
		return IssueTrackerInboundChanged()
	}
	// These fields describe this inspection only. Never change the policy baseline,
	// pending intent, lifecycle, scheduling or last successful automatic check.
	if in.Failed {
		r.Policy.Outcome = "error"
		r.Policy.ObservedState = ""
	} else {
		r.Policy.ObservedState = in.Observed.State
	}
	r.Policy.Reason = in.Reason
	if err = recordIssueTrackerSyncHistory(ctx, q, tx, r, "inspected", ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
