package managedpostgres

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCutoverSourceSessionsNeverPromotesIncompleteEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	base := CutoverSourceFence{State: "closed", ObservedAt: now, Observation: &sourceFenceObservation{Drained: true,
		Databases: []CheckpointConnectionDatabase{{Name: "private", OID: 1, OwnerOID: 2}}}}
	for _, tc := range []struct {
		name, blocker string
		change        func(*CutoverSourceFence)
	}{
		{"expired", "source_fence_observation_expired", func(f *CutoverSourceFence) { f.ObservedAt = now.Add(-CutoverVerificationMaxAge) }},
		{"future", "source_fence_observation_expired", func(f *CutoverSourceFence) { f.ObservedAt = now.Add(time.Second) }},
		{"failed_refresh", "source_fence_observation_unavailable", func(f *CutoverSourceFence) { f.LastErrorCode = "source_fence_observation_failed" }},
		{"abandoning", "source_fence_inactive", func(f *CutoverSourceFence) { f.State = "abandoning" }},
		{"pending", "maintenance_not_ready", func(f *CutoverSourceFence) { f.State = "pending" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.change(&f)
			view := sourceSessionsView(f, now)
			if view.AdmissionClosed || view.SelectedDatabasesDrained || !slices.Contains(view.Blockers, tc.blocker) {
				t.Fatal(view)
			}
		})
	}
	view := sourceSessionsView(base, now)
	if !view.AdmissionClosed || !view.SelectedDatabasesDrained || !slices.Contains(view.Blockers, "background_writers_unproven") || !slices.Contains(view.Blockers, "catalog_stability_unproven") {
		t.Fatal(view)
	}
}

func TestCutoverSourceFenceRejectsReplacedClosureAndCountOverflow(t *testing.T) {
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	f := CutoverSourceFence{OwnerToken: uuid.NewString(), Source: Database{DataResourceID: "dataset"}, DatabaseNames: []string{"a", "b"},
		Observation: &sourceFenceObservation{ClosedAt: now, Drained: true, Databases: []CheckpointConnectionDatabase{
			{Name: "a", OID: 1, OwnerOID: 10}, {Name: "b", OID: 2, OwnerOID: 10}}}}
	if err := validateSourceFenceObservation(f); err != nil {
		t.Fatal(err)
	}
	changed := *f.Observation
	changed.Databases = slices.Clone(changed.Databases)
	changed.Databases[0].OID++
	if sameSourceFenceClosurePins(f.Observation, &changed) {
		t.Fatal("changed database incarnation accepted")
	}
	f.Observation.Drained = false
	f.Observation.Databases[0].Sessions = math.MaxInt64
	f.Observation.Databases[1].Sessions = 1
	if validateSourceFenceObservation(f) == nil {
		t.Fatal("session count overflow accepted")
	}
}
