package state

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func applicationTriageScope(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, owner, actor, issue string) (sqlc.IssueApplicationTriageScopeRow, error) {
	return q.IssueApplicationTriageScope(ctx, db, sqlc.IssueApplicationTriageScopeParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), IssueID: issueUUID(issue), EntitledPlans: issueSlackEntitledPlans()})
}
func applicationTriageGet(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, scope sqlc.IssueApplicationTriageScopeRow) (IssueApplicationTriage, error) {
	out := IssueApplicationTriage{IssueID: issueID(scope.ID), Environment: scope.Environment, TeamID: issueID(scope.AssigneeTeamID), TeamName: scope.TeamName, AssigneeID: issueID(scope.AssigneeAccountID), AssigneeName: scope.AssigneeName, Active: scope.State == "open", History: []IssueApplicationTriageActivity{}}
	row, err := q.IssueApplicationTriageGet(ctx, db, scope.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.EscalatedAt = trackerSnoozeTime(row.EscalatedAt)
	out.ResponsibleTeamID = issueID(row.EscalationTeamID)
	out.ResponsibleTeamName = row.ResponsibleTeamName
	out.IncidentID = issueID(row.IncidentID)
	out.Revision = row.Revision
	out.AcknowledgedBy = issueID(row.AcknowledgedBy)
	out.AcknowledgedName = row.AcknowledgedName
	out.AcknowledgedAt = trackerSnoozeTime(row.AcknowledgedAt)
	out.SnoozedUntil = trackerSnoozeTime(row.SnoozedUntil)
	out.SnoozedBy = issueID(row.SnoozedBy)
	out.SnoozedName = row.SnoozedName
	out.SnoozeReason = row.SnoozeReason
	out.Snoozed = out.Active && row.SnoozedUntil.Valid && row.SnoozedUntil.Time.After(scope.CheckedAt.Time)
	out.Resurfaced = out.Active && row.SnoozedUntil.Valid && !out.Snoozed
	return out, nil
}
func (s *PgStore) GetIssueApplicationTriage(ctx context.Context, app, owner, actor, issue string) (IssueApplicationTriage, error) {
	out := IssueApplicationTriage{}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	scope, err := applicationTriageScope(ctx, q, tx, app, owner, actor, issue)
	if err != nil {
		return out, mapErr(err)
	}
	out, err = applicationTriageGet(ctx, q, tx, scope)
	if err != nil {
		return out, err
	}
	rows, err := q.IssueApplicationTriageHistory(ctx, tx, sqlc.IssueApplicationTriageHistoryParams{IssueID: scope.ID, Limit: api.IssueApplicationTriageHistoryMax})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.History = append(out.History, IssueApplicationTriageActivity{IncidentID: issueID(r.IncidentID), ActorID: issueID(r.ActorID), ActorName: r.ActorName, Action: r.Action, Reason: r.Reason, Revision: r.Revision, CreatedAt: r.CreatedAt.Time, SnoozedUntil: trackerSnoozeTime(r.SnoozedUntil)})
	}
	notices, err := applicationReminderHistory(ctx, q, tx, scope.ID)
	if err != nil {
		return out, err
	}
	for _, notice := range notices {
		if notice.Kind == "escalation" {
			out.Escalations = append(out.Escalations, notice)
		} else {
			out.Reminders = append(out.Reminders, notice)
		}
	}
	return out, tx.Commit(ctx)
}
func validateApplicationTriage(req *IssueApplicationTriageAction, revision int64) error {
	if revision < 0 {
		return ErrInvalidArgument
	}
	req.Reason = strings.TrimSpace(req.Reason)
	switch req.Action {
	case "acknowledge", "unsnooze":
		if req.SnoozedUntil != nil || req.Reason != "" {
			return ErrInvalidArgument
		}
	case "snooze":
		if req.SnoozedUntil == nil || len(req.Reason) > api.IssueApplicationSnoozeReasonMaxBytes || !utf8.ValidString(req.Reason) || strings.ContainsRune(req.Reason, 0) {
			return ErrInvalidArgument
		}
		until := req.SnoozedUntil.UTC().Truncate(time.Microsecond)
		req.SnoozedUntil = &until
	default:
		return ErrInvalidArgument
	}
	return nil
}
func applicationTriageConflict() error {
	return api.NewProblem(409, "issue_application_triage_changed", "Issue response changed", "reload issue response before updating")
}
func (s *PgStore) UpdateIssueApplicationTriage(ctx context.Context, app, owner, actor, issue string, revision int64, req IssueApplicationTriageAction) error {
	if err := validateApplicationTriage(&req, revision); err != nil {
		return err
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
	if err = applyApplicationTriageAction(ctx, q, tx, app, owner, actor, issue, revision, req); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func applyApplicationTriageAction(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, owner, actor, issue string, revision int64, req IssueApplicationTriageAction) error {
	if err := validateApplicationTriage(&req, revision); err != nil {
		return err
	}
	scope, err := applicationTriageScope(ctx, q, tx, app, owner, actor, issue)
	if err != nil {
		return mapErr(err)
	}
	out, err := applicationTriageGet(ctx, q, tx, scope)
	if err != nil {
		return err
	}
	if !out.Active || out.Revision != revision {
		return applicationTriageConflict()
	}
	switch req.Action {
	case "acknowledge":
		if out.AcknowledgedAt != nil {
			return nil
		}
		if err = cancelApplicationReminders(ctx, q, tx, issueUUID(app), scope.ID); err != nil {
			return err
		}
		out.AcknowledgedBy = actor
		now := scope.CheckedAt.Time
		out.AcknowledgedAt = &now
	case "snooze":
		if !req.SnoozedUntil.After(scope.CheckedAt.Time) || req.SnoozedUntil.After(scope.CheckedAt.Time.Add(api.IssueApplicationSnoozeMax)) {
			return ErrInvalidArgument
		}
		if out.SnoozedUntil != nil && out.SnoozedUntil.Equal(*req.SnoozedUntil) && out.SnoozedBy == actor && out.SnoozeReason == req.Reason {
			return nil
		}
		out.SnoozedUntil = req.SnoozedUntil
		out.SnoozedBy = actor
		out.SnoozeReason = req.Reason
		if err = q.IssueApplicationTriageReleaseLease(ctx, tx, scope.ID); err != nil {
			return err
		}
		if err = q.IssueApplicationTriageCancelSlack(ctx, tx, scope.ID); err != nil {
			return err
		}
	case "unsnooze":
		if out.SnoozedUntil == nil {
			return nil
		}
		out.SnoozedUntil = nil
		out.SnoozedBy = ""
		out.SnoozeReason = ""
	}
	if out.IncidentID == "" {
		out.IncidentID = uuid.NewString()
	}
	out.Revision++
	if err = q.IssueApplicationTriagePut(ctx, tx, sqlc.IssueApplicationTriagePutParams{IssueID: scope.ID, IncidentID: issueUUID(out.IncidentID), Revision: out.Revision, AcknowledgedBy: issueUUID(out.AcknowledgedBy), AcknowledgedAt: trackerSnoozeValue(out.AcknowledgedAt), SnoozedUntil: trackerSnoozeValue(out.SnoozedUntil), SnoozedBy: issueUUID(out.SnoozedBy), SnoozeReason: out.SnoozeReason}); err != nil {
		return err
	}
	if err = applicationTriageAudit(ctx, q, tx, out, actor, req); err != nil {
		return err
	}
	return nil
}

func applicationTriageAudit(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, out IssueApplicationTriage, actor string, req IssueApplicationTriageAction) error {
	if err := q.IssueApplicationTriageAudit(ctx, tx, sqlc.IssueApplicationTriageAuditParams{IssueID: issueUUID(out.IssueID), IncidentID: issueUUID(out.IncidentID), Revision: out.Revision, ActorID: issueUUID(actor), Action: req.Action, SnoozedUntil: trackerSnoozeValue(req.SnoozedUntil), Reason: req.Reason}); err != nil {
		return err
	}
	return q.IssueApplicationTriagePrune(ctx, tx, sqlc.IssueApplicationTriagePruneParams{IssueID: issueUUID(out.IssueID), Offset: api.IssueApplicationTriageHistoryMax})
}
func resetApplicationTriage(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, actor string) error {
	r, err := q.IssueApplicationTriageReset(ctx, tx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return applicationTriageAudit(ctx, q, tx, IssueApplicationTriage{IssueID: issueID(row.ID), IncidentID: issueID(r.IncidentID), Revision: r.Revision}, actor, IssueApplicationTriageAction{Action: "reset"})
}
