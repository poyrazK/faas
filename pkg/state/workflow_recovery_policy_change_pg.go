package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) PreviewWorkflowRecoveryPolicyChange(ctx context.Context, account, app string, req api.SetWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyChangePreview, error) {
	var out api.WorkflowRecoveryPolicyChangePreview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("begin recovery policy change preview: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := time.Now().UTC()
	queue, last, err := pgWorkflowRecoveryAutomaticQueue(ctx, q, tx, account, app, now, true)
	if err != nil {
		return out, err
	}
	if queue.Policy.Version != req.ExpectedVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	b, err := q.WorkflowRecoveryPolicyBudget(ctx, tx, sqlc.WorkflowRecoveryPolicyBudgetParams{AppID: mustPgUUID(app), SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
	if err != nil {
		return out, fmt.Errorf("read proposed preparation budget: %w", err)
	}
	budget := recoveryPolicyBudget{Hourly: int(b.HourlyActions), Last: b.LastPreparedAt.Time, Existing: map[string]bool{}}
	out = recoveryPolicyChangePreview(queue, req.Policy, last, budget)
	jobs, err := q.WorkflowRecoveryPolicyChangeJobs(ctx, tx, sqlc.WorkflowRecoveryPolicyChangeJobsParams{AppID: mustPgUUID(app), WorkflowNames: req.Policy.WorkflowNames, PageLimit: api.WorkflowRecoveryPolicyJobsPerPoll + 1})
	if err != nil {
		return out, fmt.Errorf("read recent policy change targets: %w", err)
	}
	if len(jobs) > api.WorkflowRecoveryPolicyJobsPerPoll {
		out.JobsTruncated = true
		jobs = jobs[:api.WorkflowRecoveryPolicyJobsPerPoll]
	}
	for _, id := range jobs {
		job, err := getEventRecoveryMetadata(ctx, q, tx, account, uuidString(id))
		if err != nil {
			return out, fmt.Errorf("read proposed policy job: %w", err)
		}
		candidates, err := pgRecoveryPolicyCandidates(ctx, q, tx, account, req.Policy, job, now)
		if err != nil {
			return out, fmt.Errorf("inspect proposed policy targets: %w", err)
		}
		runIDs := []pgtype.UUID{}
		for _, c := range candidates {
			runIDs = append(runIDs, mustPgUUID(c.Decision.RunID))
		}
		targets, err := q.WorkflowRecoveryPolicyTargets(ctx, tx, sqlc.WorkflowRecoveryPolicyTargetsParams{AppID: mustPgUUID(app), RunIds: runIDs})
		if err != nil {
			return out, fmt.Errorf("read retained policy targets: %w", err)
		}
		for _, t := range targets {
			budget.Existing[recoveryPolicyTargetKey(uuidString(t.RunID), int(t.ResumeCount), t.Action)] = true
		}
		out.InspectedJobs++
		addRecoveryPolicyChangeCandidates(&out, job, candidates, &budget)
	}
	if err = previewInitialPolicyChangeTx(ctx, q, tx, account, app, &out, &budget, last); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("finish policy change preview: %w", err)
	}
	return out, nil
}
