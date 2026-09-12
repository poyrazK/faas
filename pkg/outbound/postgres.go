package outbound

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresBackend provides the cross-process admission boundary. The
// integration row is locked for the short transaction, so twenty gateway
// processes still consume one token bucket and one lease set.
type PostgresBackend struct {
	pool *pgxpool.Pool
}

func NewPostgresBackend(pool *pgxpool.Pool) (*PostgresBackend, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound postgres pool is required")
	}
	return &PostgresBackend{pool: pool}, nil
}

func (b *PostgresBackend) Admit(ctx context.Context, spec AdmissionSpec) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if spec.IntegrationID == "" || math.IsNaN(spec.RatePerSecond) || math.IsInf(spec.RatePerSecond, 0) || spec.RatePerSecond <= 0 || spec.Burst < 1 || spec.MaxInFlight < 1 {
		return Decision{}, fmt.Errorf("%w: invalid admission spec", ErrInvalidIntegration)
	}
	integrationID, err := uuid.Parse(spec.IntegrationID)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	ttl := spec.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return Decision{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Decision{}, err
	}
	// The state row is created lazily. The lock below serializes all admissions
	// for this integration; leases are still deleted by expiry during admission.
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbound_admission_state (integration_id, tokens, last_refill)
		VALUES ($1, $2, $3) ON CONFLICT (integration_id) DO NOTHING`, integrationID, float64(spec.Burst), now); err != nil {
		return Decision{}, err
	}
	var tokens float64
	var lastRefill time.Time
	if err := tx.QueryRow(ctx, `
		SELECT tokens, last_refill
		FROM outbound_admission_state
		WHERE integration_id = $1
		FOR UPDATE`, integrationID).Scan(&tokens, &lastRefill); err != nil {
		return Decision{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_admission_leases WHERE integration_id = $1 AND expires_at <= $2`, integrationID, now); err != nil {
		return Decision{}, err
	}
	if elapsed := now.Sub(lastRefill).Seconds(); elapsed > 0 {
		tokens += elapsed * spec.RatePerSecond
		if tokens > float64(spec.Burst) {
			tokens = float64(spec.Burst)
		}
		lastRefill = now
	}
	// Persist refill progress even when the request is rejected. This prevents
	// repeated callers from repeatedly receiving a stale Retry-After value.
	if _, err := tx.Exec(ctx, `UPDATE outbound_admission_state SET tokens = $2, last_refill = $3 WHERE integration_id = $1`, integrationID, tokens, lastRefill); err != nil {
		return Decision{}, err
	}
	var inFlight int
	var earliest time.Time
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int, COALESCE(min(expires_at), $2)
		FROM outbound_admission_leases WHERE integration_id = $1`, integrationID, now).Scan(&inFlight, &earliest); err != nil {
		return Decision{}, err
	}
	if inFlight >= spec.MaxInFlight {
		retry := ttl
		if earliest.After(now) && earliest.Sub(now) < retry {
			retry = earliest.Sub(now)
		}
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: ReasonConcurrency}, nil
	}
	if tokens < 1 {
		retry := time.Duration((1 - tokens) / spec.RatePerSecond * float64(time.Second))
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: ReasonRate}, nil
	}
	tokens--
	leaseID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO outbound_admission_leases (lease_id, integration_id, expires_at) VALUES ($1, $2, $3)`, leaseID, integrationID, now.Add(ttl)); err != nil {
		return Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_admission_state SET tokens = $2, last_refill = $3 WHERE integration_id = $1`, integrationID, tokens, lastRefill); err != nil {
		return Decision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Decision{}, err
	}
	return Decision{Granted: true, LeaseID: leaseID.String()}, nil
}

func (b *PostgresBackend) Release(ctx context.Context, integrationID, leaseID string) error {
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	leaseUUID, err := uuid.Parse(leaseID)
	if err != nil {
		return fmt.Errorf("invalid outbound lease id: %w", err)
	}
	_, err = b.pool.Exec(ctx, `DELETE FROM outbound_admission_leases WHERE integration_id = $1 AND lease_id = $2`, integrationUUID, leaseUUID)
	return err
}

var _ Backend = (*PostgresBackend)(nil)
