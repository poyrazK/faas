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

func (s *PgStore) GetIssueApplicationEscalationPolicy(ctx context.Context, app, owner string) (IssueApplicationEscalationPolicy, error) {
	r, err := sqlc.New().IssueApplicationEscalationPolicy(ctx, s.pool, sqlc.IssueApplicationEscalationPolicyParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	return IssueApplicationEscalationPolicy{Enabled: r.Enabled, Revision: r.Revision, DelayMinutes: int(r.DelayMinutes), TeamID: issueID(r.TeamID), TeamName: r.TeamName}, mapErr(err)
}
func (s *PgStore) SetIssueApplicationEscalationPolicy(ctx context.Context, app, owner string, in IssueApplicationEscalationPolicy) (IssueApplicationEscalationPolicy, error) {
	if in.Revision < 0 || in.DelayMinutes < api.IssueApplicationEscalationMinMinutes || in.DelayMinutes > api.IssueApplicationEscalationMaxMinutes {
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
	a, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return in, mapErr(err)
	}
	if issueID(a.AccountID) != owner {
		return in, ErrNotFound
	}
	r, err := q.IssueApplicationEscalationPolicy(ctx, tx, sqlc.IssueApplicationEscalationPolicyParams{ID: a.ID, AccountID: a.AccountID})
	if err != nil {
		return in, mapErr(err)
	}
	if r.Revision != in.Revision {
		return in, api.NewProblem(409, "issue_application_escalation_changed", "Escalation settings changed", "reload native escalation settings before saving")
	}
	if in.TeamID != "" {
		ok, err := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: a.ID, TeamID: issueUUID(in.TeamID)})
		if err != nil {
			return in, err
		}
		if !ok {
			return in, ErrInvalidArgument
		}
	}
	if r.Enabled == in.Enabled && int(r.DelayMinutes) == in.DelayMinutes && issueID(r.TeamID) == in.TeamID {
		in.TeamName = r.TeamName
		return in, tx.Commit(ctx)
	}
	in.Revision++
	if err = q.IssueApplicationEscalationPolicyPut(ctx, tx, sqlc.IssueApplicationEscalationPolicyPutParams{AppID: a.ID, Enabled: in.Enabled, Revision: in.Revision, DelayMinutes: int32(in.DelayMinutes), TeamID: issueUUID(in.TeamID)}); err != nil {
		return in, err
	}
	if err = q.IssueApplicationEscalationRelease(ctx, tx, a.ID); err != nil {
		return in, err
	}
	if err = q.IssueApplicationEscalationCancel(ctx, tx, a.ID); err != nil {
		return in, err
	}
	r, err = q.IssueApplicationEscalationPolicy(ctx, tx, sqlc.IssueApplicationEscalationPolicyParams{ID: a.ID, AccountID: a.AccountID})
	if err != nil {
		return in, err
	}
	in.TeamName = r.TeamName
	return in, tx.Commit(ctx)
}

// One attempt per response, recorded with admission under the shared app lock.
// Only an admitted escalation transfers responsibility for response reminders.
func (s *PgStore) EnqueueIssueApplicationEscalation(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	app, err := q.IssueApplicationEscalationApp(ctx, tx, issueSlackEntitledPlans())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	due, err := q.IssueApplicationEscalationDue(ctx, tx, sqlc.IssueApplicationEscalationDueParams{AppID: app, EntitledPlans: issueSlackEntitledPlans()})
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
	route, err := q.IssueApplicationSlackRoute(ctx, tx, sqlc.IssueApplicationSlackRouteParams{AppID: due.AppID, AccountID: due.AccountID, AssignedTeamID: due.BackupTeamID})
	outcome := "queued"
	if errors.Is(err, pgx.ErrNoRows) {
		outcome = "unrouted"
	} else if err != nil {
		return false, err
	}
	if outcome == "queued" {
		original, e := q.IssueApplicationSlackRoute(ctx, tx, sqlc.IssueApplicationSlackRouteParams{AppID: due.AppID, AccountID: due.AccountID, AssignedTeamID: due.AssigneeTeamID, FallbackTeamID: issueUUID(policy.FallbackTeamID)})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return false, e
		}
		if original.ID == route.ID {
			outcome = "already_responsible"
		}
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
	if outcome == "unrouted" || outcome == "queue_full" {
		if err = q.IssueApplicationSlackMissed(ctx, tx, sqlc.IssueApplicationSlackMissedParams{AppID: due.AppID, Capacity: outcome == "queue_full"}); err != nil {
			return false, err
		}
	}
	id := issueUUID(uuid.NewString())
	if err = q.IssueApplicationEscalationRecord(ctx, tx, sqlc.IssueApplicationEscalationRecordParams{ID: id, IssueID: due.ID, ResponseID: due.ResponseID, PolicyRevision: due.ReminderPolicyRevision, EscalationPolicyRevision: pgtype.Int8{Int64: due.EscalationPolicyRevision, Valid: true}, ResponsibilityTeamID: due.BackupTeamID, TargetTeamName: due.BackupTeamName, Outcome: outcome}); err != nil {
		return false, err
	}
	if outcome == "queued" {
		if err = cancelApplicationReminders(ctx, q, tx, due.AppID, due.ID); err != nil {
			return false, err
		}
		if err = q.IssueApplicationReminderEnqueue(ctx, tx, sqlc.IssueApplicationReminderEnqueueParams{ID: issueUUID(uuid.NewString()), DestinationID: route.ID, DestinationRevision: route.Revision, ReminderID: id, IssueID: due.ID, ChannelID: route.ChannelID, PolicyRevision: pgtype.Int8{Int64: policy.Revision, Valid: true}, Recipients: route.Recipients, AssignedTeamID: due.AssigneeTeamID, AssignedAccountID: due.AssigneeAccountID}); err != nil {
			return false, err
		}
	}
	if err = q.IssueApplicationEscalationReceipt(ctx, tx, sqlc.IssueApplicationEscalationReceiptParams{IssueID: due.ID, Queued: outcome == "queued", TeamID: due.BackupTeamID}); err != nil {
		return false, err
	}
	if err = q.IssueApplicationReminderPrune(ctx, tx, sqlc.IssueApplicationReminderPruneParams{IssueID: due.ID, Offset: api.IssueApplicationReminderHistoryMax}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
