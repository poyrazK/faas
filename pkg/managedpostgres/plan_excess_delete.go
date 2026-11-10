package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Deleting databases past their plan (ADR-20261110184700 slice 2). A database
// the sweep has kept suspended past its subject's plan for
// api.ManagedPostgresPlanExcessDeleteAfter is deleted through the delete
// grace period: its bindings are revoked, its compute stays held, and the
// owner can undo the delete for api.ManagedPostgresDeleteGracePeriod more.
// The clock is the database's plan excess notice, which the sweep records at
// the first suspension and clears once the database fits the plan again.

// PlanExcessDeleteEnv selects the deletion pass: off, shadow (the default:
// log the databases due) or on. On deletes only while the plan excess sweep
// itself is on.
const PlanExcessDeleteEnv = "FAAS_MANAGED_POSTGRES_PLAN_EXCESS_DELETE"

// ParsePlanExcessDeleteMode reads PlanExcessDeleteEnv; empty means shadow.
func ParsePlanExcessDeleteMode(value string) (CapEnforcementMode, error) {
	switch mode := CapEnforcementMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return CapEnforcementShadow, nil
	case CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want off, shadow or on: %w", PlanExcessDeleteEnv, value, ErrInvalid)
	}
}

// PlanExcessSinceStore is the optional store capability the deletion pass
// reads: when each of the given databases was first suspended past its plan.
// A database with no notice is absent from the map.
type PlanExcessSinceStore interface {
	PlanExcessSince(ctx context.Context, databaseIDs []string) (map[string]time.Time, error)
}

// PlanExcessDeleteFunc schedules a database's delete with the grace period.
// Pending reports bindings still being revoked: the next sweep retries.
type PlanExcessDeleteFunc func(ctx context.Context, database Database) (pending bool, err error)

// PlanExcessDeleteDue reports whether a database suspended past its plan
// since `since` is due for deletion at now.
func PlanExcessDeleteDue(since, now time.Time) bool {
	return !since.IsZero() && !now.Before(since.Add(api.ManagedPostgresPlanExcessDeleteAfter))
}

// PlanExcessDeleteAt is when a database suspended past its plan since
// `since` is deleted, if it still does not fit.
func PlanExcessDeleteAt(since time.Time) time.Time {
	return since.Add(api.ManagedPostgresPlanExcessDeleteAfter)
}

// deletePlanExcess deletes the subject's databases the sweep suspended past
// the plan once their clock has run out. It runs only after this sweep
// confirmed them past the plan, so an upgrade between sweeps never deletes a
// database that now fits. An inactive subject (suspended, abuse-held or
// pending deletion) is left to its own lifecycle.
func (e *CapEnforcer) deletePlanExcess(ctx context.Context, subjectID string, databases []Database, suspended map[string]string, inactive bool, now time.Time, summary *CapSweepSummary) {
	if len(suspended) == 0 || inactive || e.planExcessDelete == CapEnforcementOff {
		return
	}
	store, ok := e.store.(PlanExcessSinceStore)
	if !ok {
		return
	}
	ids := sortedKeys(suspended)
	since, err := store.PlanExcessSince(ctx, ids)
	if err != nil {
		summary.PlanExcessDeleteFailed++
		e.logger.Warn("managed postgres plan excess deletion clock not read", "subject_id", subjectID, "error", err)
		return
	}
	byID := make(map[string]Database, len(databases))
	for _, database := range databases {
		byID[database.ID] = database
	}
	for _, id := range ids {
		database, ok := byID[id]
		if !ok || database.State != StateReady || !PlanExcessDeleteDue(since[id], now) {
			continue
		}
		summary.PlanExcessDeleteDue++
		attrs := []any{"subject_id", subjectID, "database", id, "reason", suspended[id], "suspended_since", since[id]}
		if e.planExcessDelete != CapEnforcementOn || e.planDelete == nil {
			summary.PlanExcessDeleteShadowed++
			e.logger.Info("managed postgres plan excess due for deletion (shadow)", attrs...)
			continue
		}
		pending, err := e.planDelete(ctx, database)
		switch {
		case err != nil:
			summary.PlanExcessDeleteFailed++
			e.logger.Warn("managed postgres plan excess delete not scheduled", append(attrs, "error", err)...)
		case pending:
			summary.PlanExcessDeleteWaiting++
		default:
			summary.PlanExcessDeleteScheduled++
			e.logger.Info("managed postgres plan excess delete scheduled", attrs...)
		}
	}
}

// SetPlanExcessDelete attaches the function that schedules a database's
// delete. apid builds the enforcer before the services the function needs.
func (e *CapEnforcer) SetPlanExcessDelete(fn PlanExcessDeleteFunc) {
	if e != nil {
		e.planDelete = fn
	}
}

// RetirePastPlan revokes the database's bindings and then schedules its
// delete with the grace period (ADR-20261110184700). It reports pending while
// a binding is still being deleted, and treats a delete the grace period
// already holds as done.
func (s *Service) RetirePastPlan(ctx context.Context, bindings RestoreSlotBindings, database Database) (bool, error) {
	if s == nil || bindings == nil {
		return false, ErrInvalid
	}
	pending, err := revokeDatabaseBindings(ctx, bindings, database)
	if err != nil || pending {
		return true, err
	}
	if _, err := s.ScheduleDelete(ctx, database.AccountID, database.ID); err != nil {
		// A binding still being deleted holds the delete; the next sweep
		// retries.
		if errors.Is(err, ErrConflict) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// revokeDatabaseBindings deletes the database's live bindings. It reports
// pending while any binding is not yet gone.
func revokeDatabaseBindings(ctx context.Context, bindings RestoreSlotBindings, database Database) (bool, error) {
	items, err := bindings.List(ctx, database.AccountID, database.ID)
	if err != nil {
		return false, err
	}
	pending := false
	for _, binding := range items {
		switch binding.State {
		case BindingStateDeleted:
			continue
		case BindingStateDeleting:
			pending = true
			continue
		}
		if _, err := bindings.Delete(ctx, database.AccountID, binding.ID); err != nil {
			return true, err
		}
		pending = true
	}
	return pending, nil
}

// PlanExcessSince implements PlanExcessSinceStore.
func (s *PostgresStore) PlanExcessSince(ctx context.Context, databaseIDs []string) (map[string]time.Time, error) {
	ids := make([]pgtype.UUID, 0, len(databaseIDs))
	for _, id := range databaseIDs {
		uid, err := postgresUUID(id)
		if err != nil {
			return nil, ErrInvalid
		}
		ids = append(ids, uid)
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresPlanExcessSince(ctx, s.catalog, ids)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	since := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		since[databaseUUID(row.DatabaseID)] = row.RecordedAt.Time.UTC()
	}
	return since, nil
}

// PlanExcessSince implements PlanExcessSinceStore.
func (s *MemoryStore) PlanExcessSince(_ context.Context, databaseIDs []string) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := make(map[string]time.Time, len(databaseIDs))
	for _, id := range databaseIDs {
		if n, ok := s.planExcessNotices[id]; ok {
			since[id] = n.RecordedAt
		}
	}
	return since, nil
}
