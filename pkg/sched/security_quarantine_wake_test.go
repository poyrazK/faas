package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// quarantineLiveDeployment parks the app's live deployment the way imaged
// does when a scheduled re-scan regresses (pkg/imaged/security_rescan.go).
func quarantineLiveDeployment(t *testing.T, store *state.MemStore, app state.App, dep state.Deployment) {
	t.Helper()
	ctx := context.Background()
	if err := store.SetDeploymentParked(ctx, dep.ID, string(state.ParkReasonSecurityScanRegressed), time.Now().UTC()); err != nil {
		t.Fatalf("SetDeploymentParked: %v", err)
	}
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatalf("UpdateApp evicted_cold: %v", err)
	}
}

func assertSecurityQuarantineRefusal(t *testing.T, err error) {
	t.Helper()
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeSecurityPostureBlocked || !errors.Is(err, ErrPermanentWake) {
		t.Fatalf("err = %v, want a permanent %s refusal", err, api.CodeSecurityPostureBlocked)
	}
}

// adr: 075 — a deployment whose live scan evidence regressed is quarantined
// (security_scan_regressed) and must not boot from any schedd trigger:
// cron, service mesh, floors and prewarm reach schedd without passing the
// gateway or apid checks that refuse quarantined traffic.
func TestSecurityQuarantinedDeploymentNeverBoots(t *testing.T) {
	for _, trigger := range []string{TriggerCronSched, TriggerServiceMesh, TriggerFloor, TriggerPrewarm, TriggerMeterd} {
		t.Run(trigger, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			quarantineLiveDeployment(t, store, app, dep)

			_, err := e.EnsureWake(context.Background(), app.ID, trigger)
			assertSecurityQuarantineRefusal(t, err)
			if _, err := e.AdmitInstanceForDeployment(context.Background(), app.ID, dep.ID, "", trigger); err == nil {
				t.Fatal("explicit deployment admission booted a quarantined deployment")
			}
			if vmm.coldBoots != 0 || vmm.restores != 0 {
				t.Fatalf("vmm boots = cold %d restore %d, want none", vmm.coldBoots, vmm.restores)
			}
			got, _ := store.AppByID(context.Background(), app.ID)
			if got.Status != state.AppEvictedCold {
				t.Fatalf("app status = %s, want evicted_cold kept", got.Status)
			}
		})
	}
}

// Recovery stays open: once a clean replacement is the live deployment, the
// app wakes normally (the cmd/apid/handlers_security.go recovery flow).
func TestSecurityQuarantineCleanReplacementWakes(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	quarantineLiveDeployment(t, store, app, dep)
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatalf("supersede quarantined deployment: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:clean", Status: state.DeployLive,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := e.EnsureWake(ctx, app.ID, TriggerCronSched); err != nil {
		t.Fatalf("wake with a clean live replacement: %v", err)
	}
	if vmm.coldBoots+vmm.restores != 1 {
		t.Fatalf("vmm boots = %d, want 1", vmm.coldBoots+vmm.restores)
	}
}

// App tasks run the deployment's image, so a quarantined deployment refuses
// them too.
func TestSecurityQuarantineRefusesAppTasks(t *testing.T) {
	store, account, app, deployment, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := store.SetDeploymentParked(context.Background(), deployment.ID, string(state.ParkReasonSecurityScanRegressed), time.Now().UTC()); err != nil {
		t.Fatalf("SetDeploymentParked: %v", err)
	}
	_, err := engine.ResolveAppTaskRuntime(context.Background(), AppTaskRestoreRequest{
		ID: tasks[0].ID, AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: tasks[0].Kind, DeploymentScope: tasks[0].DeploymentScope,
		ArtifactKey: tasks[0].ArtifactKey, ImageDigest: tasks[0].ImageDigest,
	})
	if !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		t.Fatalf("app task on a quarantined deployment = %v, want ErrAppTaskDeploymentUnavailable", err)
	}
}

// Prime boots the deployment's image to capture its snapshot; it refuses a
// quarantined deployment.
func TestSecurityQuarantineRefusesPrime(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	quarantineLiveDeployment(t, store, app, dep)
	assertSecurityQuarantineRefusal(t, e.Prime(context.Background(), app.ID, dep.ID))
	if vmm.coldBoots != 0 {
		t.Fatalf("Prime cold-booted %d quarantined VMs", vmm.coldBoots)
	}
}
