package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// withComputeHold serializes provider writes with deletion, resize and
// provisioning without changing the database's lifecycle state. An uncertain
// provider outcome keeps its lease until expiry, before another owner acts.
func (e *CapEnforcer) withComputeHold(ctx context.Context, database Database, apply func(context.Context, Database) error) error {
	providerCtx, cancel := context.WithTimeout(ctx, defaultProviderTimeout)
	defer cancel()
	if e.mode == CapEnforcementShadow {
		return apply(providerCtx, database)
	}
	store, ok := e.store.(ComputeHoldStore)
	if !ok {
		return ErrUnsupported
	}
	now := time.Now
	if e.now != nil {
		now = e.now
	}
	at := now()
	claimed, err := store.ClaimComputeHold(providerCtx, database, uuid.NewString(), at, at.Add(defaultLeaseDuration))
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		return nil // another owner or lifecycle generation won; retry next sweep
	}
	if err != nil {
		return err
	}
	if err := apply(providerCtx, claimed); err != nil {
		return err
	}
	finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer finishCancel()
	return store.ReleaseComputeHold(finish, claimed.ID, claimed.LeaseToken, now())
}
