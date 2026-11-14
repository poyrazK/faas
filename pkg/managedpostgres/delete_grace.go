package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Older reconcilers only read retry_at. Keep that at the purge deadline and
// let updated reconcilers discover pending holds through this durable marker.
const deletionHoldPendingCode = "deletion_hold_pending"

// The delete grace period (ADR-20261029174950). A customer's delete keeps the
// database in 'deleting' until PurgeAfter, with the provider resource held so
// nothing runs; the lifecycle reconciler deletes it once retry_at reaches
// PurgeAfter, through the ordinary delete path. Until then Undelete returns it
// to ready. Internal deletes and purges keep using Delete, which is immediate
// and ends any window.

// DeletionScheduleStore is an optional Store capability.
type DeletionScheduleStore interface {
	// ClaimScheduledDelete moves a ready database with no dependants to
	// deleting under a lease with a pending hold and retry_at at purgeAfter.
	// An expired restore slot's notice commits with the deletion schedule,
	// before provider I/O.
	ClaimScheduledDelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil, purgeAfter time.Time) (Database, error)
	// ClaimUndelete leases a database whose delete window is still open. A
	// database past its window, or never scheduled, is ErrNotFound; one
	// another operation holds is ErrConflict. The claim marks the hold pending
	// again so an interrupted undo is re-held before the purge deadline.
	ClaimUndelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil time.Time) (Database, error)
	// CancelScheduledDelete returns a leased, scheduled database to ready.
	CancelScheduledDelete(ctx context.Context, databaseID, leaseToken string, now time.Time) (Database, error)
}

// DeletionHoldProvider is an optional Provider capability: stop a database's
// compute and refuse its connections while its delete can still be undone,
// reversibly. Both calls are idempotent and confirm the provider's state.
type DeletionHoldProvider interface {
	HoldForDeletion(ctx context.Context, dataResourceID string) error
	ReleaseDeletionHold(ctx context.Context, dataResourceID string) error
}

// DeletionHoldProber is the qualification-only proof of a DeletionHoldProvider
// on a disposable resource: the hold stops the compute and refuses a new
// connection, and the release lets the next connection in.
type DeletionHoldProber interface {
	ProbeDeletionHold(ctx context.Context, providerResourceID string, material CredentialMaterial) (DeletionHoldEvidence, error)
}

// DeletionHoldEvidence is what a qualification run observed of the hold.
type DeletionHoldEvidence struct {
	SuspendedWhileHeld  bool `json:"suspended_while_held"`
	RefusedWhileHeld    bool `json:"refused_while_held"`
	ResumedAfterRelease bool `json:"resumed_after_release"`
}

func (e DeletionHoldEvidence) Validate() error {
	if !e.SuspendedWhileHeld || !e.RefusedWhileHeld || !e.ResumedAfterRelease {
		return ErrUnavailable
	}
	return nil
}

// ScheduleDelete is the customer's delete. A ready database is held and kept
// for api.ManagedPostgresDeleteGracePeriod; any other database is deleted at
// once, as before: one that never became ready has nothing to keep, and a
// failed one may not be able to hold. Repeating a confirmed schedule is a no-op;
// a pending hold is retried without restarting the undo window.
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
		if database.LastErrorCode != deletionHoldPendingCode {
			return database, nil
		}
		return s.reconcileScheduledDelete(ctx, database)
	case database.State != StateReady:
		return s.Delete(ctx, accountID, databaseID)
	}
	leaseToken := s.newLeaseToken()
	claimed, err := store.ClaimScheduledDelete(ctx, accountID, databaseID, leaseToken, now, now.Add(s.leaseDuration), now.Add(api.ManagedPostgresDeleteGracePeriod))
	if err != nil {
		return Database{}, err
	}
	return s.holdScheduledDelete(ctx, claimed)
}

// reconcileScheduledDelete recovers a hold before the undo window ends. The
// claim also fences undelete and purge, without requiring provisioning to be on.
func (s *Service) reconcileScheduledDelete(ctx context.Context, database Database) (Database, error) {
	store, ok := s.store.(DeletionScheduleStore)
	if !ok {
		return Database{}, ErrUnsupported
	}
	now := s.now()
	claimed, err := store.ClaimUndelete(ctx, database.AccountID, database.ID, s.newLeaseToken(), now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	return s.holdScheduledDelete(ctx, claimed)
}

func (s *Service) holdScheduledDelete(ctx context.Context, claimed Database) (Database, error) {
	holdErr := s.deletionHold(ctx, claimed, true)
	if err := s.releaseScheduledDelete(ctx, claimed, holdErr != nil); err != nil {
		return Database{}, errors.Join(holdErr, err)
	}
	if holdErr != nil {
		// A failed confirmation does not prove the provider left it enabled.
		return Database{}, holdErr
	}
	return s.Get(ctx, claimed.AccountID, claimed.ID)
}

func (s *Service) releaseScheduledDelete(ctx context.Context, claimed Database, pending bool) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	now := s.now()
	retryAt := claimed.PurgeAfter
	if retryAt.Before(now) {
		retryAt = now
	}
	code := ""
	if pending {
		code = deletionHoldPendingCode
	}
	return s.store.Release(finishCtx, claimed.ID, claimed.LeaseToken, StateDeleting, code, now, retryAt)
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
	if err := s.undeleteNeedsRoom(ctx, accountID, databaseID, now); err != nil {
		return Database{}, err
	}
	leaseToken := s.newLeaseToken()
	claimed, err := store.ClaimUndelete(ctx, accountID, databaseID, leaseToken, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	if err := s.deletionHold(ctx, claimed, false); err != nil {
		return Database{}, errors.Join(err, s.releaseScheduledDelete(ctx, claimed, true))
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return store.CancelScheduledDelete(finishCtx, claimed.ID, leaseToken, s.now())
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
		err = holder.HoldForDeletion(providerCtx, databaseDataResource(database))
	} else {
		err = holder.ReleaseDeletionHold(providerCtx, databaseDataResource(database))
	}
	if err != nil {
		return fmt.Errorf("%w: provider deletion hold: %w", ErrUnavailable, err)
	}
	return nil
}
