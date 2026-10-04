package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ExecutionStore = (*PgStore)(nil)
var _ ExecutionPrincipalListStore = (*PgStore)(nil)
var _ ExecutionQueueStore = (*PgStore)(nil)
var _ ExecutionArtifactGrantStore = (*PgStore)(nil)
var _ ExecutionWorkflowStore = (*PgStore)(nil)

func executionTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func executionStringPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	value := v.String
	return &value
}

func executionUUIDPtr(v pgtype.UUID) *string {
	if !v.Valid {
		return nil
	}
	value := pgUUIDString(v)
	return &value
}

func executionIntPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	value := int(v.Int32)
	return &value
}

// executionNullableTime converts sqlc's nullable aggregate timestamp. The
// generated query uses interface{} because the project-wide sqlc config does
// not currently map nullable min(timestamptz) expressions to a concrete
// pgtype, so keep the adapter tolerant of pgx's concrete scan variants.
func executionNullableTime(value interface{}) (*time.Time, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case time.Time:
		t := v.UTC()
		return &t, nil
	case *time.Time:
		if v == nil {
			return nil, nil
		}
		t := v.UTC()
		return &t, nil
	case pgtype.Timestamptz:
		if !v.Valid {
			return nil, nil
		}
		t := v.Time.UTC()
		return &t, nil
	case *pgtype.Timestamptz:
		if v == nil || !v.Valid {
			return nil, nil
		}
		t := v.Time.UTC()
		return &t, nil
	default:
		return nil, fmt.Errorf("state: unexpected nullable execution timestamp type %T", value)
	}
}

func executionFromSQL(row sqlc.Execution) Execution {
	var artifacts []api.ExecutionArtifact
	if len(row.Artifacts) != 0 {
		_ = json.Unmarshal(row.Artifacts, &artifacts)
	}
	return Execution{
		Profile:            api.ExecutionProfile(row.Profile),
		RuntimeImageDigest: row.RuntimeImageDigest.String,
		WorkflowID:         row.WorkflowID.String,
		StepLabel:          row.StepLabel.String,
		Artifacts:          artifacts,
		ID:                 pgUUIDString(row.ID),
		AccountID:          pgUUIDString(row.AccountID),
		RunsPrincipalID:    executionUUIDPtr(row.RunsPrincipalID),
		Runtime:            api.ExecutionRuntime(row.Runtime),
		Status:             api.ExecutionStatus(row.Status),
		NetworkMode:        api.ExecutionNetworkMode(row.NetworkMode),
		Limits: api.ResolvedExecutionLimits{
			TimeoutMS:       int(row.TimeoutMs),
			MemoryMB:        int(row.MemoryMb),
			CPUMillicores:   int(row.CpuMillicores),
			EphemeralDiskMB: int(row.EphemeralDiskMb),
			MaxOutputBytes:  int(row.MaxOutputBytes),
			PIDsMax:         int(row.PidsMax),
		},
		SourceBytes:     int(row.SourceBytes),
		InputBytes:      int(row.InputBytes),
		DeadlineAt:      timestamptzToTime(row.DeadlineAt),
		LeaseToken:      executionUUIDPtr(row.LeaseToken),
		LeaseOwner:      executionStringPtr(row.LeaseOwner),
		LeaseExpiresAt:  timestamptzToTimePtr(row.LeaseExpiresAt),
		CancelRequested: timestamptzToTimePtr(row.CancelRequestedAt),
		Result:          append([]byte(nil), row.Result...),
		Stdout:          row.Stdout,
		Stderr:          row.Stderr,
		OutputTruncated: row.OutputTruncated,
		ExitCode:        executionIntPtr(row.ExitCode),
		FailureCode:     executionStringPtr(row.FailureCode),
		FailureMessage:  executionStringPtr(row.FailureMessage),
		Usage: api.ExecutionUsage{
			WallTimeMS:   row.WallTimeMs,
			CPUTimeMS:    row.CpuTimeMs,
			PeakMemoryMB: int(row.PeakMemoryMb),
		},
		StartedAt:  timestamptzToTimePtr(row.StartedAt),
		FinishedAt: timestamptzToTimePtr(row.FinishedAt),
		CreatedAt:  timestamptzToTime(row.CreatedAt),
		UpdatedAt:  timestamptzToTime(row.UpdatedAt),
	}
}

func executionRowsFromSQL(rows []sqlc.Execution) []Execution {
	out := make([]Execution, 0, len(rows))
	for _, row := range rows {
		out = append(out, executionFromSQL(row))
	}
	return out
}

func recordExecutionUsage(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, executionID pgtype.UUID) error {
	if err := q.ExecutionUsageRecord(ctx, db, executionID); err != nil {
		return fmt.Errorf("record execution usage: %w", err)
	}
	return nil
}

