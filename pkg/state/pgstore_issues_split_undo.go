package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// UndoIssueSplitRule disables routing and merges an isolated branch atomically.
func (s *PgStore) UndoIssueSplitRule(ctx context.Context, app, source, id, actor string, in api.IssueSplitRuleUndoRequest, now time.Time) (api.IssueSplitRule, error) {
	expected, err := uuid.Parse(in.CanonicalSourceIssueID)
	if err != nil {
		return api.IssueSplitRule{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return api.IssueSplitRule{}, mapErr(err)
	}
	rule, err := q.IssueGetSplitRule(ctx, tx, sqlc.IssueGetSplitRuleParams{AppID: issueUUID(app), SourceIssueID: issueUUID(source), ID: issueUUID(id)})
	if err != nil {
		return api.IssueSplitRule{}, mapErr(err)
	}
	destination, err := issueCanonicalRow(ctx, q, tx, app, source)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	if issueID(destination.ID) != expected.String() {
		return api.IssueSplitRule{}, ErrIssueGroupingConflict
	}
	branch, err := issueCanonicalRow(ctx, q, tx, app, issueID(rule.TargetIssueID))
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	if branch.ID != destination.ID {
		if branch.ID != rule.TargetIssueID {
			return api.IssueSplitRule{}, ErrIssueGroupingConflict
		}
		members, err := q.IssueGroupMembers(ctx, tx, branch.ID)
		if err != nil {
			return api.IssueSplitRule{}, err
		}
		if len(members) != 1 {
			return api.IssueSplitRule{}, ErrIssueGroupingConflict
		}
	}
	rule, err = issueSetSplitRuleEnabled(ctx, q, tx, rule, actor, false, now)
	if err != nil {
		return api.IssueSplitRule{}, err
	}
	if branch.ID != destination.ID {
		_, err = issueMergeGroups(ctx, q, tx, branch, issueID(branch.ID), actor, api.IssueActionRequest{Action: "merge", TargetIssueID: issueID(destination.ID)}, now)
		if err != nil {
			return api.IssueSplitRule{}, err
		}
	}
	return issueSplitRuleRow(rule), tx.Commit(ctx)
}
