// adr: 20261110160000 — amendment 2026-11-10: the API reports why a ready
// database is held, from the same rules the cap sweep applies.
package managedpostgres

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSubjectHoldFollowsTheSweepsOrder(t *testing.T) {
	on := HoldInputs{Mode: CapEnforcementOn, ComputeCeiling: 100, EgressCeiling: 1000}
	tests := map[string]struct {
		in   HoldInputs
		want string
	}{
		"nothing used":         {in: on, want: ""},
		"shadow holds nothing": {in: HoldInputs{Mode: CapEnforcementShadow, Inactive: true, ComputeUsed: 100, ComputeCeiling: 100}, want: ""},
		"inactive first":       {in: HoldInputs{Mode: CapEnforcementOn, Inactive: true, ComputeUsed: 100, ComputeCeiling: 100}, want: api.ManagedPostgresHoldInactive},
		"compute ceiling":      {in: HoldInputs{Mode: CapEnforcementOn, ComputeUsed: 100, ComputeCeiling: 100, DatabaseEgress: 1000, EgressCeiling: 1000}, want: api.ManagedPostgresHoldComputeCeiling},
		"egress shared":        {in: HoldInputs{Mode: CapEnforcementOn, ComputeCeiling: 100, DatabaseEgress: 400, AppEgress: 600, EgressCeiling: 1000}, want: api.ManagedPostgresHoldEgress},
		"egress just under":    {in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: 400, AppEgress: 599, EgressCeiling: 1000}, want: ""},
		"no egress allowance":  {in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: math.MaxInt64}, want: ""},
		"egress overflow":      {in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: math.MaxInt64, AppEgress: 1, EgressCeiling: 1000}, want: api.ManagedPostgresHoldEgress},
	}
	for name, tc := range tests {
		if got := SubjectHold(tc.in); got != tc.want {
			t.Errorf("%s: SubjectHold = %q, want %q", name, got, tc.want)
		}
	}
}

func TestDatabaseHoldNamesTheDatabasesOwnReason(t *testing.T) {
	ready := Database{State: StateReady}
	preview := Database{State: StateReady, Preview: PreviewOwner{SourceDatabaseID: "source", EnvironmentID: "env", PRNumber: 7}}
	on := HoldInputs{Mode: CapEnforcementOn, ComputeCeiling: 100}
	if got := DatabaseHold(ready, false, on); got != "" {
		t.Fatalf("ready database under every limit = %q", got)
	}
	if got := DatabaseHold(ready, true, on); got != api.ManagedPostgresHoldPlanExcess {
		t.Fatalf("past the plan = %q", got)
	}
	at80 := on
	at80.ComputeUsed = 80
	if got := DatabaseHold(preview, false, at80); got != api.ManagedPostgresHoldPreview {
		t.Fatalf("preview at 80%% = %q", got)
	}
	if got := DatabaseHold(ready, false, at80); got != "" {
		t.Fatalf("production at 80%% = %q, want running", got)
	}
	egress := HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: 10, AppEgress: 10, EgressCeiling: 20}
	if got := DatabaseHold(ready, true, egress); got != api.ManagedPostgresHoldEgress {
		t.Fatalf("subject hold outranks plan excess: %q", got)
	}
	// A database being created, changed or deleted is its lifecycle's, as
	// the sweep leaves it.
	for _, state := range []State{StateProvisioning, StateDeleting, StateDeleted} {
		if got := DatabaseHold(Database{State: state}, true, egress); got != "" {
			t.Fatalf("%s database = %q, want no hold", state, got)
		}
	}
}

func TestEgressState(t *testing.T) {
	in := HoldInputs{Mode: CapEnforcementOn, EgressCeiling: 100}
	for _, tc := range []struct {
		database, app int64
		mode          CapEnforcementMode
		want          string
	}{
		{0, 0, CapEnforcementOn, api.ManagedPostgresEgressHealthy},
		{40, 39, CapEnforcementOn, api.ManagedPostgresEgressHealthy},
		{40, 40, CapEnforcementOn, api.ManagedPostgresEgressWarning},
		{50, 50, CapEnforcementOn, api.ManagedPostgresEgressHeld},
		{50, 50, CapEnforcementShadow, api.ManagedPostgresEgressWarning},
	} {
		in.DatabaseEgress, in.AppEgress, in.Mode = tc.database, tc.app, tc.mode
		if got := EgressState(in); got != tc.want {
			t.Errorf("EgressState(db %d app %d %s) = %q, want %q", tc.database, tc.app, tc.mode, got, tc.want)
		}
	}
	if got := EgressState(HoldInputs{Mode: CapEnforcementOn}); got != "" {
		t.Fatalf("no allowance = %q, want omitted", got)
	}
}
