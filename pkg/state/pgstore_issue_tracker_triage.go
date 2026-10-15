package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func trackerTriageConflict() error {
	return api.NewProblem(409, "issue_tracker_triage_changed", "Incident triage changed", "reload the incident before updating triage")
}

func trackerTriageScope(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, owner, actor, notice string) (sqlc.IssueTrackerTriageScopeRow, error) {
	return q.IssueTrackerTriageScope(ctx, tx, sqlc.IssueTrackerTriageScopeParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), NotificationID: issueUUID(notice)})
}

func trackerTriageGet(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, scope sqlc.IssueTrackerTriageScopeRow) (IssueTrackerTriage, error) {
	out := IssueTrackerTriage{IncidentID: issueID(scope.IncidentID), Active: scope.Active, History: []IssueTrackerTriageActivity{}}
	row, err := q.IssueTrackerTriageGet(ctx, tx, scope.IncidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Revision = row.Revision
	out.AcknowledgedBy, out.ClaimedBy = issueID(row.AcknowledgedBy), issueID(row.ClaimedBy)
	out.AcknowledgedName, out.ClaimedName = row.AcknowledgedName, row.ClaimedName
	return out, nil
}

func (s *PgStore) GetIssueTrackerTriage(ctx context.Context, app, owner, actor, notice string) (IssueTrackerTriage, error) {
	out := IssueTrackerTriage{}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	scope, err := trackerTriageScope(ctx, q, tx, app, owner, actor, notice)
	if err != nil {
		return out, mapErr(err)
	}
	out, err = trackerTriageGet(ctx, q, tx, scope)
	if err != nil {
		return out, err
	}
	rows, err := q.IssueTrackerTriageActivityList(ctx, tx, sqlc.IssueTrackerTriageActivityListParams{IncidentID: scope.IncidentID, Limit: api.IssueTrackerSyncTriageHistoryMax})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.History = append(out.History, IssueTrackerTriageActivity{ActorID: issueID(r.ActorID), ActorName: r.ActorName, Action: r.Action, Revision: r.Revision, CreatedAt: r.CreatedAt.Time})
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ActOnIssueTrackerTriage(ctx context.Context, app, owner, actor, notice string, revision int64, action string) error {
	if revision < 0 || (action != "acknowledge" && action != "claim" && action != "release") {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	locked, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return mapErr(err)
	}
	if issueID(locked.AccountID) != owner {
		return ErrNotFound
	}
	scope, err := trackerTriageScope(ctx, q, tx, app, owner, actor, notice)
	if err != nil {
		return mapErr(err)
	}
	out, err := trackerTriageGet(ctx, q, tx, scope)
	if err != nil {
		return err
	}
	if !out.Active || out.Revision != revision {
		return trackerTriageConflict()
	}
	changed, err := applyTrackerTriageAction(&out, actor, owner, action)
	if err != nil {
		return err
	}
	if !changed {
		return tx.Commit(ctx)
	}
	out.Revision++
	if err = q.IssueTrackerTriagePut(ctx, tx, sqlc.IssueTrackerTriagePutParams{IncidentID: scope.IncidentID, LinkID: scope.LinkID, Revision: out.Revision, AcknowledgedBy: issueUUID(out.AcknowledgedBy), ClaimedBy: issueUUID(out.ClaimedBy)}); err != nil {
		return err
	}
	if err = q.IssueTrackerTriageActivityAdd(ctx, tx, sqlc.IssueTrackerTriageActivityAddParams{ID: issueUUID(uuid.NewString()), IncidentID: scope.IncidentID, ActorID: issueUUID(actor), Action: action, Revision: out.Revision}); err != nil {
		return err
	}
	if err = q.IssueTrackerTriageActivityPrune(ctx, tx, sqlc.IssueTrackerTriageActivityPruneParams{IncidentID: scope.IncidentID, RetainedCount: api.IssueTrackerSyncTriageHistoryMax}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func applyTrackerTriageAction(out *IssueTrackerTriage, actor, owner, action string) (bool, error) {
	switch action {
	case "acknowledge":
		if out.AcknowledgedBy != "" {
			return false, nil
		}
		out.AcknowledgedBy = actor
	case "claim":
		if out.ClaimedBy == actor {
			return false, nil
		}
		if out.ClaimedBy != "" {
			return false, trackerTriageConflict()
		}
		out.ClaimedBy = actor
	case "release":
		if out.ClaimedBy == "" {
			return false, nil
		}
		if out.ClaimedBy != actor && actor != owner {
			return false, trackerTriageConflict()
		}
		out.ClaimedBy = ""
	}
	return true, nil
}
