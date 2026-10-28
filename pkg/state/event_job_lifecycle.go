package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventReplayBackfillLifecycleStore ends backfills (ADR-20261028095548):
// cancel on request, expire when idle, and release what an ended job left.
type EventReplayBackfillLifecycleStore interface {
	CancelEventReplayBackfill(ctx context.Context, accountID, jobID string) (api.EventReplayBackfillJobResponse, error)
	ExpireIdleEventReplayBackfills(ctx context.Context, now time.Time, limit int) (int, error)
	ReleaseEndedEventReplayBackfills(ctx context.Context, limit int) (int, error)
}

// EventJobError is a recovery or backfill job whose processing failed. The
// job was backed off, or ended once it reached api.EventJobFailureLimit
// failures in a row, so a sweep can go on to the next job.
type EventJobError struct {
	JobID string
	Code  string
	Ended bool
	Err   error
}

func (e *EventJobError) Error() string {
	return fmt.Sprintf("event job %s failed (%s): %v", e.JobID, e.Code, e.Err)
}

func (e *EventJobError) Unwrap() error { return e.Err }

// errEventJobInvalid marks a job whose stored target or selection cannot be
// read; retrying it cannot help.
var errEventJobInvalid = errors.New("event job definition is invalid")

// eventJobReleasePage bounds the receipts one release transaction locks.
const eventJobReleasePage = 500

// eventJobFailureCode is the short, customer-visible failure class; the
// full error is only logged.
func eventJobFailureCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errEventJobInvalid):
		return "invalid_job"
	case errors.Is(err, ErrConflict):
		return "conflict"
	default:
		return "internal"
	}
}

func eventJobFailureParams(now time.Time, code string) (int32, pgtype.Timestamptz, float64, float64, string) {
	return api.EventJobFailureLimit, pgtypeFromTime(now), api.EventJobFailureBackoffMin.Seconds(), api.EventJobFailureBackoffMax.Seconds(), code
}

