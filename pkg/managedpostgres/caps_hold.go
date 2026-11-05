package managedpostgres

import (
	"context"
	"errors"
)

// ComputeHoldProvider is an optional ComputeCapProvider capability: the
// delete grace period's hold, which stops a database's compute and refuses
// its connections until released (ADR-20261025094915 amendment 2026-11-05).
// The provider's quota trips only when its counters move, so the cap sweep
// holds each database the cap suspends and releases it when room returns.
// ComputeHeld reports whether the database is held now.
type ComputeHoldProvider interface {
	DeletionHoldProvider
	ComputeHeld(ctx context.Context, providerResourceID string) (bool, error)
}

// capHoldable keeps the databases the sweep holds and releases. A database
// being created or changed is left to its lifecycle worker, which connects to
// it; the next sweep after it settles decides. One in its delete grace period
// is not in the sweep at all, so a cap never releases a deletion hold.
func capHoldable(database Database) bool {
	return database.State == StateReady || database.State == StateFailed
}

// Why the sweep holds every database of a subject.
const (
	capHoldNone     = ""
	capHoldCeiling  = "ceiling"
	capHoldInactive = "inactive"
)

// holdDatabases holds every database of a subject when all names a reason
// (nothing left of its ceiling, or an inactive subject), and each database
// the sweep suspended past its plan (excess, empty unless that is enforced);
// it releases the rest. Quotas are written first, so a released database
// already has room. Mode shadow logs what it would change; a failure is the
// subject's failure, and the next sweep tries again.
func (e *CapEnforcer) holdDatabases(ctx context.Context, subjectID string, databases []Database, all string, excess map[string]string, summary *CapSweepSummary) error {
	var failures []error
	for _, database := range databases {
		if !capHoldable(database) {
			continue
		}
		backend, err := e.registry.Resolve(database.BackendID, database.BackendFingerprint)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		provider, ok := backend.Provider.(ComputeHoldProvider)
		if !ok {
			continue
		}
		reason := all
		if _, past := excess[database.ID]; past && reason == capHoldNone {
			reason = "plan_excess"
		}
		hold := reason != capHoldNone
		held, err := provider.ComputeHeld(ctx, database.ProviderResourceID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if held {
			summary.HeldDatabases++
		}
		if held == hold {
			continue
		}
		attrs := []any{"subject_id", subjectID, "database_id", database.ID, "hold", hold, "reason", reason}
		if e.mode == CapEnforcementShadow {
			summary.HoldsShadowed++
			e.logger.Info("managed postgres compute hold (shadow)", attrs...)
			continue
		}
		if hold {
			err = provider.HoldForDeletion(ctx, database.ProviderResourceID)
		} else {
			err = provider.ReleaseDeletionHold(ctx, database.ProviderResourceID)
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if hold {
			summary.HoldsApplied++
			summary.HeldDatabases++
			e.logger.Info("managed postgres compute held", attrs...)
		} else {
			summary.HoldsReleased++
			summary.HeldDatabases--
			e.logger.Info("managed postgres compute hold released", attrs...)
		}
	}
	return errors.Join(failures...)
}
