package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SafeReleaseWorkerLeaseHealth uses the database clock for both fields, so
// APID metrics remain meaningful when daemon and database clocks differ.
type SafeReleaseWorkerLeaseHealth struct {
	CheckedAt time.Time
	ExpiresAt time.Time
	Exists    bool
}

func (h SafeReleaseWorkerLeaseHealth) Ready() bool {
	return h.Exists && h.ExpiresAt.After(h.CheckedAt)
}

func (h SafeReleaseWorkerLeaseHealth) SecondsUntilExpiry() float64 {
	if !h.Exists {
		return 0
	}
	return h.ExpiresAt.Sub(h.CheckedAt).Seconds()
}

type SafeReleaseWorkerLeaseHealthStore interface {
	SafeReleaseWorkerLeaseHealth(context.Context) (SafeReleaseWorkerLeaseHealth, error)
}

var (
	// ErrSafeReleaseLeaseNotExpired means the worker has renewed recently
	// enough that emergency recovery must leave traffic alone.
	ErrSafeReleaseLeaseNotExpired = errors.New("state: safe release worker lease has not expired past the recovery grace")
	ErrSafeReleaseLeaseMissing    = errors.New("state: safe release worker lease is missing")
)

// SafeReleaseEmergencyRecoveryStore is the narrow APID fallback surface. It
// does not advance rollouts or consume health metrics.
type SafeReleaseEmergencyRecoveryStore interface {
	ListCanaryInFlight(context.Context) ([]Deployment, error)
	AbortCanaryOnExpiredWorkerLease(context.Context, string, string, time.Duration) (Deployment, int64, error)
}

// SafeReleaseWorkerLeaseStore is shared by meterd's successful tick observer
// and apid's canary admission gate. The database owns the lease clock.
type SafeReleaseWorkerLeaseStore interface {
	StampSafeReleaseWorkerLease(context.Context, time.Duration) error
	SafeReleaseWorkerLeaseReady(context.Context) (bool, error)
}

func (s *PgStore) StampSafeReleaseWorkerLease(ctx context.Context, ttl time.Duration) error {
	if ttl < time.Second || ttl%time.Second != 0 {
		return errors.New("state: safe release worker lease TTL must be positive whole seconds")
	}
	return sqlc.New().StampSafeReleaseWorkerLease(ctx, s.pool, int64(ttl/time.Second))
}

func (s *PgStore) SafeReleaseWorkerLeaseReady(ctx context.Context) (bool, error) {
	return sqlc.New().SafeReleaseWorkerLeaseReady(ctx, s.pool)
}

func (s *PgStore) SafeReleaseWorkerLeaseHealth(ctx context.Context) (SafeReleaseWorkerLeaseHealth, error) {
	var checkedAt time.Time
	var expiresAt sql.NullTime
	if err := s.pool.QueryRow(ctx,
		`select clock_timestamp(), (select expires_at from safe_release_worker_lease where singleton = true)`,
	).Scan(&checkedAt, &expiresAt); err != nil {
		return SafeReleaseWorkerLeaseHealth{}, fmt.Errorf("state: read safe release worker lease health: %w", err)
	}
	return SafeReleaseWorkerLeaseHealth{CheckedAt: checkedAt, ExpiresAt: expiresAt.Time, Exists: expiresAt.Valid}, nil
}

func (m *MemStore) StampSafeReleaseWorkerLease(_ context.Context, ttl time.Duration) error {
	if ttl < time.Second || ttl%time.Second != 0 {
		return errors.New("state: safe release worker lease TTL must be positive whole seconds")
	}
	m.mu.Lock()
	m.safeReleaseWorkerLeaseUntil = time.Now().Add(ttl)
	m.mu.Unlock()
	return nil
}

func (m *MemStore) SafeReleaseWorkerLeaseReady(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Now().Before(m.safeReleaseWorkerLeaseUntil), nil
}

func (m *MemStore) SafeReleaseWorkerLeaseHealth(_ context.Context) (SafeReleaseWorkerLeaseHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return SafeReleaseWorkerLeaseHealth{
		CheckedAt: time.Now(), ExpiresAt: m.safeReleaseWorkerLeaseUntil,
		Exists: !m.safeReleaseWorkerLeaseUntil.IsZero(),
	}, nil
}

var _ SafeReleaseWorkerLeaseStore = (*PgStore)(nil)
var _ SafeReleaseWorkerLeaseStore = (*MemStore)(nil)
var _ SafeReleaseEmergencyRecoveryStore = (*PgStore)(nil)
var _ SafeReleaseWorkerLeaseHealthStore = (*PgStore)(nil)
var _ SafeReleaseWorkerLeaseHealthStore = (*MemStore)(nil)
