package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueTrackerReminderPolicy(ctx context.Context, app, owner string) (IssueTrackerReminderPolicy, error) {
	row, err := sqlc.New().IssueTrackerReminderPolicyGet(ctx, s.pool, sqlc.IssueTrackerReminderPolicyGetParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueTrackerReminderPolicy{IntervalMinutes: api.IssueTrackerReminderIntervalDefaultMinutes}, nil
	}
	return IssueTrackerReminderPolicy{Enabled: row.Enabled, IntervalMinutes: int(row.IntervalMinutes), Revision: row.Revision}, err
}
func (s *PgStore) SetIssueTrackerReminderPolicy(ctx context.Context, app, owner string, in IssueTrackerReminderPolicy) (IssueTrackerReminderPolicy, error) {
	if in.Revision < 0 || in.IntervalMinutes < api.IssueTrackerReminderIntervalMinMinutes || in.IntervalMinutes > api.IssueTrackerReminderIntervalMaxMinutes {
		return in, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	locked, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return in, mapErr(err)
	}
	if issueID(locked.AccountID) != owner {
		return in, ErrNotFound
	}
	old, err := q.IssueTrackerReminderPolicyGet(ctx, tx, sqlc.IssueTrackerReminderPolicyGetParams{ID: locked.ID, AccountID: locked.AccountID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return in, err
	}
	if old.Revision != in.Revision {
		return in, api.NewProblem(409, "issue_tracker_reminder_policy_changed", "Reminder settings changed", "reload reminder settings before saving")
	}
	row, err := q.IssueTrackerReminderPolicyPut(ctx, tx, sqlc.IssueTrackerReminderPolicyPutParams{AppID: locked.ID, AccountID: locked.AccountID, Enabled: in.Enabled, IntervalMinutes: int32(in.IntervalMinutes)})
	if err != nil {
		return in, err
	}
	return IssueTrackerReminderPolicy{Enabled: row.Enabled, IntervalMinutes: int(row.IntervalMinutes), Revision: row.Revision}, tx.Commit(ctx)
}

// App locks serialize reminder enqueue with acknowledgment, snoozes, recovery,
// routing changes and opt-outs. No provider call or external HTTP occurs here.
func (s *PgStore) EnqueueIssueTrackerReminder(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plans := []string{}
	for _, plan := range api.Plans {
		if plan.IssueLimits().Enabled {
			plans = append(plans, string(plan))
		}
	}
	app, err := q.IssueTrackerReminderApp(ctx, tx, sqlc.IssueTrackerReminderAppParams{EntitledPlans: plans, ReminderMax: api.IssueTrackerRemindersMax})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	due, err := q.IssueTrackerReminderDue(ctx, tx, sqlc.IssueTrackerReminderDueParams{AppID: app.ID, AccountID: app.AccountID, EntitledPlans: plans, ReminderMax: api.IssueTrackerRemindersMax})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stored, err := q.IssueTrackerAutoSyncGet(ctx, tx, due.LinkID)
	if err != nil {
		return false, err
	}
	record, err := autoSyncRow(stored)
	if err != nil {
		return false, err
	}
	origin, err := q.IssueTrackerReminderOrigin(ctx, tx, due.IncidentID)
	if err != nil {
		return false, err
	}
	policy, err := q.IssueTrackerNotificationPolicyGet(ctx, tx, sqlc.IssueTrackerNotificationPolicyGetParams{AppID: app.ID, AccountID: app.AccountID})
	if err != nil {
		return false, err
	}
	record.Policy.Outcome, record.Policy.Reason = origin.Outcome, origin.Reason
	route := trackerSyncIncident{ID: issueID(due.IncidentID), Failures: int(due.Failures), IssueID: issueID(due.IssueID), TeamID: issueID(due.TeamID)}
	for _, id := range due.RecipientAccountIds {
		route.Recipients = append(route.Recipients, issueID(id))
	}
	sequence := int(due.ReminderCount) + 1
	if err = pgTrackerSyncNotice(ctx, q, tx, record, route, &route, "reminder", int(policy.FailureThreshold), sequence); err != nil {
		return false, err
	}
	if err = q.IssueTrackerReminderReceipt(ctx, tx, sqlc.IssueTrackerReminderReceiptParams{LinkID: due.LinkID, IncidentID: due.IncidentID, Sequence: int32(sequence), IssueID: issueUUID(route.IssueID), TeamID: issueUUID(route.TeamID), RecipientIds: trackerNotificationUUIDs(route.Recipients)}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
