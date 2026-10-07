// adr: 602
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

func (s *PgStore) OperationForJobRun(ctx context.Context, runID string) (Operation, bool, error) {
	raw, err := sqlc.New().GetCustomerOperationForJob(ctx, s.pool, mustPgUUID(runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	op, err := operationPGRecord(raw)
	return op, err == nil, err
}
func admitOperationJobTx(ctx context.Context, tx pgx.Tx, op *Operation, inv Invocation, def OperationDefinition, plan api.Plan) error {
	row, err := sqlc.New().CustomerOperationJobByName(ctx, tx, sqlc.CustomerOperationJobByNameParams{AccountID: mustPgUUID(op.AccountID), Name: def.Spec.Job})
	if err != nil {
		return mapErr(err)
	}
	job := Job{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), Name: row.Name, Kind: row.Kind, Status: row.Status, ImageRef: row.ImageRef, ImageResolvedDigest: row.ImageResolvedDigest.String, ImageStorageKey: row.ImageStorageKey.String, ImageMaterializationStatus: row.ImageMaterializationStatus, Command: row.Command, EnvOverrides: row.EnvOverrides, RAMMB: int(row.RamMb), TaskTimeoutS: int(row.TaskTimeoutS)}
	run, err := prepareOperationJob(op, inv, job, plan)
	if err != nil {
		return err
	}
	target, err := sqlc.New().ReadCustomerOperationWorkflowTarget(ctx, tx, mustPgUUID(op.AppID))
	if err != nil {
		return err
	}
	if target.MaintenanceMode || target.AppStatus == string(AppDeleted) {
		return ErrConflict
	}
	available, err := sqlc.New().ReadCustomerOperationJobTarget(ctx, tx, sqlc.ReadCustomerOperationJobTargetParams{JobID: mustPgUUID(job.ID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return err
	}
	if !available {
		return ErrConflict
	}
	return insertOperationJobRunTx(ctx, tx, run)
}
func insertOperationJobRunTx(ctx context.Context, tx pgx.Tx, run JobRun) error {
	q := sqlc.New()
	if err := q.InsertCustomerOperationJobRun(ctx, tx, sqlc.InsertCustomerOperationJobRunParams{ID: mustPgUUID(run.ID), JobID: mustPgUUID(run.JobID), AccountID: mustPgUUID(run.AccountID), Timeout: int32(*run.TaskTimeoutS), Command: run.Command, Image: run.ImageRefSnapshot, Digest: run.ImageResolvedDigestSnapshot, StorageKey: run.ImageStorageKeySnapshot, Ram: int32(*run.RAMMBSnapshot), Env: run.EffectiveEnvSnapshot}); err != nil {
		return err
	}
	return q.InsertCustomerOperationJobTask(ctx, tx, mustPgUUID(run.ID))
}
func operationJobTaskFromSQL(row sqlc.JobTask) JobTask {
	task := JobTask{RunID: pgUUIDString(row.RunID), TaskIndex: int(row.TaskIndex), Attempt: int(row.Attempt), Status: row.Status, CreatedAt: row.CreatedAt.Time, StartedAt: workflowResumeTimePtr(row.StartedAt), FinishedAt: workflowResumeTimePtr(row.FinishedAt), LeaseExpiresAt: workflowResumeTimePtr(row.LeaseExpiresAt)}
	if row.InstanceID.Valid {
		v := pgUUIDString(row.InstanceID)
		task.InstanceID = &v
	}
	if row.LeaseToken.Valid {
		v := pgUUIDString(row.LeaseToken)
		task.LeaseToken = &v
	}
	return task
}
func lockOperationJobTx(ctx context.Context, tx pgx.Tx, runID string) (Operation, JobTask, error) {
	q := sqlc.New()
	id := mustPgUUID(runID)
	if _, err := q.LockCustomerOperationJobRun(ctx, tx, id); err != nil {
		return Operation{}, JobTask{}, mapErr(err)
	}
	row, err := q.LockCustomerOperationJobTask(ctx, tx, id)
	if err != nil {
		return Operation{}, JobTask{}, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForJob(ctx, tx, id)
	if err != nil {
		return Operation{}, JobTask{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	return op, operationJobTaskFromSQL(row), err
}
func guardOperationJobTx(ctx context.Context, tx pgx.Tx, runID string) error {
	owned, err := sqlc.New().CustomerOperationJobOwned(ctx, tx, mustPgUUID(runID))
	if err != nil || !owned {
		return err
	}
	op, task, err := lockOperationJobTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if op.JobRunID != runID || !operationIsActive(op) {
		return ErrOperationStaleAttempt
	}
	if task.Status == "queued" {
		q := sqlc.New()
		tenant, err := q.CustomerOperationRecoveryTenantStatus(ctx, tx, sqlc.CustomerOperationRecoveryTenantStatusParams{AccountID: mustPgUUID(op.AccountID), TenantID: mustPgUUID(op.PlatformTenantID)})
		if err != nil {
			return err
		}
		if tenant != PlatformTenantActive {
			return ErrPlatformTenantSuspended
		}
		target, err := q.ReadCustomerOperationWorkflowTarget(ctx, tx, mustPgUUID(op.AppID))
		if err != nil {
			return err
		}
		if !operationJobPolicyAvailable(op, api.Plan(target.Plan)) || target.AppStatus == string(AppDeleted) || target.MaintenanceMode || target.AbuseHoldAt.Valid || target.AccountStatus != "active" && target.AccountStatus != "past_due" {
			return ErrConflict
		}
	}
	return nil
}
func insertOperationJobExecutionTx(ctx context.Context, tx pgx.Tx, op Operation, task JobTask) error {
	row, err := sqlc.New().ReadCustomerOperationJobTask(ctx, tx, mustPgUUID(task.RunID))
	if err != nil {
		return err
	}
	task = operationJobTaskFromSQL(row)
	raw, err := json.Marshal(operationJobExecution(op, task))
	if err != nil {
		return err
	}
	q := sqlc.New()
	if rows, err := q.SetCustomerOperationJobIdentity(ctx, tx, sqlc.SetCustomerOperationJobIdentityParams{RunID: mustPgUUID(task.RunID), OperationID: mustPgUUID(op.ID)}); err != nil {
		return err
	} else if rows != 1 {
		return ErrConflict
	}
	if rows, err := q.InsertCustomerOperationJobExecution(ctx, tx, sqlc.InsertCustomerOperationJobExecutionParams{OperationID: mustPgUUID(op.ID), Generation: int32(op.Generation), RunID: mustPgUUID(task.RunID)}); err != nil {
		return err
	} else if rows != 1 {
		return ErrConflict
	}
	return q.InsertCustomerOperationJobExecutionRecord(ctx, tx, sqlc.InsertCustomerOperationJobExecutionRecordParams{OperationID: mustPgUUID(op.ID), Generation: int32(op.Generation), RunID: mustPgUUID(task.RunID), Record: raw})
}
func syncOperationJobTx(ctx context.Context, tx pgx.Tx, runID string) error {
	owned, err := sqlc.New().CustomerOperationJobOwned(ctx, tx, mustPgUUID(runID))
	if err != nil || !owned {
		return err
	}
	op, task, err := lockOperationJobTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	if op.JobRunID != runID {
		return nil
	}
	if err := sqlc.New().RecomputeCustomerOperationJobRun(ctx, tx, mustPgUUID(runID)); err != nil {
		return err
	}
	events := operationJobProjection(&op, task, time.Now().UTC())
	raw, err := json.Marshal(operationJobExecution(op, task))
	if err != nil {
		return err
	}
	if err := sqlc.New().UpdateCustomerOperationJobExecution(ctx, tx, sqlc.UpdateCustomerOperationJobExecutionParams{RunID: mustPgUUID(runID), Record: raw}); err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	def, err := operationJobDefinitionTx(ctx, tx, op)
	if err != nil {
		return err
	}
	if op.State.Terminal() {
		if err := operationCompletionTx(ctx, tx, &op, def); err != nil {
			return err
		}
	}
	for _, event := range events {
		if err := operationSaveTx(ctx, tx, op, event); err != nil {
			return err
		}
	}
	return nil
}
func operationJobDefinitionTx(ctx context.Context, tx pgx.Tx, op Operation) (OperationDefinition, error) {
	row, err := sqlc.New().GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{ID: mustPgUUID(op.DefinitionID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return OperationDefinition{}, err
	}
	return operationPGDefinition(row)
}
func (s *PgStore) OperationJobDispatchEnv(ctx context.Context, runID, instanceID, lease string) (map[string]string, error) {
	owned, err := sqlc.New().CustomerOperationJobOwned(ctx, s.pool, mustPgUUID(runID))
	if err != nil || !owned {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, task, err := lockOperationJobTx(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	return operationJobDispatchEnv(op, task, instanceID, lease, time.Now().UTC())
}
func (s *PgStore) OperationJobControl(ctx context.Context, id string, a JobOperationAuthority) (api.OperationJobControlResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationJobControlResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, task, err := jobOperationAuthorityTx(ctx, tx, id, a)
	if err != nil {
		return api.OperationJobControlResponse{}, err
	}
	now := time.Now().UTC()
	return operationJobControlResponse(op, task, now), nil
}
func (s *PgStore) ReportOperationJob(ctx context.Context, id string, a JobOperationAuthority, kind string, report api.OperationJobReportRequest) (Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, task, err := jobOperationAuthorityTx(ctx, tx, id, a)
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC()
	def, err := operationJobDefinitionTx(ctx, tx, op)
	if err != nil {
		return Operation{}, err
	}
	event, changed, err := operationJobReport(&op, def, task, kind, report, now)
	if err != nil {
		return Operation{}, err
	}
	if changed {
		if err := operationSaveTx(ctx, tx, op, event); err != nil {
			return Operation{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return op, nil
}
func (s *PgStore) cancelOperationJob(ctx context.Context, snapshot Operation, generation int) (Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	op, task, err := lockOperationJobTx(ctx, tx, snapshot.JobRunID)
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC()
	if err := validateOperationCancellation(op, snapshot.AccountID, snapshot.PlatformTenantID, generation, now); err != nil {
		return Operation{}, err
	}
	if op.State.Terminal() || op.CancellationRequested {
		return op, nil
	}
	op.CancellationRequested = true
	event := operationJobEvent(&op, task, "cancellation_requested", map[string]any{"cancellation_requested": true}, now)
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Operation{}, err
	}
	if err := sqlc.New().CancelQueuedCustomerOperationJobTask(ctx, tx, mustPgUUID(task.RunID)); err != nil {
		return Operation{}, err
	}
	if err := syncOperationJobTx(ctx, tx, task.RunID); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return s.OperationByID(ctx, op.AccountID, op.PlatformTenantID, op.ID)
}
func (s *PgStore) operationJobExecutions(ctx context.Context, op Operation, after int32, limit int) (api.OperationExecutionsResponse, error) {
	raw, err := sqlc.New().ListCustomerOperationJobExecutions(ctx, s.pool, sqlc.ListCustomerOperationJobExecutionsParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), After: after, PageLimit: int32(limit + 1)})
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
func (s *PgStore) cancelOperationJobTask(ctx context.Context, runID string, index int) error {
	if index != 0 {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockOperationJobTx(ctx, tx, runID); err != nil {
		return err
	}
	if err := sqlc.New().CancelCustomerOperationJobTask(ctx, tx, mustPgUUID(runID)); err != nil {
		return err
	}
	if err := syncOperationJobTx(ctx, tx, runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) recoverOperationJob(ctx context.Context, snapshot Operation, req api.OperationRecoveryRequest) (Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if req.Resolution == "safe_to_retry" {
		if err := lockOperationCodeTx(ctx, tx, snapshot); err != nil {
			return Operation{}, err
		}
	}
	op, task, err := lockOperationJobTx(ctx, tx, snapshot.JobRunID)
	if err != nil {
		return Operation{}, err
	}
	if op.ID != snapshot.ID || op.AccountID != snapshot.AccountID || op.PlatformTenantID != snapshot.PlatformTenantID {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	expiry := op.ExpiresAt
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
	if task.Status == "queued" || task.Status == "claimed" {
		return Operation{}, ErrConflict
	}
	if req.Resolution == "safe_to_retry" {
		status, err := q.LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{AccountID: mustPgUUID(op.AccountID), TenantID: mustPgUUID(op.PlatformTenantID)})
		if err != nil {
			return Operation{}, err
		}
		if status != PlatformTenantActive {
			return Operation{}, ErrPlatformTenantSuspended
		}
		inspection, err := operationRecoverySnapshotTx(ctx, tx, op.AccountID, op.ID)
		if err != nil {
			return Operation{}, err
		}
		blockers, _ := operationRecoveryRetryPlan(inspection, now)
		if len(blockers) > 0 {
			return Operation{}, ErrConflict
		}
	}
	def, err := operationJobDefinitionTx(ctx, tx, op)
	if err != nil {
		return Operation{}, err
	}
	event, err := operationJobRecovery(&op, def, req, now)
	if err != nil {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		run := *op.JobSnapshot
		run.ID, run.CreatedAt = newOperationID(), now
		op.JobRunID = run.ID
		if err := insertOperationJobRunTx(ctx, tx, run); err != nil {
			return Operation{}, err
		}
		if err := insertOperationJobExecutionTx(ctx, tx, op, JobTask{RunID: run.ID, Status: "queued", Attempt: 1, CreatedAt: now}); err != nil {
			return Operation{}, err
		}
		event.Data = mustOperationJSON(map[string]any{"state": op.State, "resolution": req.Resolution, "job_run_id": op.JobRunID, "generation": op.Generation})
	} else if err := operationCompletionTx(ctx, tx, &op, def); err != nil {
		return Operation{}, err
	}
	decision, err := json.Marshal(newOperationRecoveryDecision(op, req, fingerprint, now, expiry))
	if err != nil {
		return Operation{}, err
	}
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

func (s *PgStore) OperationJobImageRetained(ctx context.Context, key string) (bool, error) {
	return sqlc.New().CustomerOperationJobSnapshotRetained(ctx, s.pool, key)
}
