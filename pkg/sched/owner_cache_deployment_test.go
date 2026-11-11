// adr: 1741 — amendment 2026-11-05: a wake that names its production
// deployment (the gateway's AdmitInstance, the drain's invocations, service and
// floor wakes) reads from the owner cache like one that names nothing.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

type namedDeploymentFixture struct {
	ownerCacheFixture
	dep state.Deployment
}

func newNamedDeploymentFixture(t *testing.T, ownerCache bool) namedDeploymentFixture {
	t.Helper()
	mem := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, mem, api.PlanPro, 512, 5)
	counter := &sched.ReadCountingStore{MemStore: mem}
	ops := wire.NewOpsMetrics("schedd")
	e := sched.XNewEngine(t, statetest.NewMem(counter), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	if ownerCache {
		e.WithOwnerCache()
	}
	return namedDeploymentFixture{ownerCacheFixture{mem: mem, counter: counter, e: e, ops: ops, app: app}, dep}
}

// wakeNamedAndPark wakes the app's deployment by name, as the gateway and the
// drain do, and parks the instance so the next wake admits a new one.
func (f namedDeploymentFixture) wakeNamedAndPark(t *testing.T, scope, trigger string) sched.WakeResult {
	t.Helper()
	result, err := f.e.Wake(t.Context(), f.app.ID, f.dep.ID, scope, trigger)
	if err != nil || result.InstanceID == "" {
		t.Fatalf("Wake of deployment %s: %+v, %v", f.dep.ID, result, err)
	}
	if result.DeploymentID != f.dep.ID {
		t.Fatalf("woke deployment %q, want %q", result.DeploymentID, f.dep.ID)
	}
	if err := f.e.Park(t.Context(), result.InstanceID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	return result
}

// The first wake of a named deployment loads the entry; the next ones are
// served from it. The cutover barrier read, which the entry stands in for,
// shows the difference; a cache that was off reads it on every wake.
func TestOwnerCacheServesAWakeThatNamesItsProductionDeployment(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ownerCache  bool
		scope       string
		wantBarrier int
		wantHits    bool
	}{
		{name: "cache off reads the barrier in phase 1 and at admission", wantBarrier: 2},
		{name: "cache on, no scope", ownerCache: true, wantHits: true},
		{name: "cache on, the deployment's own scope", ownerCache: true, scope: state.DefaultEnvScope, wantHits: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNamedDeploymentFixture(t, tc.ownerCache)
			f.wakeNamedAndPark(t, tc.scope, sched.TriggerInvocation)
			before := f.counter.Reads()["ManagedPostgresAdmissionFenced"]
			f.wakeNamedAndPark(t, tc.scope, sched.TriggerInvocation)
			if got := f.counter.Reads()["ManagedPostgresAdmissionFenced"] - before; got != tc.wantBarrier {
				t.Fatalf("barrier reads by the second wake = %d, want %d", got, tc.wantBarrier)
			}
			outcomes := f.outcomes()
			if hit := outcomes["hit"] > 0 && outcomes["miss"] == 1; hit != tc.wantHits {
				t.Fatalf("owner cache outcomes = %v, want hits after one miss = %v", outcomes, tc.wantHits)
			}
			if outcomes["refused"] != 0 || outcomes["selection"] != 0 {
				t.Fatalf("owner cache outcomes = %v, want no refusal and no other selection", outcomes)
			}
		})
	}
}

// An entry that names no scope serves the deployment's own and nothing else.
func TestOwnerEntryServesOnlyItsOwnDeployment(t *testing.T) {
	f := newNamedDeploymentFixture(t, true)
	f.wakeNamedAndPark(t, "", sched.TriggerInvocation)
	entry, ok := (*f.e.Owners()).Lookup(f.app.ID)
	if !ok {
		t.Fatal("no entry after a wake")
	}
	for _, tc := range []struct {
		name         string
		deploymentID string
		scope        string
		want         bool
	}{
		{name: "its deployment", deploymentID: f.dep.ID, want: true},
		{name: "its deployment, its scope", deploymentID: f.dep.ID, scope: state.DefaultEnvScope, want: true},
		{name: "its deployment, another environment", deploymentID: f.dep.ID, scope: "staging"},
		{name: "another deployment", deploymentID: "00000000-0000-4000-8000-0000000000d2"},
		{name: "no deployment", deploymentID: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := entry.ServesDeployment(tc.deploymentID, tc.scope); got != tc.want {
				t.Fatalf("ServesDeployment(%q, %q) = %v, want %v", tc.deploymentID, tc.scope, got, tc.want)
			}
		})
	}
}

// The authenticated smoke wakes a candidate that production does not point at
// yet; it never reads the entry, even when it names the live deployment.
func TestOwnerCacheDoesNotServeTheDeploymentSmoke(t *testing.T) {
	f := newNamedDeploymentFixture(t, true)
	f.wakeNamedAndPark(t, "", sched.TriggerInvocation)
	before := f.outcomes()
	f.wakeNamedAndPark(t, "", sched.TriggerDeploymentSmoke)
	after := f.outcomes()
	for _, outcome := range []string{"hit", "miss", "selection", "refused"} {
		if after[outcome] != before[outcome] {
			t.Fatalf("owner cache outcomes %v -> %v; the smoke must not consult the entry", before, after)
		}
	}
}

// A deployment superseded without the notification arriving leaves the entry
// naming it. The admission insert refuses the moved generation, and the wake
// decides again from fresh reads, which find the deployment no longer live:
// nothing is admitted, as without the cache.
func TestOwnerCacheRefusesANamedDeploymentSupersededBehindItsBack(t *testing.T) {
	f := newNamedDeploymentFixture(t, true)
	f.wakeNamedAndPark(t, "", sched.TriggerInvocation)
	next, err := f.mem.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:next", Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.mem.MarkDeploymentLive(t.Context(), next.ID); err != nil {
		t.Fatal(err)
	}
	result, err := f.e.AdmitInstanceForDeployment(t.Context(), f.app.ID, f.dep.ID, "", sched.TriggerInvocation)
	if err != nil {
		t.Fatalf("AdmitInstanceForDeployment of a superseded deployment: %v", err)
	}
	if !result.AtCapacity || result.InstanceID != "" {
		t.Fatalf("result = %+v, want nothing admitted for a deployment that is no longer live", result)
	}
	if got := f.outcomes(); got["refused"] != 1 {
		t.Fatalf("owner cache outcomes = %v, want the stale entry refused once", got)
	}
	// The new production deployment is not the entry's: it reads its own rows.
	woken, err := f.e.Wake(t.Context(), f.app.ID, next.ID, "", sched.TriggerInvocation)
	if err != nil || woken.DeploymentID != next.ID {
		t.Fatalf("Wake of the new deployment = %+v, %v", woken, err)
	}
}
