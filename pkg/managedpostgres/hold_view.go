package managedpostgres

import (
	"math"

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
	// ComputeUsed and ComputeCeiling are the subject's month-to-date compute
	// and the ceiling its running databases stop at (zero: none).
	ComputeUsed, ComputeCeiling int64
	// DatabaseEgress, AppEgress and EgressCeiling are the subject's egress
	// this month and its plan's shared allowance (zero: none).
	DatabaseEgress, AppEgress, EgressCeiling int64
}

// SubjectHold is the reason every database of the subject is held, or "":
// inactive, then the compute ceiling, then egress, as the sweep orders them.
func SubjectHold(in HoldInputs) string {
	if in.Mode != CapEnforcementOn {
		return ""
	}
	switch {
	case in.Inactive:
		return api.ManagedPostgresHoldInactive
	case in.ComputeCeiling > 0 && in.ComputeUsed >= in.ComputeCeiling:
		return api.ManagedPostgresHoldComputeCeiling
	case egressAllowanceReached(in.DatabaseEgress, in.AppEgress, in.EgressCeiling):
		return api.ManagedPostgresHoldEgress
	default:
		return ""
	}
}

// DatabaseHold is the reason one database is held, or "": its subject's, then
// past its plan (planExcess, as PlanExcess reports it), then a PR preview
// database once the subject used 80% of its compute ceiling.
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
	default:
		return ""
	}
}

// EgressState is the usage view's egress_state: held only while caps are
// enforced, warning at 80% of the allowance, "" without an allowance.
func EgressState(in HoldInputs) string {
	if in.EgressCeiling <= 0 {
		return ""
	}
	switch {
	case in.Mode == CapEnforcementOn && egressAllowanceReached(in.DatabaseEgress, in.AppEgress, in.EgressCeiling):
		return api.ManagedPostgresEgressHeld
	case egressAllowanceReached(in.DatabaseEgress, in.AppEgress, in.EgressCeiling-in.EgressCeiling/5):
		return api.ManagedPostgresEgressWarning
	default:
		return api.ManagedPostgresEgressHealthy
	}
}

// egressAllowanceReached reports whether databases and apps together sent the
// allowance; a ceiling of zero or less is no allowance.
func egressAllowanceReached(databaseEgress, appEgress, ceiling int64) bool {
	if ceiling <= 0 {
		return false
	}
	databaseEgress, appEgress = max(databaseEgress, 0), max(appEgress, 0)
	if databaseEgress > math.MaxInt64-appEgress {
		return true
	}
	return databaseEgress+appEgress >= ceiling
}
