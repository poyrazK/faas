package conformance

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func testImageRuntimeProfile(t *testing.T, fx *Fixture) {
	t.Helper()
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:runtime-profile", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := []byte(`{"version":"v1","framework":"unknown","port":5678}`)
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, profile); err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.DeploymentByID(fx.Ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(stored.InferredProfile, &got); err != nil || got.Port != 5678 || stored.OverridePort != 0 {
		t.Fatalf("runtime profile=%s override_port=%d err=%v", stored.InferredProfile, stored.OverridePort, err)
	}
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, []byte(`{bad`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
	if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, dep.ID, state.DeployCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, profile); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("terminal profile write error=%v, want invalid state transition", err)
	}
}
