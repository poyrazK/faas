package managedpostgres

import (
	"fmt"
	"slices"
	"strings"

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
// one without a provider resource are not ranked. An unknown plan (no
// database limit) has nothing past it.
func PlanExcess(databases []Database, limits api.ManagedPostgresPlanLimits) map[string]string {
	if limits.DatabasesMax <= 0 {
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

// suspendPlanExcess holds each quota scope whose databases are all past the
// subject's plan at its own use, so the provider suspends it. A scope that
// also holds a database inside the plan keeps running. In shadow, or while
// the compute cap is not on, it only logs.
func (e *CapEnforcer) suspendPlanExcess(subjectID string, databases []Database, limits CapLimits, plan *computeCapPlan, summary *CapSweepSummary) {
	if e.planExcess == CapEnforcementOff {
		return
	}
	excess := PlanExcess(databases, limits.Entitlement)
	if len(excess) == 0 {
		return
	}
	summary.PlanExcessDatabases += len(excess)
	enforce := e.planExcess == CapEnforcementOn && e.mode == CapEnforcementOn
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
			decision.target = max(decision.used, 1)
			decision.apply = capQuotaNeedsWrite(decision.current, decision.target, decision.used)
			if decision.apply {
				e.logger.Info("managed postgres plan excess suspended", attrs...)
			}
		}
	}
}
