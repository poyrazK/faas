package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Caller holds the source lock. Control/approval transactions pass nil and only
// track approval or supersession. Report transactions independently read the
// current workload graph and receipts on the fenced source before publishing.
func pgEnvironmentGitRolloutNotification(ctx context.Context, tx pgx.Tx, sourceID, runID string, plan *api.EnvironmentGitOpsPlan, now time.Time) error {
	q := sqlc.New()
	row, err := q.GetEnvironmentGitSourceByID(ctx, tx, mustPgUUID(sourceID))
	if err != nil {
		return mapErr(err)
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, row.ID)
	if err != nil {
		return mapErr(err)
	}
	source := environmentGitSourceFromSQL(row, scope.EnvironmentSlug)
	var revision EnvironmentDesiredRevision
	if row.ApprovedRevisionID.Valid {
		rawRevision, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{SourceID: row.ID, RevisionID: row.ApprovedRevisionID})
		if err != nil {
			return mapErr(err)
		}
		revision = environmentRevisionFromSQL(rawRevision)
	}
	var evidence *EnvironmentWorkloadActivationEvidence
	if plan != nil {
		if source.Spec.Mode != "enforce" || !environmentGitRolloutReportCurrent(source, revision, *plan) {
			return nil
		}
		evidence, err = currentEnvironmentGitOpsWorkloadEvidenceTx(ctx, tx, source.AccountID, source.ID)
		if err != nil {
			return err
		}
		if evidence != nil && evidence.PlanHash != plan.Hash {
			return nil
		}
	}
	var baseline environmentGitNotificationState
	if err := json.Unmarshal(row.NotificationState, &baseline); err != nil {
		return err
	}
	next, events, err := prepareEnvironmentGitRolloutNotifications(baseline, source, revision, scope.ProjectSlug, runID, evidence, now)
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
	for _, event := range events {
		if err := q.EnqueueEnvironmentGitNotification(ctx, tx, sqlc.EnqueueEnvironmentGitNotificationParams{
			AccountID: row.AccountID, EventID: mustPgUUID(event.id), Payload: event.body, NowAt: gitOpsTime(now)}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}
