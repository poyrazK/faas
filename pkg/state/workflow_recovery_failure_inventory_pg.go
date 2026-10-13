package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func recoveryFailureSnapshotRead(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app string, req api.WorkflowRecoveryPolicyEvaluationRequest, c recoveryHistoryCursor) (recoveryFailureSnapshot, error) {
	out := recoveryFailureSnapshot{Failures: []workflowFailureEvidence{}}
	started, err := q.WorkflowRecoveryFailureCaptureStart(ctx, db)
	if err != nil {
		return out, fmt.Errorf("read failure capture coverage: %w", err)
	}
	out.StartedAt = timeFromPgtype(started)
	rows, err := q.WorkflowRecoveryFailureEvaluation(ctx, db, sqlc.WorkflowRecoveryFailureEvaluationParams{AppID: mustPgUUID(app), StartAt: pgtypeFromTime(c.Start), EndAt: pgtypeFromTime(c.End), ObservedAt: pgtypeFromTime(c.ObservedAt), RetainedAfter: pgtypeFromTime(c.ObservedAt.Add(-api.WorkflowRecoveryFailureRetention)), Workflow: req.Workflow, PageLimit: api.WorkflowRecoveryPolicyEvaluationFailuresMax + 1})
	if err != nil {
		return out, fmt.Errorf("read policy-independent failure evidence: %w", err)
	}
	for _, r := range rows {
		var eligibility *api.WorkflowRecoveryFailureEligibility
		if len(r.Eligibility) > 0 {
			if err := json.Unmarshal(r.Eligibility, &eligibility); err != nil {
				return out, fmt.Errorf("decode failure eligibility: %w", err)
			}
		}
		out.Failures = append(out.Failures, workflowFailureEvidence{Eligibility: eligibility, ID: uuidString(r.ID), AppID: uuidString(r.AppID), RunID: uuidString(r.RunID), Workflow: r.Workflow, ResumeCount: int(r.ResumeCount), FailureStatus: r.FailureStatus, ObservedAt: timeFromPgtype(r.ObservedAt), CompletedAt: timestamptzToTimePtr(r.CompletedAt)})
	}
	return out, nil
}
