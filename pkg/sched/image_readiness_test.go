// adr:683
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type imageCheckFailureVMM struct {
	*fakeVMM
	spec AppSpec
}

func (v *imageCheckFailureVMM) CreateColdBoot(_ context.Context, _, _ string, app AppSpec) (*WakeOutcome, error) {
	v.spec = app
	return nil, api.NewProblem(422, api.CodeAppStartupTimeout, "image healthcheck did not become ready", "startup_phase=image_healthcheck: fresh command failed")
}

func TestImageHealthcheckPrimeFailurePreservesServingDeployment(t *testing.T) {
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanHobby, 256, 2)
	if err := store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	serving, err := e.Wake(t.Context(), app.ID, stable.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:candidate", Status: state.DeploySnapshotting, InferredProfile: []byte(`{"version":"v1","image_healthcheck_required":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	failing := &imageCheckFailureVMM{fakeVMM: vmm}
	e.vmm = failing
	loop := NewLoop(nil, e, testLog())
	loop.handleNotification(t.Context(), db.Notification{Channel: db.NotifySnapshotPrime, Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + candidate.ID + `"}`})
	loop.waitPrimes()
	if !failing.spec.ImageHealthcheckRequired {
		t.Fatal("prime lost the effective image healthcheck contract")
	}
	old, err := store.DeploymentByID(t.Context(), stable.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.DeploymentByID(t.Context(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != state.DeployLive || old.TrafficPercent != 100 || failed.Status != state.DeployFailed || failed.TrafficPercent != 0 {
		t.Fatalf("serving route changed: stable=%+v candidate=%+v", old, failed)
	}
	instance, err := store.InstanceByID(t.Context(), serving.InstanceID)
	if err != nil || instance.State != string(state.StateRunning) {
		t.Fatalf("serving instance lost: %+v %v", instance, err)
	}
	if vmm.snapshots != 0 || vmm.destroys != 0 {
		t.Fatalf("failed candidate touched serving VM: snapshots=%d destroys=%d", vmm.snapshots, vmm.destroys)
	}
}

func TestImageHealthcheckSpecProjection(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{}`, false}, {`{"version":"v1","image_healthcheck_required":false}`, false}, {`{"version":"v1","image_healthcheck_required":true}`, true},
		{`{"version":"v1","image_healthcheck_required":null}`, true}, {`{"version":"v1","image_healthcheck_required":"false"}`, true}, {`{bad`, true},
	} {
		dep := state.Deployment{Kind: state.DeploymentKindImage, InferredProfile: []byte(tc.raw)}
		if got := imageHealthcheckRequiredFromDep(dep); got != tc.want {
			t.Fatalf("profile %s requires=%v want=%v", tc.raw, got, tc.want)
		}
	}
	got := (AppSpec{ImageHealthcheckRequired: true}).toProto()
	if !got.GetImageHealthcheckRequired() {
		t.Fatal("wake wire lost required image check")
	}
}
