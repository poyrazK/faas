package managedpostgres

import (
	"testing"
	"time"
)

func TestConnectionBudgetRuntimeCoverageSeparatesResidentAndWakeEvidence(t *testing.T) {
	s := budgetSnapshot()
	b := &s.Bindings[0]
	b.ObservedResidents, b.ObservedConnections = 1, 2
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
