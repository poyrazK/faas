package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The delete grace period (ADR-20261029174950). A customer's delete keeps the
// database in 'deleting' until PurgeAfter, with the provider resource held so
// nothing runs; the lifecycle reconciler deletes it once retry_at reaches
// PurgeAfter, through the ordinary delete path. Until then Undelete returns it
// to ready. Internal deletes and purges keep using Delete, which is immediate
// and ends any window.

// DeletionScheduleStore is an optional Store capability.
type DeletionScheduleStore interface {
	// ClaimScheduledDelete moves a ready database with no dependants to
	// deleting under a lease, with PurgeAfter and retry_at at purgeAfter.
	ClaimScheduledDelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil, purgeAfter time.Time) (Database, error)
	// ClaimUndelete leases a database whose delete window is still open. A
	// database past its window, or never scheduled, is ErrNotFound; one
	// another operation holds is ErrConflict.
	ClaimUndelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil time.Time) (Database, error)
	// CancelScheduledDelete returns a leased, scheduled database to ready.
	CancelScheduledDelete(ctx context.Context, databaseID, leaseToken string, now time.Time) (Database, error)
}

// DeletionHoldProvider is an optional Provider capability: stop a database's
// compute and refuse its connections while its delete can still be undone,
// reversibly. Both calls are idempotent and confirm the provider's state.
type DeletionHoldProvider interface {
	HoldForDeletion(ctx context.Context, providerResourceID string) error
	ReleaseDeletionHold(ctx context.Context, providerResourceID string) error
}

// ScheduleDelete is the customer's delete. A ready database is held and kept
// for api.ManagedPostgresDeleteGracePeriod; any other database is deleted at
// once, as before: one that never became ready has nothing to keep, and a
// failed one may not be able to hold. Scheduling twice is a no-op.
func (s *Service) ScheduleDelete(ctx context.Context, accountID, databaseID string) (Database, error) {
	store, ok := s.store.(DeletionScheduleStore)
	if !ok {
		return Database{}, ErrUnsupported
	}
	database, err := s.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	now := s.now()
	switch {
	case database.Restorable(now):
		return database, nil
	case database.State != StateReady:
		return s.Delete(ctx, accountID, databaseID)
	}
	leaseToken := s.newLeaseToken()
	claimed, err := store.ClaimScheduledDelete(ctx, accountID, databaseID, leaseToken, now, now.Add(s.leaseDuration), now.Add(api.ManagedPostgresDeleteGracePeriod))
	if err != nil {
		return Database{}, err
	}
	if err := s.deletionHold(ctx, claimed, true); err != nil {
		// The database was never out of the customer's hands: put it back.
		if _, cancelErr := store.CancelScheduledDelete(context.WithoutCancel(ctx), claimed.ID, leaseToken, s.now()); cancelErr != nil {
			return Database{}, errors.Join(err, fmt.Errorf("return database to ready: %w", cancelErr))
		}
		return Database{}, err
	}
	if err := s.store.Release(ctx, claimed.ID, leaseToken, StateDeleting, "", s.now(), claimed.PurgeAfter); err != nil {
		return Database{}, err
	}
	return s.Get(ctx, accountID, databaseID)
}

// Undelete returns a scheduled database to ready while its window is open.
// If the provider cannot release the hold, the schedule stands and the call
// can be retried.
func (s *Service) Undelete(ctx context.Context, accountID, databaseID string) (Database, error) {
	store, ok := s.store.(DeletionScheduleStore)
	if !ok {
		return Database{}, ErrUnsupported
	}
	now := s.now()
	leaseToken := s.newLeaseToken()
	claimed, err := store.ClaimUndelete(ctx, accountID, databaseID, leaseToken, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	if err := s.deletionHold(ctx, claimed, false); err != nil {
		if releaseErr := s.store.Release(context.WithoutCancel(ctx), claimed.ID, leaseToken, StateDeleting, "", s.now(), claimed.PurgeAfter); releaseErr != nil {
			return Database{}, errors.Join(err, fmt.Errorf("keep delete scheduled: %w", releaseErr))
		}
		return Database{}, err
	}
	return store.CancelScheduledDelete(ctx, claimed.ID, leaseToken, s.now())
}

// deletionHold applies or releases the provider hold. A provider without the
// capability is skipped: the database has no bindings, so nothing connects,
// and a scale-to-zero compute suspends on its own.
func (s *Service) deletionHold(ctx context.Context, database Database, hold bool) error {
	if database.ProviderResourceID == "" {
		return nil
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return ErrUnavailable
	}
	holder, ok := backend.Provider.(DeletionHoldProvider)
	if !ok {
		return nil
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	if hold {
		err = holder.HoldForDeletion(providerCtx, database.ProviderResourceID)
	} else {
		err = holder.ReleaseDeletionHold(providerCtx, database.ProviderResourceID)
	}
	if err != nil {
		return fmt.Errorf("%w: provider deletion hold: %w", ErrUnavailable, err)
	}
	return nil
}
