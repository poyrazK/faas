package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGConcurrencyQueueAdmission coordinates warm-saturation queue permits across
// every gatewayd-internal replica. It persists permits, not request bodies;
// gateways retain local FIFO ownership and release the permit when the request
// leaves that queue. Expiry bounds leaked permits after a process crash.
type PGConcurrencyQueueAdmission struct {
	pool *pgxpool.Pool
}

func NewPGConcurrencyQueueAdmission(pool *pgxpool.Pool) *PGConcurrencyQueueAdmission {
	return &PGConcurrencyQueueAdmission{pool: pool}
}

// TryAcquireConcurrencyQueueLease atomically removes expired permits, checks
// the fleet-wide per-app cap, and creates one lease. The transaction-scoped
// advisory lock serializes only contenders for the same app.
func (b *PGConcurrencyQueueAdmission) TryAcquireConcurrencyQueueLease(ctx context.Context, appID string, limit int, ttl time.Duration) (string, int, bool, error) {
	if b == nil || b.pool == nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: postgres pool is not configured")
	}
	if appID == "" || limit <= 0 || ttl <= 0 {
		return "", 0, false, fmt.Errorf("concurrency queue admission: invalid lease policy")
	}
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// hashtextextended keeps the lock key stable without requiring a second
	// coordination row. A hash collision only serializes unrelated apps; it
	// cannot weaken either app's cap.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('gateway-concurrency-queue:' || $1, 0))`, appID); err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: lock app: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM gateway_concurrency_queue_leases WHERE app_id = $1 AND expires_at <= now()`, appID); err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: reap expired leases: %w", err)
	}

	var depth int
	if err := tx.QueryRow(ctx, `SELECT count(*)::integer FROM gateway_concurrency_queue_leases WHERE app_id = $1`, appID).Scan(&depth); err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: count leases: %w", err)
	}
	if depth >= limit {
		if err := tx.Commit(ctx); err != nil {
			return "", 0, false, fmt.Errorf("concurrency queue admission: commit reap: %w", err)
		}
		return "", depth, false, nil
	}

	leaseID := uuid.NewString()
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO gateway_concurrency_queue_leases (lease_id, app_id, expires_at)
		VALUES ($1, $2, now() + $3 * interval '1 millisecond')`, leaseID, appID, ttlMillis); err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: insert lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: commit lease: %w", err)
	}
	return leaseID, depth + 1, true, nil
}

// ReleaseConcurrencyQueueLease releases one permit. It is idempotent so a
// canceled request and normal deferred cleanup may safely race.
func (b *PGConcurrencyQueueAdmission) ReleaseConcurrencyQueueLease(ctx context.Context, appID, leaseID string) error {
	if b == nil || b.pool == nil {
		return fmt.Errorf("concurrency queue admission: postgres pool is not configured")
	}
	if appID == "" || leaseID == "" {
		return nil
	}
	if _, err := b.pool.Exec(ctx, `DELETE FROM gateway_concurrency_queue_leases WHERE app_id = $1 AND lease_id = $2`, appID, leaseID); err != nil {
		return fmt.Errorf("concurrency queue admission: release lease: %w", err)
	}
	return nil
}
