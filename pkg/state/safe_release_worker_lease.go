package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

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

var _ SafeReleaseWorkerLeaseStore = (*PgStore)(nil)
var _ SafeReleaseWorkerLeaseStore = (*MemStore)(nil)
var _ SafeReleaseEmergencyRecoveryStore = (*PgStore)(nil)