func (s *PgStore) recordEventReplayBackfillFailure(ctx context.Context, jobID pgtype.UUID, now time.Time, cause error) (bool, error) {
	jobErr := &EventJobError{JobID: uuidString(jobID), Code: eventJobFailureCode(cause), Err: cause}
	limit, at, minDelay, maxDelay, code := eventJobFailureParams(now, jobErr.Code)
	count, err := sqlc.New().EventReplayBackfillRecordFailure(ctx, s.pool, sqlc.EventReplayBackfillRecordFailureParams{
		ID: jobID, FailureLimit: limit, NowAt: at, BackoffMinSeconds: minDelay, BackoffMaxSeconds: maxDelay, LastFailure: code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return true, jobErr
	}
	if err != nil {
		return false, errors.Join(cause, fmt.Errorf("record event replay backfill failure: %w", err))
	}
	if count >= api.EventJobFailureLimit {
		// The job is already backed off, so a failed end only delays it.
		if jobErr.Ended, err = s.endEventReplayBackfill(ctx, jobID, "failed", time.Time{}); err != nil {
			jobErr.Err = errors.Join(cause, err)
		}
	}
	return true, jobErr
}

func (s *PgStore) recordEventRecoveryFailure(ctx context.Context, jobID pgtype.UUID, now time.Time, cause error) (bool, error) {
	jobErr := &EventJobError{JobID: uuidString(jobID), Code: eventJobFailureCode(cause), Err: cause}
	limit, at, minDelay, maxDelay, code := eventJobFailureParams(now, jobErr.Code)
	q := sqlc.New()
	row, err := q.EventRecoveryRecordFailure(ctx, s.pool, sqlc.EventRecoveryRecordFailureParams{
		ID: jobID, FailureLimit: limit, NowAt: at, BackoffMinSeconds: minDelay, BackoffMaxSeconds: maxDelay, LastFailure: code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return true, jobErr
	}
	if err != nil {
		return false, errors.Join(cause, fmt.Errorf("record event recovery failure: %w", err))
	}
	if row.FailureCount < api.EventJobFailureLimit {
		return true, jobErr
	}
	// Recovery keeps its four states: a job that keeps failing is cancelled,
	// and its history names the system actor and the failure.
	audit := recoveryAudit{Kind: "system", ID: "recovery_failure",
		Reason: fmt.Sprintf("processing failed %d times in a row (last failure: %s)", api.EventJobFailureLimit, code)}
	if err := s.cancelFailingEventRecovery(context.WithValue(ctx, recoveryAuditKey{}, audit), uuidString(row.AccountID), jobErr.JobID, now); err != nil {
		jobErr.Err = errors.Join(cause, err)
		return true, jobErr
	}
	jobErr.Ended = true
	return true, jobErr
}

func (s *PgStore) cancelFailingEventRecovery(ctx context.Context, accountID, jobID string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EventRecoveryLock(ctx, tx, sqlc.EventRecoveryLockParams{AccountID: mustPgUUID(accountID), JobID: mustPgUUID(jobID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !eventRecoveryActive(row.State) {
		return nil
	}
	if err := cancelEventRecoveryTx(ctx, q, tx, jobID, now, "cancelled"); err != nil {
		return fmt.Errorf("cancel failing event recovery: %w", err)
	}
	return tx.Commit(ctx)
}

// CancelEventReplayBackfill ends a running backfill as cancelled and returns
// it; an ended job is returned unchanged.
func (s *PgStore) CancelEventReplayBackfill(ctx context.Context, accountID, jobID string) (api.EventReplayBackfillJobResponse, error) {
	if _, err := uuid.Parse(jobID); err != nil {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuery
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	if _, err := q.EventReplayBackfillLockAnyJob(ctx, tx, sqlc.EventReplayBackfillLockAnyJobParams{ID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayBackfillJobResponse{}, ErrNotFound
	} else if err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	ended, err := q.EventReplayBackfillEnd(ctx, tx, sqlc.EventReplayBackfillEndParams{ID: mustPgUUID(jobID), State: "cancelled"})
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("cancel event replay backfill: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	if ended > 0 {
		// schedd repeats the release for anything this one leaves.
		_, _ = s.releaseEndedEventReplayBackfill(ctx, mustPgUUID(jobID))
	}
	return getEventReplayBackfill(ctx, q, s.pool, accountID, jobID)
}

// ExpireIdleEventReplayBackfills ends running backfills that have not moved
// for api.EventReplayBackfillIdleExpiry.
func (s *PgStore) ExpireIdleEventReplayBackfills(ctx context.Context, now time.Time, limit int) (int, error) {
	idleBefore := now.Add(-api.EventReplayBackfillIdleExpiry)
	ids, err := sqlc.New().EventReplayBackfillIdleJobs(ctx, s.pool, sqlc.EventReplayBackfillIdleJobsParams{IdleBefore: pgtypeFromTime(idleBefore), PageLimit: int32(limit)})
	if err != nil {
		return 0, fmt.Errorf("list idle event replay backfills: %w", err)
	}
	expired := 0
	for _, id := range ids {
		ended, err := s.endEventReplayBackfill(ctx, id, "expired", idleBefore)
		if err != nil {
			return expired, err
		}
		if ended {
			expired++
		}
	}
	return expired, nil
}

// ReleaseEndedEventReplayBackfills finishes the release of ended jobs whose
// undelivered items are not yet cancelled.
func (s *PgStore) ReleaseEndedEventReplayBackfills(ctx context.Context, limit int) (int, error) {
	ids, err := sqlc.New().EventReplayBackfillEndedJobs(ctx, s.pool, int32(limit))
	if err != nil {
		return 0, fmt.Errorf("list ended event replay backfills: %w", err)
	}
	for i, id := range ids {
		if _, err := s.releaseEndedEventReplayBackfill(ctx, id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// endEventReplayBackfill ends a running job, then releases what it holds. A
// non-zero idleBefore ends it only if it has not moved since then.
func (s *PgStore) endEventReplayBackfill(ctx context.Context, jobID pgtype.UUID, state string, idleBefore time.Time) (bool, error) {
	params := sqlc.EventReplayBackfillEndParams{ID: jobID, State: state}
	if !idleBefore.IsZero() {
		params.IdleBefore = pgtypeFromTime(idleBefore)
	}
	ended, err := sqlc.New().EventReplayBackfillEnd(ctx, s.pool, params)
	if err != nil {
		return false, fmt.Errorf("end event replay backfill: %w", err)
	}
	if ended == 0 {
		return false, nil
	}
	_, err = s.releaseEndedEventReplayBackfill(ctx, jobID)
	return true, err
}

// releaseEndedEventReplayBackfill deletes one page of an ended job's
// undelivered recipients and settles their receipts, locking receipts first
// as routing does. A delivery a sweep is admitting keeps its invocation:
// the release waits for that receipt and then skips the settled recipient.
// Once no recipient remains, the job's open items become cancelled.
func (s *PgStore) releaseEndedEventReplayBackfill(ctx context.Context, jobID pgtype.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	ids, err := q.EventReplayBackfillLockEndedReceipts(ctx, tx, sqlc.EventReplayBackfillLockEndedReceiptsParams{JobID: jobID, PageLimit: eventJobReleasePage})
	if err != nil {
		return false, fmt.Errorf("lock ended event replay receipts: %w", err)
	}
	if len(ids) > 0 {
		if _, err := q.EventReplayBackfillDropEndedRecipients(ctx, tx, sqlc.EventReplayBackfillDropEndedRecipientsParams{JobID: jobID, OutboxIds: ids}); err != nil {
			return false, fmt.Errorf("drop ended event replay recipients: %w", err)
		}
		if err := q.EventReplayBackfillSettleReceipts(ctx, tx, ids); err != nil {
			return false, fmt.Errorf("settle ended event replay receipts: %w", err)
		}
	}
	more := len(ids) == eventJobReleasePage
	if !more {
		if _, err := q.EventReplayBackfillCancelEndedItems(ctx, tx, jobID); err != nil {
			return false, fmt.Errorf("cancel ended event replay items: %w", err)
		}
	}
	return more, tx.Commit(ctx)
}
