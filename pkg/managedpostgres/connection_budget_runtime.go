package managedpostgres

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// dataAPIInstanceConnections estimates one unobserved or planned instance of
// a binding (ADR-20261021180534): the largest pool an instance of it reports,
// or the default pool when none does.
func dataAPIInstanceConnections(b connectionBudgetBinding) int64 {
	if b.ObservedResidents <= 0 || b.ObservedMaxConnections < api.DataAPIDefaultPoolConnections || b.ObservedMaxConnections > api.DataAPIMaxPoolConnections {
		return api.DataAPIDefaultPoolConnections
	}
	return b.ObservedMaxConnections
}

func accountRuntimeConnectionCoverage(out *api.ManagedPostgresConnectionBudget, b connectionBudgetBinding) {
	r := out.Demand.RuntimeObservations
	r.State = "unknown"
	observed, connections := b.ObservedResidents, b.ObservedConnections
	if observed < 0 || observed > b.Residents || connections < observed || connections > observed*api.DataAPIMaxPoolConnections ||
		(observed > 0 && (b.ObservedMaxConnections < 1 || b.ObservedMaxConnections > api.DataAPIMaxPoolConnections || connections > observed*b.ObservedMaxConnections)) ||
		(observed > 0 && (b.ObservationsCheckedAt.IsZero() || !b.ObservationsExpiresAt.After(b.ObservationsCheckedAt))) {
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
