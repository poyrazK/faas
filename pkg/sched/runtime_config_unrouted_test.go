package sched

// adr: 199

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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
	_, app, serving := seedApp(t, store, api.PlanPro, 256, 1)
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
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	old, err := engine.Wake(t.Context(), app.ID, serving.ID, serving.Scope, TriggerAppWake)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := engine.Wake(t.Context(), app.ID, staged.ID, staged.Scope, TriggerAppWake)
	if err != nil {
		t.Fatalf("waking the staged revision (preview URL): %v", err)
	}
	if _, err := state.InvalidateAppSnapshots(t.Context(), store, app.ID); err != nil {
		t.Fatal(err)
	}
	bootsBefore := vmm.coldBoots + vmm.restores

	wakeID := uuid.NewString()
	out, err := engine.RefreshRuntimeConfig(t.Context(), app.ID, wakeID)
	if err != nil {
		t.Fatalf("RefreshRuntimeConfig: %v", err)
	}
	if out.Instance == nil || out.Instance.DeploymentID != serving.ID {
		t.Fatalf("refreshed instance = %+v; want a fresh instance of the serving deployment", out.Instance)
	}
	if boots := vmm.coldBoots + vmm.restores - bootsBefore; boots != 1 {
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
		if instance.DeploymentID == staged.ID && runtimeConfigResident(instance) {
			t.Fatalf("staged revision still has resident instance %+v", instance)
		}
	}
}
