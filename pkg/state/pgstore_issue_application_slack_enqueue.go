package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The caller holds the app lock. Routing, source activity and queue admission
// commit atomically, without requiring an app webhook subscription or network I/O.
func enqueueApplicationIssueSlack(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, row sqlc.AppIssue, activityID pgtype.UUID, action string, now time.Time) error {
	if !ApplicationSlackEvent(action) {
		return nil
	}
	if row.State == "ignored" && (!row.IgnoredUntil.Valid || row.IgnoredUntil.Time.After(now)) {
		return nil
	}
	snoozed, err := q.IssueApplicationTriageSnoozed(ctx, tx, row.ID)
	if err != nil {
		return err
	}
	if snoozed {
		return nil
	}
	policy, err := issueApplicationSlackPolicy(ctx, q, tx, issueID(row.AppID), issueID(row.AccountID))
	if err != nil {
		return err
	}
	if !policy.Enabled || !slices.Contains(policy.Events, action) {
		return nil
	}
	route, err := q.IssueApplicationSlackRoute(ctx, tx, sqlc.IssueApplicationSlackRouteParams{AppID: row.AppID, AccountID: row.AccountID, AssignedTeamID: row.AssigneeTeamID, FallbackTeamID: issueUUID(policy.FallbackTeamID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return q.IssueApplicationSlackMissed(ctx, tx, sqlc.IssueApplicationSlackMissedParams{AppID: row.AppID, Capacity: false})
	}
	if err != nil {
		return err
	}
	count, err := q.IssueApplicationSlackCount(ctx, tx, row.AppID)
	if err != nil {
		return err
	}
	if count >= api.IssueApplicationSlackMaxDeliveries {
		removed, e := q.IssueApplicationSlackHistoryTrim(ctx, tx, row.AppID)
		if e != nil {
			return e
		}
		if count-removed >= api.IssueApplicationSlackMaxDeliveries {
			return q.IssueApplicationSlackMissed(ctx, tx, sqlc.IssueApplicationSlackMissedParams{AppID: row.AppID, Capacity: true})
		}
	}
	id := uuid.NewSHA1(uuid.UUID(activityID.Bytes), []byte("application-slack"))
	return q.IssueApplicationSlackEnqueue(ctx, tx, sqlc.IssueApplicationSlackEnqueueParams{ID: issueUUID(id.String()), DestinationID: route.ID, DestinationRevision: route.Revision, ActivityID: activityID, IssueID: row.ID, ChannelID: route.ChannelID, PolicyRevision: pgtype.Int8{Int64: policy.Revision, Valid: true}, Recipients: route.Recipients, AssignedTeamID: row.AssigneeTeamID, AssignedAccountID: row.AssigneeAccountID})
}
