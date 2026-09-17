package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgMigrationLeaser exposes the durable migration_leases row through the
// common Leaser surface. The phase handlers still keep the local tracker for
// the live paused-VM object; this adapter is for scheduler/ops callers that
// need a restart-safe lease authority.
type pgMigrationLeaser struct {
	store state.MigrationLeaseStore
	now   func() time.Time
}

// NewPGMigrationLeaser returns a durable migration lease adapter. It returns
// nil when no store is supplied, matching the optional vmmd wiring posture.
func NewPGMigrationLeaser(store state.MigrationLeaseStore, now func() time.Time) sched.Leaser[any] {
	if store == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &pgMigrationLeaser{store: store, now: now}
}

func (l *pgMigrationLeaser) Acquire(ctx context.Context, key string, policy sched.LeasePolicy, ownerID string) (sched.LeaseToken, any, error) {
	if err := policy.Validate(); err != nil {
		return "", nil, err
	}
	now := l.now().UTC()
	lease := state.MigrationLease{
		InstanceID:     key,
		LeaseToken:     mintLeaseToken(),
		SourceNodeID:   ownerID,
		CreatedAt:      now,
		LeaseExpiresAt: now.Add(policy.TTL),
	}
	if err := l.store.ReserveMigrationLease(ctx, lease); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return "", nil, fmt.Errorf("%w: instance=%s", sched.ErrLeaseHeldByOther, key)
		}
		return "", nil, err
	}
	return sched.LeaseToken(lease.LeaseToken), lease, nil
}

func (l *pgMigrationLeaser) Renew(ctx context.Context, token sched.LeaseToken, ownerID string, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("%w: renew TTL must be > 0", sched.ErrInvalidLeasePolicy)
	}
	lease, err := l.store.GetMigrationLeaseByToken(ctx, string(token))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return sched.ErrLeaseNotFound
		}
		return err
	}
	if lease.SourceNodeID != ownerID {
		return fmt.Errorf("%w: owner=%s", sched.ErrLeaseHeldByOther, lease.SourceNodeID)
	}
	if !l.now().UTC().Before(lease.LeaseExpiresAt) {
		return sched.ErrLeaseExpired
	}
	err = l.store.RenewMigrationLease(ctx, string(token), ownerID, l.now().UTC().Add(ttl))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrNotFound):
		return sched.ErrLeaseNotFound
	case errors.Is(err, state.ErrMigrationLeaseExpired):
		return sched.ErrLeaseExpired
	case errors.Is(err, state.ErrConflict):
		return sched.ErrLeaseHeldByOther
	default:
		return err
	}
}

func (l *pgMigrationLeaser) Release(ctx context.Context, token sched.LeaseToken, ownerID string) error {
	lease, err := l.store.GetMigrationLeaseByToken(ctx, string(token))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return sched.ErrLeaseNotFound
		}
		return err
	}
	if lease.SourceNodeID != ownerID {
		return fmt.Errorf("%w: owner=%s", sched.ErrLeaseHeldByOther, lease.SourceNodeID)
	}
	err = l.store.DeleteMigrationLease(ctx, string(token))
	if errors.Is(err, state.ErrNotFound) {
		return sched.ErrLeaseNotFound
	}
	return err
}

func (l *pgMigrationLeaser) Lookup(ctx context.Context, token sched.LeaseToken) (string, time.Time, string, bool, error) {
	lease, err := l.store.GetMigrationLeaseByToken(ctx, string(token))
	if errors.Is(err, state.ErrNotFound) {
		return "", time.Time{}, "", false, nil
	}
	if err != nil {
		return "", time.Time{}, "", false, err
	}
	if !l.now().UTC().Before(lease.LeaseExpiresAt) {
		return "", time.Time{}, "", false, nil
	}
	return lease.InstanceID, lease.LeaseExpiresAt, lease.SourceNodeID, true, nil
}

var _ sched.Leaser[any] = (*pgMigrationLeaser)(nil)