func (s *PgStore) CreateExecution(ctx context.Context, params CreateExecutionParams) (Execution, error) {
	normalizedIntegrationIDs, err := api.NormalizeExecutionIntegrationIDs(params.OutboundIntegrationIDs)
	if err != nil {
		return Execution{}, fmt.Errorf("%w: %w", ErrExecutionInvalid, err)
	}
	params.OutboundIntegrationIDs = normalizedIntegrationIDs
	if err := validateCreateExecution(params); err != nil {
		return Execution{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, fmt.Errorf("create execution: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New()
	accountID := mustPgUUID(params.AccountID)
	var runsPrincipalID pgtype.UUID
	if params.RunsPrincipalID != nil {
		runsPrincipalID, err = parsePgUUID(*params.RunsPrincipalID)
		if err != nil {
			return Execution{}, fmt.Errorf("create execution: runs principal: %w", err)
		}
	}
	account, err := q.ExecutionLockAccount(ctx, tx, accountID)
	if err != nil {
		return Execution{}, mapErr(err)
	}
	if isAgentWorkflowStep(params) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM executions
			 WHERE account_id = $1 AND runs_principal_id = $2
			   AND workflow_id = $3 AND step_label = $4
		)`, accountID, runsPrincipalID, params.WorkflowID, params.StepLabel).Scan(&exists); err != nil {
			return Execution{}, fmt.Errorf("create execution: check agent workflow step: %w", err)
		}
		if exists {
			return Execution{}, ErrExecutionWorkflowStepExists
		}
	}
	planLimits, planOK := api.Plan(account.Plan).ExecutionLimits()
	if !planOK {
		return Execution{}, ErrExecutionsNotAllowed
	}
	if err := validateExecutionPlan(params, planLimits); err != nil {
		return Execution{}, err
	}
	active, err := q.ExecutionCountActive(ctx, tx, accountID)
	if err != nil {
		return Execution{}, fmt.Errorf("create execution: count active: %w", err)
	}
	if active >= int64(planLimits.MaxConcurrent) {
		return Execution{}, &ExecutionQuotaError{Limit: planLimits.MaxConcurrent, Observed: int(active) + 1}
	}
	integrationIDs := make([]pgtype.UUID, 0, len(params.OutboundIntegrationIDs))
	for _, rawID := range params.OutboundIntegrationIDs {
		id, err := parsePgUUID(rawID)
		if err != nil {
			return Execution{}, fmt.Errorf("%w: invalid outbound integration id", ErrExecutionInvalid)
		}
		integrationIDs = append(integrationIDs, id)
	}
	if len(integrationIDs) > 0 {
		rows, err := tx.Query(ctx, `
			SELECT integration.id
			  FROM outbound_integrations integration
			 WHERE integration.account_id = $1 AND integration.id = ANY($2::uuid[])
			   AND integration.owner_kind = 'customer'
			   AND integration.provider_auth_mode = 'managed'
			   AND integration.credential_source = 'customer_sealed'
			   AND integration.enabled AND integration.runs_enabled
			   AND cardinality(integration.allowed_methods) > 0
			   AND cardinality(integration.allowed_path_prefixes) > 0
			 ORDER BY integration.id
			 FOR SHARE OF integration`, accountID, integrationIDs)
		if err != nil {
			return Execution{}, fmt.Errorf("create execution: lock outbound integrations: %w", err)
		}
		eligible := 0
		for rows.Next() {
			var id pgtype.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return Execution{}, fmt.Errorf("create execution: scan outbound integrations: %w", err)
			}
			eligible++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Execution{}, fmt.Errorf("create execution: read outbound integrations: %w", err)
		}
		rows.Close()
		if eligible != len(integrationIDs) {
			return Execution{}, ErrExecutionOutboundIntegrationUnavailable
		}
		// Credential mutations serialize on the integration rows above. A
		// separate READ COMMITTED statement sees whether that mutation committed
		// before admission acquired the locks.
		var configured int64
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM outbound_integration_credentials
			 WHERE account_id = $1 AND integration_id = ANY($2::uuid[])`, accountID, integrationIDs).Scan(&configured); err != nil {
			return Execution{}, fmt.Errorf("create execution: check outbound credentials: %w", err)
		}
		if configured != int64(len(integrationIDs)) {
			return Execution{}, ErrExecutionOutboundIntegrationUnavailable
		}
	}

	row, err := q.ExecutionInsert(ctx, tx, sqlc.ExecutionInsertParams{
		AccountID:       accountID,
		Runtime:         string(params.Request.Runtime),
		Profile:         string(params.Request.Profile.Normalized()),
		NetworkMode:     string(params.Request.Network.Mode),
		TimeoutMs:       int32(params.Request.Limits.TimeoutMS),
		MemoryMb:        int32(params.Request.Limits.MemoryMB),
		CpuMillicores:   int32(params.Request.Limits.CPUMillicores),
		EphemeralDiskMb: int32(params.Request.Limits.EphemeralDiskMB),
		MaxOutputBytes:  int32(params.Request.Limits.MaxOutputBytes),
		PidsMax:         int32(params.Request.Limits.PIDsMax),
		SourceBytes:     int32(params.SourceBytes),
		InputBytes:      int32(params.InputBytes),
		DeadlineAt:      executionTime(params.DeadlineAt),
		AdmittedAt:      executionTime(params.AdmittedAt),
		RunsPrincipalID: runsPrincipalID,
		WorkflowID:      pgtype.Text{String: params.WorkflowID, Valid: params.WorkflowID != ""},
		StepLabel:       pgtype.Text{String: params.StepLabel, Valid: params.StepLabel != ""},
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if isAgentWorkflowStep(params) && errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "executions_account_agent_workflow_step_uniq" {
			return Execution{}, ErrExecutionWorkflowStepExists
		}
		return Execution{}, mapErr(err)
	}
	for _, integrationID := range integrationIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO execution_outbound_integrations (execution_id, integration_id) VALUES ($1, $2)`, row.ID, integrationID); err != nil {
			return Execution{}, fmt.Errorf("create execution: persist outbound integration intent: %w", err)
		}
	}
	for _, redemption := range params.ArtifactGrantRedemptions {
		tag, err := tx.Exec(ctx, `UPDATE execution_artifact_grants
			SET redeemed_at = $1, redeemed_execution_id = $2
			WHERE id = $3 AND account_id = $4 AND token_hash = $5
			  AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > $1`,
			executionTime(params.AdmittedAt), row.ID, mustPgUUID(redemption.GrantID), accountID, redemption.TokenHash)
		if err != nil {
			return Execution{}, fmt.Errorf("create execution: redeem artifact grant: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return Execution{}, ErrExecutionArtifactGrantUnavailable
		}
	}
	if err := q.ExecutionPayloadInsert(ctx, tx, sqlc.ExecutionPayloadInsertParams{
		ExecutionID:   row.ID,
		SealedPayload: append([]byte(nil), params.SealedPayload...),
		Kid:           params.PayloadKID,
		CreatedAt:     executionTime(params.AdmittedAt),
	}); err != nil {
		return Execution{}, mapErr(err)
	}
	if _, err := appendExecutionEvent(ctx, tx, params.AccountID, pgUUIDString(row.ID), ExecutionEventStatus, executionStatusPayload(api.ExecutionStatusQueued), params.AdmittedAt); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, fmt.Errorf("create execution: commit: %w", err)
	}
	return executionFromSQL(row), nil
}

func (s *PgStore) ExecutionByID(ctx context.Context, accountID, executionID string) (Execution, error) {
	row, err := sqlc.New().ExecutionGetForAccount(ctx, s.pool, sqlc.ExecutionGetForAccountParams{
		AccountID: mustPgUUID(accountID), ExecutionID: mustPgUUID(executionID),
	})
	if err != nil {
		return Execution{}, mapErr(err)
	}
	return executionFromSQL(row), nil
}

func (s *PgStore) ListExecutions(ctx context.Context, accountID string, limit, offset int) ([]Execution, error) {
	limit, offset = normalizeExecutionPage(limit, offset)
	if limit > math.MaxInt32 || offset > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pagination values are outside int32 bounds", ErrExecutionInvalid)
	}
	rows, err := sqlc.New().ExecutionListForAccount(ctx, s.pool, sqlc.ExecutionListForAccountParams{
		AccountID: mustPgUUID(accountID), PageLimit: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return executionRowsFromSQL(rows), nil
}

func (s *PgStore) ListExecutionsByStatus(ctx context.Context, accountID string, status api.ExecutionStatus, limit, offset int) ([]Execution, error) {
	limit, offset = normalizeExecutionPage(limit, offset)
	if limit > math.MaxInt32 || offset > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pagination values are outside int32 bounds", ErrExecutionInvalid)
	}
	rows, err := sqlc.New().ExecutionListForAccountStatus(ctx, s.pool, sqlc.ExecutionListForAccountStatusParams{
		AccountID: mustPgUUID(accountID), Status: string(status), PageLimit: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return executionRowsFromSQL(rows), nil
}

func (s *PgStore) ListExecutionsByPrincipal(ctx context.Context, accountID, principalID string, limit, offset int) ([]Execution, error) {
	limit, offset = normalizeExecutionPage(limit, offset)
	if limit > math.MaxInt32 || offset > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pagination values are outside int32 bounds", ErrExecutionInvalid)
	}
	rows, err := sqlc.New().ExecutionListForAccountPrincipal(ctx, s.pool, sqlc.ExecutionListForAccountPrincipalParams{
		AccountID: mustPgUUID(accountID), RunsPrincipalID: mustPgUUID(principalID),
		PageLimit: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return executionRowsFromSQL(rows), nil
}

func (s *PgStore) ListExecutionsByPrincipalStatus(ctx context.Context, accountID, principalID string, status api.ExecutionStatus, limit, offset int) ([]Execution, error) {
	limit, offset = normalizeExecutionPage(limit, offset)
	if limit > math.MaxInt32 || offset > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pagination values are outside int32 bounds", ErrExecutionInvalid)
	}
	rows, err := sqlc.New().ExecutionListForAccountPrincipalStatus(ctx, s.pool, sqlc.ExecutionListForAccountPrincipalStatusParams{
		AccountID: mustPgUUID(accountID), RunsPrincipalID: mustPgUUID(principalID), Status: string(status),
		PageLimit: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return executionRowsFromSQL(rows), nil
}

func (s *PgStore) ListExecutionsByWorkflow(ctx context.Context, accountID, workflowID string, principalID *string, status api.ExecutionStatus, limit, offset int) ([]Execution, error) {
	if strings.TrimSpace(accountID) == "" || api.ValidateExecutionWorkflowMetadata(workflowID, "") != nil || workflowID == "" {
		return nil, ErrExecutionInvalid
	}
	limit, offset = normalizeExecutionPage(limit, offset)
	if limit > math.MaxInt32 || offset > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pagination values are outside int32 bounds", ErrExecutionInvalid)
	}
	var principal pgtype.UUID
	if principalID != nil {
		var err error
		principal, err = parsePgUUID(*principalID)
		if err != nil {
			return nil, fmt.Errorf("list execution workflow: principal: %w", err)
		}
	}
	rows, err := sqlc.New().ExecutionListForWorkflow(ctx, s.pool, sqlc.ExecutionListForWorkflowParams{
		AccountID: mustPgUUID(accountID), WorkflowID: workflowID, RunsPrincipalID: principal,
		Status: string(status), PageLimit: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return executionRowsFromSQL(rows), nil
}

func (s *PgStore) ExecutionWorkflowStepByLabel(ctx context.Context, accountID, workflowID string, principalID *string, stepLabel string) (Execution, error) {
	if strings.TrimSpace(accountID) == "" || principalID == nil || workflowID == "" || stepLabel == "" ||
		api.ValidateExecutionWorkflowMetadata(workflowID, stepLabel) != nil || !strings.HasPrefix(stepLabel, "gwf:") {
		return Execution{}, ErrExecutionInvalid
	}
	accountUUID, err := parsePgUUID(accountID)
	if err != nil {
		return Execution{}, err
	}
	var id pgtype.UUID
	var label, status string
	var result []byte
	principalUUID, err := parsePgUUID(*principalID)
	if err != nil {
		return Execution{}, err
	}
	row := s.pool.QueryRow(ctx, `SELECT id, step_label, status, result FROM executions
		WHERE account_id = $1 AND runs_principal_id = $2 AND workflow_id = $3
		  AND step_label = $4 AND step_label LIKE 'gwf:%'
		ORDER BY created_at DESC LIMIT 1`, accountUUID, principalUUID, workflowID, stepLabel)
	if err := row.Scan(&id, &label, &status, &result); errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrNotFound
	} else if err != nil {
		return Execution{}, fmt.Errorf("get execution workflow step: %w", err)
	}
	return Execution{
		ID: pgUUIDString(id), StepLabel: label, Status: api.ExecutionStatus(status),
		Result: append([]byte(nil), result...),
	}, nil
}

func (s *PgStore) ExecutionWorkflowSummary(ctx context.Context, accountID, workflowID string, principalID *string) (ExecutionWorkflowResponse, error) {
	if strings.TrimSpace(accountID) == "" || api.ValidateExecutionWorkflowMetadata(workflowID, "") != nil || workflowID == "" {
		return ExecutionWorkflowResponse{}, ErrExecutionInvalid
	}
	var principal pgtype.UUID
	if principalID != nil {
		var err error
		principal, err = parsePgUUID(*principalID)
		if err != nil {
			return ExecutionWorkflowResponse{}, fmt.Errorf("read execution workflow: principal: %w", err)
		}
	}
	row, err := sqlc.New().ExecutionWorkflowSummary(ctx, s.pool, sqlc.ExecutionWorkflowSummaryParams{
		AccountID: mustPgUUID(accountID), WorkflowID: workflowID, RunsPrincipalID: principal,
	})
	if err != nil {
		return ExecutionWorkflowResponse{}, mapErr(err)
	}
	if row.RunCount == 0 {
		return ExecutionWorkflowResponse{}, ErrNotFound
	}
	return ExecutionWorkflowResponse{
		RunCount: row.RunCount,
		StatusCounts: api.ExecutionWorkflowStatusCounts{
			Queued: row.Queued, Restoring: row.Restoring, Running: row.Running,
			Succeeded: row.Succeeded, Failed: row.Failed, TimedOut: row.TimedOut,
			OutOfMemory: row.OutOfMemory, Cancelled: row.Cancelled,
		},
		Usage: api.ExecutionWorkflowUsage{
			WallTimeMS: row.WallTimeMs, CPUTimeMS: row.CpuTimeMs,
			PeakMemoryMB: int(row.PeakMemoryMb), OutputBytes: row.OutputBytes,
		},
	}, nil
}

func (s *PgStore) ExecutionQueueStats(ctx context.Context, at time.Time) (ExecutionQueueStats, error) {
	if at.IsZero() {
		return ExecutionQueueStats{}, fmt.Errorf("%w: queue observation time is required", ErrExecutionInvalid)
	}
	row, err := sqlc.New().ExecutionQueueStats(ctx, s.pool, executionTime(at))
	if err != nil {
		return ExecutionQueueStats{}, mapErr(err)
	}
	oldest, err := executionNullableTime(row.OldestCreatedAt)
	if err != nil {
		return ExecutionQueueStats{}, err
	}
	queued := row.Queued
	if queued < 0 {
		queued = 0
	}
	return ExecutionQueueStats{Queued: int(queued), OldestCreatedAt: oldest}, nil
}

func (s *PgStore) ListExecutionQueueAccounts(ctx context.Context, at time.Time, limit int) ([]ExecutionQueueAccount, error) {
	if at.IsZero() {
		return nil, fmt.Errorf("%w: queue observation time is required", ErrExecutionInvalid)
	}
	if limit <= 0 {
		limit = 64
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := sqlc.New().ExecutionQueueAccounts(ctx, s.pool, sqlc.ExecutionQueueAccountsParams{
		At: executionTime(at), PageLimit: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	accounts := make([]ExecutionQueueAccount, 0, len(rows))
	for _, row := range rows {
		oldest, err := executionNullableTime(row.OldestCreatedAt)
		if err != nil {
			return nil, err
		}
		if oldest == nil {
			continue
		}
		queued := row.QueuedCount
		if queued < 0 {
			queued = 0
		}
		accounts = append(accounts, ExecutionQueueAccount{
			AccountID:       pgUUIDString(row.AccountID),
			Queued:          int(queued),
			OldestCreatedAt: *oldest,
		})
	}
	return accounts, nil
}

func (s *PgStore) ClaimExecution(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
	return s.claimExecution(ctx, "", owner, claimedAt, leaseDuration)
}

func (s *PgStore) ClaimExecutionForAccount(ctx context.Context, accountID, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ExecutionClaim{}, fmt.Errorf("%w: account id is required", ErrExecutionInvalid)
	}
	return s.claimExecution(ctx, accountID, owner, claimedAt, leaseDuration)
}

func (s *PgStore) claimExecution(ctx context.Context, accountID, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || claimedAt.IsZero() || leaseDuration <= 0 {
		return ExecutionClaim{}, fmt.Errorf("%w: claim owner, time, and positive lease are required", ErrExecutionInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionClaim{}, fmt.Errorf("claim execution: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	token := uuid.New()
	tokenPG := pgtype.UUID{Bytes: token, Valid: true}
	q := sqlc.New()
	var row sqlc.Execution
	if accountID == "" {
		row, err = q.ExecutionClaimNext(ctx, tx, sqlc.ExecutionClaimNextParams{
			LeaseToken:     tokenPG,
			LeaseOwner:     pgtype.Text{String: owner, Valid: true},
			LeaseExpiresAt: executionTime(claimedAt.Add(leaseDuration)),
			ClaimedAt:      executionTime(claimedAt),
		})
	} else {
		row, err = q.ExecutionClaimNextForAccount(ctx, tx, sqlc.ExecutionClaimNextForAccountParams{
			LeaseToken:     tokenPG,
			LeaseOwner:     pgtype.Text{String: owner, Valid: true},
			LeaseExpiresAt: executionTime(claimedAt.Add(leaseDuration)),
			ClaimedAt:      executionTime(claimedAt),
			AccountID:      mustPgUUID(accountID),
		})
	}
	if err != nil {
		return ExecutionClaim{}, mapErr(err)
	}
	payload, err := q.ExecutionPayloadForLease(ctx, tx, sqlc.ExecutionPayloadForLeaseParams{
		ExecutionID: row.ID, LeaseToken: tokenPG,
	})
	if err != nil {
		return ExecutionClaim{}, mapErr(err)
	}
	outboundIntegrationIDs := make([]string, 0)
	integrationRows, err := tx.Query(ctx, `
		SELECT integration_id::text
		  FROM execution_outbound_integrations
		 WHERE execution_id = $1
	 ORDER BY integration_id`, row.ID)
	if err != nil {
		return ExecutionClaim{}, fmt.Errorf("claim execution: load outbound integration intent: %w", err)
	}
	for integrationRows.Next() {
		var integrationID string
		if err := integrationRows.Scan(&integrationID); err != nil {
			integrationRows.Close()
			return ExecutionClaim{}, fmt.Errorf("claim execution: scan outbound integration intent: %w", err)
		}
		outboundIntegrationIDs = append(outboundIntegrationIDs, integrationID)
	}
	if err := integrationRows.Err(); err != nil {
		integrationRows.Close()
		return ExecutionClaim{}, fmt.Errorf("claim execution: read outbound integration intent: %w", err)
	}
	integrationRows.Close()
	if err := tx.Commit(ctx); err != nil {
		return ExecutionClaim{}, fmt.Errorf("claim execution: commit: %w", err)
	}
	// The event is best-effort after the claim commit. The durable row remains
	// authoritative if a transient event-log write fails.
	_, _ = appendExecutionEvent(ctx, s.pool, pgUUIDString(row.AccountID), pgUUIDString(row.ID), ExecutionEventStatus, executionStatusPayload(api.ExecutionStatusRestoring), claimedAt)
	return ExecutionClaim{
		Execution:              executionFromSQL(row),
		SealedPayload:          append([]byte(nil), payload.SealedPayload...),
		PayloadKID:             payload.Kid,
		OutboundIntegrationIDs: outboundIntegrationIDs,
	}, nil
}

func (s *PgStore) MarkExecutionRunning(ctx context.Context, executionID, leaseToken string, startedAt time.Time) (Execution, error) {
	if executionID == "" || leaseToken == "" || startedAt.IsZero() {
		return Execution{}, ErrExecutionInvalid
	}
	row, err := sqlc.New().ExecutionMarkRunning(ctx, s.pool, sqlc.ExecutionMarkRunningParams{
		ExecutionID: mustPgUUID(executionID), LeaseToken: mustPgUUID(leaseToken), StartedAt: executionTime(startedAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrExecutionLeaseLost
	}
	if err != nil {
		return Execution{}, mapErr(err)
	}
	// MarkExecutionRunning intentionally remains a single CAS query. Recording
	// the lifecycle event after the CAS keeps the lease fence unchanged.
	_, _ = appendExecutionEvent(ctx, s.pool, pgUUIDString(row.AccountID), pgUUIDString(row.ID), ExecutionEventStatus, executionStatusPayload(api.ExecutionStatusRunning), startedAt)
	return executionFromSQL(row), nil
}

func (s *PgStore) RenewExecutionLease(ctx context.Context, executionID, leaseToken string, renewedAt time.Time, leaseDuration time.Duration) error {
	if executionID == "" || leaseToken == "" || renewedAt.IsZero() || leaseDuration <= 0 {
		return ErrExecutionInvalid
	}
	count, err := sqlc.New().ExecutionRenewLease(ctx, s.pool, sqlc.ExecutionRenewLeaseParams{
		ExecutionID: mustPgUUID(executionID), LeaseToken: mustPgUUID(leaseToken),
		RenewedAt: executionTime(renewedAt), LeaseExpiresAt: executionTime(renewedAt.Add(leaseDuration)),
	})
	if err != nil {
		return mapErr(err)
	}
	if count == 0 {
		return ErrExecutionLeaseLost
	}
	return nil
}

func (s *PgStore) CompleteExecution(ctx context.Context, params CompleteExecutionParams) (Execution, error) {
	if params.ID == "" || params.LeaseToken == "" {
		return Execution{}, ErrExecutionInvalidTerminal
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, fmt.Errorf("complete execution: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New()
	locked, err := q.ExecutionLockForLease(ctx, tx, sqlc.ExecutionLockForLeaseParams{
		ExecutionID: mustPgUUID(params.ID), LeaseToken: mustPgUUID(params.LeaseToken),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrExecutionLeaseLost
	}
	if err != nil {
		return Execution{}, mapErr(err)
	}
	if err := validateCompletion(params, int(locked.MaxOutputBytes)); err != nil {
		return Execution{}, err
	}
	if !locked.LeaseExpiresAt.Valid || !params.FinishedAt.Before(locked.LeaseExpiresAt.Time) {
		return Execution{}, ErrExecutionLeaseLost
	}
	if locked.CancelRequestedAt.Valid && params.Status != api.ExecutionStatusCancelled {
		return Execution{}, fmt.Errorf("%w: cancellation was already requested", ErrExecutionInvalidTerminal)
	}
	if !locked.DeadlineAt.Time.After(params.FinishedAt) && params.Status != api.ExecutionStatusTimedOut &&
		params.Status != api.ExecutionStatusCancelled {
		return Execution{}, fmt.Errorf("%w: execution cannot succeed after its deadline", ErrExecutionInvalidTerminal)
	}

	var exitCode pgtype.Int4
	if params.ExitCode != nil {
		exitCode = pgtype.Int4{Int32: int32(*params.ExitCode), Valid: true}
	}
	var failureCode, failureMessage any
	if params.FailureCode != nil {
		failureCode = *params.FailureCode
	}
	if params.FailureMessage != nil {
		failureMessage = *params.FailureMessage
	}
	artifacts := []byte{}
	if len(params.Artifacts) != 0 {
		artifacts, _ = json.Marshal(params.Artifacts)
	}
	row, err := q.ExecutionMarkTerminal(ctx, tx, sqlc.ExecutionMarkTerminalParams{
		Artifacts: artifacts, TerminalStatus: string(params.Status), ResultJson: string(params.Result), ResultBytes: int32(len(params.Result)),
		Stdout: params.Stdout, Stderr: params.Stderr, OutputTruncated: params.OutputTruncated,
		ExitCode: exitCode, FailureCode: failureCode, FailureMessage: failureMessage,
		WallTimeMs: params.Usage.WallTimeMS, CpuTimeMs: params.Usage.CPUTimeMS,
		PeakMemoryMb: int32(params.Usage.PeakMemoryMB), FinishedAt: executionTime(params.FinishedAt),
		ExecutionID: mustPgUUID(params.ID), LeaseToken: mustPgUUID(params.LeaseToken),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrExecutionLeaseLost
	}
	if err != nil {
		return Execution{}, mapErr(err)
	}
	if _, err := q.ExecutionPayloadDelete(ctx, tx, row.ID); err != nil {
		return Execution{}, fmt.Errorf("complete execution: delete payload: %w", err)
	}
	if err := recordExecutionUsage(ctx, q, tx, row.ID); err != nil {
		return Execution{}, fmt.Errorf("complete execution: %w", err)
	}
	projected := executionFromSQL(row)
	if !params.OutputEventsPersisted {
		var eventErr error
		forEachExecutionOutputEvent(projected, func(eventType ExecutionEventType, payload json.RawMessage) {
			if eventErr != nil {
				return
			}
			_, eventErr = appendExecutionEvent(ctx, tx, projected.AccountID, projected.ID, eventType, payload, params.FinishedAt)
		})
		if eventErr != nil {
			return Execution{}, fmt.Errorf("complete execution: append output event: %w", eventErr)
		}
	}
	if _, err := appendExecutionEvent(ctx, tx, projected.AccountID, projected.ID, ExecutionEventTerminal, executionTerminalPayload(projected), params.FinishedAt); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, fmt.Errorf("complete execution: commit: %w", err)
	}
	return executionFromSQL(row), nil
}

func (s *PgStore) RequestExecutionCancellation(ctx context.Context, accountID, executionID string, requestedAt time.Time) (Execution, error) {
	if accountID == "" || executionID == "" || requestedAt.IsZero() {
		return Execution{}, ErrExecutionInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, fmt.Errorf("cancel execution: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New()
	locked, err := q.ExecutionLockForAccount(ctx, tx, sqlc.ExecutionLockForAccountParams{
		AccountID: mustPgUUID(accountID), ExecutionID: mustPgUUID(executionID),
	})
	if err != nil {
		return Execution{}, mapErr(err)
	}
	if locked.Status == string(api.ExecutionStatusSucceeded) ||
		api.ExecutionStatus(locked.Status).Terminal() {
		if err := recordExecutionUsage(ctx, q, tx, locked.ID); err != nil {
			return Execution{}, fmt.Errorf("cancel execution: %w", err)
		}
		if _, err := q.ExecutionPayloadDelete(ctx, tx, locked.ID); err != nil {
			return Execution{}, fmt.Errorf("cancel execution: clean terminal payload: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Execution{}, fmt.Errorf("cancel execution: commit terminal read: %w", err)
		}
		return executionFromSQL(locked), nil
	}
	if requestedAt.Before(locked.CreatedAt.Time) {
		return Execution{}, fmt.Errorf("%w: cancellation predates admission", ErrExecutionInvalid)
	}
	row, err := q.ExecutionRequestCancel(ctx, tx, sqlc.ExecutionRequestCancelParams{
		ExecutionID: locked.ID, RequestedAt: executionTime(requestedAt),
	})
	if err != nil {
		return Execution{}, mapErr(err)
	}
	if api.ExecutionStatus(row.Status).Terminal() {
		if err := recordExecutionUsage(ctx, q, tx, row.ID); err != nil {
			return Execution{}, fmt.Errorf("cancel execution: %w", err)
		}
		projected := executionFromSQL(row)
		if _, err := appendExecutionEvent(ctx, tx, projected.AccountID, projected.ID, ExecutionEventTerminal, executionTerminalPayload(projected), requestedAt); err != nil {
			return Execution{}, fmt.Errorf("cancel execution: append event: %w", err)
		}
		if _, err := q.ExecutionPayloadDelete(ctx, tx, row.ID); err != nil {
			return Execution{}, fmt.Errorf("cancel execution: delete payload: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, fmt.Errorf("cancel execution: commit: %w", err)
	}
	return executionFromSQL(row), nil
}

func (s *PgStore) SweepExecutions(ctx context.Context, at time.Time, limit int) (ExecutionSweepResult, error) {
	if at.IsZero() {
		return ExecutionSweepResult{}, ErrExecutionInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New()
	sweepAt := executionTime(at)
	batch := int32(limit)
	expired, err := q.ExecutionExpireQueued(ctx, tx, sqlc.ExecutionExpireQueuedParams{SweepAt: sweepAt, BatchLimit: batch})
	if err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: expire queued: %w", err)
	}
	finishedRestores, err := q.ExecutionFinishExpiredRestores(ctx, tx, sqlc.ExecutionFinishExpiredRestoresParams{SweepAt: sweepAt, BatchLimit: batch})
	if err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: finish restores: %w", err)
	}
	requeued, err := q.ExecutionRequeueExpiredRestores(ctx, tx, sqlc.ExecutionRequeueExpiredRestoresParams{SweepAt: sweepAt, BatchLimit: batch})
	if err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: requeue restores: %w", err)
	}
	finishedRuns, err := q.ExecutionFinishExpiredRuns(ctx, tx, sqlc.ExecutionFinishExpiredRunsParams{SweepAt: sweepAt, BatchLimit: batch})
	if err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: finish runs: %w", err)
	}

	terminalRows := make([]sqlc.Execution, 0, len(expired)+len(finishedRestores)+len(finishedRuns))
	terminalRows = append(terminalRows, expired...)
	terminalRows = append(terminalRows, finishedRestores...)
	terminalRows = append(terminalRows, finishedRuns...)
	for _, row := range requeued {
		projected := executionFromSQL(row)
		if _, err := appendExecutionEvent(ctx, tx, projected.AccountID, projected.ID, ExecutionEventStatus, executionStatusPayload(api.ExecutionStatusQueued), at); err != nil {
			return ExecutionSweepResult{}, fmt.Errorf("sweep executions: append requeue event: %w", err)
		}
	}
	ids := make([]pgtype.UUID, 0, len(terminalRows))
	for _, row := range terminalRows {
		ids = append(ids, row.ID)
		projected := executionFromSQL(row)
		var eventErr error
		forEachExecutionOutputEvent(projected, func(eventType ExecutionEventType, payload json.RawMessage) {
			if eventErr != nil {
				return
			}
			_, eventErr = appendExecutionEvent(ctx, tx, projected.AccountID, projected.ID, eventType, payload, at)
		})
		if eventErr != nil {
			return ExecutionSweepResult{}, fmt.Errorf("sweep executions: append output event: %w", eventErr)
		}
		if _, err := appendExecutionEvent(ctx, tx, projected.AccountID, projected.ID, ExecutionEventTerminal, executionTerminalPayload(projected), at); err != nil {
			return ExecutionSweepResult{}, fmt.Errorf("sweep executions: append terminal event: %w", err)
		}
	}
	var deleted int64
	if len(ids) > 0 {
		for _, id := range ids {
			if err := recordExecutionUsage(ctx, q, tx, id); err != nil {
				return ExecutionSweepResult{}, fmt.Errorf("sweep executions: %w", err)
			}
		}
		deleted, err = q.ExecutionPayloadDeleteMany(ctx, tx, ids)
		if err != nil {
			return ExecutionSweepResult{}, fmt.Errorf("sweep executions: delete payloads: %w", err)
		}
	}
	orphans, err := q.ExecutionPayloadDeleteTerminal(ctx, tx, batch)
	if err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: delete terminal payloads: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionSweepResult{}, fmt.Errorf("sweep executions: commit: %w", err)
	}
	return ExecutionSweepResult{
		ExpiredQueued: len(expired), RequeuedRestores: len(requeued),
		FinishedRestores: len(finishedRestores), FinishedRuns: len(finishedRuns),
		PayloadsDeleted: int(deleted + orphans),
	}, nil
}

// ExecutionUsageByAccount returns the UTC-month aggregate from the durable
// execution usage ledger. Terminal payload deletion does not affect this read.
func (s *PgStore) ExecutionUsageByAccount(ctx context.Context, accountID string, month time.Time) (ExecutionUsageSummary, error) {
	if strings.TrimSpace(accountID) == "" || month.IsZero() {
		return ExecutionUsageSummary{}, ErrExecutionInvalid
	}
	month = month.UTC()
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	row, err := sqlc.New().ExecutionUsageByAccount(ctx, s.pool, sqlc.ExecutionUsageByAccountParams{
		AccountID: mustPgUUID(accountID), MonthStart: executionTime(start), MonthEnd: executionTime(end),
	})
	if err != nil {
		return ExecutionUsageSummary{}, mapErr(err)
	}
	return ExecutionUsageSummary{
		AccountID: accountID, Month: start, Runs: row.Runs,
		WallTimeMS: row.WallTimeMs, CPUTimeMS: row.CpuTimeMs,
		PeakMemoryMB: row.PeakMemoryMb, OutputBytes: row.OutputBytes,
		Succeeded: row.Succeeded, Failed: row.Failed, TimedOut: row.TimedOut,
		OutOfMemory: row.OutOfMemory, Cancelled: row.Cancelled,
	}, nil
}
