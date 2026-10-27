package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/leaser"
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
func NewPGMigrationLeaser(store state.MigrationLeaseStore, now func() time.Time) leaser.Leaser[any] {
	if store == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &pgMigrationLeaser{store: store, now: now}
}

func (l *pgMigrationLeaser) Acquire(ctx context.Context, key string, policy leaser.Policy, ownerID string) (leaser.Token, any, error) {
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
			return "", nil, fmt.Errorf("%w: instance=%s", leaser.ErrHeldByOther, key)
		}
		return "", nil, err
	}
	return leaser.Token(lease.LeaseToken), lease, nil
}

func (l *pgMigrationLeaser) Renew(ctx context.Context, token leaser.Token, ownerID string, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("%w: renew TTL must be > 0", leaser.ErrInvalidPolicy)
	}
	lease, err := l.store.GetMigrationLeaseByToken(ctx, string(token))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return leaser.ErrNotFound
		}
		return err
	}
	if lease.SourceNodeID != ownerID {
		return fmt.Errorf("%w: owner=%s", leaser.ErrHeldByOther, lease.SourceNodeID)
	}
	if !l.now().UTC().Before(lease.LeaseExpiresAt) {
		return leaser.ErrExpired
	}
	err = l.store.RenewMigrationLease(ctx, string(token), ownerID, l.now().UTC().Add(ttl))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrNotFound):
		return leaser.ErrNotFound
	case errors.Is(err, state.ErrMigrationLeaseExpired):
		return leaser.ErrExpired
	case errors.Is(err, state.ErrConflict):
		return leaser.ErrHeldByOther
	default:
		return err
	}
}

func (l *pgMigrationLeaser) Release(ctx context.Context, token leaser.Token, ownerID string) error {
	lease, err := l.store.GetMigrationLeaseByToken(ctx, string(token))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return leaser.ErrNotFound
		}
		return err
	}
	if lease.SourceNodeID != ownerID {
		return fmt.Errorf("%w: owner=%s", leaser.ErrHeldByOther, lease.SourceNodeID)
	}
	if sources, ok := l.store.(state.FunctionMigrationSourceStore); ok {
		_, err := sources.GetFunctionMigrationSource(ctx, lease.InstanceID, lease.LeaseToken)
		if err == nil {
			return fmt.Errorf("%w: function source requires phase completion", state.ErrConflict)
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	err = l.store.DeleteMigrationLease(ctx, string(token))
	if errors.Is(err, state.ErrNotFound) {
		return leaser.ErrNotFound
	}
	return err
}

func (l *pgMigrationLeaser) Lookup(ctx context.Context, token leaser.Token) (string, time.Time, string, bool, error) {
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

var _ leaser.Leaser[any] = (*pgMigrationLeaser)(nil)
