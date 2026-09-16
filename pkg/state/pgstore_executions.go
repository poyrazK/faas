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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ExecutionStore = (*PgStore)(nil)
var _ ExecutionQueueStore = (*PgStore)(nil)

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

func recordExecutionUsage(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, executionID pgtype.UUID) error {
	if err := q.ExecutionUsageRecord(ctx, db, executionID); err != nil {
		return fmt.Errorf("record execution usage: %w", err)
	}
	return nil
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
	if err := tx.Commit(ctx); err != nil {
		return ExecutionClaim{}, fmt.Errorf("claim execution: commit: %w", err)
	}
	// The event is best-effort after the claim commit. The durable row remains
	// authoritative if a transient event-log write fails.
	_, _ = appendExecutionEvent(ctx, s.pool, pgUUIDString(row.AccountID), pgUUIDString(row.ID), ExecutionEventStatus, executionStatusPayload(api.ExecutionStatusRestoring), claimedAt)
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
