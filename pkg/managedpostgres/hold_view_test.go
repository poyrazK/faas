// adr: 20261110160000 — amendment 2026-11-10: the API reports why a ready
// database is held, from the same rules the cap sweep applies; amendment
// 2026-11-11: each database's egress allowance is its own.
package managedpostgres

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSubjectHoldFollowsTheSweepsOrder(t *testing.T) {
	spent := map[string]int64{"db": 1000}
	tests := map[string]struct {
		in   HoldInputs
		want string
	}{
		"nothing used":         {in: HoldInputs{Mode: CapEnforcementOn, ComputeCeiling: 100, EgressAllowance: 1000}, want: ""},
		"shadow holds nothing": {in: HoldInputs{Mode: CapEnforcementShadow, Inactive: true, ComputeUsed: 100, ComputeCeiling: 100}, want: ""},
		"inactive first":       {in: HoldInputs{Mode: CapEnforcementOn, Inactive: true, ComputeUsed: 100, ComputeCeiling: 100}, want: api.ManagedPostgresHoldInactive},
		"compute ceiling":      {in: HoldInputs{Mode: CapEnforcementOn, ComputeUsed: 100, ComputeCeiling: 100, DatabaseEgress: spent, EgressAllowance: 1000}, want: api.ManagedPostgresHoldComputeCeiling},
		"spend cap reached":    {in: HoldInputs{Mode: CapEnforcementOn, SpendCapReached: true, ComputeCeiling: 100}, want: api.ManagedPostgresHoldComputeCeiling},
		"egress holds one database, not the subject": {in: HoldInputs{Mode: CapEnforcementOn, ComputeCeiling: 100, DatabaseEgress: spent, EgressAllowance: 1000}, want: ""},
	}
	for name, tc := range tests {
		if got := SubjectHold(tc.in); got != tc.want {
			t.Errorf("%s: SubjectHold = %q, want %q", name, got, tc.want)
		}
	}
}

func TestDatabaseHoldNamesTheDatabasesOwnReason(t *testing.T) {
	ready := Database{ID: "db", State: StateReady}
	other := Database{ID: "other", State: StateReady}
	preview := Database{ID: "preview", State: StateReady, Preview: PreviewOwner{SourceDatabaseID: "source", EnvironmentID: "env", PRNumber: 7}}
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
	for _, tc := range []struct {
		name   string
		in     HoldInputs
		db     Database
		excess bool
		want   string
	}{
		{name: "its own egress past its allowance", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": 20, "other": 1}, EgressAllowance: 20}, db: ready, want: api.ManagedPostgresHoldEgress},
		{name: "another database's egress holds only that one", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": 20, "other": 19}, EgressAllowance: 20}, db: other},
		{name: "just under", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": 19}, EgressAllowance: 20}, db: ready},
		{name: "plan excess outranks egress", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": 20}, EgressAllowance: 20}, db: ready, excess: true, want: api.ManagedPostgresHoldPlanExcess},
		{name: "no egress allowance", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": math.MaxInt64}}, db: ready},
		{name: "billed overage past the allowance", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": 39}, EgressAllowance: 20, EgressOverageBilled: true}, db: ready},
		{name: "billed overage at twice it", in: HoldInputs{Mode: CapEnforcementOn, DatabaseEgress: map[string]int64{"db": 40}, EgressAllowance: 20, EgressOverageBilled: true}, db: ready, want: api.ManagedPostgresHoldEgress},
		{name: "shadow holds nothing", in: HoldInputs{Mode: CapEnforcementShadow, DatabaseEgress: map[string]int64{"db": 40}, EgressAllowance: 20}, db: ready},
	} {
		if got := DatabaseHold(tc.db, tc.excess, tc.in); got != tc.want {
			t.Errorf("%s: DatabaseHold = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A database being created, changed or deleted is its lifecycle's, as
	// the sweep leaves it.
	inactive := HoldInputs{Mode: CapEnforcementOn, Inactive: true}
	for _, state := range []State{StateProvisioning, StateDeleting, StateDeleted} {
		if got := DatabaseHold(Database{ID: "db", State: state}, true, inactive); got != "" {
			t.Fatalf("%s database = %q, want no hold", state, got)
		}
	}
}

func TestEgressState(t *testing.T) {
	in := HoldInputs{Mode: CapEnforcementOn, EgressAllowance: 100}
	for _, tc := range []struct {
		egress map[string]int64
		mode   CapEnforcementMode
		want   string
	}{
		{nil, CapEnforcementOn, api.ManagedPostgresEgressHealthy},
		{map[string]int64{"a": 79, "b": 79}, CapEnforcementOn, api.ManagedPostgresEgressHealthy},
		{map[string]int64{"a": 80, "b": 1}, CapEnforcementOn, api.ManagedPostgresEgressWarning},
		{map[string]int64{"a": 100, "b": 1}, CapEnforcementOn, api.ManagedPostgresEgressHeld},
		{map[string]int64{"a": 100}, CapEnforcementShadow, api.ManagedPostgresEgressWarning},
	} {
		in.DatabaseEgress, in.Mode = tc.egress, tc.mode
		if got := EgressState(in); got != tc.want {
			t.Errorf("EgressState(%v %s) = %q, want %q", tc.egress, tc.mode, got, tc.want)
		}
	}
	billed := HoldInputs{Mode: CapEnforcementOn, EgressAllowance: 100, EgressOverageBilled: true}
	for _, tc := range []struct {
		egress int64
		want   string
	}{
		{80, api.ManagedPostgresEgressHealthy}, // warning moves to 80% of the 2x hold
		{99, api.ManagedPostgresEgressHealthy},
		{100, api.ManagedPostgresEgressOverage},
		{199, api.ManagedPostgresEgressOverage},
		{200, api.ManagedPostgresEgressHeld},
	} {
		billed.DatabaseEgress = map[string]int64{"a": tc.egress, "b": 1}
		if got := EgressState(billed); got != tc.want {
			t.Errorf("billed EgressState(%d) = %q, want %q", tc.egress, got, tc.want)
		}
	}
	if got := EgressState(HoldInputs{Mode: CapEnforcementOn}); got != "" {
		t.Fatalf("no allowance = %q, want omitted", got)
	}
}
