package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ObserveNextWorkflowFailureEligibility(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var worked bool
	var err error
	for attempt := 0; attempt < api.WorkflowRecoveryFailureObserverTxnAttempts; attempt++ {
		worked, err = s.observeWorkflowFailureEligibility(ctx, now)
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || conflict.Code != "40001" {
			return worked, err
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
	}
	return false, err
}

func (s *PgStore) observeWorkflowFailureEligibility(ctx context.Context, now time.Time) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin failure eligibility observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.WorkflowFailureEligibilityNext(ctx, tx, sqlc.WorkflowFailureEligibilityNextParams{NowAt: pgtypeFromTime(now), RetainedAfter: pgtypeFromTime(now.Add(-api.WorkflowRecoveryFailureRetention)), RefreshBefore: pgtypeFromTime(now.Add(-api.WorkflowRecoveryFailureObserverRefresh))})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim failure eligibility observation: %w", err)
	}
	e := workflowFailureEvidence{ID: uuidString(row.ID), AppID: uuidString(row.AppID), RunID: uuidString(row.RunID), Workflow: row.Workflow, ResumeCount: int(row.ResumeCount), FailureStatus: row.FailureStatus}
	account := uuidString(row.AccountID)
	inspection, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin failure inspection: %w", err)
	}
	d, diagnosticErr := workflowRunDiagnosticsRead(ctx, q, inspection, WorkflowDiagnosticsOptions{RunID: e.RunID, AppID: e.AppID, AccountID: account})
	_, ownerErr := q.GetCustomerOperationForWorkflow(ctx, inspection, row.RunID)
	if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
		diagnosticErr = ownerErr
	}
	observation, inspectionErr := failureEligibilityDiagnostic(e, d, diagnosticErr, ownerErr == nil, now)
	if inspectionErr != nil {
		if err = inspection.Rollback(ctx); err != nil {
			return false, fmt.Errorf("rollback failure inspection: %w", err)
		}
		observation = &api.WorkflowRecoveryFailureEligibility{ObservedAt: now.UTC().Truncate(time.Microsecond), Reason: "inspection_error", Blockers: []string{}}
	} else if err = inspection.Commit(ctx); err != nil {
		return false, fmt.Errorf("finish failure inspection: %w", err)
	}
	parent, err := q.WorkflowFailureEligibilityParent(ctx, tx, sqlc.WorkflowFailureEligibilityParentParams{AppID: row.AppID, AccountID: row.AccountID, RunID: row.RunID, Workflow: row.Workflow, ResumeCount: row.ResumeCount, NowAt: pgtypeFromTime(now)})
	if err == nil {
		observation.ParentJobID, observation.ParentPosition = uuidString(parent.ID), parent.Position
	} else if errors.Is(err, pgx.ErrNoRows) {
		if observation.Reason == "eligible" || observation.Reason == "capacity" {
			observation.Reason = "parent_not_retained"
		}
	} else {
		return false, fmt.Errorf("find saved failure recovery parent: %w", err)
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return false, fmt.Errorf("encode failure eligibility observation: %w", err)
	}
	if err = q.WorkflowFailureEligibilitySave(ctx, tx, sqlc.WorkflowFailureEligibilitySaveParams{ID: row.ID, ObservedAt: pgtypeFromTime(observation.ObservedAt), Eligibility: encoded}); err != nil {
		return false, fmt.Errorf("save failure eligibility observation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit failure eligibility observation: %w", err)
	}
	return true, nil
}
