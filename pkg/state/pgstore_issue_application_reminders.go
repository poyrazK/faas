package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueApplicationReminderPolicy(ctx context.Context, app, owner string) (IssueApplicationReminderPolicy, error) {
	r, err := sqlc.New().IssueApplicationReminderPolicy(ctx, s.pool, sqlc.IssueApplicationReminderPolicyParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	return IssueApplicationReminderPolicy{Enabled: r.Enabled, Revision: r.Revision, IntervalMinutes: int(r.IntervalMinutes), RepeatLimit: int(r.RepeatLimit)}, mapErr(err)
}
func (s *PgStore) SetIssueApplicationReminderPolicy(ctx context.Context, app, owner string, in IssueApplicationReminderPolicy) (IssueApplicationReminderPolicy, error) {
	if in.Revision < 0 || in.IntervalMinutes < api.IssueApplicationReminderMinMinutes || in.IntervalMinutes > api.IssueApplicationReminderMaxMinutes || in.RepeatLimit < 1 || in.RepeatLimit > api.IssueApplicationRemindersMax {
		return in, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return in, mapErr(err)
	}
	if issueID(a.AccountID) != owner {
		return in, ErrNotFound
	}
	r, err := q.IssueApplicationReminderPolicy(ctx, tx, sqlc.IssueApplicationReminderPolicyParams{ID: a.ID, AccountID: a.AccountID})
	if err != nil {
		return in, mapErr(err)
	}
	if r.Revision != in.Revision {
		return in, api.NewProblem(409, "issue_application_reminders_changed", "Reminder settings changed", "reload native reminder settings before saving")
	}
	if r.Enabled == in.Enabled && int(r.IntervalMinutes) == in.IntervalMinutes && int(r.RepeatLimit) == in.RepeatLimit {
		return in, tx.Commit(ctx)
	}
	in.Revision++
	if err = q.IssueApplicationReminderPolicyPut(ctx, tx, sqlc.IssueApplicationReminderPolicyPutParams{AppID: a.ID, Enabled: in.Enabled, Revision: in.Revision, IntervalMinutes: int32(in.IntervalMinutes), RepeatLimit: int32(in.RepeatLimit)}); err != nil {
		return in, err
	}
	if err = cancelApplicationReminders(ctx, q, tx, a.ID, pgtype.UUID{}); err != nil {
		return in, err
	}
	return in, tx.Commit(ctx)
}
func cancelApplicationReminders(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app, issue pgtype.UUID) error {
	if err := q.IssueApplicationReminderRelease(ctx, tx, sqlc.IssueApplicationReminderReleaseParams{AppID: app, IssueID: issue}); err != nil {
		return err
	}
	return q.IssueApplicationReminderCancel(ctx, tx, sqlc.IssueApplicationReminderCancelParams{AppID: app, IssueID: issue})
}

// Serialize scheduling and captured routing with acknowledgment, snooze and policy
// changes under the same app lock used by ingestion. No network calls occur here.
func (s *PgStore) EnqueueIssueApplicationReminder(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	app, err := q.IssueApplicationReminderApp(ctx, tx, issueSlackEntitledPlans())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	due, err := q.IssueApplicationReminderDue(ctx, tx, sqlc.IssueApplicationReminderDueParams{AppID: app, EntitledPlans: issueSlackEntitledPlans()})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	policy, err := issueApplicationSlackPolicy(ctx, q, tx, issueID(due.AppID), issueID(due.AccountID))
	if err != nil {
		return false, err
	}
	route, err := q.IssueApplicationSlackRoute(ctx, tx, sqlc.IssueApplicationSlackRouteParams{AppID: due.AppID, AccountID: due.AccountID, AssignedTeamID: due.PreferredTeamID, FallbackTeamID: issueUUID(policy.FallbackTeamID)})
	outcome := "queued"
	if errors.Is(err, pgx.ErrNoRows) {
		outcome = "unrouted"
	} else if err != nil {
		return false, err
	}
	if outcome == "queued" {
		ok, e := applicationSlackCapacity(ctx, q, tx, due.AppID)
		if e != nil {
			return false, e
		}
		if !ok {
			outcome = "queue_full"
		}
	}
	if outcome != "queued" {
		if err = q.IssueApplicationSlackMissed(ctx, tx, sqlc.IssueApplicationSlackMissedParams{AppID: due.AppID, Capacity: outcome == "queue_full"}); err != nil {
			return false, err
		}
	}
	id := issueUUID(uuid.NewString())
	if err = q.IssueApplicationReminderRecord(ctx, tx, sqlc.IssueApplicationReminderRecordParams{ID: id, IssueID: due.ID, ResponseID: due.ResponseID, Sequence: due.ReminderCount + 1, PolicyRevision: due.ReminderPolicyRevision, Outcome: outcome, ResponsibilityTeamID: due.ResponsibilityTeamID}); err != nil {
		return false, err
	}
	if outcome == "queued" {
		if err = q.IssueApplicationReminderEnqueue(ctx, tx, sqlc.IssueApplicationReminderEnqueueParams{ID: issueUUID(uuid.NewString()), DestinationID: route.ID, DestinationRevision: route.Revision, ReminderID: id, IssueID: due.ID, ChannelID: route.ChannelID, PolicyRevision: pgtype.Int8{Int64: policy.Revision, Valid: true}, Recipients: route.Recipients, AssignedTeamID: due.AssigneeTeamID, AssignedAccountID: due.AssigneeAccountID}); err != nil {
			return false, err
		}
	}
	if err = q.IssueApplicationReminderReceipt(ctx, tx, sqlc.IssueApplicationReminderReceiptParams{IssueID: due.ID, Queued: outcome == "queued"}); err != nil {
		return false, err
	}
	if err = q.IssueApplicationReminderPrune(ctx, tx, sqlc.IssueApplicationReminderPruneParams{IssueID: due.ID, Offset: api.IssueApplicationReminderHistoryMax}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func applicationReminderHistory(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, issue pgtype.UUID) ([]IssueApplicationReminder, error) {
	rows, err := q.IssueApplicationReminderHistory(ctx, tx, sqlc.IssueApplicationReminderHistoryParams{IssueID: issue, Limit: api.IssueApplicationReminderHistoryMax})
	out := []IssueApplicationReminder{}
	for _, r := range rows {
		out = append(out, IssueApplicationReminder{ID: issueID(r.ID), Kind: r.Kind, ResponseID: issueID(r.ResponseID), Sequence: int(r.Sequence), Outcome: r.Outcome, DeliveryStatus: r.DeliveryStatus, TeamName: r.TeamName, CreatedAt: r.CreatedAt.Time})
	}
	return out, err
}
