package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueTrackerEscalationPolicy(ctx context.Context, app, owner string) (IssueTrackerEscalationPolicy, error) {
	row, err := sqlc.New().IssueTrackerEscalationPolicyGet(ctx, s.pool, sqlc.IssueTrackerEscalationPolicyGetParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueTrackerEscalationPolicy{DelayMinutes: api.IssueTrackerEscalationDelayDefaultMinutes}, nil
	}
	return IssueTrackerEscalationPolicy{Enabled: row.Enabled, DelayMinutes: int(row.DelayMinutes), Revision: row.Revision, TeamID: issueID(row.TeamID)}, err
}
func (s *PgStore) SetIssueTrackerEscalationPolicy(ctx context.Context, app, owner string, in IssueTrackerEscalationPolicy) (IssueTrackerEscalationPolicy, error) {
	if in.Revision < 0 || in.DelayMinutes < api.IssueTrackerEscalationDelayMinMinutes || in.DelayMinutes > api.IssueTrackerEscalationDelayMaxMinutes {
		return in, ErrInvalidArgument
	}
	if in.TeamID != "" {
		id, err := uuid.Parse(in.TeamID)
		if err != nil || id == uuid.Nil {
			return in, ErrInvalidArgument
		}
		in.TeamID = id.String()
	}
	if in.Enabled && in.TeamID == "" {
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
	old, err := q.IssueTrackerEscalationPolicyGet(ctx, tx, sqlc.IssueTrackerEscalationPolicyGetParams{ID: locked.ID, AccountID: locked.AccountID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return in, err
	}
	if old.Revision != in.Revision {
		return in, api.NewProblem(409, "issue_tracker_escalation_policy_changed", "Escalation settings changed", "reload escalation settings before saving")
	}
	if in.TeamID != "" {
		exists, err := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: locked.ID, TeamID: issueUUID(in.TeamID)})
		if err != nil {
			return in, err
		}
		if !exists {
			return in, ErrInvalidArgument
		}
	}
	row, err := q.IssueTrackerEscalationPolicyPut(ctx, tx, sqlc.IssueTrackerEscalationPolicyPutParams{AppID: locked.ID, AccountID: locked.AccountID, Enabled: in.Enabled, TeamID: issueUUID(in.TeamID), DelayMinutes: int32(in.DelayMinutes)})
	if err != nil {
		return in, err
	}
	return IssueTrackerEscalationPolicy{Enabled: row.Enabled, DelayMinutes: int(row.DelayMinutes), Revision: row.Revision, TeamID: issueID(row.TeamID)}, tx.Commit(ctx)
}

// App locks serialize escalation enqueue with acknowledgment, snoozes, recovery,
// routing changes and opt-outs. No provider call or external HTTP occurs here.
func (s *PgStore) EnqueueIssueTrackerEscalation(ctx context.Context) (bool, error) {
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
	app, err := q.IssueTrackerEscalationApp(ctx, tx, plans)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	due, err := q.IssueTrackerEscalationDue(ctx, tx, sqlc.IssueTrackerEscalationDueParams{AppID: app.ID, AccountID: app.AccountID, EntitledPlans: plans})
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
	escalation, err := q.IssueTrackerEscalationPolicyGet(ctx, tx, sqlc.IssueTrackerEscalationPolicyGetParams(app))
	if err != nil {
		return false, err
	}
	route.TeamID = issueID(escalation.TeamID)
	if err = pgTrackerSyncNotice(ctx, q, tx, record, route, &route, "escalation", int(policy.FailureThreshold), 0); err != nil {
		return false, err
	}
	if err = q.IssueTrackerEscalationReceipt(ctx, tx, sqlc.IssueTrackerEscalationReceiptParams{LinkID: due.LinkID, IncidentID: due.IncidentID, EscalationTeamID: escalation.TeamID, IssueID: issueUUID(route.IssueID), TeamID: issueUUID(route.TeamID), RecipientIds: trackerNotificationUUIDs(route.Recipients)}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
