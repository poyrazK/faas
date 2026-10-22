package sched_test

// adr: 199

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us rc.251: `gregale app h9-split restart --fresh` on an app at
// max_concurrency 1 with v1 serving 100% and v3 staged by deploy
// --no-traffic first cold-booted a v3 replacement, then refused the v1
// replacement at the cap ("max_concurrency is 2; 2 already live"). The
// durable request retried forever, cold-booting v3 again each attempt, and
// `restart --wait` timed out.
func TestRefreshRuntimeConfigRetiresUnroutedDeploymentInsteadOfReplacingIt(t *testing.T) {
	store := state.NewMemStore()
	_, app, serving := sched.SeedApp(t, store, api.PlanPro, 256, 1)
	if _, err := store.UpdateDeploymentTraffic(t.Context(), serving.ID, 100); err != nil {
		t.Fatal(err)
	}
	staged, err := store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive,
		TrafficPercent: 0, TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	old, err := engine.Wake(t.Context(), app.ID, serving.ID, serving.Scope, sched.TriggerAppWake)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := engine.Wake(t.Context(), app.ID, staged.ID, staged.Scope, sched.TriggerAppWake)
	if err != nil {
		t.Fatalf("waking the staged revision (preview URL): %v", err)
	}
	if _, err := state.InvalidateAppSnapshots(t.Context(), store, app.ID); err != nil {
		t.Fatal(err)
	}
	bootsBefore := (*vmm.ColdBoots()) + (*vmm.Restores())

	wakeID := uuid.NewString()
	out, err := engine.RefreshRuntimeConfig(t.Context(), app.ID, wakeID)
	if err != nil {
		t.Fatalf("RefreshRuntimeConfig: %v", err)
	}
	if out.Instance == nil || out.Instance.DeploymentID != serving.ID {
		t.Fatalf("refreshed instance = %+v; want a fresh instance of the serving deployment", out.Instance)
	}
	if boots := (*vmm.ColdBoots()) + (*vmm.Restores()) - bootsBefore; boots != 1 {
		t.Fatalf("restart booted %d VMs; want only the serving replacement", boots)
	}
	for id, want := range map[string]state.State{old.InstanceID: state.StateStopped, preview.InstanceID: state.StateStopped} {
		instance, err := store.InstanceByID(t.Context(), id)
		if err != nil || state.State(instance.State) != want {
			t.Fatalf("instance %s = %+v, %v; want %s", id, instance, err, want)
		}
	}
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.DeploymentID == staged.ID && sched.RuntimeConfigResident(instance) {
			t.Fatalf("staged revision still has resident instance %+v", instance)
		}
	}
}

// H10-39: after scale-out to three instances, lowering max_concurrency to 1
// left three serving instances against a ceiling of two (max plus the
// rollout grant). Every replacement boot was refused and the restart retried
// forever. Surplus predecessors are now retired first.
func TestRefreshRuntimeConfigRetiresSurplusAboveLoweredConcurrency(t *testing.T) {
	store := state.NewMemStore()
	_, app, serving := sched.SeedApp(t, store, api.PlanPro, 256, 3)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	var old []string
	for range 3 {
		res, err := engine.AdmitAndDispatchWithOptions(t.Context(), app.ID, serving.ID, "normal", sched.TriggerAppWake, true, true)
		if err != nil || res.InstanceID == "" {
			t.Fatalf("scale-out admission: %+v, %v", res, err)
		}
		old = append(old, res.InstanceID)
	}
	lowered := 1
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{MaxConcurrency: &lowered}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.InvalidateAppSnapshots(t.Context(), store, app.ID); err != nil {
		t.Fatal(err)
	}

	out, err := engine.RefreshRuntimeConfig(t.Context(), app.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("RefreshRuntimeConfig over the lowered cap: %v", err)
	}
	if out.Instance == nil || out.Instance.DeploymentID != serving.ID {
		t.Fatalf("refreshed instance = %+v; want a fresh serving instance", out.Instance)
	}
	for _, id := range old {
		instance, err := store.InstanceByID(t.Context(), id)
		if err != nil || sched.RuntimeConfigResident(instance) {
			t.Fatalf("predecessor %s = %+v, %v; want retired", id, instance, err)
		}
	}
}
