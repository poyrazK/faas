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
// A database is deleted only on the date its queued suspension email named:
// the mail outbox stores that date when it links the email to the notice, so
// an owner who was never mailed a date never loses a database. While
// deletion is on, a notice mailed without a date (deletion was off then) or
// never mailed inside its window starts again, so its email names one.

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

// PlanExcessClock is a suspended database's deletion clock.
type PlanExcessClock struct {
	// RecordedAt is when the sweep first suspended it past its plan.
	RecordedAt time.Time
	// DeleteAt is the deletion date its queued email named; zero until an
	// email named one.
	DeleteAt time.Time
}

// PlanExcessSinceStore is the optional store capability the deletion pass
// reads and writes.
type PlanExcessSinceStore interface {
	// PlanExcessSince reports the clock of each of the given databases; a
	// database with no notice is absent from the map.
	PlanExcessSince(ctx context.Context, databaseIDs []string) (map[string]PlanExcessClock, error)
	// RearmPlanExcessNotices restarts at now the notice of each of the given
	// databases whose email named no deletion date: mailed while deletion
	// was off, or recorded at or before staleBefore and never mailed. The
	// next pass mails it again with a date. It reports how many it restarted.
	RearmPlanExcessNotices(ctx context.Context, databaseIDs []string, now, staleBefore time.Time) (int, error)
}

// PlanExcessDeleteFunc schedules a database's delete with the grace period.
// Pending reports bindings still being revoked: the next sweep retries.
type PlanExcessDeleteFunc func(ctx context.Context, database Database) (pending bool, err error)

// PlanExcessDeleteDue reports whether a database suspended past its plan
// since `since` is due for deletion at now. The shadow pass logs by it.
func PlanExcessDeleteDue(since, now time.Time) bool {
	return !since.IsZero() && !now.Before(since.Add(api.ManagedPostgresPlanExcessDeleteAfter))
}

// Due reports whether the database is deleted at now: only once an email
// named its deletion date and that date has come.
func (c PlanExcessClock) Due(now time.Time) bool {
	return !c.DeleteAt.IsZero() && !now.Before(c.DeleteAt)
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
	deleting := e.planExcessDelete == CapEnforcementOn && e.planDelete != nil
	if deleting {
		rearmed, err := store.RearmPlanExcessNotices(ctx, ids, now, now.Add(-api.InternalMailManagedPostgresPlanExcessTTL))
		summary.PlanExcessDeleteRearmed += rearmed
		if err != nil {
			summary.PlanExcessDeleteFailed++
			e.logger.Warn("managed postgres plan excess notices not restarted", "subject_id", subjectID, "error", err)
			return
		}
	}
	clocks, err := store.PlanExcessSince(ctx, ids)
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
		clock := clocks[id]
		due := PlanExcessDeleteDue(clock.RecordedAt, now)
		if deleting {
			due = clock.Due(now)
		}
		if !ok || database.State != StateReady || !due {
			continue
		}
		summary.PlanExcessDeleteDue++
		attrs := []any{"subject_id", subjectID, "database", id, "reason", suspended[id], "suspended_since", clock.RecordedAt}
		if !deleting {
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

// PlanExcessSince implements PlanExcessSinceStore.
func (s *PostgresStore) PlanExcessSince(ctx context.Context, databaseIDs []string) (map[string]PlanExcessClock, error) {
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
	clocks := make(map[string]PlanExcessClock, len(rows))
	for _, row := range rows {
		clock := PlanExcessClock{RecordedAt: row.RecordedAt.Time.UTC()}
		if row.DeleteAt.Valid {
			clock.DeleteAt = row.DeleteAt.Time.UTC()
		}
		clocks[databaseUUID(row.DatabaseID)] = clock
	}
	return clocks, nil
}

// RearmPlanExcessNotices implements PlanExcessSinceStore.
func (s *PostgresStore) RearmPlanExcessNotices(ctx context.Context, databaseIDs []string, now, staleBefore time.Time) (int, error) {
	if now.IsZero() || staleBefore.IsZero() {
		return 0, ErrInvalid
	}
	ids := make([]pgtype.UUID, 0, len(databaseIDs))
	for _, id := range databaseIDs {
		uid, err := postgresUUID(id)
		if err != nil {
			return 0, ErrInvalid
		}
		ids = append(ids, uid)
	}
	n, err := new(sqlc.Queries).RearmManagedPostgresPlanExcessNotices(ctx, s.catalog, sqlc.RearmManagedPostgresPlanExcessNoticesParams{
		Now: databaseNullableTime(now), DatabaseIds: ids, StaleBefore: databaseNullableTime(staleBefore)})
	if err != nil {
		return 0, mapPostgresError(err)
	}
	return int(n), nil
}

// PlanExcessSince implements PlanExcessSinceStore.
func (s *MemoryStore) PlanExcessSince(_ context.Context, databaseIDs []string) (map[string]PlanExcessClock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clocks := make(map[string]PlanExcessClock, len(databaseIDs))
	for _, id := range databaseIDs {
		if n, ok := s.planExcessNotices[id]; ok {
			clocks[id] = PlanExcessClock{RecordedAt: n.RecordedAt, DeleteAt: n.deleteAt}
		}
	}
	return clocks, nil
}

// RearmPlanExcessNotices implements PlanExcessSinceStore.
func (s *MemoryStore) RearmPlanExcessNotices(_ context.Context, databaseIDs []string, now, staleBefore time.Time) (int, error) {
	if now.IsZero() || staleBefore.IsZero() {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rearmed := 0
	for _, id := range databaseIDs {
		n, ok := s.planExcessNotices[id]
		if !ok || !n.deleteAt.IsZero() || (!n.mailed && n.RecordedAt.After(staleBefore)) {
			continue
		}
		n.RecordedAt, n.mailed = now.UTC(), false
		s.planExcessNotices[id] = n
		rearmed++
	}
	return rearmed, nil
}
