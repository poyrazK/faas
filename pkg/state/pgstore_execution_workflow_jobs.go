package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ExecutionWorkflowJobStore = (*PgStore)(nil)

const executionWorkflowJobSelectCols = `id, account_id, runs_principal_id, workflow_id, plan_id,
	status, step_count, next_step, sealed_plan, payload_kid, lease_token, lease_owner,
	lease_expires_at, scheduled_for, last_error, created_at, updated_at, finished_at`

const executionWorkflowJobReturnCols = `job.id, job.account_id, job.runs_principal_id, job.workflow_id, job.plan_id,
	job.status, job.step_count, job.next_step, job.sealed_plan, job.payload_kid, job.lease_token, job.lease_owner,
	job.lease_expires_at, job.scheduled_for, job.last_error, job.created_at, job.updated_at, job.finished_at`

func scanExecutionWorkflowJob(scan func(...any) error) (ExecutionWorkflowJob, error) {
	var row ExecutionWorkflowJob
	var id, accountID, principalID, leaseToken pgtype.UUID
	var status string
	var stepCount, nextStep int16
	var leaseOwner pgtype.Text
	var leaseExpires, finished pgtype.Timestamptz
	if err := scan(&id, &accountID, &principalID, &row.WorkflowID, &row.PlanID,
		&status, &stepCount, &nextStep, &row.SealedPlan, &row.PayloadKID, &leaseToken,
		&leaseOwner, &leaseExpires, &row.ScheduledFor, &row.LastError, &row.CreatedAt,
		&row.UpdatedAt, &finished); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	row.ID, row.AccountID = pgUUIDString(id), pgUUIDString(accountID)
	row.RunsPrincipalID = executionUUIDPtr(principalID)
	row.Status = api.ManagedExecutionWorkflowStatus(status)
	row.StepCount, row.NextStep = int(stepCount), int(nextStep)
	row.LeaseToken, row.LeaseOwner = executionUUIDPtr(leaseToken), executionStringPtr(leaseOwner)
	row.LeaseExpiresAt, row.FinishedAt = timestamptzToTimePtr(leaseExpires), timestamptzToTimePtr(finished)
	return row, nil
}

