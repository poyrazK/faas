package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Original identities own evidence and counters. Canonical identities own the
// current inbox entry and lifecycle; every link points directly to a root.
func issueCanonicalRow(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, id string) (sqlc.AppIssue, error) {
	root, err := q.IssueCanonicalID(ctx, db, sqlc.IssueCanonicalIDParams{AppID: issueUUID(app), ID: issueUUID(id)})
	if err != nil {
		return sqlc.AppIssue{}, mapErr(err)
	}
	row, err := q.IssueGet(ctx, db, sqlc.IssueGetParams{AppID: issueUUID(app), ID: root})
	return row, mapErr(err)
}

func issueGroupRow(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, row sqlc.AppIssue) (sqlc.AppIssue, error) {
	group, err := q.IssueGetGroup(ctx, db, sqlc.IssueGetGroupParams{AppID: row.AppID, ID: row.ID})
	return sqlc.AppIssue(group), mapErr(err)
}

func issueGroupView(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, row sqlc.AppIssue) (api.Issue, error) {
	group, err := issueGroupRow(ctx, q, db, row)
	return issueRow(group), err
}

func issueRegroupAudit(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, action, actor string, details map[string]string, now time.Time) error {
	if err := q.IssueSlackActionsInvalidate(ctx, tx, sqlc.IssueSlackActionsInvalidateParams{AppID: row.AppID, IssueID: row.ID}); err != nil {
		return err
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.IssueAddActivity(ctx, tx, sqlc.IssueAddActivityParams{
		IssueID: row.ID, Action: action, ActorAccountID: issueUUID(actor), CreatedAt: issueTime(now), Details: raw,
	})
	return err
}

// Caller holds the app lock shared with ingestion and lifecycle mutations.
func issueMergeGroups(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, source sqlc.AppIssue, requestedID, actor string, in api.IssueActionRequest, now time.Time) (sqlc.AppIssue, error) {
	if requestedID == in.TargetIssueID {
		return sqlc.AppIssue{}, ErrInvalidArgument
	}
	target, err := issueCanonicalRow(ctx, q, tx, issueID(source.AppID), in.TargetIssueID)
	if err != nil {
		return sqlc.AppIssue{}, err
	}
	if source.ID == target.ID {
		if requestedID == issueID(source.ID) {
			return sqlc.AppIssue{}, ErrIssueGroupingConflict
		}
		return issueGroupRow(ctx, q, tx, target)
	}
	if issueID(source.ID) != requestedID || source.Environment != target.Environment {
		return sqlc.AppIssue{}, ErrIssueGroupingConflict
	}
	members, err := q.IssueGroupMembers(ctx, tx, source.ID)
	if err != nil {
		return sqlc.AppIssue{}, err
	}
	dest, err := q.IssueGroupMembers(ctx, tx, target.ID)
	if err != nil {
		return sqlc.AppIssue{}, err
	}
	total := len(members) + len(dest)
	if total > api.IssueGroupMembersMax {
		return sqlc.AppIssue{}, &IssueLimitError{Resource: "group_members", Limit: api.IssueGroupMembersMax, Observed: int64(total)}
	}
	// Reparent children first; the database guard forbids a linked parent.
	if err = q.IssueReparentGroup(ctx, tx, sqlc.IssueReparentGroupParams{SourceID: source.ID, TargetID: target.ID, Now: issueTime(now)}); err != nil {
		return sqlc.AppIssue{}, err
	}
	if err = q.IssueLinkGroup(ctx, tx, sqlc.IssueLinkGroupParams{
		SourceID: source.ID, TargetID: target.ID, AppID: source.AppID, Environment: source.Environment, Now: issueTime(now),
	}); err != nil {
		return sqlc.AppIssue{}, err
	}
	if err = q.IssueSyncGroupAction(ctx, tx, target.ID); err != nil {
		return sqlc.AppIssue{}, err
	}
	details := map[string]string{"source_issue_id": issueID(source.ID), "canonical_issue_id": issueID(target.ID), "group_members": fmt.Sprint(total)}
	for _, row := range []sqlc.AppIssue{source, target} {
		if err = issueRegroupAudit(ctx, q, tx, row, "merged", actor, details, now); err != nil {
			return sqlc.AppIssue{}, err
		}
	}
	return issueGroupRow(ctx, q, tx, target)
}

func issueSplitGroup(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, root sqlc.AppIssue, actor string, in api.IssueActionRequest, now time.Time) (sqlc.AppIssue, error) {
	if in.MemberIssueID == issueID(root.ID) {
		return sqlc.AppIssue{}, ErrInvalidArgument
	}
	current, err := issueCanonicalRow(ctx, q, tx, issueID(root.AppID), in.MemberIssueID)
	if err != nil {
		return sqlc.AppIssue{}, err
	}
	if current.ID != root.ID {
		if issueID(current.ID) == in.MemberIssueID {
			done, checkErr := q.IssueHasSplitAudit(ctx, tx, sqlc.IssueHasSplitAuditParams{MemberID: current.ID, CanonicalID: issueID(root.ID)})
			if checkErr != nil {
				return sqlc.AppIssue{}, checkErr
			}
			if done {
				return issueGroupRow(ctx, q, tx, root)
			}
		}
		return sqlc.AppIssue{}, ErrIssueGroupingConflict
	}
	n, err := q.IssueUnlinkGroup(ctx, tx, sqlc.IssueUnlinkGroupParams{ID: root.ID, MemberID: issueUUID(in.MemberIssueID)})
	if err != nil {
		return sqlc.AppIssue{}, err
	}
	if n != 1 {
		return sqlc.AppIssue{}, ErrIssueGroupingConflict
	}
	member, err := q.IssueUpdateAction(ctx, tx, sqlc.IssueUpdateActionParams{
		ID: issueUUID(in.MemberIssueID), State: "open", Assignee: root.AssigneeAccountID, AssigneeTeamID: root.AssigneeTeamID,
	})
	if err != nil {
		return sqlc.AppIssue{}, err
	}
	if err = resetApplicationTriage(ctx, q, tx, member, actor); err != nil {
		return sqlc.AppIssue{}, err
	}
	details := map[string]string{"member_issue_id": in.MemberIssueID, "canonical_issue_id": issueID(root.ID)}
	for _, row := range []sqlc.AppIssue{root, member} {
		if err = issueRegroupAudit(ctx, q, tx, row, "split", actor, details, now); err != nil {
			return sqlc.AppIssue{}, err
		}
	}
	return issueGroupRow(ctx, q, tx, root)
}
