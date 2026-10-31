package managedpostgres

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestConnectionBudgetRuntimeCoverageSeparatesResidentAndWakeEvidence(t *testing.T) {
	s := budgetSnapshot()
	b := &s.Bindings[0]
	b.ObservedResidents, b.ObservedConnections, b.ObservedMaxConnections = 1, 2, 2
	b.ObservationsCheckedAt = s.CheckedAt.Add(-time.Second)
	b.ObservationsExpiresAt = s.CheckedAt.Add(10 * time.Second)
	got := describeConnectionBudget(s, s.CheckedAt)
	r := got.Demand.RuntimeObservations
	if r.State != "partial" || r.ObservedInstances != 1 || r.ConfiguredConnections != 2 || r.UnobservedInstances != 1 || r.ParkedInstances != 1 || got.Demand.DataAPIResidentEstimate != 4 || got.Demand.DataAPIWakeEstimate != 6 || got.ExpiresAt != b.ObservationsExpiresAt.Format(time.RFC3339Nano) {
		t.Fatal(r, got)
	}
	// Capacity must not extend the shorter runtime evidence lifetime.
	if got.Capacity.ExpiresAt == got.ExpiresAt {
		t.Fatal("runtime expiry was replaced by later capacity expiry")
	}
	b.ObservedResidents, b.ObservedConnections, b.Parked = 2, 4, 0
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.RuntimeObservations.State != "observed" || got.Demand.DataAPIWakeEstimate != 6 {
		t.Fatal("resident coverage incorrectly rewrote future wake estimate", got)
	}
	for _, change := range []func(*connectionBudgetBinding){
		func(b *connectionBudgetBinding) { b.ObservedResidents = 3 },
		func(b *connectionBudgetBinding) { b.ObservedConnections = 999 },
		func(b *connectionBudgetBinding) { b.ObservedMaxConnections = 0 },
		func(b *connectionBudgetBinding) { b.ObservedMaxConnections = 9 },
		func(b *connectionBudgetBinding) { b.ObservationsExpiresAt = s.CheckedAt },
		func(b *connectionBudgetBinding) { b.ObservationsCheckedAt = s.CheckedAt.Add(time.Second) },
	} {
		copy := s
		copy.Bindings = append([]connectionBudgetBinding(nil), s.Bindings...)
		change(&copy.Bindings[0])
		got = describeConnectionBudget(copy, s.CheckedAt)
		if r := got.Demand.RuntimeObservations; r.ObservedInstances != 0 || r.UnobservedInstances != 2 || r.State != "unknown" || got.Demand.DataAPIResidentEstimate != 4 {
			t.Fatal("invalid evidence replaced estimates", r, got)
		}
	}
}

// adr: 20261021180534 — a configured engine pool reaches the budget through
// observed reports, and unobserved or planned instances of the binding are
// estimated at the largest valid pool observed, never below the default, or
// at the largest settable pool when DATA_API_POOL is set and nothing valid is
// observed.
func TestConnectionBudgetEstimatesFromTheLargestObservedPool(t *testing.T) {
	s := budgetSnapshot()
	b := &s.Bindings[0]
	b.Parked = 0
	b.ObservedResidents, b.ObservedConnections, b.ObservedMaxConnections = 1, 6, 6
	b.ObservationsCheckedAt = s.CheckedAt.Add(-time.Second)
	b.ObservationsExpiresAt = s.CheckedAt.Add(10 * time.Second)
	got := describeConnectionBudget(s, s.CheckedAt)
	r := got.Demand.RuntimeObservations
	if r.ConfiguredConnections != 6 || r.UnobservedInstances != 1 || got.Demand.DataAPIResidentEstimate != 12 || got.Demand.DataAPIWakeEstimate != 3*6 {
		t.Fatal("a six-connection pool was not carried into the estimates", r, got)
	}
	b.ObservedConnections, b.ObservedMaxConnections = 1, 1
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.RuntimeObservations.ConfiguredConnections != 1 || got.Demand.DataAPIResidentEstimate != 1+2 || got.Demand.DataAPIWakeEstimate != 3*2 {
		t.Fatal("a one-connection pool lowered unobserved estimates below the default", got)
	}
	b.ObservedConnections, b.ObservedMaxConnections = 12, 6
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.RuntimeObservations.ObservedInstances != 0 || got.Demand.DataAPIResidentEstimate != 2*2 || got.Demand.DataAPIWakeEstimate != 3*2 {
		t.Fatal("rejected reports still set the estimate", got)
	}
	b.PoolConfigured = true
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.DataAPIResidentEstimate != 2*api.DataAPIMaxPoolConnections || got.Demand.DataAPIWakeEstimate != 3*api.DataAPIMaxPoolConnections {
		t.Fatal("a configured pool without valid reports was assumed to be the default", got)
	}
	b.ObservedResidents, b.ObservedConnections, b.ObservedMaxConnections = 0, 0, 0
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.DataAPIResidentEstimate != 2*api.DataAPIMaxPoolConnections || got.Demand.DataAPIWakeEstimate != 3*api.DataAPIMaxPoolConnections {
		t.Fatal("a parked binding with a configured pool was estimated at the default", got)
	}
	b.ObservedResidents, b.ObservedConnections, b.ObservedMaxConnections = 1, 6, 6
	got = describeConnectionBudget(s, s.CheckedAt)
	if got.Demand.DataAPIResidentEstimate != 6+6 || got.Demand.DataAPIWakeEstimate != 3*6 {
		t.Fatal("valid reports did not take precedence over the configured-pool bound", got)
	}
}
