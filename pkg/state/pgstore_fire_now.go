package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// pgstore_fire_now.go — CRUD for the cron_fire_now_requests table
// (migrations/00193, ADR-090 PR-C).
//
// Two producers, one consumer:
//
//   - apid (cmd/apid/handlers_cron_run.go::fireCronNow) is the only
//     caller of InsertFireNowRequest. Status starts at `pending`.
//   - schedd (pkg/sched/fire_now.go) is the only caller of
//     ClaimPendingFireNowRequest / MarkFireNowRequestRunning /
//     MarkFireNowRequestSucceeded / MarkFireNowRequestFailed. It
//     updates status to `running` on claim and to a terminal value
//     after RunCronNow returns.
//
// Cross-account safety: every helper takes an explicit account_id or
// cron_id parameter; schedd never queries by account_id alone (the
// claim query selects by status='pending' ORDER BY requested_at — the
// row's account_id is for audit-log correlation, not access control).
// The API surface is gated by the IDOR check in fireCronNow
// (CronByID → AppByID → AccountID == acct.ID).

// ErrFireNowRequestNotFound is returned by Mark* helpers when no row
// matches the given request id. Distinct from ErrNotFound so callers
// can tell "the row vanished (someone deleted the cron, or the
// cancelled terminal was already stamped)" from "the cron is missing"
// upstream.
var ErrFireNowRequestNotFound = errors.New("state: fire-now request not found")

// InsertFireNowRequest writes a new pending row and returns the
// generated id. Caller (apid) is responsible for the subsequent
// `pg_notify('cron_run_now', request_id)`; the helper does NOT emit
// the notify so the producer has a clean transactional boundary
// (the row is committed before the notify fires).
//
// Both ids are passed as strings for symmetry with the rest of the
// pgstore surface (uuid.UUID casts inline below).
func (s *PgStore) InsertFireNowRequest(ctx context.Context, cronID, accountID string) (string, error) {
	id := uuid.NewString()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cron_fire_now_requests (id, cron_id, account_id, status)
		VALUES ($1, $2, $3, 'pending')
	`, id, cronID, accountID)
	if err != nil {
		return "", fmt.Errorf("state: insert fire_now_request: %w", err)
	}
	return id, nil
}

// ClaimPendingFireNowRequest atomically transitions one pending row
// to `running` and returns it. The SKIP LOCKED clause lets N schedd
// instances race for the wakeup without blocking each other; a
// already-claimed row is invisible to the next caller.
//
// Returns ErrFireNowRequestNotFound when no pending row exists
// (caller stops processing this tick). On success the returned row's
// Status is `running` and the caller is expected to update it to a
// terminal value via MarkFireNowRequestSucceeded / MarkFireNowRequestFailed.
//
// The TX is a single-statement transaction so the claim + status
// update are atomic against the rest of the system. The 5 s statement
// timeout matches pgstore.go defaults.
func (s *PgStore) ClaimPendingFireNowRequest(ctx context.Context) (FireNowRequest, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return FireNowRequest{}, fmt.Errorf("state: claim fire_now_request begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var req FireNowRequest
	row := tx.QueryRow(ctx, `
		SELECT id, cron_id, account_id, requested_at, status
		FROM cron_fire_now_requests
		WHERE status = 'pending'
		ORDER BY requested_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`)
	if err := row.Scan(&req.ID, &req.CronID, &req.AccountID, &req.RequestedAt, &req.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FireNowRequest{}, ErrFireNowRequestNotFound
		}
		return FireNowRequest{}, fmt.Errorf("state: claim fire_now_request scan: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE cron_fire_now_requests
		SET status = 'running'
		WHERE id = $1
	`, req.ID); err != nil {
		return FireNowRequest{}, fmt.Errorf("state: claim fire_now_request update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return FireNowRequest{}, fmt.Errorf("state: claim fire_now_request commit: %w", err)
	}
	req.Status = FireNowStatusRunning
	return req, nil
}

// MarkFireNowRequestSucceeded stamps the row's terminal state. The
// invocation_id is required so the customer-side `GET /v1/crons/{id}/runs`
// (PR-A's surface) can join against the invocations row. finished_at
// is server-stamped to wall-clock now; callers do not pass it.
func (s *PgStore) MarkFireNowRequestSucceeded(ctx context.Context, requestID, invocationID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE cron_fire_now_requests
		SET status = 'succeeded',
		    invocation_id = $2,
		    finished_at = $3
		WHERE id = $1 AND status = 'running'
	`, requestID, invocationID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("state: mark fire_now_request succeeded: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrFireNowRequestNotFound
	}
	return nil
}

// MarkFireNowRequestFailed stamps the row's terminal state with the
// failure message. Used for both dispatch failures
// (sched.RunCronNow returns ErrNoCapacity, ErrCronDisabled) and for
// errors that surface mid-dispatch (e.g. app suspended).
//
// errMsg is the free-text error string the customer will see via
// `GET /v1/crons/{id}/runs` (joined with the audit event). Cap to 1 KB
// to match the audit payload convention (pkg/sched/loop.go:1840-1864).
func (s *PgStore) MarkFireNowRequestFailed(ctx context.Context, requestID, errMsg string) error {
	errMsg = safetext.Truncate(errMsg, api.AuditReasonMaxBytes)
	tag, err := s.pool.Exec(ctx, `
		UPDATE cron_fire_now_requests
		SET status = 'failed',
		    error = $2,
		    finished_at = $3
		WHERE id = $1 AND status = 'running'
	`, requestID, errMsg, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("state: mark fire_now_request failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrFireNowRequestNotFound
	}
	return nil
}

// GetFireNowRequest reads one row by id. Used by the API surface to
// poll request status (currently internal; future PR can expose
// `GET /v1/cron-fire-now-requests/{id}` if the customer UX needs it).
func (s *PgStore) GetFireNowRequest(ctx context.Context, requestID string) (FireNowRequest, error) {
	var req FireNowRequest
	row := s.pool.QueryRow(ctx, `
		SELECT id, cron_id, account_id, requested_at, status,
		       invocation_id, error, finished_at
		FROM cron_fire_now_requests
		WHERE id = $1
	`, requestID)
	if err := row.Scan(
		&req.ID, &req.CronID, &req.AccountID, &req.RequestedAt, &req.Status,
		&req.InvocationID, &req.Error, &req.FinishedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FireNowRequest{}, ErrFireNowRequestNotFound
		}
		return FireNowRequest{}, fmt.Errorf("state: get fire_now_request: %w", err)
	}
	return req, nil
}
