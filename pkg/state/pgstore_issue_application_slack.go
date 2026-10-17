package state

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueApplicationSlackPolicy(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, owner string) (IssueApplicationSlackPolicy, error) {
	row, err := q.IssueApplicationSlackPolicy(ctx, db, sqlc.IssueApplicationSlackPolicyParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	if err != nil {
		return IssueApplicationSlackPolicy{}, mapErr(err)
	}
	return IssueApplicationSlackPolicy{Enabled: row.Enabled, Revision: row.Revision, Events: row.Events, FallbackTeamID: issueID(row.FallbackTeamID), SkippedDeliveries: row.SkippedDeliveries, UnroutedAlerts: row.UnroutedAlerts}, nil
}
func (s *PgStore) GetIssueApplicationSlackPolicy(ctx context.Context, app, owner string) (IssueApplicationSlackPolicy, error) {
	return issueApplicationSlackPolicy(ctx, sqlc.New(), s.pool, app, owner)
}
func (s *PgStore) SetIssueApplicationSlackPolicy(ctx context.Context, app, owner string, in PutIssueApplicationSlackPolicy) (IssueApplicationSlackPolicy, error) {
	empty := IssueApplicationSlackPolicy{}
	events, err := normalizeApplicationSlackEvents(in.Events)
	if err != nil || in.Revision < 0 || in.Enabled && len(events) == 0 {
		return empty, ErrInvalidArgument
	}
	if in.FallbackTeamID != "" {
		id, e := uuid.Parse(in.FallbackTeamID)
		if e != nil || id == uuid.Nil {
			return empty, ErrInvalidArgument
		}
		in.FallbackTeamID = id.String()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	appRow, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return empty, mapErr(err)
	}
	if issueID(appRow.AccountID) != owner {
		return empty, ErrNotFound
	}
	current, err := issueApplicationSlackPolicy(ctx, q, tx, app, owner)
	if err != nil {
		return empty, err
	}
	if current.Revision != in.Revision {
		return empty, api.NewProblem(409, "issue_application_slack_changed", "Slack alert policy changed", "reload application Slack settings before saving")
	}
	if in.FallbackTeamID != "" {
		exists, e := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: appRow.ID, TeamID: issueUUID(in.FallbackTeamID)})
		if e != nil {
			return empty, e
		}
		if !exists {
			return empty, ErrInvalidArgument
		}
	}
	oldEvents := append([]string{}, current.Events...)
	slices.Sort(oldEvents)
	if current.Enabled == in.Enabled && current.FallbackTeamID == in.FallbackTeamID && slices.Equal(events, oldEvents) {
		return current, nil
	}
	if err = q.IssueApplicationSlackPolicyPut(ctx, tx, sqlc.IssueApplicationSlackPolicyPutParams{AppID: appRow.ID, Enabled: in.Enabled, Revision: in.Revision + 1, Events: events, FallbackTeamID: issueUUID(in.FallbackTeamID)}); err != nil {
		return empty, err
	}
	if err = q.IssueApplicationReminderStopSchedules(ctx, tx, appRow.ID); err != nil {
		return empty, err
	}
	if err = q.IssueApplicationSlackReleaseClaims(ctx, tx, appRow.ID); err != nil {
		return empty, err
	}
	if err = q.IssueApplicationSlackCancelPolicy(ctx, tx, appRow.ID); err != nil {
		return empty, err
	}
	out, err := issueApplicationSlackPolicy(ctx, q, tx, app, owner)
	if err != nil {
		return empty, err
	}
	return out, tx.Commit(ctx)
}
