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
// is not in the sweep. The lifecycle claim rechecks this before provider I/O,
// so a stale sweep cannot release a deletion hold.
func capHoldable(database Database) bool {
	return database.State == StateReady || database.State == StateFailed
}

// Why the sweep holds every database of a subject.
const (
	capHoldNone     = ""
	capHoldCeiling  = "ceiling"
	capHoldInactive = "inactive"
)

// Why the sweep holds one database of a subject.
const (
	capHoldPlanExcess = "plan_excess"
	// capHoldPreview holds a PR preview database once its subject reaches
	// 80% of its ceiling, so production keeps the last fifth
	// (ADR-20261102123523 decision 6). A copy shares its source's provider
	// object, and so its quota, which cannot hold it alone.
	capHoldPreview = "preview"
)

// capHeldDatabases is the databases the sweep holds on their own, by reason:
// each one suspended past its plan, and every PR preview database once the
// subject has used 80% of its ceiling.
func capHeldDatabases(databases []Database, plan computeCapPlan, excess map[string]string) map[string]string {
	held := make(map[string]string, len(excess))
	for id := range excess {
		held[id] = capHoldPlanExcess
	}
	if plan.used < previewComputeCeiling(plan.ceiling) {
		return held
	}
	for _, database := range databases {
		if _, ok := held[database.ID]; !ok && !database.Preview.IsZero() {
			held[database.ID] = capHoldPreview
		}
	}
	return held
}

// holdDatabases holds every database of a subject when all names a reason
// (nothing left of its ceiling, or an inactive subject), and each database
// reasons names (capHeldDatabases: past its plan, a PR preview past 80%); it
// releases the rest. Quotas are written first, so a released database
// already has room. Mode shadow logs what it would change; a failure is the
// subject's failure, and the next sweep tries again.
func (e *CapEnforcer) holdDatabases(ctx context.Context, subjectID string, databases []Database, all string, reasons map[string]string, summary *CapSweepSummary) error {
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
		if err := e.withComputeHold(ctx, database, func(providerCtx context.Context, claimed Database) error {
			return e.holdDatabase(providerCtx, subjectID, claimed, provider, all, reasons, summary)
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// holdDatabase observes and changes one hold while its lifecycle lease is held.
func (e *CapEnforcer) holdDatabase(ctx context.Context, subjectID string, database Database, provider ComputeHoldProvider, all string, reasons map[string]string, summary *CapSweepSummary) error {
	reason := all
	if own, ok := reasons[database.ID]; ok && reason == capHoldNone {
		reason = own
	}
	hold := reason != capHoldNone
	resourceID := databaseDataResource(database)
	held, err := provider.ComputeHeld(ctx, resourceID)
	if err != nil {
		return err
	}
	if held {
		summary.HeldDatabases++
	}
	if held == hold {
		return nil
	}
	attrs := []any{"subject_id", subjectID, "database_id", database.ID, "hold", hold, "reason", reason}
	if e.mode == CapEnforcementShadow {
		summary.HoldsShadowed++
		e.logger.Info("managed postgres compute hold (shadow)", attrs...)
		return nil
	}
	if hold {
		err = provider.HoldForDeletion(ctx, resourceID)
	} else {
		err = provider.ReleaseDeletionHold(ctx, resourceID)
	}
	if err != nil {
		return err
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
	return nil
}
