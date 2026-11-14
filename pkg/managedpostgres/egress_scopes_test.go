// adr: 20261110160000 — amendment 2026-11-13: a restore copy shares its root
// source's egress allowance, as a Neon copy is a branch of its source's project
// and Neon's transfer allowance is per project (billing review R6-1).
package managedpostgres

import (
	"reflect"
	"testing"
)

func TestEgressScopesShareTheRootSourcesAllowance(t *testing.T) {
	snapshot := UsageSnapshot{
		DatabaseEgress: map[string]int64{"source": 300 * testGB, "preview": 250 * testGB, "branch-of-branch": 100 * testGB, "other": 450 * testGB},
		RestoreSources: map[string]string{"preview": "source", "branch": "source", "branch-of-branch": "branch", "loop": "loop"},
	}
	for db, want := range map[string]string{"source": "source", "preview": "source", "branch-of-branch": "source", "other": "other", "loop": "loop"} {
		if got := snapshot.EgressScopeOf(db); got != want {
			t.Errorf("EgressScopeOf(%s) = %s, want %s", db, got, want)
		}
	}
	if got, want := snapshot.EgressByScope(), map[string]int64{"source": 650 * testGB, "other": 450 * testGB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EgressByScope = %v, want %v", got, want)
	}
	// Every database of a scope is held on the scope's total.
	if got := snapshot.ScopedDatabaseEgress(); got["preview"] != 650*testGB || got["source"] != 650*testGB || got["other"] != 450*testGB {
		t.Fatalf("ScopedDatabaseEgress = %v", got)
	}
	// Billed once per scope: the source family is 150 GB past one 500 GB
	// allowance; before, each copy brought its own and nothing was billed.
	allowance := 500 * testGB
	if billed, _ := EgressOverageCharge(snapshot.EgressByScope(), allowance); billed != 150*testGB {
		t.Fatalf("billed = %d GB, want 150", billed/testGB)
	}
	if billed, _ := EgressOverageCharge(snapshot.DatabaseEgress, allowance); billed != 0 {
		t.Fatalf("per-database billing = %d, the old behaviour billed nothing", billed)
	}
}
