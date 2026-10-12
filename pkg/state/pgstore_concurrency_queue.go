package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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
// the fleet-wide per-app cap, and creates one lease. The work runs in one
// server-side call (gateway_try_acquire_concurrency_queue_lease) whose
// transaction-scoped advisory lock serializes only contenders for the same
// app. Holding that lock across client round trips made a burst of overflow
// requests serialize at network latency while occupying every gateway pool
// connection (production-us hunt #9, H9-76).
func (b *PGConcurrencyQueueAdmission) TryAcquireConcurrencyQueueLease(ctx context.Context, appID string, limit int, ttl time.Duration) (string, int, bool, error) {
	if b == nil || b.pool == nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: postgres pool is not configured")
	}
	if appID == "" || limit <= 0 || ttl <= 0 {
		return "", 0, false, fmt.Errorf("concurrency queue admission: invalid lease policy")
	}
	leaseID := uuid.NewString()
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	var admitted bool
	var depth int
	if err := b.pool.QueryRow(ctx, `SELECT admitted, depth FROM gateway_try_acquire_concurrency_queue_lease($1, $2, $3, $4)`,
		appID, leaseID, limit, ttlMillis).Scan(&admitted, &depth); err != nil {
		return "", 0, false, fmt.Errorf("concurrency queue admission: acquire lease: %w", err)
	}
	if !admitted {
		return "", depth, false, nil
	}
	return leaseID, depth, true, nil
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
