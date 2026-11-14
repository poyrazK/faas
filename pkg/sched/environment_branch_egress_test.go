// adr: 20261031193635
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedContainedBranch adds a contained branch pr-2 beside seedEnvironmentBranch's
// live pr-1 and returns its deployment, marked live.
func seedContainedBranch(t *testing.T, store *state.MemStore, app state.App) state.Deployment {
	t.Helper()
	ctx := context.Background()
	limits := api.MustLimitsFor(api.PlanPro) // seedEnvironmentBranch's account plan
	if _, _, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: app.AccountID, ProjectID: app.ProjectID, ParentSlug: "production", Slug: "pr-2",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressContained,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeploymentForEnvironmentBranch(ctx, app.AccountID, app.ProjectID, "pr-2", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	return dep
}

// ADR-20261031193635 slice 6a: a contained branch's boot asks vmmd for the
// contained profile, and an unacknowledged boot is refused and destroyed.
func TestContainedBranchWakeRequiresTheContainedProfile(t *testing.T) {
	for _, ack := range []bool{true, false} {
		store := state.NewMemStore()
		app, live := seedEnvironmentBranch(t, store, api.ExecutionModeRequest, false)
		contained := seedContainedBranch(t, store, app)
		vmm := &sched.FakeVMM{}
		if ack {
			vmm.AckEnvironmentBranchContainment()
		}
		engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
		ctx := context.Background()
		_, err := engine.Wake(ctx, app.ID, contained.ID, contained.Scope, "")
		if (err == nil) != ack {
			t.Fatalf("ack=%v: Wake(contained) = %v", ack, err)
		}
		if !vmm.LastColdBootSpec().EnvironmentBranchContained {
			t.Fatalf("ack=%v: the contained branch boot did not ask for containment", ack)
		}
		if ack {
			if err := store.MarkDeploymentLive(ctx, live.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Wake(ctx, app.ID, live.ID, live.Scope, ""); err != nil {
				t.Fatalf("Wake(live branch): %v", err)
			}
			if vmm.LastColdBootSpec().EnvironmentBranchContained {
				t.Fatal("a live branch boot asked for containment")
			}
		}
	}
}

// Slice 6b: a rehearsal member seeded from a simulated branch has no branch
// lineage, so its prime boots it (under the run's policy) instead of the
// branch's boot-free activation.
func TestPrimeSeededRehearsalMemberBoots(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	app, _ := seedEnvironmentBranch(t, store, api.ExecutionModeRequest, false)
	limits := api.MustLimitsFor(api.PlanPro)
	if _, _, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: app.AccountID, ProjectID: app.ProjectID, ParentSlug: "production", Slug: "sim-1",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressSimulated,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits); err != nil {
		t.Fatal(err)
	}
	branchDep, err := store.CreateDeploymentForEnvironmentBranch(ctx, app.AccountID, app.ProjectID, "sim-1", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, branchDep.ID); err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateApp(ctx, state.App{AccountID: app.AccountID, Slug: "dev-web-twin", Status: state.AppActive,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60, PreviewOfSlug: "web"})
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.SeedScenarioMemberFromEnvironmentBranch(ctx, state.EnvironmentBranchSeed{
		AccountID: app.AccountID, ProjectID: app.ProjectID, Branch: "sim-1", Workload: "web", MemberAppID: member.ID})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	if err := engine.Prime(ctx, member.ID, seeded.ID); err != nil {
		t.Fatalf("Prime(seeded member): %v", err)
	}
	if *vmm.ColdBoots() != 1 {
		t.Fatalf("seeded member cold boots = %d; want 1", *vmm.ColdBoots())
	}
	if got := vmm.LastColdBootSpec(); got.AppID != member.ID {
		t.Fatalf("seeded member booted app %q, want the twin %q", got.AppID, member.ID)
	}
}
