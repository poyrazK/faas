package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func deploymentWithMissingSecret(t *testing.T, store state.Store, appID string) state.Deployment {
	t.Helper()
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:              appID,
		Kind:               state.DeploymentKindImage,
		ImageDigest:        "sha256:missing-secret-test",
		Status:             state.DeployLive,
		OverrideEnvSecrets: json.RawMessage(`{"MISSING":"secret:MISSING"}`),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return dep
}

func TestWakeSealedEnvFailureReleasesAdmissionImmediately(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 1)
	dep := deploymentWithMissingSecret(t, store, app.ID)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID, dep.ID, "", ""); err == nil {
		t.Fatal("Wake returned nil, want missing-secret error")
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Fatalf("resident RAM = %d, want 0 after sealed-env rejection", got)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 1 || instances[0].State != string(state.StateFailed) {
		t.Fatalf("instances after sealed-env rejection = %+v, want one failed row", instances)
	}
}

func TestPrimeSealedEnvFailureReleasesAdmissionImmediately(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 1)
	dep := deploymentWithMissingSecret(t, store, app.ID)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	if err := e.Prime(context.Background(), app.ID, dep.ID); err == nil {
		t.Fatal("Prime returned nil, want missing-secret error")
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Fatalf("resident RAM = %d, want 0 after sealed-env rejection", got)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 1 || instances[0].State != string(state.StateFailed) {
		t.Fatalf("instances after sealed-env rejection = %+v, want one failed row", instances)
	}
}
