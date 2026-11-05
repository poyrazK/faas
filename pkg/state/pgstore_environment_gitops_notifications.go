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

// The caller holds the source row lock. The baseline and complete delivery
// fan-out commit with the report or source poll, avoiding crash notification gaps.
func pgEnvironmentGitNotification(ctx context.Context, tx pgx.Tx, sourceID, axis, status, runID, code string, plan api.EnvironmentGitOpsPlan, now time.Time) error {
	q := sqlc.New()
	row, err := q.GetEnvironmentGitSourceByID(ctx, tx, mustPgUUID(sourceID))
	if err != nil {
		return mapErr(err)
	}
	if axis == "drift" && plan.Hash != "" && row.IntentVersion != plan.ObservedVersion {
		return nil // historical observations must not change the current alert baseline
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, row.ID)
	if err != nil {
		return mapErr(err)
	}
	if axis == "source" || plan.CommitSHA == "" {
		plan.CommitSHA = ""
		if row.ApprovedRevisionID.Valid {
			revision, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{SourceID: row.ID, RevisionID: row.ApprovedRevisionID})
			if err != nil {
				return mapErr(err)
			}
			plan.CommitSHA = revision.CommitSha
		}
	}
	var baseline environmentGitNotificationState
	if err := json.Unmarshal(row.NotificationState, &baseline); err != nil {
		return fmt.Errorf("read GitOps notification baseline: %w", err)
	}
	next, payload, eventID, err := prepareEnvironmentGitNotification(baseline, environmentGitSourceFromSQL(row, scope.EnvironmentSlug), scope.ProjectSlug, axis, status, runID, code, plan, now)
	if err != nil {
		return err
	}
	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := q.WriteEnvironmentGitNotificationState(ctx, tx, sqlc.WriteEnvironmentGitNotificationStateParams{SourceID: row.ID, NotificationState: body}); err != nil {
		return mapErr(err)
	}
	if eventID == "" {
		return nil
	}
	return mapErr(q.EnqueueEnvironmentGitNotification(ctx, tx, sqlc.EnqueueEnvironmentGitNotificationParams{
		AccountID: row.AccountID, SourceID: row.ID, EventID: mustPgUUID(eventID), Payload: payload, NowAt: gitOpsTime(now)}))
}
