package managedpostgres

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// validRuntimeObservations reports whether a binding's runtime reports can
// count: no more observed than resident instances, 1 to
// DataAPIMaxPoolConnections each and within the largest pool reported, and a
// sound observation window.
func validRuntimeObservations(b connectionBudgetBinding) bool {
	observed, connections := b.ObservedResidents, b.ObservedConnections
	return observed >= 0 && observed <= b.Residents && connections >= observed && connections <= observed*api.DataAPIMaxPoolConnections &&
		(observed == 0 || (b.ObservedMaxConnections >= 1 && b.ObservedMaxConnections <= api.DataAPIMaxPoolConnections && connections <= observed*b.ObservedMaxConnections &&
			!b.ObservationsCheckedAt.IsZero() && b.ObservationsExpiresAt.After(b.ObservationsCheckedAt)))
}

// dataAPIInstanceConnections estimates one unobserved or planned instance of
// a binding (ADR-20261021180534): the largest pool its valid reports show,
// never below the default. Without one, a binding whose app sets
// DATA_API_POOL counts at the largest pool an owner can set: the value is
// sealed, and a larger pool is never assumed to be the default without
// evidence.
func dataAPIInstanceConnections(b connectionBudgetBinding) int64 {
	switch {
	case b.ObservedResidents > 0 && validRuntimeObservations(b):
		return max(b.ObservedMaxConnections, api.DataAPIDefaultPoolConnections)
	case b.PoolConfigured:
		return api.DataAPIMaxPoolConnections
	default:
		return api.DataAPIDefaultPoolConnections
	}
}

func accountRuntimeConnectionCoverage(out *api.ManagedPostgresConnectionBudget, b connectionBudgetBinding) {
	r := out.Demand.RuntimeObservations
	r.State = "unknown"
	observed, connections := b.ObservedResidents, b.ObservedConnections
	if !validRuntimeObservations(b) {
		observed, connections = 0, 0
	}
	r.ObservedInstances += observed
	r.UnobservedInstances += max(0, b.Residents-observed)
	r.ParkedInstances += max(0, b.Parked)
	r.ConfiguredConnections += connections
	out.Demand.DataAPIResidentEstimate += connections + max(0, b.Residents-observed)*dataAPIInstanceConnections(b)
	if observed > 0 {
		checked, _ := time.Parse(time.RFC3339Nano, r.CheckedAt)
		expires, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt)
		if checked.IsZero() || b.ObservationsCheckedAt.Before(checked) {
			r.CheckedAt = b.ObservationsCheckedAt.UTC().Format(time.RFC3339Nano)
		}
		if expires.IsZero() || b.ObservationsExpiresAt.Before(expires) {
			r.ExpiresAt = b.ObservationsExpiresAt.UTC().Format(time.RFC3339Nano)
		}
	}
}

func finalizeRuntimeConnectionCoverage(out *api.ManagedPostgresConnectionBudget, now time.Time) {
	r := out.Demand.RuntimeObservations
	if r.ObservedInstances == 0 {
		return
	}
	checked, first := time.Parse(time.RFC3339Nano, r.CheckedAt)
	expires, second := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	asOf, _ := time.Parse(time.RFC3339Nano, out.CheckedAt)
	if first != nil || second != nil || checked.After(asOf) || now.Before(checked) || !now.Before(expires) || expires.Sub(checked) > api.DataAPIConnectionBudgetMaxAge {
		r.UnobservedInstances += r.ObservedInstances
		r.ObservedInstances, r.ConfiguredConnections = 0, 0
		r.CheckedAt, r.ExpiresAt = "", ""
		return
	}
	r.State = "observed"
	if r.UnobservedInstances > 0 || r.ParkedInstances > 0 {
		r.State = "partial"
	}
	reportExpires, _ := time.Parse(time.RFC3339Nano, out.ExpiresAt)
	if expires.Before(reportExpires) {
		out.ExpiresAt = r.ExpiresAt
	}
}
