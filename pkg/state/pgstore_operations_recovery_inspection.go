// adr: 598
package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func operationRecoverySnapshotTx(ctx context.Context, db sqlc.DBTX, accountID, id string) (operationRecoverySnapshot, error) {
	q := sqlc.New()
	operationID, err := operationUUID(id)
	if err != nil {
		return operationRecoverySnapshot{}, err
	}
	account, err := operationUUID(accountID)
	if err != nil {
		return operationRecoverySnapshot{}, err
	}
	raw, err := q.GetCustomerOperation(ctx, db, sqlc.GetCustomerOperationParams{ID: operationID, AccountID: account})
	if err != nil {
		return operationRecoverySnapshot{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return operationRecoverySnapshot{}, err
	}
	if !operationRetained(op, time.Now()) {
		return operationRecoverySnapshot{}, ErrOperationExpired
	}
	s := operationRecoverySnapshot{op: op, blobs: map[string]OperationResultBlob{}}
	definition, err := q.GetCustomerOperationDefinition(ctx, db, sqlc.GetCustomerOperationDefinitionParams{ID: mustPgUUID(op.DefinitionID), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return s, mapErr(err)
	}
	s.def, err = operationPGDefinition(definition)
	if err != nil {
		return s, err
	}
	plan, err := q.CustomerOperationAccountPlan(ctx, db, mustPgUUID(accountID))
	if err != nil {
		return s, mapErr(err)
	}
	s.plan = api.Plan(plan)
	s.codeAvailable, err = operationRecoveryCodeAvailableTx(ctx, db, op)
	if err != nil {
		return s, err
	}
	status, err := q.CustomerOperationRecoveryTenantStatus(ctx, db, sqlc.CustomerOperationRecoveryTenantStatusParams{TenantID: mustPgUUID(op.PlatformTenantID), AccountID: mustPgUUID(accountID)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	s.tenantActive = err == nil && status == PlatformTenantActive
	for _, key := range op.ArtifactStorageKeys {
		row, err := q.CustomerOperationBlobByKey(ctx, db, key)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return s, err
		}
		s.blobs[key] = operationPGBlob(row)
	}
	if op.JobRunID != "" {
		row, err := q.ReadCustomerOperationJobTask(ctx, db, mustPgUUID(op.JobRunID))
		if err != nil {
			return s, err
		}
		task := operationJobTaskFromSQL(row)
		s.jobTask = &task
		s.targetAvailable, err = q.ReadCustomerOperationJobTarget(ctx, db, sqlc.ReadCustomerOperationJobTargetParams{JobID: mustPgUUID(op.JobSnapshot.JobID), AccountID: mustPgUUID(accountID)})
		if err != nil {
			return s, err
		}
		target, err := q.ReadCustomerOperationWorkflowTarget(ctx, db, mustPgUUID(op.AppID))
		if err != nil {
			return s, err
		}
		s.targetAvailable = s.targetAvailable && target.AppStatus != string(AppDeleted) && !target.MaintenanceMode
		return s, nil
	}
	if op.WorkflowRunID == "" {
		row, err := q.ReadCustomerOperationRecoveryInvocation(ctx, db, mustPgUUID(op.CurrentInvocationID))
		if err != nil {
			return s, mapErr(err)
		}
		s.inv, err = invocationFromSQL(row)
		return s, err
	}
	return operationRecoveryWorkflowSnapshotTx(ctx, db, s)
}

func operationRecoveryWorkflowSnapshotTx(ctx context.Context, db sqlc.DBTX, s operationRecoverySnapshot) (operationRecoverySnapshot, error) {
	q := sqlc.New()
	runID := mustPgUUID(s.op.WorkflowRunID)
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, db, runID)
	if err != nil {
		return s, mapErr(err)
	}
	s.run = workflowRunFromSQLC(row)
	if s.run.AppID != s.op.AppID || s.run.PlatformTenantID != s.op.PlatformTenantID {
		return s, ErrNotFound
	}
	rows, err := q.ReadCustomerOperationWorkflowSteps(ctx, db, runID)
	if err != nil {
		return s, err
	}
	s.steps = make(map[string]WorkflowStep, len(rows))
	for _, step := range rows {
		s.steps[step.StepName] = WorkflowStep{RunID: s.run.ID, StepName: step.StepName, Status: step.Status, Attempt: int(step.Attempt), Input: step.Input, Output: step.Output,
			SkipReason: workflowResumeTextPtr(step.SkipReason), ForEachParent: workflowResumeTextPtr(step.ForeachParent), ForEachIndex: workflowResumeIntPtr(step.ForeachIndex), ForEachCount: workflowResumeIntPtr(step.ForeachCount), RetryBase: int(step.RetryBase)}
	}
	target, err := q.ReadCustomerOperationWorkflowTarget(ctx, db, mustPgUUID(s.op.AppID))
	if err != nil {
		return s, mapErr(err)
	}
	s.targetAvailable = pgUUIDString(target.AccountID) == s.op.AccountID && s.plan.WorkflowsAllowed() && (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid && target.AppStatus != string(AppDeleted) && !target.MaintenanceMode && (!target.PlatformTenantRequired || s.run.PlatformTenantID != "")
	s.runningAttempt, err = q.WorkflowResumeHasRunningAttempts(ctx, db, runID)
	if err != nil {
		return s, err
	}
	active, err := q.CountActiveWorkflowRunsForAdmission(ctx, db, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(s.op.AppID)})
	s.activeRuns = int(active)
	return s, err
}

func (s *PgStore) readOperationRecoverySnapshot(ctx context.Context, accountID, id string) (operationRecoverySnapshot, error) {
	// PostgreSQL rejects mutations and row locks. These read queries also
	// avoid advisory locks, which read-only transactions do not prohibit.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return operationRecoverySnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return operationRecoverySnapshotTx(ctx, tx, accountID, id)
}

func (s *PgStore) InspectOperationRecovery(ctx context.Context, accountID, id string) (api.OperationRecoveryInspection, error) {
	snapshot, err := s.readOperationRecoverySnapshot(ctx, accountID, id)
	if err != nil {
		return api.OperationRecoveryInspection{}, err
	}
	return operationRecoveryInspection(snapshot, time.Now().UTC()), nil
}

func (s *PgStore) PreviewOperationRecovery(ctx context.Context, accountID, id string, req api.OperationRecoveryPreviewRequest) (api.OperationRecoveryPreview, error) {
	snapshot, err := s.readOperationRecoverySnapshot(ctx, accountID, id)
	if err != nil {
		return api.OperationRecoveryPreview{}, err
	}
	return operationRecoveryPreview(snapshot, req, time.Now().UTC())
}

func checkOperationInspectionRevisionTx(ctx context.Context, db sqlc.DBTX, op Operation, revision string) error {
	if revision == "" {
		return nil
	}
	s, err := operationRecoverySnapshotTx(ctx, db, op.AccountID, op.ID)
	if err != nil {
		return err
	}
	if operationRecoveryInspection(s, time.Now().UTC()).InspectionRevision != revision {
		return ErrConflict
	}
	return nil
}
