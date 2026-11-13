package managedpostgres

import (
	"github.com/onebox-faas/faas/pkg/api"
)

// HoldInputs is what the cap sweep decides a hold from, as the API reads it
// (ADR-20261110160000 amendment 2026-11-10): customers see why a ready
// database refuses connections. The sweep applies the same rules within one
// sweep interval, so a view can lead or trail the provider by that much.
type HoldInputs struct {
	// Mode is the compute cap enforcement mode; only on holds anything.
	Mode     CapEnforcementMode
	Inactive bool
	// SpendCapReached holds every database at the compute ceiling while the
	// subject's month is at its spend cap (CapLimits.SpendCapReached).
	SpendCapReached bool
	// ComputeUsed and ComputeCeiling are the subject's month-to-date compute
	// and the ceiling its running databases stop at (zero: none).
	ComputeUsed, ComputeCeiling int64
	// DatabaseEgress is each database's egress this month, by database ID.
	// EgressAllowance is each database's own monthly egress allowance (zero:
	// none), as Neon's is per project (ADR-20261110160000 amendment
	// 2026-11-11).
	DatabaseEgress  map[string]int64
	EgressAllowance int64
	// EgressOverageBilled is set while the subject's egress overage election
	// lifts the allowance and database egress is billed live: a database is
	// then held at twice its allowance (EgressHoldCeiling).
	EgressOverageBilled bool
}

// egressHoldCeiling is the egress one database is held at.
func (in HoldInputs) egressHoldCeiling() int64 {
	return EgressHoldCeiling(in.EgressAllowance, in.EgressOverageBilled)
}

// MostDatabaseEgress is the most egress any one database sent this month.
func (in HoldInputs) MostDatabaseEgress() int64 {
	var most int64
	for _, egress := range in.DatabaseEgress {
		most = max(most, egress)
	}
	return most
}

// SubjectHold is the reason every database of the subject is held, or "":
// inactive, then the compute ceiling, as the sweep orders them. Egress holds
// one database at a time.
func SubjectHold(in HoldInputs) string {
	if in.Mode != CapEnforcementOn {
		return ""
	}
	switch {
	case in.Inactive:
		return api.ManagedPostgresHoldInactive
	case in.SpendCapReached, in.ComputeCeiling > 0 && in.ComputeUsed >= in.ComputeCeiling:
		return api.ManagedPostgresHoldComputeCeiling
	default:
		return ""
	}
}

// DatabaseHold is the reason one database is held, or "": its subject's, then
// past its plan (planExcess, as PlanExcess reports it), then a PR preview
// database once the subject used 80% of its compute ceiling, then its own
// egress past its allowance.
func DatabaseHold(database Database, planExcess bool, in HoldInputs) string {
	if in.Mode != CapEnforcementOn || !capHoldable(database) {
		return ""
	}
	if reason := SubjectHold(in); reason != "" {
		return reason
	}
	switch {
	case planExcess:
		return api.ManagedPostgresHoldPlanExcess
	case !database.Preview.IsZero() && in.ComputeCeiling > 0 && in.ComputeUsed >= previewComputeCeiling(in.ComputeCeiling):
		return api.ManagedPostgresHoldPreview
	case egressAllowanceReached(in.DatabaseEgress[database.ID], in.egressHoldCeiling()):
		return api.ManagedPostgresHoldEgress
	default:
		return ""
	}
}

// EgressState is the usage view's egress_state for the database that sent
// the most: held only while caps are enforced, overage past the allowance
// while database egress is billed, warning at 80% of the hold, "" without an
// allowance.
func EgressState(in HoldInputs) string {
	if in.EgressAllowance <= 0 {
		return ""
	}
	hold, most := in.egressHoldCeiling(), in.MostDatabaseEgress()
	switch {
	case in.Mode == CapEnforcementOn && egressAllowanceReached(most, hold):
		return api.ManagedPostgresEgressHeld
	case in.EgressOverageBilled && egressAllowanceReached(most, in.EgressAllowance):
		return api.ManagedPostgresEgressOverage
	case egressAllowanceReached(most, hold-hold/5):
		return api.ManagedPostgresEgressWarning
	default:
		return api.ManagedPostgresEgressHealthy
	}
}

// egressAllowanceReached reports whether a database sent the allowance; a
// ceiling of zero or less is no allowance.
func egressAllowanceReached(egress, ceiling int64) bool {
	return ceiling > 0 && egress >= ceiling
}
