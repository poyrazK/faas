package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) recoverOperationWorkflowLocked(op Operation, req api.OperationRecoveryRequest, fingerprint string) (Operation, error) {
	def := m.operationData.definitions[op.DefinitionID]
	run, exists := m.workflowRuns[op.WorkflowRunID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	receiptExpiry := op.ExpiresAt
	event, err := operationWorkflowRecovery(&op, def, run, req, now)
	if err != nil {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		if !m.operationCodeAvailableLocked(op) {
			return Operation{}, ErrConflict
		}
		if !api.MustLimitsFor(m.accounts[op.AccountID].Plan).Operations.Allowed {
			return Operation{}, NewOperationLimitError("plan_admission", 0, 1)
		}
		tenant := m.platformTenants[op.PlatformTenantID]
		if tenant.Status != PlatformTenantActive {
			return Operation{}, ErrPlatformTenantSuspended
		}
		plan := m.accounts[op.AccountID].Plan
		resumed, _, active, err := m.resumeWorkflowRunLocked(WorkflowResumeOptions{RunID: run.ID, AppID: op.AppID, AccountID: op.AccountID, PlatformTenantID: op.PlatformTenantID, ExpectedResumeCount: run.ResumeCount}, true)
		if err != nil {
			return Operation{}, operationWorkflowResumeError(err, plan, active)
		}
		m.saveOperationWorkflowExecutionLocked(op, *resumed)
	} else if err := m.operationCompletionLocked(&op, def); err != nil {
		return Operation{}, err
	}
	m.saveOperationRecoveryDecisionLocked(op, req, fingerprint, now, receiptExpiry)
	m.operationSaveLocked(op, event)
	return cloneOperation(m.operationDeliveryLocked(op)), nil
}

func (s *PgStore) recoverOperationWorkflow(ctx context.Context, snapshot Operation, req api.OperationRecoveryRequest) (Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if req.Resolution == "safe_to_retry" {
		if err := q.LockWorkflowRunAdmission(ctx, tx, snapshot.AppID); err != nil {
			return Operation{}, err
		}
		if err := lockOperationCodeTx(ctx, tx, snapshot); err != nil {
			return Operation{}, err
		}
	}
	runID := mustPgUUID(snapshot.WorkflowRunID)
	if _, err := q.LockWorkflowRecovery(ctx, tx, runID); err != nil {
		return Operation{}, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, runID)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return Operation{}, err
	}
	if op.ID != snapshot.ID || op.AccountID != snapshot.AccountID || op.PlatformTenantID != snapshot.PlatformTenantID {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if !operationRetained(op, now) {
		return Operation{}, ErrOperationExpired
	}
	fingerprint, err := operationRecoveryFingerprint(req, api.Limits{MaxSourceBytesPerInvocation: op.ValueMaxBytes})
	if err != nil {
		return Operation{}, err
	}
	prior, err := q.GetCustomerOperationRecovery(ctx, tx, sqlc.GetCustomerOperationRecoveryParams{OperationID: mustPgUUID(op.ID), RecoveryID: req.RecoveryID})
	if err == nil {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, err
	}
	if err := checkOperationInspectionRevisionTx(ctx, tx, op, req.ExpectedInspectionRevision); err != nil {
		return Operation{}, err
	}
	definition, err := q.GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{ID: mustPgUUID(op.DefinitionID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return Operation{}, err
	}
	def, err := operationPGDefinition(definition)
	if err != nil {
		return Operation{}, err
	}
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, tx, runID)
	if err != nil {
		return Operation{}, err
	}
	run := workflowRunFromSQLC(row)
	receiptExpiry := op.ExpiresAt
	event, err := operationWorkflowRecovery(&op, def, *run, req, now)
	if err != nil {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		planName, err := q.CustomerOperationAccountPlan(ctx, tx, mustPgUUID(op.AccountID))
		if err != nil {
			return Operation{}, mapErr(err)
		}
		plan := api.Plan(planName)
		if !api.MustLimitsFor(plan).Operations.Allowed {
			return Operation{}, NewOperationLimitError("plan_admission", 0, 1)
		}
		status, err := q.LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{AccountID: mustPgUUID(op.AccountID), TenantID: mustPgUUID(op.PlatformTenantID)})
		if err != nil {
			return Operation{}, mapErr(err)
		}
		if status != PlatformTenantActive {
			return Operation{}, ErrPlatformTenantSuspended
		}
		resumed, _, active, err := resumeWorkflowRunTx(ctx, tx, WorkflowResumeOptions{RunID: run.ID, AppID: op.AppID, AccountID: op.AccountID, PlatformTenantID: op.PlatformTenantID, ExpectedResumeCount: run.ResumeCount}, true)
		if err != nil {
			return Operation{}, operationWorkflowResumeError(err, plan, active)
		}
		if err := insertOperationWorkflowExecutionTx(ctx, tx, op, *resumed, now); err != nil {
			return Operation{}, err
		}
	} else if err := operationCompletionTx(ctx, tx, &op, def); err != nil {
		return Operation{}, err
	}
	decision, _ := json.Marshal(newOperationRecoveryDecision(op, req, fingerprint, now, receiptExpiry))
	request, err := json.Marshal(req)
	if err != nil {
		return Operation{}, err
	}
	if err := q.InsertCustomerOperationRecovery(ctx, tx, sqlc.InsertCustomerOperationRecoveryParams{OperationID: mustPgUUID(op.ID), RecoveryID: req.RecoveryID, Fingerprint: fingerprint, Request: request, Decision: decision, Now: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return Operation{}, err
	}
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return op, nil
}

func (s *PgStore) operationWorkflowExecutions(ctx context.Context, op Operation, after int32, limit int) (api.OperationExecutionsResponse, error) {
	raw, err := sqlc.New().ListCustomerOperationWorkflowExecutions(ctx, s.pool, sqlc.ListCustomerOperationWorkflowExecutionsParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), AfterGeneration: after, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.OperationExecutionsResponse{}, err
	}
	rows := make([]api.OperationExecution, 0, len(raw))
	for _, record := range raw {
		var row api.OperationExecution
		if err := json.Unmarshal(record, &row); err != nil {
			return api.OperationExecutionsResponse{}, err
		}
		rows = append(rows, row)
	}
	return operationExecutionPage(rows, limit), nil
}
