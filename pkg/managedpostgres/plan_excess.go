package managedpostgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Databases past a lowered plan (ADR-20261102125208). A plan can drop without
// the pre-downgrade check: a subscription ends, or the plan changes in the
// billing portal. The compute cap sweep suspends the databases the plan no
// longer includes and lifts them once the subject fits again. Nothing is
// deleted.

// PlanExcessEnforcementEnv selects off, shadow (the default) or on. Shadow
// logs each database the sweep would suspend; on suspends it, and only while
// the compute cap is on too.
const PlanExcessEnforcementEnv = "FAAS_MANAGED_POSTGRES_PLAN_EXCESS_ENFORCEMENT"

// ParsePlanExcessEnforcementMode reads PlanExcessEnforcementEnv; empty means
// shadow.
func ParsePlanExcessEnforcementMode(value string) (CapEnforcementMode, error) {
	switch mode := CapEnforcementMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return CapEnforcementShadow, nil
	case CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want off, shadow or on: %w", PlanExcessEnforcementEnv, value, ErrInvalid)
	}
}

// Why a database is past its subject's plan.
const (
	PlanExcessDatabaseLimit = "database_limit"
	PlanExcessServiceClass  = "service_class"
)

// PlanAllowsClass reports whether the plan includes the service class.
func PlanAllowsClass(limits api.ManagedPostgresPlanLimits, class ServiceClass) bool {
	switch class {
	case ClassDevelopment:
		return limits.DevelopmentAllowed
	case ClassBurstable:
		return limits.BurstableAllowed
	case ClassProduction:
		return limits.ProductionAllowed
	default:
		return false
	}
}

// PlanExcess maps each of a subject's databases that its plan no longer
// includes to the reason. Databases rank oldest first; one ranked past the
// plan's database limit, or in a service class the plan does not include, is
// past the plan. A restore holding the restore slot, a deleting database and
// one without a provider resource are not ranked. A plan that includes no
// managed PostgreSQL (NotIncluded) has every ranked database past it; an
// unknown plan (no database limit) has nothing past it.
func PlanExcess(databases []Database, limits api.ManagedPostgresPlanLimits) map[string]string {
	if limits.DatabasesMax <= 0 && !limits.NotIncluded {
		return nil
	}
	ranked := make([]Database, 0, len(databases))
	for _, database := range databases {
		if !database.RestoreSlotUntil.IsZero() || !capEnforceable(database) {
			continue
		}
		ranked = append(ranked, database)
	}
	slices.SortFunc(ranked, func(a, b Database) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	excess := make(map[string]string)
	for i, database := range ranked {
		switch {
		case i >= limits.DatabasesMax:
			excess[database.ID] = PlanExcessDatabaseLimit
		case !PlanAllowsClass(limits, database.Spec.Class):
			excess[database.ID] = PlanExcessServiceClass
		}
	}
	return excess
}

// planExcessEnforced reports whether the sweep suspends databases past their
// plan, rather than only logging them.
func (e *CapEnforcer) planExcessEnforced() bool {
	return e.planExcess == CapEnforcementOn && e.mode == CapEnforcementOn
}

// suspendPlanExcess holds each quota scope whose databases are all past the
// subject's plan at its own use, so the provider suspends it. A scope that
// also holds a database inside the plan keeps running. In shadow, or while
// the compute cap is not on, it only logs. It returns the databases it
// suspends, by reason.
func (e *CapEnforcer) suspendPlanExcess(subjectID string, databases []Database, limits CapLimits, plan *computeCapPlan, summary *CapSweepSummary) map[string]string {
	if e.planExcess == CapEnforcementOff {
		return nil
	}
	excess := PlanExcess(databases, limits.Entitlement)
	if len(excess) == 0 {
		return nil
	}
	summary.PlanExcessDatabases += len(excess)
	enforce := e.planExcessEnforced()
	suspended := make(map[string]string)
	for i := range plan.decisions {
		decision := &plan.decisions[i]
		var reasons []string
		for _, id := range decision.databaseIDs {
			if reason, ok := excess[id]; ok {
				reasons = append(reasons, reason)
			}
		}
		if len(reasons) == 0 {
			continue
		}
		attrs := []any{"subject_id", subjectID, "database_ids", decision.databaseIDs, "reasons", reasons,
			"used_unit_seconds", decision.used, "quota_unit_seconds", decision.current}
		switch {
		case len(reasons) < len(decision.databaseIDs):
			summary.PlanExcessScopesShared++
			e.logger.Warn("managed postgres plan excess left running: its scope holds a database inside the plan", attrs...)
		case !enforce:
			summary.PlanExcessScopesShadowed++
			e.logger.Info("managed postgres plan excess (shadow)", attrs...)
		default:
			summary.PlanExcessScopesSuspended++
			for _, id := range decision.databaseIDs {
				suspended[id] = excess[id]
			}
			decision.target = max(decision.used, 1)
			decision.apply = capQuotaNeedsWrite(decision.current, decision.target, decision.used)
			if decision.apply {
				e.logger.Info("managed postgres plan excess suspended", attrs...)
			}
		}
	}
	return suspended
}

// recordPlanExcessNotices records a notice for each database the sweep
// suspended and drops the notices of the subject's other databases. A
// failure is logged; the next sweep tries again.
func (e *CapEnforcer) recordPlanExcessNotices(ctx context.Context, databases []Database, suspended map[string]string, now time.Time, summary *CapSweepSummary) {
	store, ok := e.store.(PlanExcessNoticeStore)
	if !ok {
		return
	}
	ids := make([]string, len(databases))
	for i, database := range databases {
		ids[i] = database.ID
	}
	recorded, err := store.SyncPlanExcessNotices(ctx, ids, suspended, now)
	summary.PlanExcessNoticed += recorded
	if err != nil {
		e.logger.Warn("managed postgres plan excess notices not recorded", "database_ids", ids, "error", err)
	}
}

// mailPlanExcessNotices queues the email for each notice still unmailed and
// under a day old. A failed email does not fail the sweep.
func (e *CapEnforcer) mailPlanExcessNotices(ctx context.Context, summary *CapSweepSummary) {
	store, ok := e.store.(PlanExcessNoticeStore)
	if !ok || e.planNotify == nil || !e.planExcessEnforced() {
		return
	}
	notices, err := store.UnmailedPlanExcessNotices(ctx, e.now().UTC().Add(-api.InternalMailManagedPostgresPlanExcessTTL), e.batchSize)
	if err != nil {
		summary.PlanExcessNotifyFailed++
		e.logger.Warn("managed postgres plan excess notices not listed", "error", err)
		return
	}
	for _, notice := range notices {
		if err := e.planNotify(ctx, notice); err != nil {
			summary.PlanExcessNotifyFailed++
			e.logger.Warn("managed postgres plan excess email not queued", "database", notice.DatabaseID, "error", err)
		}
	}
}
