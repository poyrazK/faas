package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
	"time"
	"unicode/utf8"

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
	out := IssueTrackerTriage{IncidentID: issueID(scope.IncidentID), Active: scope.Active, History: []IssueTrackerTriageActivity{}, Escalated: scope.Escalated, ResponsibleTeamID: issueID(scope.ResponsibleTeamID), ResponsibleTeamName: scope.ResponsibleTeamName}
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
	out.SnoozedUntil = trackerSnoozeTime(row.SnoozedUntil)
	out.SnoozedBy, out.SnoozedName, out.SnoozeReason = issueID(row.SnoozedBy), row.SnoozedName, row.SnoozeReason
	out.Snoozed = scope.Active && row.SnoozedUntil.Valid && row.SnoozedUntil.Time.After(scope.CheckedAt.Time)
	out.Resurfaced = scope.Active && row.SnoozedUntil.Valid && !out.Snoozed
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
		out.History = append(out.History, IssueTrackerTriageActivity{ActorID: issueID(r.ActorID), ActorName: r.ActorName, Action: r.Action, Revision: r.Revision, CreatedAt: r.CreatedAt.Time, SnoozedUntil: trackerSnoozeTime(r.SnoozedUntil), Reason: r.Reason})
	}
	reminders, err := q.IssueTrackerReminderHistory(ctx, tx, sqlc.IssueTrackerReminderHistoryParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), IncidentID: scope.IncidentID, HistoryLimit: api.IssueTrackerRemindersMax})
	if err != nil {
		return out, err
	}
	out.Reminders = []IssueTrackerReminder{}
	for _, v := range reminders {
		out.Reminders = append(out.Reminders, IssueTrackerReminder{Sequence: int(v.ReminderSequence), CreatedAt: v.CreatedAt.Time, TeamName: v.TeamName})
	}
	escalations, err := q.IssueTrackerEscalationHistory(ctx, tx, sqlc.IssueTrackerEscalationHistoryParams{AppID: issueUUID(app), OwnerAccountID: issueUUID(owner), ActorAccountID: issueUUID(actor), IncidentID: scope.IncidentID})
	if err != nil {
		return out, err
	}
	for _, v := range escalations {
		out.Escalations = append(out.Escalations, IssueTrackerEscalation{TeamID: issueID(v.TeamID), TeamName: v.TeamName, CreatedAt: v.CreatedAt.Time})
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ActOnIssueTrackerTriage(ctx context.Context, app, owner, actor, notice string, revision int64, action string) error {
	return s.UpdateIssueTrackerTriage(ctx, app, owner, actor, notice, revision, IssueTrackerTriageAction{Action: action})
}

func (s *PgStore) UpdateIssueTrackerTriage(ctx context.Context, app, owner, actor, notice string, revision int64, req IssueTrackerTriageAction) error {
	if revision < 0 {
		return ErrInvalidArgument
	}
	req.Reason = strings.TrimSpace(req.Reason)
	switch req.Action {
	case "acknowledge", "claim", "release", "unsnooze":
		if req.SnoozedUntil != nil || req.Reason != "" {
			return ErrInvalidArgument
		}
	case "snooze":
		if req.SnoozedUntil == nil || len(req.Reason) > api.IssueTrackerSyncSnoozeReasonMaxBytes || !utf8.ValidString(req.Reason) || strings.ContainsRune(req.Reason, 0) {
			return ErrInvalidArgument
		}
		until := req.SnoozedUntil.UTC().Truncate(time.Microsecond)
		req.SnoozedUntil = &until
	default:
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
	if req.Action == "snooze" && (!req.SnoozedUntil.After(scope.CheckedAt.Time) || req.SnoozedUntil.After(scope.CheckedAt.Time.Add(api.IssueTrackerSyncSnoozeMax))) {
		return ErrInvalidArgument
	}
	changed, err := applyTrackerTriageUpdate(&out, actor, owner, req)
	if err != nil {
		return err
	}
	if !changed {
		return tx.Commit(ctx)
	}
	out.Revision++
	if err = saveTrackerTriage(ctx, q, tx, scope, out, actor, req); err != nil {
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

func saveTrackerTriage(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, scope sqlc.IssueTrackerTriageScopeRow, out IssueTrackerTriage, actor string, req IssueTrackerTriageAction) error {
	if err := q.IssueTrackerTriagePut(ctx, tx, sqlc.IssueTrackerTriagePutParams{IncidentID: scope.IncidentID, LinkID: scope.LinkID, Revision: out.Revision, AcknowledgedBy: issueUUID(out.AcknowledgedBy), ClaimedBy: issueUUID(out.ClaimedBy), SnoozedUntil: trackerSnoozeValue(out.SnoozedUntil), SnoozedBy: issueUUID(out.SnoozedBy), SnoozeReason: out.SnoozeReason}); err != nil {
		return err
	}
	if err := q.IssueTrackerTriageActivityAdd(ctx, tx, sqlc.IssueTrackerTriageActivityAddParams{ID: issueUUID(uuid.NewString()), IncidentID: scope.IncidentID, ActorID: issueUUID(actor), Action: req.Action, Revision: out.Revision, SnoozedUntil: trackerSnoozeValue(req.SnoozedUntil), Reason: req.Reason}); err != nil {
		return err
	}
	return q.IssueTrackerTriageActivityPrune(ctx, tx, sqlc.IssueTrackerTriageActivityPruneParams{IncidentID: scope.IncidentID, RetainedCount: api.IssueTrackerSyncTriageHistoryMax})
}

func applyTrackerTriageUpdate(out *IssueTrackerTriage, actor, owner string, req IssueTrackerTriageAction) (bool, error) {
	switch req.Action {
	case "snooze":
		if out.SnoozedUntil != nil && out.SnoozedUntil.Equal(*req.SnoozedUntil) && out.SnoozedBy == actor && out.SnoozeReason == req.Reason {
			return false, nil
		}
		out.SnoozedUntil, out.SnoozedBy, out.SnoozeReason = req.SnoozedUntil, actor, req.Reason
		return true, nil
	case "unsnooze":
		if !out.Snoozed {
			return false, nil
		}
		out.SnoozedUntil = nil
		out.SnoozedBy = ""
		out.SnoozeReason = ""
		return true, nil
	default:
		return applyTrackerTriageAction(out, actor, owner, req.Action)
	}
}
func trackerSnoozeTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}
func trackerSnoozeValue(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: v.UTC(), Valid: true}
}
