package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

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