func (s *PgStore) CreateExecutionWorkflowJob(ctx context.Context, params CreateExecutionWorkflowJobParams) (ExecutionWorkflowJob, error) {
	if err := validateExecutionWorkflowJobParams(params); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	accountID, err := parsePgUUID(params.AccountID)
	if err != nil {
		return ExecutionWorkflowJob{}, fmt.Errorf("create execution workflow: account id: %w", err)
	}
	var principalID pgtype.UUID
	if params.RunsPrincipalID != nil {
		principalID, err = parsePgUUID(*params.RunsPrincipalID)
		if err != nil {
			return ExecutionWorkflowJob{}, fmt.Errorf("create execution workflow: principal id: %w", err)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionWorkflowJob{}, fmt.Errorf("create execution workflow: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var lockedAccount pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&lockedAccount); err != nil {
		return ExecutionWorkflowJob{}, mapErr(err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM agent_execution_workflows
		 WHERE account_id = $1 AND workflow_id = $2 AND runs_principal_id IS NOT DISTINCT FROM $3
	)`, accountID, params.WorkflowID, principalID).Scan(&exists); err != nil {
		return ExecutionWorkflowJob{}, fmt.Errorf("create execution workflow: check duplicate: %w", err)
	}
	if exists {
		return ExecutionWorkflowJob{}, ErrExecutionWorkflowJobExists
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_execution_workflows
		WHERE account_id = $1 AND status IN ('queued', 'running')`, accountID).Scan(&active); err != nil {
		return ExecutionWorkflowJob{}, fmt.Errorf("create execution workflow: count active: %w", err)
	}
	if active >= ExecutionWorkflowManagedMaxActivePerAccount {
		return ExecutionWorkflowJob{}, ErrExecutionWorkflowQueueFull
	}
	query := `INSERT INTO agent_execution_workflows
		(account_id, runs_principal_id, workflow_id, plan_id, step_count, sealed_plan, payload_kid, created_at, updated_at, scheduled_for)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8)
		RETURNING ` + executionWorkflowJobSelectCols
	row, err := scanExecutionWorkflowJob(tx.QueryRow(ctx, query,
		accountID, principalID, params.WorkflowID, params.PlanID, int16(params.StepCount),
		params.SealedPlan, params.PayloadKID, executionTime(params.CreatedAt)).Scan)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ExecutionWorkflowJob{}, ErrExecutionWorkflowJobExists
		}
		return ExecutionWorkflowJob{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionWorkflowJob{}, fmt.Errorf("create execution workflow: commit: %w", err)
	}
	return row, nil
}

func (s *PgStore) ClaimExecutionWorkflowJob(ctx context.Context, owner string, at time.Time, leaseDuration time.Duration) (ExecutionWorkflowJobClaim, error) {
	if owner == "" || at.IsZero() || leaseDuration <= 0 {
		return ExecutionWorkflowJobClaim{}, ErrExecutionInvalid
	}
	token := uuid.NewString()
	tokenID, err := parsePgUUID(token)
	if err != nil {
		return ExecutionWorkflowJobClaim{}, err
	}
	query := `WITH target AS (
		SELECT id FROM agent_execution_workflows
		 WHERE status IN ('queued', 'running') AND scheduled_for <= $1
		   AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
		 ORDER BY scheduled_for, created_at, id
		 FOR UPDATE SKIP LOCKED LIMIT 1
	)
	UPDATE agent_execution_workflows AS job
	   SET status = 'running', lease_token = $2, lease_owner = $3,
	       lease_expires_at = $1 + ($4::bigint * interval '1 millisecond'), updated_at = $1
	  FROM target WHERE job.id = target.id
	RETURNING ` + executionWorkflowJobReturnCols
	row, err := scanExecutionWorkflowJob(s.pool.QueryRow(ctx, query, executionTime(at), tokenID, owner, leaseDuration.Milliseconds()).Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionWorkflowJobClaim{}, ErrNotFound
	}
	if err != nil {
		return ExecutionWorkflowJobClaim{}, fmt.Errorf("claim execution workflow: %w", err)
	}
	return ExecutionWorkflowJobClaim{ExecutionWorkflowJob: row, ClaimToken: token}, nil
}

func (s *PgStore) UpdateExecutionWorkflowJob(ctx context.Context, id, leaseToken string, update ExecutionWorkflowJobUpdate) error {
	if update.Status != api.ManagedExecutionWorkflowQueued && update.Status != api.ManagedExecutionWorkflowRunning &&
		update.Status != api.ManagedExecutionWorkflowSucceeded && update.Status != api.ManagedExecutionWorkflowFailed {
		return ErrExecutionInvalid
	}
	if update.UpdatedAt.IsZero() || update.ScheduledFor.IsZero() || len(update.LastError) > 2048 || update.NextStep < 0 || update.NextStep > api.ExecutionWorkflowManagedMaxSteps {
		return ErrExecutionInvalid
	}
	jobID, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	tokenID, err := parsePgUUID(leaseToken)
	if err != nil {
		return err
	}
	finishedAt := any(nil)
	if update.Status == api.ManagedExecutionWorkflowSucceeded || update.Status == api.ManagedExecutionWorkflowFailed {
		finishedAt = executionTime(update.UpdatedAt)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE agent_execution_workflows
		SET status = $3, next_step = $4, scheduled_for = $5, last_error = $6,
		    updated_at = $7, finished_at = $8, lease_token = NULL, lease_owner = NULL,
		    lease_expires_at = NULL,
		    sealed_plan = CASE WHEN $3 IN ('succeeded', 'failed') THEN NULL ELSE sealed_plan END,
		    payload_kid = CASE WHEN $3 IN ('succeeded', 'failed') THEN '' ELSE payload_kid END
		WHERE id = $1 AND lease_token = $2 AND status = 'running'`, jobID, tokenID,
		string(update.Status), int16(update.NextStep), executionTime(update.ScheduledFor),
		update.LastError, executionTime(update.UpdatedAt), finishedAt)
	if err != nil {
		return fmt.Errorf("update execution workflow: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrExecutionWorkflowLeaseLost
	}
	return nil
}

func (s *PgStore) ExecutionWorkflowJobByKey(ctx context.Context, accountID, workflowID string, principalID *string) (ExecutionWorkflowJob, error) {
	accountUUID, err := parsePgUUID(accountID)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	var row pgx.Row
	if principalID == nil {
		row = s.pool.QueryRow(ctx, `SELECT `+executionWorkflowJobSelectCols+`
			FROM agent_execution_workflows WHERE account_id = $1 AND workflow_id = $2
			ORDER BY created_at DESC LIMIT 1`, accountUUID, workflowID)
	} else {
		principalUUID, parseErr := parsePgUUID(*principalID)
		if parseErr != nil {
			return ExecutionWorkflowJob{}, parseErr
		}
		row = s.pool.QueryRow(ctx, `SELECT `+executionWorkflowJobSelectCols+`
			FROM agent_execution_workflows WHERE account_id = $1 AND workflow_id = $2 AND runs_principal_id = $3
			ORDER BY created_at DESC LIMIT 1`, accountUUID, workflowID, principalUUID)
	}
	job, err := scanExecutionWorkflowJob(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	if err != nil {
		return ExecutionWorkflowJob{}, fmt.Errorf("get execution workflow: %w", err)
	}
	return job, nil
}

func (s *PgStore) ListExecutionWorkflowJobsByKey(ctx context.Context, accountID, workflowID string, principalID *string) ([]ExecutionWorkflowJob, error) {
	accountUUID, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	var rows pgx.Rows
	if principalID == nil {
		rows, err = s.pool.Query(ctx, `SELECT `+executionWorkflowJobSelectCols+`
			FROM agent_execution_workflows WHERE account_id = $1 AND workflow_id = $2
			ORDER BY created_at DESC LIMIT 100`, accountUUID, workflowID)
	} else {
		principalUUID, parseErr := parsePgUUID(*principalID)
		if parseErr != nil {
			return nil, parseErr
		}
		rows, err = s.pool.Query(ctx, `SELECT `+executionWorkflowJobSelectCols+`
			FROM agent_execution_workflows WHERE account_id = $1 AND workflow_id = $2 AND runs_principal_id = $3
			ORDER BY created_at DESC LIMIT 100`, accountUUID, workflowID, principalUUID)
	}
	if err != nil {
		return nil, fmt.Errorf("list execution workflows: %w", err)
	}
	defer rows.Close()
	var jobs []ExecutionWorkflowJob
	for rows.Next() {
		job, scanErr := scanExecutionWorkflowJob(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("list execution workflows: scan: %w", scanErr)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list execution workflows: rows: %w", err)
	}
	return jobs, nil
}
