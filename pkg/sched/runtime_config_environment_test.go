// adr: 567 — environment intent and runtime ownership contracts.
package sched

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func runtimeEnvironmentDeployment(t *testing.T, store state.Store, appID, scope string) state.Deployment {
	t.Helper()
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: appID, Scope: scope,
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	return deployment
}

func TestRefreshRuntimeConfigForEnvironmentPreservesNeighborsAndIdleDeployments(t *testing.T) {
	store := state.NewMemStore()
	account, app, _ := seedApp(t, store, api.PlanPro, 256, 5)
	production := runtimeEnvironmentDeployment(t, store, app.ID, "production")
	idleCanary := runtimeEnvironmentDeployment(t, store, app.ID, "production")
	staging := runtimeEnvironmentDeployment(t, store, app.ID, "staging")
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "old"); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	old, err := engine.Wake(t.Context(), app.ID, production.ID, "production", TriggerAppWake)
	if err != nil {
		t.Fatal(err)
	}
	neighbor, err := engine.Wake(t.Context(), app.ID, staging.ID, "staging", TriggerAppWake)
	if err != nil {
		t.Fatal(err)
	}
	neighborBoot, err := store.CreateInstance(t.Context(), app.ID, staging.ID, string(state.StateWaking), 256, "test-node", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.InvalidateAppSnapshotsInScope(t.Context(), store, app.ID, "production"); err != nil {
		t.Fatal(err)
	}
	vmm.coldBootHook = func() {
		instance, err := store.InstanceByID(t.Context(), old.InstanceID)
		if err != nil || instance.State != string(state.StateRunning) {
			t.Errorf("predecessor withdrawn before replacement became ready: %+v %v", instance, err)
		}
	}
	wakeID := uuid.NewString()
	// Exercise the actual durable event decoder, not only the engine method.
	raw, _ := json.Marshal(state.EnvironmentGitOpsRuntimeRequest{AppID: app.ID, WakeID: wakeID, Scope: "production"})
	loop := NewLoop(nil, engine, testLog())
	if err := loop.handleRuntimeConfigRestart(t.Context(), db.Notification{Channel: db.NotifyRuntimeConfigRestart, Payload: string(raw)}); err != nil {
		t.Fatal(err)
	}
	for id, expected := range map[string]string{old.InstanceID: string(state.StateStopped), neighbor.InstanceID: string(state.StateRunning), neighborBoot.ID: string(state.StateWaking)} {
		instance, err := store.InstanceByID(t.Context(), id)
		if err != nil || instance.State != expected {
			t.Fatalf("instance %s: %+v %v; want %s", id, instance, err, expected)
		}
	}
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.DeploymentID == idleCanary.ID {
			t.Fatal("refresh booted a previously idle canary")
		}
	}
	if vmm.coldBoots != 3 || vmm.destroys != 1 || vmm.snapshots != 0 || vmm.warmSnapshots != 0 {
		t.Fatalf("unexpected VM work: cold=%d destroy=%d snapshots=%d warm=%d", vmm.coldBoots, vmm.destroys, vmm.snapshots, vmm.warmSnapshots)
	}
	if err := loop.handleRuntimeConfigRestart(t.Context(), db.Notification{Payload: string(raw)}); err != nil || vmm.coldBoots != 3 {
		t.Fatalf("duplicate delivery booted again: cold=%d %v", vmm.coldBoots, err)
	}
}

func TestRefreshRuntimeConfigForEnvironmentPreservesScaleToZero(t *testing.T) {
	for _, status := range []state.AppStatus{state.AppActive, state.AppEvictedCold} {
		t.Run(string(status), func(t *testing.T) {
			store := state.NewMemStore()
			_, app, _ := seedApp(t, store, api.PlanPro, 256, 1)
			runtimeEnvironmentDeployment(t, store, app.ID, "production")
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Status: &status}); err != nil {
				t.Fatal(err)
			}
			vmm := &fakeVMM{}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			out, err := engine.RefreshRuntimeConfigForEnvironment(t.Context(), app.ID, uuid.NewString(), "production")
			current, readErr := store.AppByID(t.Context(), app.ID)
			if err != nil || out.Instance != nil || readErr != nil || current.Status != status || vmm.coldBoots != 0 || vmm.restores != 0 || vmm.destroys != 0 {
				t.Fatalf("cold environment was activated: %+v %+v %v %v", out, current, err, readErr)
			}
		})
	}
}

func TestRefreshRuntimeConfigForEnvironmentRejectsInvalidScope(t *testing.T) {
	engine := &Engine{}
	for _, scope := range []string{"default", "../production", "Production", ""} {
		if _, err := engine.RefreshRuntimeConfigForEnvironment(t.Context(), "app", uuid.NewString(), scope); err == nil {
			t.Fatalf("invalid scope %q accepted", scope)
		}
	}
}
