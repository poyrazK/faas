package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ExecutionStore = (*PgStore)(nil)

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

func executionFromSQL(row sqlc.Execution) Execution {
	return Execution{
		ID:          pgUUIDString(row.ID),
		AccountID:   pgUUIDString(row.AccountID),
		Runtime:     api.ExecutionRuntime(row.Runtime),
		Status:      api.ExecutionStatus(row.Status),
		NetworkMode: api.ExecutionNetworkMode(row.NetworkMode),
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

func (s *PgStore) CreateExecution(ctx context.Context, params CreateExecutionParams) (Execution, error) {
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
	account, err := q.ExecutionLockAccount(ctx, tx, accountID)
	if err != nil {
		return Execution{}, mapErr(err)
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

	row, err := q.ExecutionInsert(ctx, tx, sqlc.ExecutionInsertParams{
		AccountID:       accountID,
		Runtime:         string(params.Request.Runtime),
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
	})
	if err != nil {
		return Execution{}, mapErr(err)
	}
	if err := q.ExecutionPayloadInsert(ctx, tx, sqlc.ExecutionPayloadInsertParams{
		ExecutionID:   row.ID,
		SealedPayload: append([]byte(nil), params.SealedPayload...),
		Kid:           params.PayloadKID,
		CreatedAt:     executionTime(params.AdmittedAt),
	}); err != nil {
		return Execution{}, mapErr(err)
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

func (s *PgStore) ClaimExecution(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
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
	row, err := q.ExecutionClaimNext(ctx, tx, sqlc.ExecutionClaimNextParams{
		LeaseToken:     tokenPG,
		LeaseOwner:     pgtype.Text{String: owner, Valid: true},
		LeaseExpiresAt: executionTime(claimedAt.Add(leaseDuration)),
		ClaimedAt:      executionTime(claimedAt),
	})
	if err != nil {
		return ExecutionClaim{}, mapErr(err)
	}
	payload, err := q.ExecutionPayloadForLease(ctx, tx, sqlc.ExecutionPayloadForLeaseParams{
		ExecutionID: row.ID, LeaseToken: tokenPG,
	})
	if err != nil {
		return ExecutionClaim{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionClaim{}, fmt.Errorf("claim execution: commit: %w", err)
	}
	return ExecutionClaim{
		Execution:     executionFromSQL(row),
		SealedPayload: append([]byte(nil), payload.SealedPayload...),
		PayloadKID:    payload.Kid,
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
	row, err := q.ExecutionMarkTerminal(ctx, tx, sqlc.ExecutionMarkTerminalParams{
		TerminalStatus: string(params.Status), ResultJson: string(params.Result), ResultBytes: int32(len(params.Result)),
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
	ids := make([]pgtype.UUID, 0, len(terminalRows))
	for _, row := range terminalRows {
		ids = append(ids, row.ID)
	}
	var deleted int64
	if len(ids) > 0 {
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
