package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) EvaluateWorkflowRecoveryPolicy(ctx context.Context, account, app string, req api.WorkflowRecoveryPolicyEvaluationRequest) (api.WorkflowRecoveryPolicyEvaluation, error) {
	var out api.WorkflowRecoveryPolicyEvaluation
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	c, err := recoveryEvaluationWindow(account, app, req, time.Now().UTC())
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("begin historical policy evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.WorkflowRecoveryAutomaticQueueApp(ctx, tx, sqlc.WorkflowRecoveryAutomaticQueueAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, fmt.Errorf("scope historical policy evaluation: %w", err)
	}
	current := defaultWorkflowRecoveryPolicy(canonicalMemUUID(app))
	row, err := q.WorkflowRecoveryPolicyGet(ctx, tx, sqlc.WorkflowRecoveryPolicyGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err == nil {
		current, err = pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
	} else if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return out, fmt.Errorf("read historical evaluation baseline: %w", err)
	}
	if current.Version != req.ExpectedVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	rows, err := q.WorkflowRecoveryPolicyEvaluationReviews(ctx, tx, sqlc.WorkflowRecoveryPolicyEvaluationReviewsParams{RetainedAfter: pgtypeFromTime(c.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention)), ObservedAt: pgtypeFromTime(c.ObservedAt), AppID: mustPgUUID(app), StartAt: pgtypeFromTime(c.Start), EndAt: pgtypeFromTime(c.End), Workflow: req.Workflow, PageLimit: api.WorkflowRecoveryPolicyEvaluationReviewsMax + 1})
	if err != nil {
		return out, fmt.Errorf("read historical review evidence: %w", err)
	}
	reviews := make([]recoveryEvaluationReview, 0, len(rows))
	for _, r := range rows {
		var review api.WorkflowRecoveryPolicyReview
		if err = json.Unmarshal(r.Review, &review); err != nil {
			return out, fmt.Errorf("decode historical review evidence: %w", err)
		}
		if !review.ObservedAt.Before(c.Start) && !review.ObservedAt.After(c.End) {
			reviews = append(reviews, recoveryEvaluationReview{review: review, workflow: r.Workflow, evidenceID: r.EvidenceID})
		}
	}
	rawExecutions, err := q.WorkflowRecoveryPolicyEvaluationExecutions(ctx, tx, sqlc.WorkflowRecoveryPolicyEvaluationExecutionsParams{AppID: mustPgUUID(app), StartAt: pgtypeFromTime(c.Start.Add(-recoveryEvaluationLookback(current.Policy, req.Policy))), EndAt: pgtypeFromTime(c.End), RetainedAfter: pgtypeFromTime(c.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention)), ObservedAt: pgtypeFromTime(c.ObservedAt), PageLimit: api.WorkflowRecoveryPolicyEvaluationExecutionsMax + 1})
	if err != nil {
		return out, fmt.Errorf("read historical application evidence: %w", err)
	}
	executions := make([]api.WorkflowRecoveryHistoryEntry, 0, len(rawExecutions))
	for _, raw := range rawExecutions {
		var e api.WorkflowRecoveryHistoryEntry
		if err = json.Unmarshal(raw, &e); err != nil {
			return out, fmt.Errorf("decode historical application evidence: %w", err)
		}
		executions = append(executions, e)
	}
	failures, err := recoveryFailureSnapshotRead(ctx, q, tx, app, req, c)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit read-only policy evaluation: %w", err)
	}
	return evaluateRecoveryPolicy(ctx, c, current, req, reviews, executions, failures)
}
